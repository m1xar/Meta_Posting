package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

const DefaultMaxJobErrorBytes = 4096

type JobService interface {
	SyncConnection(context.Context, uuid.UUID) (application.SyncSummary, error)
	PublishAccountResult(context.Context, uuid.UUID) error
	CollectInsights(context.Context, uuid.UUID) error
	EvaluateDueGuards(context.Context, *uuid.UUID) error
	SyncTrackerStats(context.Context) error
}

type JobStore interface {
	Claim(context.Context, string, time.Duration, time.Time) (*domain.Job, error)
	ExtendLease(context.Context, uuid.UUID, string, time.Duration, time.Time) error
	Complete(context.Context, uuid.UUID, string, time.Time) error
	Fail(context.Context, uuid.UUID, string, string, time.Duration, time.Time) (*domain.Job, error)
}

type RunnerOptions struct {
	WorkerID       string
	PollInterval   time.Duration
	LeaseDuration  time.Duration
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
	CleanupTimeout time.Duration
	MaxErrorBytes  int
	Now            func() time.Time
	Wait           func(context.Context, time.Duration) error
	Logger         *slog.Logger
}

type Runner struct {
	service JobService
	store   JobStore
	options RunnerOptions
}

func NewRunner(service JobService, store JobStore, options RunnerOptions) (*Runner, error) {
	if service == nil {
		return nil, errors.New("worker: application service is required")
	}
	if store == nil {
		return nil, errors.New("worker: job store is required")
	}
	if strings.TrimSpace(options.WorkerID) == "" {
		return nil, errors.New("worker: worker ID is required")
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.LeaseDuration <= 0 {
		return nil, errors.New("worker: lease duration must be positive")
	}
	if options.RetryBaseDelay <= 0 {
		options.RetryBaseDelay = 5 * time.Second
	}
	if options.RetryMaxDelay <= 0 {
		options.RetryMaxDelay = 15 * time.Minute
	}
	if options.CleanupTimeout <= 0 {
		options.CleanupTimeout = 10 * time.Second
	}
	if options.MaxErrorBytes <= 0 {
		options.MaxErrorBytes = DefaultMaxJobErrorBytes
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Wait == nil {
		options.Wait = waitContext
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Runner{service: service, store: store, options: options}, nil
}

// Run polls durably until ctx is cancelled. Empty queues and repository
// failures both wait before the next claim, preventing a database hot loop.
func (r *Runner) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		processed, err := r.ProcessOne(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			r.options.Logger.Error("worker iteration failed", "error", err)
			if waitErr := r.options.Wait(ctx, r.options.PollInterval); waitErr != nil {
				return nil
			}
			continue
		}
		if processed {
			continue
		}
		if waitErr := r.options.Wait(ctx, r.options.PollInterval); waitErr != nil {
			return nil
		}
	}
}

// ProcessOne claims and processes at most one job. It returns false without an
// error when no due job exists.
func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	job, err := r.store.Claim(
		ctx,
		r.options.WorkerID,
		r.options.LeaseDuration,
		r.options.Now(),
	)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim job: %w", err)
	}
	if job == nil || job.ID == uuid.Nil {
		return false, errors.New("worker: job store returned an empty claimed job")
	}
	return true, r.processClaimed(ctx, job)
}

func (r *Runner) processClaimed(parent context.Context, job *domain.Job) error {
	jobCtx, cancelJob := context.WithCancel(parent)
	defer cancelJob()

	heartbeatDone := make(chan struct{})
	heartbeatResult := make(chan error, 1)
	go func() {
		defer close(heartbeatDone)
		leaseErr := r.maintainLease(jobCtx, job)
		if leaseErr != nil {
			cancelJob()
		}
		heartbeatResult <- leaseErr
	}()

	handlerErr := r.dispatch(jobCtx, job)
	cancelJob()
	<-heartbeatDone
	leaseErr := <-heartbeatResult
	if leaseErr != nil {
		r.options.Logger.Error(
			"job lease lost",
			"job_id", job.ID,
			"job_type", job.Type,
			"error", leaseErr,
		)
		return leaseErr
	}

	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), r.options.CleanupTimeout)
	defer cleanupCancel()
	now := r.options.Now()
	if handlerErr == nil {
		if err := r.store.Complete(cleanupCtx, job.ID, r.options.WorkerID, now); err != nil {
			return fmt.Errorf("complete job %s: %w", job.ID, err)
		}
		r.options.Logger.Info(
			"job completed",
			"job_id", job.ID,
			"job_type", job.Type,
			"attempt", job.Attempts,
		)
		return nil
	}

	message := truncateMessage(handlerErr.Error(), r.options.MaxErrorBytes)
	retryDelay := domain.RetryDelay(job.Attempts, r.options.RetryBaseDelay, r.options.RetryMaxDelay)
	failedJob, err := r.store.Fail(
		cleanupCtx,
		job.ID,
		r.options.WorkerID,
		message,
		retryDelay,
		now,
	)
	if err != nil {
		return errors.Join(handlerErr, fmt.Errorf("fail job %s: %w", job.ID, err))
	}
	status := domain.JobPending
	if failedJob != nil {
		status = failedJob.Status
	}
	r.options.Logger.Warn(
		"job failed",
		"job_id", job.ID,
		"job_type", job.Type,
		"attempt", job.Attempts,
		"status", status,
		"retry_delay", retryDelay,
		"error", message,
	)
	return nil
}

