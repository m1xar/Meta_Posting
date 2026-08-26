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

type BatchFilter struct {
	ConnectionID *uuid.UUID
	Statuses     []domain.BatchStatus
	Page         domain.PageRequest
}

type BatchAccountResultFilter struct {
	BatchID  uuid.UUID
	Statuses []domain.BatchAccountStatus
	Page     domain.PageRequest
}

type PublishedObjectFilter struct {
	BatchID     *uuid.UUID
	AdAccountID *uuid.UUID
	ObjectTypes []domain.PublishedObjectType
	Page        domain.PageRequest
}

type BatchRepository struct {
	db *gorm.DB
}

func (r *BatchRepository) Create(ctx context.Context, batch *domain.Batch, results []domain.BatchAccountResult) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		batch.TotalAccounts = len(results)
		if err := tx.Create(batch).Error; err != nil {
			return err
		}
		if len(results) == 0 {
			return nil
		}
		for index := range results {
			results[index].BatchID = batch.ID
			if results[index].Status == "" {
				results[index].Status = domain.BatchAccountPending
			}
		}
		return tx.CreateInBatches(&results, 500).Error
	})
}

func (r *BatchRepository) Get(ctx context.Context, id uuid.UUID) (*domain.Batch, error) {
	var batch domain.Batch
	if err := r.db.WithContext(ctx).First(&batch, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *BatchRepository) FindByIdempotencyKey(ctx context.Context, connectionID uuid.UUID, key string) (*domain.Batch, error) {
	var batch domain.Batch
	if err := r.db.WithContext(ctx).
		First(&batch, "connection_id = ? AND idempotency_key = ?", connectionID, key).Error; err != nil {
		return nil, err
	}
	return &batch, nil
}

func (r *BatchRepository) List(ctx context.Context, filter BatchFilter) (domain.Page[domain.Batch], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.Batch{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.Batch]{}, err
	}
	var items []domain.Batch
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.Batch]{}, err
	}
	return domain.Page[domain.Batch]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *BatchRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.BatchStatus, now time.Time) error {
	values := map[string]any{"status": status, "updated_at": now}
	switch status {
	case domain.BatchRunning:
		values["started_at"] = gorm.Expr("COALESCE(started_at, ?)", now)
	case domain.BatchSucceeded, domain.BatchPartiallySucceeded, domain.BatchFailed, domain.BatchCancelled:
		values["completed_at"] = now
	}
	result := r.db.WithContext(ctx).Model(&domain.Batch{}).Where("id = ?", id).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *BatchRepository) GetAccountResult(ctx context.Context, id uuid.UUID) (*domain.BatchAccountResult, error) {
	var result domain.BatchAccountResult
	if err := r.db.WithContext(ctx).First(&result, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *BatchRepository) ListAccountResults(
	ctx context.Context,
	filter BatchAccountResultFilter,
) (domain.Page[domain.BatchAccountResult], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.BatchAccountResult{}).Where("batch_id = ?", filter.BatchID)
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.BatchAccountResult]{}, err
	}
	var items []domain.BatchAccountResult
	if err := applyPage(query.Order("created_at ASC, id ASC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.BatchAccountResult]{}, err
	}
	return domain.Page[domain.BatchAccountResult]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *BatchRepository) MarkAccountRunning(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.BatchAccountResult{}).
			Where("id = ? AND status IN ?", id, []domain.BatchAccountStatus{
				domain.BatchAccountPending,
				domain.BatchAccountFailed,
				domain.BatchAccountRunning,
			}).
			Updates(map[string]any{
				"status":        domain.BatchAccountRunning,
				"attempts":      gorm.Expr("attempts + 1"),
				"started_at":    gorm.Expr("GREATEST(created_at, ?)", now),
				"completed_at":  nil,
				"error_code":    "",
				"error_subcode": "",
				"error_message": "",
				"updated_at":    now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		batchUpdate := tx.Exec(`
			UPDATE batches
			SET status = 'running',
			    started_at = COALESCE(started_at, GREATEST(created_at, ?)),
			    completed_at = NULL,
			    updated_at = ?
			WHERE id = (
			    SELECT batch_id
			    FROM batch_account_results
			    WHERE id = ?
			)`, now, now, id)
		if batchUpdate.Error != nil {
			return batchUpdate.Error
		}
		if batchUpdate.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// CheckpointPublishedObject persists a Meta ID immediately after an upstream
// create succeeds. A reclaimed worker can then resume the hierarchy instead of
// blindly creating duplicate objects after a crash or database timeout.
func (r *BatchRepository) CheckpointPublishedObject(ctx context.Context, object *domain.PublishedObject) error {
	if object == nil {
		return errors.New("published object checkpoint is required")
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "ad_account_id"}, {Name: "idempotency_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"batch_id", "batch_account_result_id", "connection_id", "object_type",
			"meta_object_id", "parent_meta_object_id", "name", "desired_status",
			"effective_status", "request_json", "response_json", "last_synced_at",
			"updated_at",
		}),
	}, clause.Returning{}).Create(object).Error
}

