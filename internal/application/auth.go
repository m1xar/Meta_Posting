package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const userSessionLifetime = 30 * 24 * time.Hour

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionExpired     = errors.New("session is missing or expired")
	ErrAccountDisabled    = errors.New("account is disabled")
	usernamePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	dummyPasswordHash     = []byte("$2a$12$7EqJtq98hPqEX7fNZaFWoO5fPvYB6Jt4fQfN5Qb3H3RZc3Y4M0J9K")

	// Names that would let a user impersonate the platform or shadow a route.
	reservedUsernames = map[string]struct{}{
		"admin": {}, "administrator": {}, "root": {}, "api": {}, "app": {},
		"support": {}, "system": {}, "me": {}, "legacy-admin": {},
		"oauth": {}, "auth": {}, "v1": {}, "health": {}, "docs": {},
	}
)

// RegisterRequest is the registration input.
type RegisterRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthSession struct {
	User      domain.User
	SessionID uuid.UUID
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

func (s *Service) RegisterUser(ctx context.Context, request RegisterRequest) (AuthSession, error) {
	email, err := normalizeEmail(request.Email)
	if err != nil {
		return AuthSession{}, err
	}
	username := normalizeUsername(request.Username)
	if !usernamePattern.MatchString(username) {
		return AuthSession{}, invalid("username",
			"must be 3-64 lowercase letters, numbers, dot, dash, or underscore")
	}
	if _, reserved := reservedUsernames[username]; reserved {
		return AuthSession{}, invalid("username", "is reserved")
	}
	if err := validatePassword(request.Password); err != nil {
		return AuthSession{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		return AuthSession{}, err
	}
	user := &domain.User{
		Email:        email,
		Username:     username,
		Role:         domain.RoleUser,
		PasswordHash: string(hash),
	}
	if err := s.Repos.Users.Create(ctx, user); err != nil {
		// Which of the two collided is deliberately not disclosed: telling an
		// anonymous caller "that email exists" enumerates the user base.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return AuthSession{}, conflict("email or username is already taken")
		}
		return AuthSession{}, err
	}
	return s.newUserSession(ctx, user)
}

// LoginUser accepts either an email address or a username as the identifier,
// which is what users expect when they registered with both.
func (s *Service) LoginUser(ctx context.Context, identifier, password string) (AuthSession, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	user, err := s.Repos.Users.FindByIdentifier(ctx, identifier)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Compare against a dummy hash so an unknown identifier costs the
			// same time as a known one, and cannot be distinguished by timing.
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
			return AuthSession{}, ErrInvalidCredentials
		}
		return AuthSession{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return AuthSession{}, ErrInvalidCredentials
	}
	// Checked after the password compare, so a disabled account is not
	// revealed to someone who does not already hold its credentials. The
	// legacy tenant's sentinel hash cannot match bcrypt anyway; this relies on
	// intent rather than on that accident.
	if !user.CanLogin() {
		return AuthSession{}, ErrAccountDisabled
	}
	now := s.Now()
	if err := s.Repos.Users.MarkLogin(ctx, user.ID, now); err != nil {
		return AuthSession{}, err
	}
	user.LastLoginAt = &now
	return s.newUserSession(ctx, user)
}

func (s *Service) AuthenticateUserSession(ctx context.Context, token string) (AuthSession, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AuthSession{}, ErrSessionExpired
	}
	tokenHash := sha256.Sum256([]byte(token))
	now := s.Now()
	found, err := s.Repos.Users.FindSession(ctx, tokenHash[:], now)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AuthSession{}, ErrSessionExpired
		}
		return AuthSession{}, err
	}
	if now.Sub(found.Session.LastSeenAt) >= 5*time.Minute {
		_ = s.Repos.Users.TouchSession(ctx, found.Session.ID, now)
	}
	return AuthSession{
		User:      found.User,
		SessionID: found.Session.ID,
		ExpiresAt: found.Session.ExpiresAt,
	}, nil
}

