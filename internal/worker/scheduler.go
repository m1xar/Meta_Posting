package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
	"gorm.io/gorm"
)

type ScheduleStore interface {
	ActiveConnectionIDs(context.Context) ([]uuid.UUID, error)
	ExpirePendingOAuth(context.Context, time.Time) (int64, error)
	Enqueue(context.Context, *domain.Job) (*domain.Job, bool, error)
}

type RepositoryScheduleStore struct {
	Repositories *database.Repositories
}

func (s RepositoryScheduleStore) ActiveConnectionIDs(ctx context.Context) ([]uuid.UUID, error) {
	if s.Repositories == nil || s.Repositories.DB() == nil {
		return nil, errors.New("worker: repositories are not initialized")
	}
	var ids []uuid.UUID
	err := s.Repositories.DB().WithContext(ctx).
		Model(&domain.MetaConnection{}).
		Where("status = ?", domain.MetaConnectionActive).
		Order("created_at ASC, id ASC").
		Pluck("id", &ids).Error
	return ids, err
}

func (s RepositoryScheduleStore) ExpirePendingOAuth(ctx context.Context, now time.Time) (int64, error) {
	if s.Repositories == nil || s.Repositories.OAuthSessions == nil {
		return 0, errors.New("worker: OAuth repository is not initialized")
	}
	return s.Repositories.OAuthSessions.ExpirePending(ctx, now)
}

func (s RepositoryScheduleStore) Enqueue(ctx context.Context, job *domain.Job) (*domain.Job, bool, error) {
	if s.Repositories == nil || s.Repositories.Jobs == nil {
		return nil, false, errors.New("worker: job repository is not initialized")
	}
	if job.ConnectionID != nil && isRecurringJob(job.Type) {
		if existing, err := s.activeRecurringJob(ctx, *job.ConnectionID, job.Type); err == nil {
			return existing, false, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, err
		}
	}
	enqueued, created, err := s.Repositories.Jobs.Enqueue(ctx, job)
	if err == nil || job.ConnectionID == nil || !isRecurringJob(job.Type) || !errors.Is(err, gorm.ErrDuplicatedKey) {
		return enqueued, created, err
	}
	existing, lookupErr := s.activeRecurringJob(ctx, *job.ConnectionID, job.Type)
	if lookupErr != nil {
		return nil, false, errors.Join(err, lookupErr)
	}
	return existing, false, nil
}

func (s RepositoryScheduleStore) activeRecurringJob(
	ctx context.Context,
	connectionID uuid.UUID,
	jobType string,
) (*domain.Job, error) {
	var job domain.Job
	err := s.Repositories.DB().WithContext(ctx).
		Where(
			"connection_id = ? AND type = ? AND status IN ?",
			connectionID,
			jobType,
			[]domain.JobStatus{domain.JobPending, domain.JobRunning},
		).
		Order("created_at ASC, id ASC").
		First(&job).Error
	return &job, err
}

func isRecurringJob(jobType string) bool {
	return jobType == application.JobCollectInsights || jobType == application.JobEvaluateGuards
}

type SchedulerOptions struct {
	InsightsInterval    time.Duration
	RuleInterval        time.Duration
	MaintenanceInterval time.Duration
	MaxAttempts         int
	Now                 func() time.Time
	Logger              *slog.Logger

	// FastLaneInterval drives rules that ask to run faster than RuleInterval,
	// together with the insight collection they depend on.
	FastLaneInterval    time.Duration
	FastRuleMaxInterval int64

	// TrackerInterval drives the global Keitaro report sync. Zero disables
	// it, for deployments without a tracker.
	TrackerInterval time.Duration

	// DiscoveryInterval re-runs ad account discovery. Without it a connection
	// is only ever discovered once, at OAuth time, so an ad account granted
	// later stays invisible and account_status goes stale.
	DiscoveryInterval time.Duration

	// Account-wide tracking. Zero values disable the corresponding pass, so
	// a deployment can run publishing alone.
	EntitySyncInterval time.Duration
	AccountLevelPlans  []AccountLevelPlan
	LookbackLevel      domain.InsightLevel
	LookbackDays       int
	LookbackInterval   time.Duration
	AdLevelBatchSize   int
	RetentionInterval  time.Duration
	InsightRetention   time.Duration
}

type Scheduler struct {
	store    ScheduleStore
	accounts AccountScheduleStore
	options  SchedulerOptions
}

// WithAccountStore enables per-ad-account scheduling. It is optional so a
// Scheduler built for connection-level work alone stays usable.
func (s *Scheduler) WithAccountStore(store AccountScheduleStore) *Scheduler {
	s.accounts = store
	return s
}