func (r *BatchRepository) ListResultPublishedObjects(
	ctx context.Context,
	resultID uuid.UUID,
) ([]domain.PublishedObject, error) {
	var objects []domain.PublishedObject
	if err := r.db.WithContext(ctx).
		Where("batch_account_result_id = ?", resultID).
		Order("created_at ASC, id ASC").
		Find(&objects).Error; err != nil {
		return nil, err
	}
	return objects, nil
}

type AccountResultCompletion struct {
	Status       domain.BatchAccountStatus
	ResponseJSON domain.JSON
	ErrorCode    string
	ErrorSubcode string
	ErrorMessage string
}

// AccountResultRetry is the durable record of an unsuccessful publish attempt
// that still has queue attempts available. The account remains non-terminal so
// API clients do not observe a false failed batch during worker backoff.
type AccountResultRetry struct {
	ResponseJSON domain.JSON
	ErrorCode    string
	ErrorSubcode string
	ErrorMessage string
}

func (r *BatchRepository) RecordAccountRetry(
	ctx context.Context,
	id uuid.UUID,
	retry AccountResultRetry,
	published []domain.PublishedObject,
	now time.Time,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var result domain.BatchAccountResult
		update := tx.Model(&result).
			Clauses(clause.Returning{}).
			Where("id = ? AND status = ?", id, domain.BatchAccountRunning).
			Updates(map[string]any{
				"status":        domain.BatchAccountPending,
				"response_json": retry.ResponseJSON,
				"error_code":    retry.ErrorCode,
				"error_subcode": retry.ErrorSubcode,
				"error_message": retry.ErrorMessage,
				"completed_at":  nil,
				"updated_at":    now,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := persistResultPublishedObjects(tx, &result, published); err != nil {
			return err
		}
		return recalculateBatch(tx, result.BatchID, now)
	})
}

func (r *BatchRepository) FinishAccountResult(
	ctx context.Context,
	id uuid.UUID,
	completion AccountResultCompletion,
	published []domain.PublishedObject,
	now time.Time,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var result domain.BatchAccountResult
		update := tx.Model(&result).
			Clauses(clause.Returning{}).
			Where("id = ? AND status = ?", id, domain.BatchAccountRunning).
			Updates(map[string]any{
				"status":        completion.Status,
				"response_json": completion.ResponseJSON,
				"error_code":    completion.ErrorCode,
				"error_subcode": completion.ErrorSubcode,
				"error_message": completion.ErrorMessage,
				"completed_at":  now,
				"updated_at":    now,
			})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		if err := persistResultPublishedObjects(tx, &result, published); err != nil {
			return err
		}
		return recalculateBatch(tx, result.BatchID, now)
	})
}

func persistResultPublishedObjects(
	tx *gorm.DB,
	result *domain.BatchAccountResult,
	published []domain.PublishedObject,
) error {
	if len(published) == 0 {
		return nil
	}
	var batch domain.Batch
	if err := tx.First(&batch, "id = ?", result.BatchID).Error; err != nil {
		return err
	}
	for index := range published {
		published[index].BatchID = result.BatchID
		published[index].BatchAccountResultID = result.ID
		published[index].AdAccountID = result.AdAccountID
		published[index].ConnectionID = batch.ConnectionID
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&published, 100).Error
}

