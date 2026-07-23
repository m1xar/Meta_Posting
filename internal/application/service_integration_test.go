package application

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	platformcrypto "github.com/watchers-factory/raze-posting/internal/platform/crypto"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
)

func TestAccessTokenExpiryAndGraph190MarkConnectionExpired(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	cipher, err := platformcrypto.NewAESGCM([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	repositories := database.NewRepositories(tx)
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := &Service{
		Repos:  repositories,
		Cipher: cipher,
		Now:    func() time.Time { return now },
	}

	createConnection := func(tokenExpiresAt, dataAccessExpiresAt *time.Time) *domain.MetaConnection {
		metaUserID := "expiry-test-" + uuid.NewString()
		encrypted, encryptErr := cipher.Encrypt([]byte("secret-token"), []byte(metaUserID))
		require.NoError(t, encryptErr)
		connection := &domain.MetaConnection{
			MetaUserID:            metaUserID,
			DisplayName:           "Expiry test",
			Status:                domain.MetaConnectionActive,
			AccessTokenCiphertext: encrypted.Ciphertext,
			AccessTokenNonce:      encrypted.Nonce,
			TokenKeyVersion:       encrypted.KeyVersion,
			TokenExpiresAt:        tokenExpiresAt,
			DataAccessExpiresAt:   dataAccessExpiresAt,
			GrantedScopes:         domain.EmptyJSONArray,
			DeclinedScopes:        domain.EmptyJSONArray,
			Metadata:              domain.EmptyJSONObject,
		}
		require.NoError(t, repositories.MetaConnections.Create(ctx, connection))
		return connection
	}

	future := now.Add(time.Hour)
	active := createConnection(&future, &future)
	_, token, err := service.accessToken(ctx, active.ID)
	require.NoError(t, err)
	require.Equal(t, "secret-token", token)

	past := now.Add(-time.Second)
	locallyExpired := createConnection(&past, &future)
	_, _, err = service.accessToken(ctx, locallyExpired.ID)
	require.ErrorIs(t, err, ErrMetaCredentialsExpired)
	locallyExpired, err = repositories.MetaConnections.Get(ctx, locallyExpired.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MetaConnectionExpired, locallyExpired.Status)

	graphExpired := createConnection(&future, &future)
	classified, err := service.markConnectionExpiredForMetaError(
		ctx,
		graphExpired.ID,
		errors.Join(
			&meta.GraphError{Code: 100, Message: "bad parameter"},
			&meta.GraphError{Code: 190, ErrorSubcode: 463, Message: "session expired"},
		),
	)
	require.True(t, classified)
	require.NoError(t, err)
	graphExpired, err = repositories.MetaConnections.Get(ctx, graphExpired.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MetaConnectionExpired, graphExpired.Status)
	require.Contains(t, graphExpired.LastError, "190/463")

	notExpired := createConnection(&future, &future)
	classified, err = service.markConnectionExpiredForMetaError(
		ctx,
		notExpired.ID,
		&meta.GraphError{Code: 200, Message: "permission denied"},
	)
	require.False(t, classified)
	require.NoError(t, err)
	notExpired, err = repositories.MetaConnections.Get(ctx, notExpired.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MetaConnectionActive, notExpired.Status)
}
