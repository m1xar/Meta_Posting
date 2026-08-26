package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
)

// fixtureUser creates a tenant for a fixture's connection to belong to.
//
// Connections previously defaulted to the legacy tenant through a
// BeforeCreate hook, which hid ownership bugs. The hook is gone, so a
// connection without a UserID now fails its foreign key - in tests exactly as
// in production, which is the point.
func fixtureUser(t *testing.T, ctx context.Context, db *gorm.DB) uuid.UUID {
	t.Helper()
	name := "fixture-" + uuid.NewString()[:8]
	user := &domain.User{
		Username:     name,
		Email:        name + "@example.test",
		Role:         domain.RoleUser,
		PasswordHash: "{fixture}",
	}
	require.NoError(t, db.WithContext(ctx).Create(user).Error)
	return user.ID
}

// isolateJobQueue empties the job queue inside the caller's transaction.
//
// Jobs.Claim deliberately reaches across the whole table - it is a worker
// queue, not a per-tenant view - so a test that claims a job competes with
// whatever else is pending. Against a clean database that is nothing; against
// a restored production snapshot it is hundreds of real jobs, and the test
// claims one of those instead of its own.
//
// The delete lives inside the transaction the caller rolls back, so the real
// queue is never touched.
func isolateJobQueue(t *testing.T, ctx context.Context, tx *gorm.DB) {
	t.Helper()
	require.NoError(t, tx.WithContext(ctx).Exec("DELETE FROM jobs").Error)
}
