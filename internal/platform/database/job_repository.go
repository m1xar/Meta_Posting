package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	publishAccountJobType        = "publish_account"
	deadPublishAccountErrorCode  = "job_attempts_exhausted"
	expiredFinalAttemptErrorText = "worker lease expired after final attempt"
)

type JobFilter struct {
	ConnectionID *uuid.UUID
	Types        []string
	Statuses     []domain.JobStatus
	Page         domain.PageRequest
}

type JobRepository struct {
	db *gorm.DB
}

func (r *JobRepository) Enqueue(ctx context.Context, job *domain.Job) (*domain.Job, bool, error) {
	if job.Status == "" {
		job.Status = domain.JobPending
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = time.Now().UTC()
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 5
	}

	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "type"}, {Name: "dedupe_key"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Expr{SQL: "dedupe_key IS NOT NULL"},
		}},
		DoNothing: true,
	}, clause.Returning{}).Create(job)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		return job, true, nil
	}
	if job.DedupeKey == nil {
		return nil, false, errors.New("job insert was ignored without a dedupe key")
	}

	var existing domain.Job
	if err := r.db.WithContext(ctx).
		First(&existing, "type = ? AND dedupe_key = ?", job.Type, *job.DedupeKey).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (r *JobRepository) Get(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	var job domain.Job
	if err := r.db.WithContext(ctx).First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *JobRepository) List(ctx context.Context, filter JobFilter) (domain.Page[domain.Job], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.Job{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if len(filter.Types) > 0 {
		query = query.Where("type IN ?", filter.Types)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.Job]{}, err
	}
	var items []domain.Job
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.Job]{}, err
	}
	return domain.Page[domain.Job]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

