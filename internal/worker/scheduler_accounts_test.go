package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// TestPerAccountJobsAreNotRecurringJobs guards the sharpest trap in this
// schema.
//
// migration 00003 creates uq_jobs_recurring_active, a partial unique index on
// (connection_id, type) covering the types isRecurringJob returns true for.
// A per-ad-account type added to that set would collapse every ad account of
// a connection into a single job: nine accounts out of ten would silently
// stop being polled, with no error anywhere.
func TestPerAccountJobsAreNotRecurringJobs(t *testing.T) {
	for _, jobType := range []string{
		application.JobSyncAdEntities,
		application.JobCollectAccountInsights,
		application.JobBackfillInsights,
		application.JobRepairInsightGaps,
		application.JobCollectWindowedReach,
		application.JobRetentionSweep,
	} {
		require.False(t, isRecurringJob(jobType),
			"%s is per-account: adding it to isRecurringJob, or to migration 00003's "+
				"uq_jobs_recurring_active, would coalesce every ad account of a "+
				"connection into one job", jobType)
	}

	// The connection-scoped ones must stay recurring.
	require.True(t, isRecurringJob(application.JobCollectInsights))
	require.True(t, isRecurringJob(application.JobEvaluateGuards))
}

func TestStaggerDelaySpreadsJobsAcrossTheInterval(t *testing.T) {
	interval := 15 * time.Minute

	// Without a stagger every account's job is available at the same instant
	// and the pool fires them as one burst - the shape that trips per-app
	// rate limits.
	require.Zero(t, staggerDelay(0, 10, interval))
	require.Equal(t, 90*time.Second, staggerDelay(1, 10, interval))
	require.Equal(t, 9*90*time.Second, staggerDelay(9, 10, interval))

	// The last job still lands inside the interval, so the next cycle does
	// not overlap this one.
	require.Less(t, staggerDelay(9, 10, interval), interval)

	require.Zero(t, staggerDelay(0, 1, interval))
	require.Zero(t, staggerDelay(3, 1, interval))
	require.Zero(t, staggerDelay(1, 10, 0))
}

// fakeAccountStore records what was enqueued.
type fakeAccountStore struct {
	connections []uuid.UUID
	accounts    []database.ScheduledAdAccount
	dueCalls    []string
	enqueued    []*domain.Job
}

func (f *fakeAccountStore) ActiveConnectionIDs(context.Context) ([]uuid.UUID, error) {
	return f.connections, nil
}

func (f *fakeAccountStore) DueAdAccounts(
	_ context.Context, _ uuid.UUID, level string, limit int, _ time.Time,
) ([]database.ScheduledAdAccount, error) {
	f.dueCalls = append(f.dueCalls, level)
	if limit > len(f.accounts) {
		limit = len(f.accounts)
	}
	return f.accounts[:limit], nil
}

func (f *fakeAccountStore) AllAdAccounts(
	_ context.Context, _ uuid.UUID, _ time.Time,
) ([]database.ScheduledAdAccount, error) {
	f.dueCalls = append(f.dueCalls, "all")
	return f.accounts, nil
}

func (f *fakeAccountStore) Enqueue(_ context.Context, job *domain.Job) (*domain.Job, bool, error) {
	f.enqueued = append(f.enqueued, job)
	return job, true, nil
}

func (f *fakeAccountStore) ExpirePendingOAuth(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newAccountScheduler(t *testing.T, store *fakeAccountStore) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(store, SchedulerOptions{
		InsightsInterval:    15 * time.Minute,
		RuleInterval:        15 * time.Minute,
		MaintenanceInterval: time.Minute,
		MaxAttempts:         5,
		Now:                 func() time.Time { return time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	return scheduler.WithAccountStore(store)
}

func testAccounts(count int, timezone string) []database.ScheduledAdAccount {
	accounts := make([]database.ScheduledAdAccount, count)
	for index := range accounts {
		accounts[index] = database.ScheduledAdAccount{
			AdAccountID:     uuid.New(),
			ConnectionID:    uuid.New(),
			MetaAdAccountID: "act_100",
			AccountID:       "100",
			TimezoneName:    timezone,
		}
	}
	return accounts
}

func TestScheduleAccountInsightsFreezesRangeAndStaggers(t *testing.T) {
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(3, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level:        domain.InsightCampaign,
		Interval:     30 * time.Minute,
		SinceDaysAgo: 1,
		Priority:     16,
	}))
	require.Len(t, store.enqueued, 3)

	var payload application.AccountInsightsJobPayload
	require.NoError(t, store.enqueued[0].Payload.Decode(&payload))
	// The range is resolved by the scheduler and frozen, so a retry hours
	// later re-fetches the same days and the upsert stays deterministic.
	require.Equal(t, "2026-03-11", payload.Since)
	require.Equal(t, "2026-03-12", payload.Until)
	require.Equal(t, domain.InsightCampaign, payload.Level)
	require.Equal(t, "incremental", payload.Reason)

	require.Equal(t, now.UTC(), store.enqueued[0].AvailableAt)
	require.True(t, store.enqueued[1].AvailableAt.After(store.enqueued[0].AvailableAt))
	require.True(t, store.enqueued[2].AvailableAt.After(store.enqueued[1].AvailableAt))
}

func TestScheduleAccountInsightsUsesTheAccountTimezone(t *testing.T) {
	// 03:00 UTC on the 12th is still the 11th in Los Angeles, so that account
	// must be asked for the 10th-11th, not the 11th-12th.
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(1, "America/Los_Angeles"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 3, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level:        domain.InsightAccount,
		Interval:     15 * time.Minute,
		SinceDaysAgo: 1,
	}))

	var payload application.AccountInsightsJobPayload
	require.NoError(t, store.enqueued[0].Payload.Decode(&payload))
	require.Equal(t, "2026-03-10", payload.Since)
	require.Equal(t, "2026-03-11", payload.Until)
}

