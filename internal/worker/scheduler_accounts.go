package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// AccountScheduleStore supplies the ad accounts due for a level. It is split
// from ScheduleStore so tests can drive account scheduling without standing
// up connection scheduling too.
type AccountScheduleStore interface {
	ActiveConnectionIDs(context.Context) ([]uuid.UUID, error)
	DueAdAccounts(ctx context.Context, connectionID uuid.UUID, level string, limit int, now time.Time) ([]database.ScheduledAdAccount, error)
	AllAdAccounts(ctx context.Context, connectionID uuid.UUID, now time.Time) ([]database.ScheduledAdAccount, error)
	Enqueue(context.Context, *domain.Job) (*domain.Job, bool, error)
}

// RepositoryAccountScheduleStore adapts the repositories.
type RepositoryAccountScheduleStore struct {
	RepositoryScheduleStore
}

func (s RepositoryAccountScheduleStore) DueAdAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	level string,
	limit int,
	now time.Time,
) ([]database.ScheduledAdAccount, error) {
	if s.Repositories == nil || s.Repositories.InsightsCursors == nil {
		return nil, errors.New("worker: insights cursor repository is not initialized")
	}
	return s.Repositories.InsightsCursors.DueAdAccounts(ctx, connectionID, level, limit, now)
}

func (s RepositoryAccountScheduleStore) ConnectionsWithFastRules(
	ctx context.Context,
	maxIntervalSeconds int64,
	now time.Time,
) ([]uuid.UUID, error) {
	if s.Repositories == nil || s.Repositories.InsightsCursors == nil {
		return nil, errors.New("worker: insights cursor repository is not initialized")
	}
	return s.Repositories.InsightsCursors.ConnectionsWithFastRules(ctx, maxIntervalSeconds, now)
}

func (s RepositoryAccountScheduleStore) AllAdAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	now time.Time,
) ([]database.ScheduledAdAccount, error) {
	if s.Repositories == nil || s.Repositories.InsightsCursors == nil {
		return nil, errors.New("worker: insights cursor repository is not initialized")
	}
	return s.Repositories.InsightsCursors.AllAdAccounts(ctx, connectionID, now)
}

// AccountLevelPlan describes how one insights level is polled.
type AccountLevelPlan struct {
	Level domain.InsightLevel
	// Interval doubles as the dedupe bucket width, so a level cannot be
	// scheduled twice within one interval however often the tick fires.
	Interval time.Duration
	// BatchSize > 0 rotates through accounts a slice at a time. Zero polls
	// every account each cycle, which is only affordable for the cheap levels.
	BatchSize int
	// SinceDaysAgo is how far back each poll reaches. One means "yesterday and
	// today", which repairs a day whose late-evening numbers were still moving
	// at the last poll before midnight.
	SinceDaysAgo int
	Priority     int

	// StaggerWindow overrides the spread used for AvailableAt. It exists for
	// the startup pass: spreading across a six-hour interval is right in
	// steady state but means a fresh deploy trickles work in over hours.
	StaggerWindow time.Duration
}

// staggerWindow is the interval unless an override is set.
func (p AccountLevelPlan) staggerWindow() time.Duration {
	if p.StaggerWindow > 0 {
		return p.StaggerWindow
	}
	return p.Interval
}

// staggerDelay spreads a batch of jobs across the polling interval.
//
// Without it every account's job is available at the same instant and the
// worker pool fires them as one burst, which is exactly the shape that trips
// per-app rate limits. Ported from Meta_Tracking, which learned this the
// expensive way.
func staggerDelay(index, total int, interval time.Duration) time.Duration {
	if index <= 0 || total <= 1 || interval <= 0 {
		return 0
	}
	return time.Duration(index) * (interval / time.Duration(total))
}

