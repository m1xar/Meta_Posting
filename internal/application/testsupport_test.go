package application

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// newTestUser creates a real tenant for a fixture to own.
//
// Connections used to default to the legacy tenant via a BeforeCreate hook,
// which quietly hid ownership bugs. With the hook gone, a connection without
// a UserID fails its foreign key - so fixtures must name an owner, exactly as
// production code must.
func newTestUser(t *testing.T, ctx context.Context, repositories *database.Repositories) uuid.UUID {
	t.Helper()
	name := "fixture-" + uuid.NewString()[:8]
	user := &domain.User{
		Username:     name,
		Email:        name + "@example.test",
		Role:         domain.RoleUser,
		PasswordHash: "{fixture}",
	}
	require.NoError(t, repositories.Users.Create(ctx, user))
	t.Cleanup(func() {
		_ = repositories.DB().WithContext(ctx).Delete(&domain.User{}, "id = ?", user.ID).Error
	})
	return user.ID
}
