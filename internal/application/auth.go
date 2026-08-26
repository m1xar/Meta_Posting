package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const userSessionLifetime = 30 * 24 * time.Hour

var (
	ErrInvalidCredentials = errors.New("invalid login or password")
	ErrSessionExpired     = errors.New("session is missing or expired")
	loginPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	dummyPasswordHash     = []byte("$2a$12$7EqJtq98hPqEX7fNZaFWoO5fPvYB6Jt4fQfN5Qb3H3RZc3Y4M0J9K")
)

type AuthSession struct {
	User      domain.User
	SessionID uuid.UUID
	Token     string
	CSRFToken string
	ExpiresAt time.Time
}

func (s *Service) RegisterUser(ctx context.Context, login, password string) (AuthSession, error) {
	login = normalizeLogin(login)
	if !loginPattern.MatchString(login) {
		return AuthSession{}, invalid("login", "must be 3-64 lowercase letters, numbers, dot, dash, or underscore")
	}
	if err := validatePassword(password); err != nil {
		return AuthSession{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return AuthSession{}, err
	}
	user := &domain.User{Login: login, PasswordHash: string(hash)}
	if err := s.Repos.Users.Create(ctx, user); err != nil {
		return AuthSession{}, err
	}
	return s.newUserSession(ctx, user)
}

func (s *Service) LoginUser(ctx context.Context, login, password string) (AuthSession, error) {
	login = normalizeLogin(login)
	user, err := s.Repos.Users.FindByLogin(ctx, login)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
			return AuthSession{}, ErrInvalidCredentials
		}
		return AuthSession{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return AuthSession{}, ErrInvalidCredentials
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

func normalizeLogin(login string) string {
	return strings.ToLower(strings.TrimSpace(login))
}

func validatePassword(password string) error {
	if len(password) < 10 || len(password) > 128 {
		return invalid("password", "must contain 10-128 characters")
	}
	return nil
}
