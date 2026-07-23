package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

const oauthSessionLifetime = 30 * time.Minute

func (s *Service) StartOAuth(ctx context.Context) (OAuthStart, error) {
	stateBytes := make([]byte, 32)
	random := s.Random
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, stateBytes); err != nil {
		return OAuthStart{}, fmt.Errorf("generate OAuth state: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	stateHash := sha256.Sum256([]byte(state))
	now := s.Now()
	expiresAt := now.Add(oauthSessionLifetime)
	session := &domain.OAuthSession{
		StateHash:       stateHash[:],
		RedirectURI:     s.Config.Meta.RedirectURI,
		RequestedScopes: domain.MustJSON(s.Config.Meta.Scopes),
		Status:          domain.OAuthSessionPending,
		ExpiresAt:       expiresAt,
		Metadata:        emptyObject(),
	}
	if err := s.Repos.OAuthSessions.Create(ctx, session); err != nil {
		return OAuthStart{}, fmt.Errorf("create OAuth session: %w", err)
	}
	authorizationURL, err := s.Meta.AuthorizationURL(meta.OAuthOptions{
		RedirectURI: s.Config.Meta.RedirectURI,
		State:       state,
		Scopes:      s.Config.Meta.Scopes,
		ConfigID:    s.Config.Meta.LoginConfigID,
		AuthType:    "rerequest",
	})
	if err != nil {
		_ = s.Repos.OAuthSessions.Fail(ctx, session.ID, err.Error(), now)
		return OAuthStart{}, err
	}
	s.audit(ctx, domain.AuditEvent{
		ActorType:  "internal_api",
		Action:     "oauth.session.created",
		EntityType: "oauth_session",
		EntityID:   session.ID.String(),
		After:      domain.MustJSON(map[string]any{"expires_at": expiresAt}),
	})
	return OAuthStart{AuthorizationURL: authorizationURL, ExpiresAt: expiresAt}, nil
}

func (s *Service) CompleteOAuth(ctx context.Context, state, code string) (OAuthCompletion, error) {
	if state == "" {
		return OAuthCompletion{}, invalid("state", "is required")
	}
	if code == "" {
		return OAuthCompletion{}, invalid("code", "is required")
	}
	stateHash := sha256.Sum256([]byte(state))
	now := s.Now()
	session, err := s.Repos.OAuthSessions.Consume(ctx, stateHash[:], now)
	if err != nil {
		return OAuthCompletion{}, fmt.Errorf("consume OAuth session: %w", err)
	}
	failSession := func(cause error) (OAuthCompletion, error) {
		_ = s.Repos.OAuthSessions.Fail(ctx, session.ID, cause.Error(), s.Now())
		return OAuthCompletion{}, cause
	}

	shortToken, err := s.Meta.ExchangeCode(ctx, code, session.RedirectURI)
	if err != nil {
		return failSession(fmt.Errorf("exchange OAuth code: %w", err))
	}
	longToken, err := s.Meta.ExchangeLongLivedToken(ctx, shortToken.AccessToken)
	if err != nil {
		return failSession(fmt.Errorf("exchange long-lived Meta token: %w", err))
	}
	debug, err := s.Meta.DebugToken(ctx, longToken.AccessToken)
	if err != nil {
		return failSession(fmt.Errorf("debug Meta token: %w", err))
	}
	if !debug.IsValid {
		return failSession(errors.New("Meta returned an invalid access token"))
	}
	if debug.UserID == "" {
		return failSession(errors.New("Meta token did not contain a user ID"))
	}
	user, err := s.Meta.GetMe(ctx, longToken.AccessToken)
	if err != nil {
		return failSession(fmt.Errorf("read Meta user profile: %w", err))
	}
	if user.ID != "" && user.ID != debug.UserID {
		return failSession(errors.New("Meta profile and debug-token user IDs differ"))
	}

	encrypted, err := s.Cipher.Encrypt([]byte(longToken.AccessToken), []byte(debug.UserID))
	if err != nil {
		return failSession(fmt.Errorf("encrypt Meta token: %w", err))
	}
	expiresAt := pointerIfNonZero(debug.ExpirationTime())
	if expiresAt == nil {
		expiresAt = pointerIfNonZero(longToken.ExpiresAt(now))
	}
	dataAccessExpiresAt := pointerIfNonZero(debug.DataAccessExpirationTime())
	missingScopes := missingStrings(s.Config.Meta.Scopes, debug.Scopes)
	connection := &domain.MetaConnection{
		MetaUserID:            debug.UserID,
		DisplayName:           user.Name,
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: encrypted.Ciphertext,
		AccessTokenNonce:      encrypted.Nonce,
		TokenKeyVersion:       encrypted.KeyVersion,
		TokenExpiresAt:        expiresAt,
		DataAccessExpiresAt:   dataAccessExpiresAt,
		GrantedScopes:         domain.MustJSON(debug.Scopes),
		DeclinedScopes:        domain.MustJSON(missingScopes),
		LastValidatedAt:       &now,
		Metadata: domain.MustJSON(map[string]any{
			"token_type":      debug.Type,
			"application":     debug.Application,
			"granular_scopes": debug.GranularScopes,
			"missing_scopes":  missingScopes,
		}),
	}
	if err := s.Repos.MetaConnections.Upsert(ctx, connection); err != nil {
		return failSession(fmt.Errorf("save Meta connection: %w", err))
	}
	connection, err = s.Repos.MetaConnections.FindByMetaUserID(ctx, debug.UserID)
	if err != nil {
		return failSession(fmt.Errorf("reload Meta connection: %w", err))
	}
	if err := s.Repos.OAuthSessions.Complete(ctx, session.ID, connection.ID, s.Now()); err != nil {
		return OAuthCompletion{}, fmt.Errorf("complete OAuth session: %w", err)
	}
	job, err := s.EnqueueConnectionSync(ctx, connection.ID, "oauth:"+session.ID.String())
	if err != nil {
		return OAuthCompletion{}, fmt.Errorf("enqueue initial account sync: %w", err)
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &connection.ID,
		ActorType:    "meta_user",
		ActorID:      debug.UserID,
		Action:       "meta.connection.connected",
		EntityType:   "meta_connection",
		EntityID:     connection.ID.String(),
		After: domain.MustJSON(map[string]any{
			"display_name":   user.Name,
			"granted_scopes": debug.Scopes,
			"missing_scopes": missingScopes,
		}),
	})
	return OAuthCompletion{
		ConnectionID: connection.ID,
		MetaUserID:   connection.MetaUserID,
		DisplayName:  connection.DisplayName,
		SyncJobID:    job.ID,
	}, nil
}

func pointerIfNonZero(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func missingStrings(required, granted []string) []string {
	missing := make([]string, 0)
	for _, item := range required {
		if !slices.Contains(granted, item) {
			missing = append(missing, item)
		}
	}
	return missing
}
