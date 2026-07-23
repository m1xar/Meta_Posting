package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/config"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	platformcrypto "github.com/watchers-factory/raze-posting/internal/platform/crypto"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/storage"
)

var (
	ErrMetaConnectionInactive = errors.New("Meta connection is not active")
	ErrMetaCredentialsExpired = errors.New("Meta access credentials have expired")
)

type Service struct {
	Config    config.Config
	Repos     *database.Repositories
	Meta      *meta.Client
	Publisher meta.Publisher
	Cipher    platformcrypto.TokenCipher
	Storage   *storage.Local
	Now       func() time.Time
	Random    io.Reader
}

func NewService(
	cfg config.Config,
	repositories *database.Repositories,
	metaClient *meta.Client,
	cipher platformcrypto.TokenCipher,
	localStorage *storage.Local,
) (*Service, error) {
	if repositories == nil || repositories.DB() == nil {
		return nil, errors.New("application: repositories are required")
	}
	if metaClient == nil {
		return nil, errors.New("application: Meta client is required")
	}
	if cipher == nil {
		return nil, errors.New("application: token cipher is required")
	}
	if localStorage == nil {
		return nil, errors.New("application: local storage is required")
	}
	return &Service{
		Config:    cfg,
		Repos:     repositories,
		Meta:      metaClient,
		Publisher: meta.Publisher{Client: metaClient},
		Cipher:    cipher,
		Storage:   localStorage,
		Now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *Service) accessToken(ctx context.Context, connectionID uuid.UUID) (*domain.MetaConnection, string, error) {
	connection, err := s.Repos.MetaConnections.Get(ctx, connectionID)
	if err != nil {
		return nil, "", err
	}
	if err := validateConnectionForToken(connection, s.Now()); err != nil {
		if errors.Is(err, ErrMetaCredentialsExpired) && connection.Status == domain.MetaConnectionActive {
			connection.Status = domain.MetaConnectionExpired
			statusErr := s.markConnectionExpired(ctx, connectionID, err.Error())
			return connection, "", errors.Join(err, statusErr)
		}
		return connection, "", err
	}
	plaintext, err := s.Cipher.Decrypt(platformcrypto.EncryptedValue{
		Ciphertext: connection.AccessTokenCiphertext,
		Nonce:      connection.AccessTokenNonce,
		KeyVersion: connection.TokenKeyVersion,
	}, []byte(connection.MetaUserID))
	if err != nil {
		return nil, "", err
	}
	token := strings.TrimSpace(string(plaintext))
	for index := range plaintext {
		plaintext[index] = 0
	}
	if token == "" {
		return nil, "", errors.New("application: decrypted Meta token is empty")
	}
	return connection, token, nil
}

func validateConnectionForToken(connection *domain.MetaConnection, now time.Time) error {
	if connection == nil {
		return errors.New("application: Meta connection is nil")
	}
	switch connection.Status {
	case domain.MetaConnectionActive:
	case domain.MetaConnectionExpired:
		return fmt.Errorf(
			"%w: connection %s is already marked expired",
			ErrMetaCredentialsExpired,
			connection.ID,
		)
	default:
		return fmt.Errorf(
			"%w: connection %s has status %q",
			ErrMetaConnectionInactive,
			connection.ID,
			connection.Status,
		)
	}

	now = now.UTC()
	if connection.TokenExpiresAt != nil && !connection.TokenExpiresAt.After(now) {
		return fmt.Errorf(
			"%w: token for connection %s expired at %s",
			ErrMetaCredentialsExpired,
			connection.ID,
			connection.TokenExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	if connection.DataAccessExpiresAt != nil && !connection.DataAccessExpiresAt.After(now) {
		return fmt.Errorf(
			"%w: data access for connection %s expired at %s",
			ErrMetaCredentialsExpired,
			connection.ID,
			connection.DataAccessExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	return nil
}

// markConnectionExpiredForMetaError is the single classification point for
// Graph token failures. Every long-running Meta workflow calls it before
// returning or swallowing a Graph error so the scheduler immediately stops
// creating work for credentials that Meta has rejected.
func (s *Service) markConnectionExpiredForMetaError(
	ctx context.Context,
	connectionID uuid.UUID,
	err error,
) (bool, error) {
	graphErr := metaAccessTokenError(err)
	if graphErr == nil {
		return false, nil
	}
	return true, s.markConnectionExpired(ctx, connectionID, graphErr.Error())
}

func (s *Service) markConnectionExpired(ctx context.Context, connectionID uuid.UUID, reason string) error {
	if err := s.Repos.MetaConnections.SetStatus(
		ctx,
		connectionID,
		domain.MetaConnectionExpired,
		reason,
	); err != nil {
		return fmt.Errorf("mark Meta connection %s expired: %w", connectionID, err)
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &connectionID,
		ActorType:    "worker",
		Action:       "meta_connection.expired",
		EntityType:   "meta_connection",
		EntityID:     connectionID.String(),
		Severity:     domain.AuditWarning,
		Metadata:     domain.MustJSON(map[string]any{"reason": reason}),
	})
	return nil
}

func isMetaAccessTokenError(err error) bool {
	return metaAccessTokenError(err) != nil
}

func metaAccessTokenError(err error) *meta.GraphError {
	if err == nil {
		return nil
	}
	if graphErr, ok := err.(*meta.GraphError); ok &&
		graphErr.Code == 190 &&
		graphErr.ErrorSubcode != 2069032 {
		return graphErr
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if graphErr := metaAccessTokenError(child); graphErr != nil {
				return graphErr
			}
		}
	case interface{ Unwrap() error }:
		return metaAccessTokenError(wrapped.Unwrap())
	}
	return nil
}

func jsonValue(value any) (domain.JSON, error) {
	return domain.NewJSON(value)
}

func emptyObject() domain.JSON {
	return append(domain.JSON(nil), domain.EmptyJSONObject...)
}

func emptyArray() domain.JSON {
	return append(domain.JSON(nil), domain.EmptyJSONArray...)
}

func (s *Service) audit(ctx context.Context, event domain.AuditEvent) {
	if event.Severity == "" {
		event.Severity = domain.AuditInfo
	}
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	if event.Before == nil {
		event.Before = emptyObject()
	}
	if event.After == nil {
		event.After = emptyObject()
	}
	if event.Metadata == nil {
		event.Metadata = emptyObject()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = s.Now()
	}
	// Auditing must not turn an already completed Meta operation into a client
	// failure. Database errors are surfaced by worker/API logs at the caller.
	_ = s.Repos.Audit.Append(ctx, &event)
}