func (s *Service) ValidateCSRF(ctx context.Context, sessionID uuid.UUID, token string) error {
	if sessionID == uuid.Nil || strings.TrimSpace(token) == "" {
		return ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	var count int64
	err := s.Repos.DB().WithContext(ctx).Model(&domain.UserSession{}).
		Where("id = ? AND csrf_hash = ? AND expires_at > ?", sessionID, hash[:], s.Now()).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) LogoutUser(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return s.Repos.Users.DeleteSession(ctx, hash[:])
}

func (s *Service) newUserSession(ctx context.Context, user *domain.User) (AuthSession, error) {
	token, tokenHash, err := randomSecret(s.Random)
	if err != nil {
		return AuthSession{}, err
	}
	csrf, csrfHash, err := randomSecret(s.Random)
	if err != nil {
		return AuthSession{}, err
	}
	now := s.Now()
	session := &domain.UserSession{
		UserID:     user.ID,
		TokenHash:  tokenHash,
		CSRFHash:   csrfHash,
		ExpiresAt:  now.Add(userSessionLifetime),
		LastSeenAt: now,
	}
	if err := s.Repos.Users.CreateSession(ctx, session); err != nil {
		return AuthSession{}, err
	}
	return AuthSession{
		User:      *user,
		SessionID: session.ID,
		Token:     token,
		CSRFToken: csrf,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

func randomSecret(source io.Reader) (string, []byte, error) {
	if source == nil {
		source = rand.Reader
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(source, value); err != nil {
		return "", nil, err
	}
	raw := base64.RawURLEncoding.EncodeToString(value)
	hash := sha256.Sum256([]byte(raw))
	return raw, hash[:], nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// normalizeEmail lowercases and validates an address.
//
// net/mail.ParseAddress is the validator rather than a regex, because a regex
// accepts forms that are not deliverable addresses. The equality check then
// rejects display-name forms such as `Name <a@b.c>`, which ParseAddress
// happily accepts but which must never be stored as an identity.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", invalid("email", "is required")
	}
	if len(email) > 254 {
		return "", invalid("email", "must not exceed 254 characters")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return "", invalid("email", "must be a valid email address")
	}
	local, _, found := strings.Cut(email, "@")
	if !found || local == "" || len(local) > 64 {
		return "", invalid("email", "must be a valid email address")
	}
	return email, nil
}

func validatePassword(password string) error {
	if len(password) < 10 || len(password) > 128 {
		return invalid("password", "must contain 10-128 characters")
	}
	return nil
}

// apiKeyTouchInterval keeps last_used_at useful without making every
// authenticated request cost a write.
const apiKeyTouchInterval = 5 * time.Minute

// APIKeyIssued carries the plaintext key. It is returned exactly once, at
// creation, and never stored.
type APIKeyIssued struct {
	Key   domain.APIKey `json:"key"`
	Token string        `json:"token"`
}

// CreateAPIKey issues a per-user bearer credential.
func (s *Service) CreateAPIKey(
	ctx context.Context,
	userID uuid.UUID,
	name string,
	expiresAt *time.Time,
) (APIKeyIssued, error) {
	if userID == uuid.Nil {
		return APIKeyIssued{}, ErrUnauthorized
	}
	name = strings.TrimSpace(name)
	if len(name) > 128 {
		return APIKeyIssued{}, invalid("name", "must not exceed 128 characters")
	}
	token, tokenHash, err := randomSecret(s.Random)
	if err != nil {
		return APIKeyIssued{}, err
	}
	key := &domain.APIKey{
		UserID:    userID,
		Name:      name,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}
	if err := s.Repos.APIKeys.Create(ctx, key); err != nil {
		return APIKeyIssued{}, err
	}
	return APIKeyIssued{Key: *key, Token: token}, nil
}

// AuthenticateAPIKey resolves a presented bearer key to its owning user.
func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) (*domain.User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUnauthorized
	}
	if s.Repos == nil || s.Repos.APIKeys == nil || s.Repos.DB() == nil {
		return nil, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	now := s.Now()
	key, user, err := s.Repos.APIKeys.FindActiveByHash(ctx, hash[:], now)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	// A key belonging to a disabled account must stop working immediately,
	// without waiting for the key itself to be revoked.
	if !user.CanLogin() {
		return nil, ErrUnauthorized
	}
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= apiKeyTouchInterval {
		_ = s.Repos.APIKeys.TouchUsed(ctx, key.ID, now)
	}
	return user, nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, id, userID uuid.UUID) error {
	return s.Repos.APIKeys.Revoke(ctx, id, userID, s.Now())
}

func (s *Service) ListAPIKeys(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	return s.Repos.APIKeys.ListForUser(ctx, userID)
}
