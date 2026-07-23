package application

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

func TestValidateConnectionForToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	active := &domain.MetaConnection{
		Model:               domain.Model{ID: uuid.New()},
		Status:              domain.MetaConnectionActive,
		TokenExpiresAt:      &future,
		DataAccessExpiresAt: &future,
	}
	require.NoError(t, validateConnectionForToken(active, now))

	tokenExpired := *active
	tokenExpiredAt := now
	tokenExpired.TokenExpiresAt = &tokenExpiredAt
	err := validateConnectionForToken(&tokenExpired, now)
	require.ErrorIs(t, err, ErrMetaCredentialsExpired)
	require.ErrorContains(t, err, "token")

	dataAccessExpired := *active
	dataAccessExpiredAt := now.Add(-time.Second)
	dataAccessExpired.TokenExpiresAt = nil
	dataAccessExpired.DataAccessExpiresAt = &dataAccessExpiredAt
	err = validateConnectionForToken(&dataAccessExpired, now)
	require.ErrorIs(t, err, ErrMetaCredentialsExpired)
	require.ErrorContains(t, err, "data access")

	alreadyExpired := *active
	alreadyExpired.Status = domain.MetaConnectionExpired
	err = validateConnectionForToken(&alreadyExpired, now)
	require.ErrorIs(t, err, ErrMetaCredentialsExpired)

	inactive := *active
	inactive.Status = domain.MetaConnectionDisconnected
	err = validateConnectionForToken(&inactive, now)
	require.ErrorIs(t, err, ErrMetaConnectionInactive)
	require.NotErrorIs(t, err, ErrMetaCredentialsExpired)
}

func TestMetaAccessTokenErrorFindsCode190ThroughWrappedAndJoinedErrors(t *testing.T) {
	t.Parallel()

	tokenErr := &meta.GraphError{Code: 190, ErrorSubcode: 463, Message: "session expired"}
	combined := errors.Join(
		&meta.GraphError{Code: 100, Message: "invalid parameter"},
		fmt.Errorf("publish account: %w", tokenErr),
	)

	require.Same(t, tokenErr, metaAccessTokenError(combined))
	require.True(t, isMetaAccessTokenError(combined))
	require.False(t, isMetaAccessTokenError(&meta.GraphError{Code: 200, Message: "permission denied"}))
	require.False(t, isMetaAccessTokenError(nil))
}

func TestMetaAccessTokenErrorIgnoresPageScopedTokenError(t *testing.T) {
	pageTokenErr := &meta.GraphError{
		Code:         190,
		ErrorSubcode: 2069032,
		Message:      "invalid page-scoped access token",
	}
	if got := metaAccessTokenError(pageTokenErr); got != nil {
		t.Fatalf("metaAccessTokenError() = %#v, want nil", got)
	}
}
