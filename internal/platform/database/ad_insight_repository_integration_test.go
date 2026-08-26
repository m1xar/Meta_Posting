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

// insightsFixture seeds a connection and one ad account inside a transaction
// that the caller rolls back.
type insightsFixture struct {
	repositories *Repositories
	ownerID      uuid.UUID
	connectionID uuid.UUID
	adAccountID  uuid.UUID
	now          time.Time
}

// scope is the tenant restriction these tests query under. Using a real user
// scope rather than AdminScope means every read here also exercises the
// fail-closed tenancy filter.
func (f insightsFixture) scope() Scope { return UserScope(f.ownerID) }

func newInsightsFixture(t *testing.T, ctx context.Context) (insightsFixture, bool) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
		return insightsFixture{}, false
	}
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	repositories := NewRepositories(tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// A connection must name a real owner now that the legacy-tenant
	// BeforeCreate default is gone.
	ownerID := fixtureUser(t, ctx, tx)
	connection := &domain.MetaConnection{
		UserID:                ownerID,
		MetaUserID:            "test-" + uuid.NewString(),
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 32),
		AccessTokenNonce:      make([]byte, 12),
		TokenKeyVersion:       1,
		GrantedScopes:         domain.MustJSON([]string{"ads_read"}),
		DeclinedScopes:        domain.EmptyJSONArray,
		Metadata:              domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Upsert(ctx, connection))

	account := domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		AccountID:       "1234567890",
		Name:            "Test account",
		Currency:        "USD",
		TimezoneName:    "America/Los_Angeles",
		// 1 = ACTIVE. Incremental polling only considers accounts Meta can
		// still serve, so a fixture must say so explicitly.
		AccountStatus: 1,
		IsActive:      true,
		Capabilities:  domain.EmptyJSONArray,
		RawJSON:       domain.EmptyJSONObject,
		LastSyncedAt:  now,
	}
	require.NoError(t, tx.WithContext(ctx).Create(&account).Error)

	return insightsFixture{
		repositories: repositories,
		ownerID:      ownerID,
		connectionID: connection.ID,
		adAccountID:  account.ID,
		now:          now,
	}, true
}