func (r *Runner) maintainLease(ctx context.Context, job *domain.Job) error {
	interval := r.options.LeaseDuration / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.store.ExtendLease(
				ctx,
				job.ID,
				r.options.WorkerID,
				r.options.LeaseDuration,
				r.options.Now(),
			); err != nil {
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return fmt.Errorf("extend lease for job %s: %w", job.ID, err)
			}
		}
	}
}

func (r *Runner) dispatch(ctx context.Context, job *domain.Job) error {
	switch job.Type {
	case application.JobSyncConnection:
		var payload application.SyncJobPayload
		if err := decodeJobPayload(job, &payload); err != nil {
			return err
		}
		if err := validateConnectionPayload(job, payload.ConnectionID); err != nil {
			return err
		}
		_, err := r.service.SyncConnection(ctx, payload.ConnectionID)
		return err

	case application.JobPublishAccount:
		var payload application.PublishJobPayload
		if err := decodeJobPayload(job, &payload); err != nil {
			return err
		}
		if payload.ResultID == uuid.Nil {
			return errors.New("worker: publish_account payload.result_id is required")
		}
		return r.service.PublishAccountResult(ctx, payload.ResultID)

	case application.JobCollectInsights:
		var payload application.InsightsJobPayload
		if err := decodeJobPayload(job, &payload); err != nil {
			return err
		}
		if err := validateConnectionPayload(job, payload.ConnectionID); err != nil {
			return err
		}
		return r.service.CollectInsights(ctx, payload.ConnectionID)

	case application.JobSyncTracker:
		return r.service.SyncTrackerStats(ctx)
	case application.JobEvaluateGuards:
		var payload application.EvaluateGuardsJobPayload
		if err := decodeJobPayload(job, &payload); err != nil {
			return err
		}
		if payload.ConnectionID != nil {
			if err := validateConnectionPayload(job, *payload.ConnectionID); err != nil {
				return err
			}
		} else if job.ConnectionID != nil {
			return errors.New("worker: evaluate_rules payload.connection_id is required for a connection-scoped job")
		}
		return r.service.EvaluateDueGuards(ctx, payload.ConnectionID)

	default:
		return fmt.Errorf("worker: unsupported job type %q", job.Type)
	}
}

func decodeJobPayload(job *domain.Job, target any) error {
	if len(job.Payload) == 0 {
		return fmt.Errorf("worker: %s job payload is empty", job.Type)
	}
	if err := job.Payload.Decode(target); err != nil {
		return fmt.Errorf("worker: decode %s payload: %w", job.Type, err)
	}
	return nil
}

func validateConnectionPayload(job *domain.Job, connectionID uuid.UUID) error {
	if connectionID == uuid.Nil {
		return fmt.Errorf("worker: %s payload.connection_id is required", job.Type)
	}
	if job.ConnectionID != nil && *job.ConnectionID != connectionID {
		return fmt.Errorf(
			"worker: %s payload connection %s does not match job connection %s",
			job.Type,
			connectionID,
			*job.ConnectionID,
		)
	}
	return nil
}

func truncateMessage(message string, maxBytes int) string {
	message = strings.ToValidUTF8(message, "�")
	if maxBytes <= 0 || len(message) <= maxBytes {
		return message
	}
	message = message[:maxBytes]
	for len(message) > 0 && !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
