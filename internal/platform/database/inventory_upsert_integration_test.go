package database

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm/schema"
	"sync"
)

// TestAdAccountUpsertRefreshesEveryColumn guards a bug that already happened.
//
// funding_source was added to the model and the schema, discovery fetched it,
// Meta returned a real card - and the column stayed empty on every account,
// because the upsert's DoUpdates list was written by hand and nobody added it.
// The symptom is indistinguishable from "Meta does not return this field",
// which is what made it expensive to find.
func TestAdAccountUpsertRefreshesEveryColumn(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := schema.Parse(&domain.AdAccount{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)

	// Columns that must not be refreshed by a sweep: identity, the conflict
	// key itself, and the creation timestamp.
	exempt := map[string]bool{
		"id": true, "created_at": true,
		"connection_id": true, "meta_ad_account_id": true,
	}
	refreshed := map[string]bool{}
	for _, column := range adAccountUpdateColumns {
		refreshed[column] = true
	}

	var missing []string
	for _, field := range parsed.Fields {
		if field.DBName == "" || exempt[field.DBName] || refreshed[field.DBName] {
			continue
		}
		missing = append(missing, field.DBName)
	}
	sort.Strings(missing)
	require.Empty(t, missing,
		"these columns exist on domain.AdAccount but are never refreshed by "+
			"UpsertAdAccount, so they keep whatever they had at first insert")
}

func TestAdAccountUpsertActuallyUpdatesFunding(t *testing.T) {
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

	userID := fixtureUser(t, ctx, tx)
	connection := &domain.MetaConnection{
		UserID: userID, MetaUserID: "u-" + uuid.NewString(),
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 32), AccessTokenNonce: make([]byte, 12),
		TokenKeyVersion: 1, GrantedScopes: domain.EmptyJSONArray,
		DeclinedScopes: domain.EmptyJSONArray, Metadata: domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Create(ctx, connection))

	metaID := "act_" + uuid.NewString()
	// First sweep: no card attached yet.
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, &domain.AdAccount{
		ConnectionID: connection.ID, MetaAdAccountID: metaID,
		AccountStatus: 1, IsActive: true,
		Capabilities: domain.EmptyJSONArray, UserTasks: domain.EmptyJSONArray,
		FundingSourceDetails: domain.EmptyJSONObject, RawJSON: domain.EmptyJSONObject,
	}))

	// Second sweep: a card appears, exactly as it does when a buyer adds one.
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, &domain.AdAccount{
		ConnectionID: connection.ID, MetaAdAccountID: metaID,
		AccountStatus: 1, IsActive: true,
		Capabilities:  domain.EmptyJSONArray,
		UserTasks:     domain.MustJSON([]string{"DRAFT", "ANALYZE", "ADVERTISE"}),
		FundingSource: "27499622336389955",
		FundingSourceDetails: domain.MustJSON(map[string]any{
			"id": "27499622336389955", "type": 1, "display_string": "VISA *7212",
		}),
		IsPrepayAccount: true,
		RawJSON:         domain.EmptyJSONObject,
	}))

	var stored domain.AdAccount
	require.NoError(t, tx.WithContext(ctx).
		First(&stored, "connection_id = ? AND meta_ad_account_id = ?", connection.ID, metaID).Error)

	require.Equal(t, "27499622336389955", stored.FundingSource)
	require.Contains(t, string(stored.FundingSourceDetails), "VISA *7212")
	require.True(t, stored.IsPrepayAccount)
	require.True(t, stored.CanAdvertise())
	require.True(t, domain.LaunchReadinessFor(&stored).Ready)
}