func utcDay(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (f insightsFixture) row(date time.Time, objectID string, spend float64) domain.AdInsightDaily {
	return domain.AdInsightDaily{
		ConnectionID:       f.connectionID,
		AdAccountID:        f.adAccountID,
		Level:              domain.InsightCampaign,
		MetaObjectID:       objectID,
		MetaAccountID:      "act_1234567890",
		ObjectName:         "Campaign " + objectID,
		Date:               date,
		AccountTimezone:    "America/Los_Angeles",
		Currency:           "USD",
		AttributionSetting: "unified",
		Spend:              spend,
		Impressions:        1000,
		Clicks:             25,
		Actions:            domain.MustJSON(map[string]map[string]float64{"purchase": {"value": 2}}),
		ActionValues:       domain.EmptyJSONObject,
		CostPerAction:      domain.EmptyJSONObject,
		Conversions:        domain.EmptyJSONObject,
		ROAS:               domain.EmptyJSONObject,
		Video:              domain.EmptyJSONObject,
		Metrics:            domain.MustJSON(map[string]float64{"spend": spend}),
		RawJSON:            domain.EmptyJSONObject,
		FetchedAt:          f.now,
	}
}

func TestAdInsightDailyUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdInsights
	date := utcDay(2026, 3, 11)

	require.NoError(t, repo.UpsertDaily(ctx, []domain.AdInsightDaily{fixture.row(date, "c1", 100)}))

	// Re-fetching the same day must rewrite, not duplicate. Backfill, gap
	// repair and the 28-day attribution lookback all re-fetch ranges.
	updated := fixture.row(date, "c1", 175.50)
	updated.Impressions = 2000
	require.NoError(t, repo.UpsertDaily(ctx, []domain.AdInsightDaily{updated}))

	page, err := repo.ListDaily(ctx, AdInsightDailyFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.InDelta(t, 175.50, page.Items[0].Spend, 1e-6)
	require.Equal(t, int64(2000), page.Items[0].Impressions)
}

func TestAdInsightDailySeparatesLevelsAndDates(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdInsights

	adLevel := fixture.row(utcDay(2026, 3, 11), "c1", 10)
	adLevel.Level = domain.InsightAd

	require.NoError(t, repo.UpsertDaily(ctx, []domain.AdInsightDaily{
		fixture.row(utcDay(2026, 3, 11), "c1", 10),
		fixture.row(utcDay(2026, 3, 12), "c1", 20),
		fixture.row(utcDay(2026, 3, 11), "c2", 30),
		adLevel, // same object id and date, different level
	}))

	page, err := repo.ListDaily(ctx, AdInsightDailyFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID})
	require.NoError(t, err)
	require.Equal(t, int64(4), page.Total)

	campaign := domain.InsightCampaign
	page, err = repo.ListDaily(ctx, AdInsightDailyFilter{
		Scope:       fixture.scope(),
		AdAccountID: &fixture.adAccountID,
		Level:       &campaign,
	})
	require.NoError(t, err)
	require.Equal(t, int64(3), page.Total)

	since, until := utcDay(2026, 3, 12), utcDay(2026, 3, 12)
	page, err = repo.ListDaily(ctx, AdInsightDailyFilter{
		Scope:       fixture.scope(),
		AdAccountID: &fixture.adAccountID,
		Since:       &since,
		Until:       &until,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.InDelta(t, 20, page.Items[0].Spend, 1e-6)
}

func TestCoverageDistinguishesQuietDayFromGap(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdInsights
	since, until := utcDay(2026, 3, 10), utcDay(2026, 3, 14)

	// Nothing fetched yet: every day is a gap.
	missing, err := repo.MissingDates(ctx, fixture.adAccountID, domain.InsightCampaign, since, until)
	require.NoError(t, err)
	require.Len(t, missing, 5)

	// Fetch three days. The 11th delivered nothing - a real answer, not a
	// hole - so it is recorded with a zero count and must NOT come back as a
	// gap. Without the coverage table every quiet day is re-fetched forever.
	require.NoError(t, repo.MarkCoverage(ctx, fixture.adAccountID, domain.InsightCampaign, map[time.Time]int{
		utcDay(2026, 3, 10): 4,
		utcDay(2026, 3, 11): 0,
		utcDay(2026, 3, 12): 6,
	}, fixture.now))

	missing, err = repo.MissingDates(ctx, fixture.adAccountID, domain.InsightCampaign, since, until)
	require.NoError(t, err)
	require.Len(t, missing, 2)
	require.Equal(t, utcDay(2026, 3, 13), missing[0].UTC())
	require.Equal(t, utcDay(2026, 3, 14), missing[1].UTC())

	// Coverage is per level.
	missing, err = repo.MissingDates(ctx, fixture.adAccountID, domain.InsightAd, since, until)
	require.NoError(t, err)
	require.Len(t, missing, 5)

	oldest, newest, err := repo.CoverageBounds(ctx, fixture.adAccountID, domain.InsightCampaign)
	require.NoError(t, err)
	require.NotNil(t, oldest)
	require.NotNil(t, newest)
	require.Equal(t, utcDay(2026, 3, 10), oldest.UTC())
	require.Equal(t, utcDay(2026, 3, 12), newest.UTC())
}

func TestCoverageMarkIsIdempotent(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdInsights
	date := utcDay(2026, 3, 10)

	require.NoError(t, repo.MarkCoverage(ctx, fixture.adAccountID, domain.InsightCampaign,
		map[time.Time]int{date: 3}, fixture.now))
	require.NoError(t, repo.MarkCoverage(ctx, fixture.adAccountID, domain.InsightCampaign,
		map[time.Time]int{date: 7}, fixture.now.Add(time.Hour)))

	var coverage domain.AdInsightCoverage
	require.NoError(t, fixture.repositories.DB().WithContext(ctx).
		Where("ad_account_id = ? AND level = ? AND date = ?",
			fixture.adAccountID, domain.InsightCampaign, date).
		First(&coverage).Error)
	require.Equal(t, 7, coverage.RowCount)
}

func TestSyncStateWatermarksMoveInOneDirectionOnly(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdAccountSync
	target := utcDay(2026, 1, 1)

	state, err := repo.Ensure(ctx, fixture.adAccountID, fixture.connectionID, &target)
	require.NoError(t, err)
	require.Equal(t, "unified", state.AttributionSetting)
	require.Nil(t, state.CampaignSyncedThru)

	// Ensure is idempotent and does not reset an existing row.
	_, err = repo.Ensure(ctx, fixture.adAccountID, fixture.connectionID, nil)
	require.NoError(t, err)

	require.NoError(t, repo.AdvanceSyncedThrough(ctx, fixture.adAccountID,
		domain.InsightCampaign, utcDay(2026, 3, 12), fixture.now))
	// A gap-repair job for an older range must not rewind the live watermark.
	require.NoError(t, repo.AdvanceSyncedThrough(ctx, fixture.adAccountID,
		domain.InsightCampaign, utcDay(2026, 3, 5), fixture.now))

	state, err = repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.NotNil(t, state.CampaignSyncedThru)
	require.Equal(t, utcDay(2026, 3, 12), state.CampaignSyncedThru.UTC())

	// Backfill walks the other way, so its watermark only decreases.
	require.NoError(t, repo.SetBackfilledThrough(ctx, fixture.adAccountID, utcDay(2026, 2, 20), fixture.now))
	require.NoError(t, repo.SetBackfilledThrough(ctx, fixture.adAccountID, utcDay(2026, 3, 1), fixture.now))

	state, err = repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.NotNil(t, state.BackfilledThrough)
	require.Equal(t, utcDay(2026, 2, 20), state.BackfilledThrough.UTC())
}

func TestSyncStateFailureAndThrottleTracking(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdAccountSync
	_, err := repo.Ensure(ctx, fixture.adAccountID, fixture.connectionID, nil)
	require.NoError(t, err)

	require.NoError(t, repo.RecordFailure(ctx, fixture.adAccountID, "graph 190", fixture.now))
	require.NoError(t, repo.RecordFailure(ctx, fixture.adAccountID, "graph 190", fixture.now))
	state, err := repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.Equal(t, 2, state.ConsecutiveFailures)
	require.Equal(t, "graph 190", state.LastError)

	// A successful advance clears the failure streak.
	require.NoError(t, repo.AdvanceSyncedThrough(ctx, fixture.adAccountID,
		domain.InsightAccount, utcDay(2026, 3, 12), fixture.now))
	state, err = repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.Zero(t, state.ConsecutiveFailures)
	require.Empty(t, state.LastError)

	until := fixture.now.Add(30 * time.Minute)
	require.NoError(t, repo.RecordUsage(ctx, fixture.adAccountID,
		domain.MustJSON(map[string]any{"call_count": 95}), &until, fixture.now))
	state, err = repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.NotNil(t, state.ThrottledUntil)
	require.Contains(t, string(state.LastUsage), "call_count")

	require.NoError(t, repo.ClearThrottle(ctx, fixture.adAccountID, fixture.now))
	state, err = repo.Get(ctx, fixture.adAccountID)
	require.NoError(t, err)
	require.Nil(t, state.ThrottledUntil)
}

func TestAdEntityUpsertPreservesProvenanceAndRevives(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdEntities

	entity := domain.AdEntity{
		ConnectionID:    fixture.connectionID,
		AdAccountID:     fixture.adAccountID,
		Level:           domain.AdEntityCampaign,
		MetaObjectID:    "c1",
		Name:            "Spring",
		EffectiveStatus: "ACTIVE",
		RawJSON:         domain.EmptyJSONObject,
		FirstSeenAt:     fixture.now,
		LastSeenAt:      fixture.now,
	}
	require.NoError(t, repo.UpsertMany(ctx, []domain.AdEntity{entity}))

	// Provenance is established at publish time; an inventory sweep has no
	// way to know it and must not clear it.
	publishedID := uuid.New()
	require.NoError(t, fixture.repositories.DB().WithContext(ctx).
		Model(&domain.AdEntity{}).
		Where("ad_account_id = ? AND meta_object_id = ?", fixture.adAccountID, "c1").
		Updates(map[string]any{"is_owned": true}).Error)
	_ = publishedID

	renamed := entity
	renamed.Name = "Spring v2"
	renamed.EffectiveStatus = "PAUSED"
	renamed.LastSeenAt = fixture.now.Add(time.Hour)
	require.NoError(t, repo.UpsertMany(ctx, []domain.AdEntity{renamed}))

	campaign := domain.AdEntityCampaign
	page, err := repo.List(ctx, AdEntityFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID, Level: &campaign})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, "Spring v2", page.Items[0].Name)
	require.Equal(t, "PAUSED", page.Items[0].EffectiveStatus)
	require.True(t, page.Items[0].IsOwned, "an inventory sweep must not clear provenance")
	require.Equal(t, fixture.now, page.Items[0].FirstSeenAt.UTC(), "first_seen_at must not move")
}