func TestDedupeKeysAreScopedPerAccountAndLevel(t *testing.T) {
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(2, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level: domain.InsightCampaign, Interval: 30 * time.Minute, SinceDaysAgo: 1,
	}))
	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level: domain.InsightAdSet, Interval: 2 * time.Hour, SinceDaysAgo: 1,
	}))

	keys := map[string]bool{}
	for _, job := range store.enqueued {
		require.NotNil(t, job.DedupeKey)
		require.False(t, keys[*job.DedupeKey], "duplicate dedupe key %s", *job.DedupeKey)
		keys[*job.DedupeKey] = true
	}
	require.Len(t, keys, 4, "two accounts at two levels must not collide")

	// Re-running the same pass inside the interval produces the same keys,
	// so the queue collapses them instead of double-polling.
	before := len(store.enqueued)
	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now.Add(time.Minute),
		AccountLevelPlan{Level: domain.InsightCampaign, Interval: 30 * time.Minute, SinceDaysAgo: 1}))
	for _, job := range store.enqueued[before:] {
		require.True(t, keys[*job.DedupeKey], "a repeat pass must reuse its dedupe key")
	}
}

func TestAdLevelRotatesWhileCheapLevelsPollEveryAccount(t *testing.T) {
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(5, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level: domain.InsightAccount, Interval: 15 * time.Minute, SinceDaysAgo: 1,
	}))
	require.Len(t, store.enqueued, 5, "the cheap level polls every account")
	require.Equal(t, []string{"all"}, store.dueCalls)

	store.enqueued = nil
	store.dueCalls = nil
	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level: domain.InsightAd, Interval: 6 * time.Hour, BatchSize: 2, SinceDaysAgo: 1,
	}))
	require.Len(t, store.enqueued, 2, "the expensive level takes a slice")
	require.Equal(t, []string{"ad"}, store.dueCalls)
}

func TestScheduleAdEntitySyncUsesItsOwnCursor(t *testing.T) {
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(2, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleAdEntitySync(context.Background(), now, 6*time.Hour, 2, 0))
	require.Len(t, store.enqueued, 2)
	// A separate cursor level, so a slow ad-level rotation does not disturb
	// inventory refresh.
	require.Equal(t, []string{domain.CursorLevelEntities}, store.dueCalls)
	require.Equal(t, application.JobSyncAdEntities, store.enqueued[0].Type)
}

func TestLookbackSchedulesTheAttributionWindow(t *testing.T) {
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(1, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleInsightsLookback(
		context.Background(), now, domain.InsightCampaign, 28, 24*time.Hour, 0))
	require.Len(t, store.enqueued, 1)

	var payload application.AccountInsightsJobPayload
	require.NoError(t, store.enqueued[0].Payload.Decode(&payload))
	// 28 days back, because 28d_click is Meta's longest standard window.
	require.Equal(t, "2026-02-12", payload.Since)
	require.Equal(t, "2026-03-12", payload.Until)
	require.Equal(t, "lookback", payload.Reason)
	require.True(t, strings.Contains(*store.enqueued[0].DedupeKey, "lookback"))
}

func TestRetentionSweepIsConnectionIndependent(t *testing.T) {
	store := &fakeAccountStore{connections: []uuid.UUID{uuid.New()}}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleRetentionSweep(
		context.Background(), now, 90*24*time.Hour, time.Hour))
	require.Len(t, store.enqueued, 1)
	require.Nil(t, store.enqueued[0].ConnectionID)

	var payload application.RetentionSweepJobPayload
	require.NoError(t, store.enqueued[0].Payload.Decode(&payload))
	require.Equal(t, now.Add(-90*24*time.Hour), payload.Before)
}

func TestAccountSchedulingIsDisabledWithoutAnAccountStore(t *testing.T) {
	// A deployment that only publishes must not be forced into tracking.
	store := &fakeAccountStore{connections: []uuid.UUID{uuid.New()}}
	scheduler, err := NewScheduler(store, SchedulerOptions{
		InsightsInterval: time.Minute, RuleInterval: time.Minute, MaxAttempts: 5,
	})
	require.NoError(t, err)

	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), time.Now(),
		AccountLevelPlan{Level: domain.InsightAccount, Interval: time.Minute}))
	require.Empty(t, store.enqueued)
}

