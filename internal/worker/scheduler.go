package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
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
	GuardInterval       time.Duration
	TrackerInterval     time.Duration
	TrackerEnabled      bool
	MaintenanceInterval time.Duration
	MaxAttempts         int
	Now                 func() time.Time
	Logger              *slog.Logger
}

type Scheduler struct {
	store   ScheduleStore
	options SchedulerOptions
}

func NewScheduler(store ScheduleStore, options SchedulerOptions) (*Scheduler, error) {
	if store == nil {
		return nil, errors.New("worker: schedule store is required")
	}
	if options.InsightsInterval <= 0 {
		return nil, errors.New("worker: insights interval must be positive")
	}
	if options.GuardInterval <= 0 {
		return nil, errors.New("worker: guard interval must be positive")
	}
	if options.TrackerEnabled && options.TrackerInterval <= 0 {
		return nil, errors.New("worker: tracker interval must be positive")
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
	guardsTicker := time.NewTicker(s.options.GuardInterval)
	trackerInterval := s.options.TrackerInterval
	if !s.options.TrackerEnabled {
		// Keep the ticker alive but effectively silent when the tracker is off.
		trackerInterval = 24 * time.Hour
	}
	trackerTicker := time.NewTicker(trackerInterval)
	maintenanceTicker := time.NewTicker(s.options.MaintenanceInterval)
	defer insightsTicker.Stop()
	defer guardsTicker.Stop()
	defer trackerTicker.Stop()
	defer maintenanceTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-insightsTicker.C:
			if err := s.ScheduleInsights(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("schedule insights jobs", "error", err)
			}
		case <-guardsTicker.C:
			if err := s.ScheduleGuardEvaluations(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("schedule guard jobs", "error", err)
			}
		case <-trackerTicker.C:
			if err := s.ScheduleTrackerSync(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("schedule tracker sync", "error", err)
			}
		case <-maintenanceTicker.C:
			if _, err := s.ExpireOAuthSessions(ctx, s.options.Now()); err != nil {
				s.options.Logger.Error("expire OAuth sessions", "error", err)
			}
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
		s.options.Logger.Error("initial guard scheduling", "error", err)
	}
	if err := s.ScheduleTrackerSync(ctx, now); err != nil && ctx.Err() == nil {
		s.options.Logger.Error("initial tracker scheduling", "error", err)
	}
}

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
		s.options.GuardInterval,
		10,
		func(connectionID uuid.UUID) domain.JSON {
			id := connectionID
			return domain.MustJSON(application.EvaluateGuardsJobPayload{ConnectionID: &id})
		},
	)
}

// ScheduleTrackerSync enqueues one global Keitaro sync per interval bucket.
func (s *Scheduler) ScheduleTrackerSync(ctx context.Context, now time.Time) error {
	if !s.options.TrackerEnabled {
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