func TestAdEntitySoftDeleteAndRevival(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdEntities
	base := func(id string) domain.AdEntity {
		return domain.AdEntity{
			ConnectionID: fixture.connectionID,
			AdAccountID:  fixture.adAccountID,
			Level:        domain.AdEntityCampaign,
			MetaObjectID: id,
			Name:         id,
			RawJSON:      domain.EmptyJSONObject,
			FirstSeenAt:  fixture.now,
			LastSeenAt:   fixture.now,
		}
	}
	require.NoError(t, repo.UpsertMany(ctx, []domain.AdEntity{base("c1"), base("c2"), base("c3")}))

	// A sweep that no longer sees c3 soft-deletes it; history is kept because
	// its insight rows stay valid for the days it ran.
	affected, err := repo.MarkDisappeared(ctx, fixture.adAccountID, domain.AdEntityCampaign,
		[]string{"c1", "c2"}, fixture.now.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	campaign := domain.AdEntityCampaign
	live, err := repo.List(ctx, AdEntityFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID, Level: &campaign})
	require.NoError(t, err)
	require.Equal(t, int64(2), live.Total)

	all, err := repo.List(ctx, AdEntityFilter{
		Scope:       fixture.scope(),
		AdAccountID: &fixture.adAccountID, Level: &campaign, IncludeGone: true,
	})
	require.NoError(t, err)
	require.Equal(t, int64(3), all.Total)

	ids, err := repo.ActiveMetaIDs(ctx, fixture.adAccountID, domain.AdEntityCampaign)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"c1", "c2"}, ids)

	// If c3 comes back, it is revived rather than duplicated.
	require.NoError(t, repo.UpsertMany(ctx, []domain.AdEntity{base("c3")}))
	live, err = repo.List(ctx, AdEntityFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID, Level: &campaign})
	require.NoError(t, err)
	require.Equal(t, int64(3), live.Total)
}