func TestInitialAccountPassDoesNotWaitOutAnInterval(t *testing.T) {
	// Ad-level and entity-sync intervals are hours. Without an initial pass a
	// restart produces no entity inventory for six hours, so the first
	// insight rows would land with no campaign names to attach them to.
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(2, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	scheduler.options.EntitySyncInterval = 6 * time.Hour
	scheduler.options.AdLevelBatchSize = 25
	scheduler.options.AccountLevelPlans = []AccountLevelPlan{
		{Level: domain.InsightAccount, Interval: 15 * time.Minute, SinceDaysAgo: 1},
		{Level: domain.InsightAd, Interval: 6 * time.Hour, BatchSize: 25, SinceDaysAgo: 1},
	}

	scheduler.runInitialAccountPass(context.Background(), time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC))

	types := map[string]int{}
	for _, job := range store.enqueued {
		types[job.Type]++
	}
	require.Equal(t, 2, types[application.JobSyncAdEntities], "inventory must be scheduled immediately")
	require.Equal(t, 4, types[application.JobCollectAccountInsights], "both levels, both accounts")
}

func TestInitialAccountPassIsSkippedWithoutAnAccountStore(t *testing.T) {
	store := &fakeAccountStore{connections: []uuid.UUID{uuid.New()}}
	scheduler, err := NewScheduler(store, SchedulerOptions{
		InsightsInterval: time.Minute, RuleInterval: time.Minute, MaxAttempts: 5,
	})
	require.NoError(t, err)
	scheduler.runInitialAccountPass(context.Background(), time.Now())
	require.Empty(t, store.enqueued)
}

func TestStartupPassSpreadsOverMinutesNotHours(t *testing.T) {
	// Ad level runs every six hours. Staggering the startup pass across that
	// interval would leave the last account waiting most of a working day
	// before its first poll, on every deploy.
	store := &fakeAccountStore{
		connections: []uuid.UUID{uuid.New()},
		accounts:    testAccounts(4, "UTC"),
	}
	scheduler := newAccountScheduler(t, store)
	scheduler.options.EntitySyncInterval = 6 * time.Hour
	scheduler.options.AdLevelBatchSize = 25
	scheduler.options.AccountLevelPlans = []AccountLevelPlan{
		{Level: domain.InsightAd, Interval: 6 * time.Hour, BatchSize: 25, SinceDaysAgo: 1},
	}
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	scheduler.runInitialAccountPass(context.Background(), now)
	require.NotEmpty(t, store.enqueued)

	for _, job := range store.enqueued {
		require.LessOrEqual(t, job.AvailableAt.Sub(now), startupStaggerWindow,
			"startup work must not be pushed beyond the startup window")
	}

	// Steady state still spreads across the full interval, so the burst
	// protection is unchanged.
	store.enqueued = nil
	require.NoError(t, scheduler.ScheduleAccountInsights(context.Background(), now, AccountLevelPlan{
		Level: domain.InsightAd, Interval: 6 * time.Hour, BatchSize: 25, SinceDaysAgo: 1,
	}))
	last := store.enqueued[len(store.enqueued)-1]
	require.Greater(t, last.AvailableAt.Sub(now), startupStaggerWindow)
}

func TestConnectionDiscoveryIsScheduledRecurringly(t *testing.T) {
	// Discovery used to run only at OAuth time, so an ad account granted to a
	// buyer afterwards stayed invisible until somebody pressed Sync by hand -
	// and account_status went stale, which the incremental poller relies on.
	store := &fakeAccountStore{connections: []uuid.UUID{uuid.New(), uuid.New()}}
	scheduler := newAccountScheduler(t, store)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleConnectionDiscovery(context.Background(), now, 6*time.Hour))
	require.Len(t, store.enqueued, 2)

	for _, job := range store.enqueued {
		require.Equal(t, application.JobSyncConnection, job.Type)
		require.NotNil(t, job.ConnectionID)
		require.NotNil(t, job.DedupeKey)
		require.Contains(t, *job.DedupeKey, "discovery")
	}
	// Discovery walks every ad account's assets, so two profiles must not
	// start at the same instant.
	require.NotEqual(t, store.enqueued[0].AvailableAt, store.enqueued[1].AvailableAt)

	// Re-running inside the same bucket reuses the key, so the queue
	// collapses it rather than discovering twice.
	before := len(store.enqueued)
	require.NoError(t, scheduler.ScheduleConnectionDiscovery(context.Background(), now.Add(time.Minute), 6*time.Hour))
	require.Equal(t, *store.enqueued[0].DedupeKey, *store.enqueued[before].DedupeKey)
}

func TestDiscoveryStaysOutOfIsRecurringJob(t *testing.T) {
	// sync_connection is connection-scoped, so it would be legal in
	// uq_jobs_recurring_active - but adding it there would make a manual Sync
	// silently collapse into a scheduled one that is hours away.
	require.False(t, isRecurringJob(application.JobSyncConnection))
}