func NewScheduler(store ScheduleStore, options SchedulerOptions) (*Scheduler, error) {
	if store == nil {
		return nil, errors.New("worker: schedule store is required")
	}
	if options.InsightsInterval <= 0 {
		return nil, errors.New("worker: insights interval must be positive")
	}
	if options.RuleInterval <= 0 {
		return nil, errors.New("worker: rule interval must be positive")
	}
	if options.MaintenanceInterval <= 0 {
		options.MaintenanceInterval = time.Minute
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 5
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Scheduler{store: store, options: options}, nil
}

// Run executes an initial scheduling pass and then waits on bounded tickers.
// Dedupe keys make startup passes safe across any number of replicas.
func (s *Scheduler) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	s.runInitialPass(ctx)

	insightsTicker := time.NewTicker(s.options.InsightsInterval)
	rulesTicker := time.NewTicker(s.options.RuleInterval)
	maintenanceTicker := time.NewTicker(s.options.MaintenanceInterval)
	defer insightsTicker.Stop()
	defer rulesTicker.Stop()
	defer maintenanceTicker.Stop()

	// One ticker per level, because the levels differ in cost by more than an
	// order of magnitude: an account-level poll is one row per day, an
	// ad-level poll is one row per ad per day.
	stop := s.startAccountTickers(ctx)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-insightsTicker.C:
			if err := s.ScheduleInsights(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("schedule insights jobs", "error", err)
			}
		case <-rulesTicker.C:
			if err := s.ScheduleGuardEvaluations(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("schedule rule jobs", "error", err)
			}
		case <-maintenanceTicker.C:
			if _, err := s.ExpireOAuthSessions(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("expire OAuth sessions", "error", err)
			}
		}
	}
}

// startAccountTickers runs the per-level passes on their own goroutines and
// returns a stop function. They are separate from the main loop because their
// intervals range from minutes to hours and a single select would have to
// tick at the shortest of them.
func (s *Scheduler) startAccountTickers(ctx context.Context) func() {
	if s.accounts == nil {
		return func() {}
	}
	var tickers []*time.Ticker
	run := func(interval time.Duration, name string, pass func(time.Time) error) {
		if interval <= 0 {
			return
		}
		ticker := time.NewTicker(interval)
		tickers = append(tickers, ticker)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := pass(s.options.Now()); err != nil {
						s.options.Logger.Error("schedule "+name, "error", err)
					}
				}
			}
		}()
	}

	for _, plan := range s.options.AccountLevelPlans {
		levelPlan := plan
		run(levelPlan.Interval, "account insights "+string(levelPlan.Level), func(now time.Time) error {
			return s.ScheduleAccountInsights(ctx, now, levelPlan)
		})
	}
	run(s.options.EntitySyncInterval, "ad entity sync", func(now time.Time) error {
		return s.ScheduleAdEntitySync(
			ctx, now, s.options.EntitySyncInterval, s.options.AdLevelBatchSize, 0,
		)
	})
	run(s.options.LookbackInterval, "insights lookback", func(now time.Time) error {
		return s.ScheduleInsightsLookback(
			ctx, now, s.options.LookbackLevel, s.options.LookbackDays,
			s.options.LookbackInterval, s.options.AdLevelBatchSize,
		)
	})
	run(s.options.RetentionInterval, "retention sweep", func(now time.Time) error {
		return s.ScheduleRetentionSweep(ctx, now, s.options.InsightRetention, s.options.RetentionInterval)
	})
	run(s.options.DiscoveryInterval, "connection discovery", func(now time.Time) error {
		return s.ScheduleConnectionDiscovery(ctx, now, s.options.DiscoveryInterval)
	})
	run(s.options.FastLaneInterval, "fast rule lane", func(now time.Time) error {
		return s.ScheduleFastLane(ctx, now,
			s.options.FastLaneInterval, s.options.FastRuleMaxInterval)
	})
	run(s.options.TrackerInterval, "tracker sync", func(now time.Time) error {
		return s.ScheduleTrackerSync(ctx, now)
	})

	return func() {
		for _, ticker := range tickers {
			ticker.Stop()
		}
	}
}

func (s *Scheduler) runInitialPass(ctx context.Context) {
	now := s.options.Now()
	if _, err := s.ExpireOAuthSessions(ctx, now); err != nil && ctx.Err() == nil {
		s.options.Logger.Error("initial OAuth expiry", "error", err)
	}
	if err := s.ScheduleInsights(ctx, now); err != nil && ctx.Err() == nil {
		s.options.Logger.Error("initial insights scheduling", "error", err)
	}
	if err := s.ScheduleGuardEvaluations(ctx, now); err != nil && ctx.Err() == nil {
		s.options.Logger.Error("initial rule scheduling", "error", err)
	}
	s.runInitialAccountPass(ctx, now)
}