func TestDueAdAccountsRotatesAndWraps(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	// Two more accounts, three in total.
	for i := 0; i < 2; i++ {
		account := domain.AdAccount{
			ConnectionID:    fixture.connectionID,
			MetaAdAccountID: "act_" + uuid.NewString(),
			AccountStatus:   1,
			IsActive:        true,
			Capabilities:    domain.EmptyJSONArray,
			RawJSON:         domain.EmptyJSONObject,
			LastSyncedAt:    fixture.now,
		}
		require.NoError(t, fixture.repositories.DB().WithContext(ctx).Create(&account).Error)
	}
	repo := fixture.repositories.InsightsCursors

	first, err := repo.DueAdAccounts(ctx, fixture.connectionID, "ad", 2, fixture.now)
	require.NoError(t, err)
	require.Len(t, first, 2)

	// The rotation continues rather than re-polling the same accounts, and
	// wraps once it runs out.
	second, err := repo.DueAdAccounts(ctx, fixture.connectionID, "ad", 2, fixture.now)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].AdAccountID, second[0].AdAccountID)

	third, err := repo.DueAdAccounts(ctx, fixture.connectionID, "ad", 2, fixture.now)
	require.NoError(t, err)
	require.Len(t, third, 2)
	require.Equal(t, first[0].AdAccountID, third[0].AdAccountID)

	// Cursors are per level, so a slow ad-level rotation does not disturb
	// the cheap account-level sweep.
	other, err := repo.DueAdAccounts(ctx, fixture.connectionID, "account", 2, fixture.now)
	require.NoError(t, err)
	require.Equal(t, first[0].AdAccountID, other[0].AdAccountID)
}

