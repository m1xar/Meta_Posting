package database

import (
	"context"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TrackerRepository struct {
	db *gorm.DB
}

// UpsertMany replaces the stored roll-up for each (campaign ID, campaign
// name) pair with the latest report values.
func (r *TrackerRepository) UpsertMany(ctx context.Context, stats []domain.TrackerStat) error {
	if len(stats) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "meta_campaign_id"}, {Name: "campaign_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"connection_id", "published_object_id", "clicks", "unique_clicks",
			"leads", "sales", "revenue", "raw", "last_synced_at", "updated_at",
		}),
	}).Create(&stats).Error
}

func (r *TrackerRepository) ListForObjects(ctx context.Context, objectIDs []uuid.UUID) ([]domain.TrackerStat, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	var stats []domain.TrackerStat
	err := r.db.WithContext(ctx).
		Where("published_object_id IN ?", objectIDs).
		Find(&stats).Error
	return stats, err
}

func (r *TrackerRepository) ForObject(ctx context.Context, objectID uuid.UUID) (*domain.TrackerStat, error) {
	var stat domain.TrackerStat
	if err := r.db.WithContext(ctx).
		Where("published_object_id = ?", objectID).
		Order("last_synced_at DESC").
		First(&stat).Error; err != nil {
		return nil, err
	}
	return &stat, nil
}
