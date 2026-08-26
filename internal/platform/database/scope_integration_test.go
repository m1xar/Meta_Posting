package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
)

// tenant is one user with a connection, an ad account, an entity and an
// insight row - enough to exercise every scoped read path.
type tenant struct {
	userID       uuid.UUID
	connectionID uuid.UUID
	adAccountID  uuid.UUID
	metaObjectID string
}

func seedTenant(t *testing.T, ctx context.Context, tx *gorm.DB, label string) tenant {
	t.Helper()
	repositories := NewRepositories(tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	userID := fixtureUser(t, ctx, tx)
	connection := &domain.MetaConnection{
		UserID:                userID,
		MetaUserID:            label + "-" + uuid.NewString(),
		DisplayName:           label,
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 32),
		AccessTokenNonce:      make([]byte, 12),
		TokenKeyVersion:       1,
		GrantedScopes:         domain.EmptyJSONArray,
		DeclinedScopes:        domain.EmptyJSONArray,
		Metadata:              domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Create(ctx, connection))

	account := &domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		AccountID:       label,
		Name:            label + " account",
		Currency:        "USD",
		TimezoneName:    "UTC",
		IsActive:        true,
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now,
	}
	require.NoError(t, tx.WithContext(ctx).Create(account).Error)

	metaObjectID := label + "-campaign"
	require.NoError(t, repositories.AdEntities.UpsertMany(ctx, []domain.AdEntity{{
		ConnectionID:    connection.ID,
		AdAccountID:     account.ID,
		Level:           domain.AdEntityCampaign,
		MetaObjectID:    metaObjectID,
		Name:            label + " campaign",
		EffectiveStatus: "ACTIVE",
		RawJSON:         domain.EmptyJSONObject,
		FirstSeenAt:     now,
		LastSeenAt:      now,
	}}))

	require.NoError(t, repositories.AdInsights.UpsertDaily(ctx, []domain.AdInsightDaily{{
		ConnectionID:  connection.ID,
		AdAccountID:   account.ID,
		Level:         domain.InsightCampaign,
		MetaObjectID:  metaObjectID,
		Date:          time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC),
		Spend:         100,
		Actions:       domain.EmptyJSONObject,
		ActionValues:  domain.EmptyJSONObject,
		CostPerAction: domain.EmptyJSONObject,
		Conversions:   domain.EmptyJSONObject,
		ROAS:          domain.EmptyJSONObject,
		Video:         domain.EmptyJSONObject,
		Metrics:       domain.EmptyJSONObject,
		RawJSON:       domain.EmptyJSONObject,
		FetchedAt:     now,
	}}))

	return tenant{
		userID:       userID,
		connectionID: connection.ID,
		adAccountID:  account.ID,
		metaObjectID: metaObjectID,
	}
}

// TestScopeDenialMatrix is the actual RBAC enforcement.
//
// The compiler cannot prove a handler passed the right scope, so this walks
// every scoped read with two tenants and asserts each sees only its own rows.
// Adding a new scoped filter without a row here is the gap to watch for.
func TestScopeDenialMatrix(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := NewRepositories(tx)

	alice := seedTenant(t, ctx, tx, "alice")
	bob := seedTenant(t, ctx, tx, "bob")

	reads := []struct {
		name  string
		count func(Scope) (int64, error)
	}{
		{"connections", func(scope Scope) (int64, error) {
			page, err := repositories.MetaConnections.List(ctx, MetaConnectionFilter{Scope: scope})
			return page.Total, err
		}},
		{"ad_accounts", func(scope Scope) (int64, error) {
			page, err := repositories.Inventory.ListAdAccounts(ctx, AdAccountFilter{Scope: scope})
			return page.Total, err
		}},
		{"ad_entities", func(scope Scope) (int64, error) {
			page, err := repositories.AdEntities.List(ctx, AdEntityFilter{Scope: scope})
			return page.Total, err
		}},
		{"ad_insights_daily", func(scope Scope) (int64, error) {
			page, err := repositories.AdInsights.ListDaily(ctx, AdInsightDailyFilter{Scope: scope})
			return page.Total, err
		}},
	}

	for _, read := range reads {
		t.Run(read.name+"/tenant sees only its own", func(t *testing.T) {
			aliceCount, err := read.count(UserScope(alice.userID))
			require.NoError(t, err)
			require.Equal(t, int64(1), aliceCount)

			bobCount, err := read.count(UserScope(bob.userID))
			require.NoError(t, err)
			require.Equal(t, int64(1), bobCount)
		})

		t.Run(read.name+"/unknown user sees nothing", func(t *testing.T) {
			count, err := read.count(UserScope(uuid.New()))
			require.NoError(t, err)
			require.Zero(t, count)
		})

		t.Run(read.name+"/admin sees across tenants", func(t *testing.T) {
			count, err := read.count(AdminScope())
			require.NoError(t, err)
			require.GreaterOrEqual(t, count, int64(2))
		})

		t.Run(read.name+"/zero scope is denied, not widened", func(t *testing.T) {
			// The whole point of the fail-closed design: a handler that
			// forgets to set a scope must break, not leak.
			_, err := read.count(Scope{})
			require.ErrorIs(t, err, ErrScopeRequired)
		})
	}
}

// TestScopeCannotBeBypassedByExplicitFilters ensures a caller cannot reach
// another tenant by naming its IDs directly.
func TestScopeCannotBeBypassedByExplicitFilters(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := NewRepositories(tx)

	alice := seedTenant(t, ctx, tx, "alice")
	bob := seedTenant(t, ctx, tx, "bob")

	// Alice asks for Bob's ad account by ID. The scope is an AND, not a
	// default, so naming the ID does not widen it.
	page, err := repositories.AdInsights.ListDaily(ctx, AdInsightDailyFilter{
		Scope:       UserScope(alice.userID),
		AdAccountID: &bob.adAccountID,
	})
	require.NoError(t, err)
	require.Zero(t, page.Total, "naming another tenant's ad account must return nothing")

	entities, err := repositories.AdEntities.List(ctx, AdEntityFilter{
		Scope:        UserScope(alice.userID),
		MetaObjectID: bob.metaObjectID,
	})
	require.NoError(t, err)
	require.Zero(t, entities.Total)

	connections, err := repositories.MetaConnections.List(ctx, MetaConnectionFilter{
		Scope:  UserScope(alice.userID),
		UserID: &bob.userID,
	})
	require.NoError(t, err)
	require.Zero(t, connections.Total, "naming another user_id must not widen the scope")
}