// Claim atomically leases one due job. FOR UPDATE SKIP LOCKED lets any number
// of workers poll concurrently without double-processing. Expired leases are
// reclaimed; jobs whose final lease expired are moved to dead first.
func (r *JobRepository) Claim(
	ctx context.Context,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) (*domain.Job, error) {
	if workerID == "" {
		return nil, errors.New("worker ID is required")
	}
	if leaseDuration <= 0 {
		return nil, errors.New("lease duration must be positive")
	}

	var claimed domain.Job
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var expiredFinalJobs []domain.Job
		if err := tx.Raw(`
			UPDATE jobs
			SET status = 'dead',
			    locked_by = '',
			    locked_at = NULL,
			    locked_until = NULL,
			    finished_at = ?,
			    last_error = CASE
			        WHEN last_error = '' THEN ?
			        ELSE last_error
			    END,
			    updated_at = ?
			WHERE status = 'running'
			  AND locked_until <= ?
			  AND attempts >= max_attempts
			RETURNING *`,
			now,
			expiredFinalAttemptErrorText,
			now,
			now,
		).Scan(&expiredFinalJobs).Error; err != nil {
			return err
		}
		for index := range expiredFinalJobs {
			if err := finalizeDeadPublishAccountResult(
				tx,
				&expiredFinalJobs[index],
				expiredFinalJobs[index].LastError,
				now,
			); err != nil {
				return err
			}
		}

		leaseSeconds := leaseDuration.Seconds()
		result := tx.Raw(`
			WITH candidate AS (
			    SELECT id
			    FROM jobs
			    WHERE (
			        (status = 'pending' AND available_at <= @now)
			        OR (status = 'running' AND locked_until <= @now)
			    )
			      AND attempts < max_attempts
			    ORDER BY priority DESC, available_at ASC, created_at ASC, id ASC
			    FOR UPDATE SKIP LOCKED
			    LIMIT 1
			)
			UPDATE jobs AS j
			SET status = 'running',
			    attempts = j.attempts + 1,
			    locked_by = @worker_id,
			    locked_at = @now,
			    locked_until = CAST(@now AS timestamptz) + (@lease_seconds * interval '1 second'),
			    finished_at = NULL,
			    updated_at = @now
			FROM candidate
			WHERE j.id = candidate.id
			RETURNING j.*`,
			map[string]any{
				"now":           now,
				"worker_id":     workerID,
				"lease_seconds": leaseSeconds,
			}).Scan(&claimed)
		if result.Error != nil {
			return result.Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if claimed.ID == uuid.Nil {
		return nil, gorm.ErrRecordNotFound
	}
	return &claimed, nil
}

func (r *JobRepository) ExtendLease(
	ctx context.Context,
	id uuid.UUID,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) error {
	if leaseDuration <= 0 {
		return errors.New("lease duration must be positive")
	}
	result := r.db.WithContext(ctx).Model(&domain.Job{}).
		Where("id = ? AND status = ? AND locked_by = ? AND locked_until > ?", id, domain.JobRunning, workerID, now).
		Updates(map[string]any{
			"locked_until": now.Add(leaseDuration),
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (r *JobRepository) Complete(ctx context.Context, id uuid.UUID, workerID string, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.Job{}).
		Where("id = ? AND status = ? AND locked_by = ? AND locked_until > ?", id, domain.JobRunning, workerID, now).
		Updates(map[string]any{
			"status":       domain.JobSucceeded,
			"locked_by":    "",
			"locked_at":    nil,
			"locked_until": nil,
			"last_error":   "",
			"finished_at":  now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Fail either schedules the job for retry or marks it dead when its attempt
// budget is exhausted. The retry delay is chosen by the worker so policies can
// differ by job type.
func (r *JobRepository) Fail(
	ctx context.Context,
	id uuid.UUID,
	workerID string,
	message string,
	retryDelay time.Duration,
	now time.Time,
) (*domain.Job, error) {
	var job domain.Job
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&job, "id = ?", id).Error; err != nil {
			return err
		}
		if job.Status != domain.JobRunning || job.LockedBy != workerID || job.LockedUntil == nil || !job.LockedUntil.After(now) {
			return ErrLeaseLost
		}

		values := map[string]any{
			"locked_by":    "",
			"locked_at":    nil,
			"locked_until": nil,
			"last_error":   message,
			"updated_at":   now,
		}
		if job.Attempts >= job.MaxAttempts {
			job.Status = domain.JobDead
			job.FinishedAt = &now
			values["status"] = domain.JobDead
			values["finished_at"] = now
		} else {
			if retryDelay < 0 {
				retryDelay = 0
			}
			job.Status = domain.JobPending
			job.AvailableAt = now.Add(retryDelay)
			job.FinishedAt = nil
			values["status"] = domain.JobPending
			values["available_at"] = job.AvailableAt
			values["finished_at"] = nil
		}
		if err := tx.Model(&domain.Job{}).Where("id = ?", id).Updates(values).Error; err != nil {
			return err
		}
		if job.Status == domain.JobDead {
			if err := finalizeDeadPublishAccountResult(tx, &job, message, now); err != nil {
				return err
			}
		}
		job.LockedBy = ""
		job.LockedAt = nil
		job.LockedUntil = nil
		job.LastError = message
		job.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// finalizeDeadPublishAccountResult closes the per-account result in the same
// transaction that makes its publish job dead. This prevents an exhausted job
// (including a final lease expiry after a worker crash) from leaving its result
// and parent batch permanently running. Other job types are deliberately
// ignored.
func finalizeDeadPublishAccountResult(
	tx *gorm.DB,
	job *domain.Job,
	message string,
	now time.Time,
) error {
	resultID, ok := deadPublishAccountResultID(job)
	if !ok {
		return nil
	}
	if message == "" {
		message = expiredFinalAttemptErrorText
	}

	var accountResult domain.BatchAccountResult
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&accountResult, "id = ?", resultID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	switch accountResult.Status {
	case domain.BatchAccountPending, domain.BatchAccountRunning:
		if err := tx.Model(&domain.BatchAccountResult{}).
			Where("id = ? AND status IN ?", resultID, []domain.BatchAccountStatus{
				domain.BatchAccountPending,
				domain.BatchAccountRunning,
			}).
			Updates(map[string]any{
				"status":        domain.BatchAccountFailed,
				"error_code":    deadPublishAccountErrorCode,
				"error_subcode": "",
				"error_message": message,
				"completed_at":  gorm.Expr("GREATEST(created_at, ?)", now),
				"updated_at":    now,
			}).Error; err != nil {
			return err
		}
	}

	// Recalculate even when the account result was already terminal. This makes
	// the operation idempotent and repairs an older partially persisted state.
	return recalculateBatch(tx, accountResult.BatchID, now)
}

func deadPublishAccountResultID(job *domain.Job) (uuid.UUID, bool) {
	if job == nil || job.Type != publishAccountJobType || len(job.Payload) == 0 {
		return uuid.Nil, false
	}
	var payload struct {
		ResultID uuid.UUID `json:"result_id"`
	}
	if err := job.Payload.Decode(&payload); err != nil || payload.ResultID == uuid.Nil {
		return uuid.Nil, false
	}
	return payload.ResultID, true
}

func (r *JobRepository) Cancel(ctx context.Context, id uuid.UUID, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.Job{}).
		Where("id = ? AND status IN ?", id, []domain.JobStatus{domain.JobPending, domain.JobRunning}).
		Updates(map[string]any{
			"status":       domain.JobCancelled,
			"locked_by":    "",
			"locked_at":    nil,
			"locked_until": nil,
			"finished_at":  now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