// ScheduleAccountInsights enqueues one insights job per due ad account.
func (s *Scheduler) ScheduleAccountInsights(
	ctx context.Context,
	now time.Time,
	plan AccountLevelPlan,
) error {
	if s.accounts == nil {
		return nil
	}
	connectionIDs, err := s.accounts.ActiveConnectionIDs(ctx)
	if err != nil {
		return fmt.Errorf("list active connections: %w", err)
	}
	bucket := scheduleBucket(now, plan.Interval)
	var failures []error

	for _, connectionID := range connectionIDs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		accounts, err := s.dueAccounts(ctx, connectionID, string(plan.Level), plan.BatchSize, now)
		if err != nil {
			failures = append(failures, fmt.Errorf("due accounts for %s: %w", connectionID, err))
			continue
		}
		for index, account := range accounts {
			window, rangeErr := meta.AccountRange(now, account.TimezoneName, plan.SinceDaysAgo, 0)
			if rangeErr != nil {
				failures = append(failures, rangeErr)
				continue
			}
			payload := application.AccountInsightsJobPayload{
				AdAccountID: account.AdAccountID,
				Level:       plan.Level,
				Since:       window.Since,
				Until:       window.Until,
				Reason:      "incremental",
			}
			dedupeKey := fmt.Sprintf("%s:%s:%s", account.AdAccountID, plan.Level, bucket)
			if err := s.enqueueAccountJob(
				ctx,
				account,
				application.JobCollectAccountInsights,
				dedupeKey,
				plan.Priority,
				domain.MustJSON(payload),
				now.Add(staggerDelay(index, len(accounts), plan.staggerWindow())),
			); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// ScheduleInsightsLookback re-fetches the trailing attribution window.
//
// Meta keeps restating a day's numbers as attribution windows close, so a day
// polled once at the live edge is not final. This pass re-reads the last N
// days; because storage is a daily upsert, it costs one row rewrite per
// object per day and no deduplication logic.
func (s *Scheduler) ScheduleInsightsLookback(
	ctx context.Context,
	now time.Time,
	level domain.InsightLevel,
	lookbackDays int,
	interval time.Duration,
	batchSize int,
) error {
	if s.accounts == nil || lookbackDays <= 0 {
		return nil
	}
	connectionIDs, err := s.accounts.ActiveConnectionIDs(ctx)
	if err != nil {
		return fmt.Errorf("list active connections: %w", err)
	}
	bucket := scheduleBucket(now, interval)
	var failures []error

	for _, connectionID := range connectionIDs {
		accounts, err := s.dueAccounts(ctx, connectionID, string(level)+":lookback", batchSize, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for index, account := range accounts {
			window, rangeErr := meta.AccountRange(now, account.TimezoneName, lookbackDays, 0)
			if rangeErr != nil {
				failures = append(failures, rangeErr)
				continue
			}
			payload := application.AccountInsightsJobPayload{
				AdAccountID: account.AdAccountID,
				Level:       level,
				Since:       window.Since,
				Until:       window.Until,
				Reason:      "lookback",
			}
			dedupeKey := fmt.Sprintf("%s:%s:lookback:%s", account.AdAccountID, level, bucket)
			if err := s.enqueueAccountJob(
				ctx,
				account,
				application.JobCollectAccountInsights,
				dedupeKey,
				gapRepairPriority,
				domain.MustJSON(payload),
				now.Add(staggerDelay(index, len(accounts), interval)),
			); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// ScheduleAdEntitySync refreshes account inventories.
func (s *Scheduler) ScheduleAdEntitySync(
	ctx context.Context,
	now time.Time,
	interval time.Duration,
	batchSize int,
	staggerWindow time.Duration,
) error {
	if staggerWindow <= 0 {
		staggerWindow = interval
	}
	if s.accounts == nil {
		return nil
	}
	connectionIDs, err := s.accounts.ActiveConnectionIDs(ctx)
	if err != nil {
		return fmt.Errorf("list active connections: %w", err)
	}
	bucket := scheduleBucket(now, interval)
	var failures []error

	for _, connectionID := range connectionIDs {
		accounts, err := s.dueAccounts(ctx, connectionID, domain.CursorLevelEntities, batchSize, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for index, account := range accounts {
			dedupeKey := fmt.Sprintf("%s:entities:%s", account.AdAccountID, bucket)
			if err := s.enqueueAccountJob(
				ctx,
				account,
				application.JobSyncAdEntities,
				dedupeKey,
				entitySyncPriority,
				domain.MustJSON(application.AdEntitiesJobPayload{AdAccountID: account.AdAccountID}),
				now.Add(staggerDelay(index, len(accounts), staggerWindow)),
			); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

// ScheduleRetentionSweep enqueues one trim per tick, connection-independent.
func (s *Scheduler) ScheduleRetentionSweep(
	ctx context.Context,
	now time.Time,
	retention time.Duration,
	interval time.Duration,
) error {
	if s.accounts == nil || retention <= 0 {
		return nil
	}
	bucket := scheduleBucket(now, interval)
	dedupeKey := "retention:" + bucket
	_, _, err := s.accounts.Enqueue(ctx, &domain.Job{
		Type:        application.JobRetentionSweep,
		Status:      domain.JobPending,
		Priority:    retentionPriority,
		Payload:     domain.MustJSON(application.RetentionSweepJobPayload{Before: now.Add(-retention)}),
		DedupeKey:   &dedupeKey,
		MaxAttempts: s.options.MaxAttempts,
		AvailableAt: now.UTC(),
	})
	return err
}

func (s *Scheduler) dueAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	level string,
	batchSize int,
	now time.Time,
) ([]database.ScheduledAdAccount, error) {
	if batchSize > 0 {
		return s.accounts.DueAdAccounts(ctx, connectionID, level, batchSize, now)
	}
	return s.accounts.AllAdAccounts(ctx, connectionID, now)
}

func (s *Scheduler) enqueueAccountJob(
	ctx context.Context,
	account database.ScheduledAdAccount,
	jobType string,
	dedupeKey string,
	priority int,
	payload domain.JSON,
	availableAt time.Time,
) error {
	connectionID := account.ConnectionID
	_, _, err := s.accounts.Enqueue(ctx, &domain.Job{
		ConnectionID: &connectionID,
		Type:         jobType,
		Status:       domain.JobPending,
		Priority:     priority,
		Payload:      payload,
		DedupeKey:    &dedupeKey,
		MaxAttempts:  s.options.MaxAttempts,
		AvailableAt:  availableAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("enqueue %s for account %s: %w", jobType, account.AdAccountID, err)
	}
	return nil
}

const (
	discoveryPriority  = 15
	entitySyncPriority = 12
	gapRepairPriority  = 8
	retentionPriority  = 1
)

// ScheduleConnectionDiscovery re-runs ad account discovery for every active
// connection.
//
// Discovery previously ran only at OAuth time, which meant an ad account
// granted to a buyer after they connected stayed invisible until somebody
// pressed Sync by hand. It is also what refreshes account_status, so without
// it an account Meta disabled - or re-enabled - keeps its stale state and the
// incremental poller keeps making the wrong decision about whether to poll it.
func (s *Scheduler) ScheduleConnectionDiscovery(
	ctx context.Context,
	now time.Time,
	interval time.Duration,
) error {
	if s.accounts == nil {
		return nil
	}
	connectionIDs, err := s.accounts.ActiveConnectionIDs(ctx)
	if err != nil {
		return fmt.Errorf("list active connections: %w", err)
	}
	bucket := scheduleBucket(now, interval)
	var failures []error

	for index, connectionID := range connectionIDs {
		connection := connectionID
		dedupeKey := fmt.Sprintf("%s:discovery:%s", connection, bucket)
		// Discovery walks every ad account's assets, so two profiles starting
		// at the same instant is a burst worth avoiding.
		availableAt := now.Add(staggerDelay(index, len(connectionIDs), interval))
		if _, _, err := s.accounts.Enqueue(ctx, &domain.Job{
			ConnectionID: &connection,
			Type:         application.JobSyncConnection,
			Status:       domain.JobPending,
			Priority:     discoveryPriority,
			Payload:      domain.MustJSON(application.SyncJobPayload{ConnectionID: connection}),
			DedupeKey:    &dedupeKey,
			MaxAttempts:  s.options.MaxAttempts,
			AvailableAt:  availableAt.UTC(),
		}); err != nil {
			failures = append(failures, fmt.Errorf("enqueue discovery for %s: %w", connection, err))
		}
	}
	return errors.Join(failures...)
}

// ScheduleFastLane drives rules that ask to run faster than the standard
// cadence.
//
// It schedules both halves, because either alone is useless: evaluating a
// 60-second rule against insights collected fifteen minutes ago just makes a
// stale decision more often. Both jobs are coalesced by the recurring-job
// index, so a slow collection cannot stack behind itself.
func (s *Scheduler) ScheduleFastLane(
	ctx context.Context,
	now time.Time,
	interval time.Duration,
	maxRuleIntervalSeconds int64,
) error {
	if s.accounts == nil || interval <= 0 {
		return nil
	}
	store, ok := s.accounts.(fastLaneStore)
	if !ok {
		return nil
	}
	connectionIDs, err := store.ConnectionsWithFastRules(ctx, maxRuleIntervalSeconds, now)
	if err != nil {
		return fmt.Errorf("list fast-rule connections: %w", err)
	}
	if len(connectionIDs) == 0 {
		return nil
	}
	bucket := scheduleBucket(now, interval)
	var failures []error

	for _, connectionID := range connectionIDs {
		connection := connectionID
		for _, spec := range []struct {
			jobType  string
			priority int
			payload  domain.JSON
		}{
			{
				application.JobCollectInsights, 22,
				domain.MustJSON(application.InsightsJobPayload{ConnectionID: connection}),
			},
			{
				application.JobEvaluateGuards, 21,
				domain.MustJSON(application.EvaluateGuardsJobPayload{ConnectionID: &connection}),
			},
		} {
			dedupeKey := fmt.Sprintf("%s:fast:%s:%s", connection, spec.jobType, bucket)
			if _, _, err := s.accounts.Enqueue(ctx, &domain.Job{
				ConnectionID: &connection,
				Type:         spec.jobType,
				Status:       domain.JobPending,
				// Above every background pass: a rule that exists to stop
				// spend is worth less the later it runs.
				Priority:    spec.priority,
				Payload:     spec.payload,
				DedupeKey:   &dedupeKey,
				MaxAttempts: s.options.MaxAttempts,
				AvailableAt: now.UTC(),
			}); err != nil {
				failures = append(failures, fmt.Errorf(
					"enqueue fast %s for %s: %w", spec.jobType, connection, err))
			}
		}
	}
	return errors.Join(failures...)
}

// fastLaneStore is satisfied by the repository-backed store. It is a separate
// interface so a test double for account scheduling does not have to know
// about rules.
type fastLaneStore interface {
	ConnectionsWithFastRules(ctx context.Context, maxIntervalSeconds int64, now time.Time) ([]uuid.UUID, error)
}