// runInitialAccountPass schedules the per-account work once at startup
// instead of waiting out a full interval.
//
// Without it a fresh deploy or a restart produces nothing at ad level for six
// hours, and no entity inventory at all for six hours - so the first insight
// rows would arrive with no campaign names to attach them to. Dedupe keys are
// bucketed by interval, so this pass collapses into the scheduled one rather
// than double-polling.
func (s *Scheduler) runInitialAccountPass(ctx context.Context, now time.Time) {
	if s.accounts == nil {
		return
	}
	// Inventory first: insight rows are far more useful once the campaign,
	// ad set and ad they belong to are known.
	if s.options.EntitySyncInterval > 0 {
		if err := s.ScheduleAdEntitySync(
			ctx, now, s.options.EntitySyncInterval, s.options.AdLevelBatchSize, startupStaggerWindow,
		); err != nil && ctx.Err() == nil {
			s.options.Logger.Error("initial ad entity scheduling", "error", err)
		}
	}
	for _, plan := range s.options.AccountLevelPlans {
		if plan.Interval <= 0 {
			continue
		}
		// Spread over a couple of minutes rather than the plan's interval,
		// which for ad level is six hours. Still avoids a burst, without
		// making a fresh deploy wait most of a day to populate.
		startup := plan
		startup.StaggerWindow = startupStaggerWindow
		if err := s.ScheduleAccountInsights(ctx, now, startup); err != nil && ctx.Err() == nil {
			s.options.Logger.Error("initial account insights scheduling",
				"level", plan.Level, "error", err)
		}
	}
}

// startupStaggerWindow bounds how far the startup pass spreads its jobs.
const startupStaggerWindow = 2 * time.Minute

func (s *Scheduler) ScheduleInsights(ctx context.Context, now time.Time) error {
	return s.scheduleForActiveConnections(
		ctx,
		now,
		application.JobCollectInsights,
		s.options.InsightsInterval,
		20,
		func(connectionID uuid.UUID) domain.JSON {
			return domain.MustJSON(application.InsightsJobPayload{ConnectionID: connectionID})
		},
	)
}

func (s *Scheduler) ScheduleGuardEvaluations(ctx context.Context, now time.Time) error {
	return s.scheduleForActiveConnections(
		ctx,
		now,
		application.JobEvaluateGuards,
		s.options.RuleInterval,
		10,
		func(connectionID uuid.UUID) domain.JSON {
			id := connectionID
			return domain.MustJSON(application.EvaluateGuardsJobPayload{ConnectionID: &id})
		},
	)
}

// ScheduleTrackerSync enqueues one global Keitaro sync per interval bucket.
func (s *Scheduler) ScheduleTrackerSync(ctx context.Context, now time.Time) error {
	if s.options.TrackerInterval <= 0 {
		return nil
	}
	dedupeKey := "tracker:" + scheduleBucket(now, s.options.TrackerInterval)
	_, _, err := s.store.Enqueue(ctx, &domain.Job{
		Type:        application.JobSyncTracker,
		Status:      domain.JobPending,
		Priority:    5,
		Payload:     domain.MustJSON(application.SyncTrackerJobPayload{}),
		DedupeKey:   &dedupeKey,
		MaxAttempts: s.options.MaxAttempts,
		AvailableAt: now.UTC(),
	})
	return err
}

func (s *Scheduler) ExpireOAuthSessions(ctx context.Context, now time.Time) (int64, error) {
	expired, err := s.store.ExpirePendingOAuth(ctx, now.UTC())
	if err != nil {
		return 0, err
	}
	if expired > 0 {
		s.options.Logger.Info("expired pending OAuth sessions", "count", expired)
	}
	return expired, nil
}

func (s *Scheduler) scheduleForActiveConnections(
	ctx context.Context,
	now time.Time,
	jobType string,
	interval time.Duration,
	priority int,
	payload func(uuid.UUID) domain.JSON,
) error {
	connectionIDs, err := s.store.ActiveConnectionIDs(ctx)
	if err != nil {
		return fmt.Errorf("list active connections: %w", err)
	}
	bucket := scheduleBucket(now, interval)
	var failures []error
	for _, connectionID := range connectionIDs {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		dedupeKey := connectionID.String() + ":" + bucket
		_, created, enqueueErr := s.store.Enqueue(ctx, &domain.Job{
			ConnectionID: &connectionID,
			Type:         jobType,
			Status:       domain.JobPending,
			Priority:     priority,
			Payload:      payload(connectionID),
			DedupeKey:    &dedupeKey,
			MaxAttempts:  s.options.MaxAttempts,
			AvailableAt:  now.UTC(),
		})
		if enqueueErr != nil {
			failures = append(failures, fmt.Errorf(
				"enqueue %s for connection %s: %w",
				jobType,
				connectionID,
				enqueueErr,
			))
			continue
		}
		if created {
			s.options.Logger.Debug(
				"scheduled job",
				"job_type", jobType,
				"connection_id", connectionID,
				"bucket", bucket,
			)
		}
	}
	return errors.Join(failures...)
}

func scheduleBucket(now time.Time, interval time.Duration) string {
	return fmt.Sprintf("%d", now.UTC().Truncate(interval).Unix())
}