func (r *BatchRepository) Recalculate(ctx context.Context, batchID uuid.UUID, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return recalculateBatch(tx, batchID, now)
	})
}

func recalculateBatch(db *gorm.DB, batchID uuid.UUID, now time.Time) error {
	// Serialize terminal result aggregation per batch. Without this lock, two
	// account completions can each count the other's uncommitted row and the
	// last writer may incorrectly leave an otherwise terminal batch running.
	var batch domain.Batch
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").
		First(&batch, "id = ?", batchID).Error; err != nil {
		return err
	}

	result := db.Exec(`
		UPDATE batches AS b
		SET succeeded_accounts = counts.succeeded,
		    failed_accounts = counts.failed,
		    status = CASE
		        WHEN counts.pending > 0 OR counts.running > 0 THEN 'running'
		        WHEN counts.succeeded = b.total_accounts THEN 'succeeded'
		        WHEN counts.succeeded > 0 AND counts.failed > 0 THEN 'partially_succeeded'
		        WHEN counts.failed = b.total_accounts THEN 'failed'
		        ELSE b.status
		    END,
		    completed_at = CASE
		        WHEN counts.pending = 0 AND counts.running = 0 THEN CAST(? AS timestamptz)
		        ELSE NULL
		    END,
		    updated_at = ?
		FROM (
		    SELECT
		        count(*) FILTER (WHERE status = 'succeeded')::integer AS succeeded,
		        count(*) FILTER (WHERE status IN ('failed', 'skipped'))::integer AS failed,
		        count(*) FILTER (WHERE status = 'pending')::integer AS pending,
		        count(*) FILTER (WHERE status = 'running')::integer AS running
		    FROM batch_account_results
		    WHERE batch_id = ?
		) AS counts
		WHERE b.id = ?`, now, now, batchID, batchID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *BatchRepository) GetPublishedObject(ctx context.Context, id uuid.UUID) (*domain.PublishedObject, error) {
	var object domain.PublishedObject
	if err := r.db.WithContext(ctx).First(&object, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &object, nil
}

func (r *BatchRepository) FindPublishedObjectByMetaID(
	ctx context.Context,
	adAccountID uuid.UUID,
	objectType domain.PublishedObjectType,
	metaObjectID string,
) (*domain.PublishedObject, error) {
	var object domain.PublishedObject
	if err := r.db.WithContext(ctx).
		First(&object, "ad_account_id = ? AND object_type = ? AND meta_object_id = ?", adAccountID, objectType, metaObjectID).Error; err != nil {
		return nil, err
	}
	return &object, nil
}

func (r *BatchRepository) ListPublishedObjects(
	ctx context.Context,
	filter PublishedObjectFilter,
) (domain.Page[domain.PublishedObject], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.PublishedObject{})
	if filter.BatchID != nil {
		query = query.Where("batch_id = ?", *filter.BatchID)
	}
	if filter.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *filter.AdAccountID)
	}
	if len(filter.ObjectTypes) > 0 {
		query = query.Where("object_type IN ?", filter.ObjectTypes)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.PublishedObject]{}, err
	}
	var items []domain.PublishedObject
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.PublishedObject]{}, err
	}
	return domain.Page[domain.PublishedObject]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *BatchRepository) UpdatePublishedStatus(
	ctx context.Context,
	id uuid.UUID,
	effectiveStatus string,
	response domain.JSON,
	now time.Time,
) error {
	result := r.db.WithContext(ctx).Model(&domain.PublishedObject{}).Where("id = ?", id).Updates(map[string]any{
		"effective_status": effectiveStatus,
		"response_json":    response,
		"last_synced_at":   now,
		"updated_at":       now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// MarkPublishedStatusChecked records a status refresh attempt without replacing
// the last successful Meta response. This prevents a permanently inaccessible
// or deleted object from being queried on every Insights polling cycle.
func (r *BatchRepository) MarkPublishedStatusChecked(
	ctx context.Context,
	id uuid.UUID,
	now time.Time,
) error {
	result := r.db.WithContext(ctx).Model(&domain.PublishedObject{}).Where("id = ?", id).Updates(map[string]any{
		"last_synced_at": now,
		"updated_at":     now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
