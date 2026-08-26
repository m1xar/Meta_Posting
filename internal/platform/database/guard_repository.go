package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

type GuardFilter struct {
	ConnectionID      *uuid.UUID
	BatchID           *uuid.UUID
	PublishedObjectID *uuid.UUID
	Statuses          []domain.GuardStatus
	Page              domain.PageRequest
}

type GuardRepository struct {
	db *gorm.DB
}

func (r *GuardRepository) Create(ctx context.Context, guard *domain.CampaignGuard) error {
	return r.db.WithContext(ctx).Create(guard).Error
}

func (r *GuardRepository) Get(ctx context.Context, id uuid.UUID) (*domain.CampaignGuard, error) {
	var guard domain.CampaignGuard
	if err := r.db.WithContext(ctx).First(&guard, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &guard, nil
}

// UpdateConfiguration changes mutable guard fields without overwriting the
// scheduling timestamps maintained by the evaluator.
func (r *GuardRepository) UpdateConfiguration(ctx context.Context, guard *domain.CampaignGuard) error {
	result := r.db.WithContext(ctx).Model(&domain.CampaignGuard{}).Where("id = ?", guard.ID).Updates(map[string]any{
		"name":                        guard.Name,
		"status":                      guard.Status,
		"checkpoints":                 guard.Checkpoints,
		"evaluation_interval_seconds": guard.EvaluationIntervalSeconds,
		"updated_at":                  time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *GuardRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.GuardStatus, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.CampaignGuard{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *GuardRepository) List(ctx context.Context, filter GuardFilter) (domain.Page[domain.CampaignGuard], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.CampaignGuard{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.BatchID != nil {
		query = query.Where("batch_id = ?", *filter.BatchID)
	}
	if filter.PublishedObjectID != nil {
		query = query.Where("published_object_id = ?", *filter.PublishedObjectID)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.CampaignGuard]{}, err
	}
	var items []domain.CampaignGuard
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.CampaignGuard]{}, err
	}
	return domain.Page[domain.CampaignGuard]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *GuardRepository) ListDue(
	ctx context.Context,
	connectionID *uuid.UUID,
	now time.Time,
	limit int,
) ([]domain.CampaignGuard, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > domain.MaxPageLimit {
		limit = domain.MaxPageLimit
	}
	query := r.db.WithContext(ctx).
		Where("status = ? AND next_evaluation_at <= ?", domain.GuardActive, now)
	if connectionID != nil {
		query = query.Where("connection_id = ?", *connectionID)
	}
	var guards []domain.CampaignGuard
	if err := query.
		Order("next_evaluation_at ASC, id ASC").
		Limit(limit).
		Find(&guards).Error; err != nil {
		return nil, err
	}
	return guards, nil
}

func (r *GuardRepository) MarkEvaluated(ctx context.Context, id uuid.UUID, evaluatedAt, next time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.CampaignGuard{}).Where("id = ?", id).Updates(map[string]any{
		"last_evaluated_at":  evaluatedAt,
		"next_evaluation_at": next,
		"updated_at":         evaluatedAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// SaveCheck records one checkpoint outcome; re-evaluating the same checkpoint
// for the same campaign updates the row in place. An overridden check is never
// downgraded back to failed so an operator's resume decision sticks.
func (r *GuardRepository) SaveCheck(ctx context.Context, check *domain.GuardCheck) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing domain.GuardCheck
		err := tx.Where(
			"guard_id = ? AND published_object_id = ? AND checkpoint_index = ?",
			check.GuardID, check.PublishedObjectID, check.CheckpointIndex,
		).First(&existing).Error
		if err == nil {
			if existing.Status == domain.GuardCheckOverridden {
				return nil
			}
			check.ID = existing.ID
			check.CreatedAt = existing.CreatedAt
			return tx.Model(&domain.GuardCheck{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"status":       check.Status,
				"observed":     check.Observed,
				"thresholds":   check.Thresholds,
				"paused":       check.Paused,
				"error":        check.Error,
				"evaluated_at": check.EvaluatedAt,
				"updated_at":   check.EvaluatedAt,
			}).Error
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		return tx.Create(check).Error
	})
}

func (r *GuardRepository) OverrideCheck(ctx context.Context, checkID uuid.UUID, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.GuardCheck{}).
		Where("id = ? AND status = ?", checkID, domain.GuardCheckFailed).
		Updates(map[string]any{"status": domain.GuardCheckOverridden, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *GuardRepository) ListChecks(ctx context.Context, guardID uuid.UUID, publishedObjectID *uuid.UUID) ([]domain.GuardCheck, error) {
	query := r.db.WithContext(ctx).Where("guard_id = ?", guardID)
	if publishedObjectID != nil {
		query = query.Where("published_object_id = ?", *publishedObjectID)
	}
	var checks []domain.GuardCheck
	err := query.Order("published_object_id ASC, checkpoint_index ASC").Find(&checks).Error
	return checks, err
}

func (r *GuardRepository) ListChecksForObjects(ctx context.Context, objectIDs []uuid.UUID) ([]domain.GuardCheck, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	var checks []domain.GuardCheck
	err := r.db.WithContext(ctx).
		Where("published_object_id IN ?", objectIDs).
		Order("published_object_id ASC, checkpoint_index ASC").
		Find(&checks).Error
	return checks, err
}
