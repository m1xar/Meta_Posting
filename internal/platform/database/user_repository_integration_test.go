package database

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

func TestTenantAllowsDuplicateMetaIdentityWithoutVisibilityLeak(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = Close(db) })
	require.NoError(t, RunMigrations(ctx, db, "../../../migrations"))

	repositories := NewRepositories(db)
	firstName := "tenant-" + uuid.NewString()[:8]
	secondName := "tenant-" + uuid.NewString()[:8]
	first := &domain.User{
		Username: firstName, Email: firstName + "@example.test",
		Role: domain.RoleUser, PasswordHash: "disabled",
	}
	second := &domain.User{
		Username: secondName, Email: secondName + "@example.test",
		Role: domain.RoleUser, PasswordHash: "disabled",
	}
	require.NoError(t, repositories.Users.Create(ctx, first))
	require.NoError(t, repositories.Users.Create(ctx, second))
	t.Cleanup(func() {
		_ = db.WithContext(ctx).Delete(&domain.User{}, "id IN ?", []uuid.UUID{first.ID, second.ID}).Error
	})

	for _, user := range []*domain.User{first, second} {
		connection := &domain.MetaConnection{
			UserID: user.ID, MetaUserID: "same-meta-user", DisplayName: user.Username,
			Status: domain.MetaConnectionActive, AccessTokenCiphertext: make([]byte, 17),
			AccessTokenNonce: make([]byte, 12), GrantedScopes: domain.EmptyJSONArray,
			DeclinedScopes: domain.EmptyJSONArray, Metadata: domain.EmptyJSONObject,
		}
		require.NoError(t, repositories.MetaConnections.Upsert(ctx, connection))
	}

	firstConnections, err := repositories.Users.ListConnections(ctx, first.ID)
	require.NoError(t, err)
	secondConnections, err := repositories.Users.ListConnections(ctx, second.ID)
	require.NoError(t, err)
	require.Len(t, firstConnections, 1)
	require.Len(t, secondConnections, 1)
	require.Equal(t, "same-meta-user", firstConnections[0].MetaUserID)
	require.Equal(t, "same-meta-user", secondConnections[0].MetaUserID)
	require.NotEqual(t, firstConnections[0].ID, secondConnections[0].ID)
}