func TestDueAdAccountsSkipsThrottledAccounts(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	_, err := fixture.repositories.AdAccountSync.Ensure(ctx, fixture.adAccountID, fixture.connectionID, nil)
	require.NoError(t, err)

	until := fixture.now.Add(time.Hour)
	require.NoError(t, fixture.repositories.AdAccountSync.RecordUsage(ctx, fixture.adAccountID,
		domain.EmptyJSONObject, &until, fixture.now))

	due, err := fixture.repositories.InsightsCursors.DueAdAccounts(
		ctx, fixture.connectionID, "account", 10, fixture.now)
	require.NoError(t, err)
	require.Empty(t, due, "an account Meta has blocked must not consume a rotation slot")

	// Once the block expires it returns.
	due, err = fixture.repositories.InsightsCursors.DueAdAccounts(
		ctx, fixture.connectionID, "account", 10, until.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, "America/Los_Angeles", due[0].TimezoneName)
}

func TestDeleteDailyBeforeRetention(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	repo := fixture.repositories.AdInsights
	require.NoError(t, repo.UpsertDaily(ctx, []domain.AdInsightDaily{
		fixture.row(utcDay(2026, 1, 1), "c1", 1),
		fixture.row(utcDay(2026, 3, 1), "c1", 2),
		fixture.row(utcDay(2026, 3, 12), "c1", 3),
	}))

	deleted, err := repo.DeleteDailyBefore(ctx, utcDay(2026, 3, 1), 100)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	page, err := repo.ListDaily(ctx, AdInsightDailyFilter{Scope: fixture.scope(), AdAccountID: &fixture.adAccountID})
	require.NoError(t, err)
	require.Equal(t, int64(2), page.Total)
}

var _ = gorm.ErrRecordNotFound

func TestIncrementalPollingSkipsAccountsMetaCannotServe(t *testing.T) {
	ctx := context.Background()
	fixture, ok := newInsightsFixture(t, ctx)
	if !ok {
		return
	}
	db := fixture.repositories.DB()

	// A real profile produced 183 disabled accounts out of 244. Polling those
	// every fifteen minutes is three quarters of the request budget spent on
	// a guaranteed empty answer.
	statuses := map[string]int{"disabled": 2, "closed": 101, "grace": 9, "unsettled": 3}
	for name, status := range statuses {
		account := domain.AdAccount{
			ConnectionID:    fixture.connectionID,
			MetaAdAccountID: "act_" + name + "_" + uuid.NewString(),
			AccountStatus:   status,
			IsActive:        true,
			Capabilities:    domain.EmptyJSONArray,
			RawJSON:         domain.EmptyJSONObject,
			LastSyncedAt:    fixture.now,
		}
		require.NoError(t, db.WithContext(ctx).Create(&account).Error)
	}
	pollable, err := fixture.repositories.InsightsCursors.AllAdAccounts(
		ctx, fixture.connectionID, fixture.now)
	require.NoError(t, err)
	// active + grace + unsettled, never disabled or closed.
	require.Len(t, pollable, 3)

	// Backfill still covers everything: a disabled account's spend already
	// happened and is worth storing once.
	all, err := fixture.repositories.InsightsCursors.AllAdAccountsForBackfill(ctx, fixture.connectionID)
	require.NoError(t, err)
	require.Len(t, all, 5)
}
