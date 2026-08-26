package database

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm/schema"
)

// TestNewModelsMatchTheirTables parses each model the way GORM will at
// runtime and compares the derived column names against what the migrations
// actually created. A mismatch here is otherwise only discovered when a query
// fails in production against a column that does not exist.
func TestNewModelsMatchTheirTables(t *testing.T) {
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

	models := []any{
		&domain.AdEntity{},
		&domain.AdInsightDaily{},
		&domain.AdInsightWindowed{},
		&domain.AdAccountSyncState{},
		&domain.InsightsSyncCursor{},
		&domain.AdInsightCoverage{},
	}

	for _, model := range models {
		parsed, err := schema.Parse(model, &sync.Map{}, db.NamingStrategy)
		require.NoError(t, err)

		var actual []string
		require.NoError(t, db.Raw(`
			SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = ?
		`, parsed.Table).Scan(&actual).Error)
		require.NotEmpty(t, actual, "table %s not found", parsed.Table)

		existing := map[string]bool{}
		for _, column := range actual {
			existing[column] = true
		}

		var missing []string
		for _, field := range parsed.Fields {
			if field.DBName == "" || field.IgnoreMigration {
				continue
			}
			if !existing[field.DBName] {
				missing = append(missing, field.Name+" -> "+field.DBName)
			}
		}
		sort.Strings(missing)
		require.Empty(t, missing, "model %s expects columns that %s does not have", parsed.Name, parsed.Table)
	}
}
