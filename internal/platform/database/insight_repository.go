package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type InsightFilter struct {
	ConnectionID      *uuid.UUID
	AdAccountID       *uuid.UUID
	PublishedObjectID *uuid.UUID
	MetaObjectID      string
	Level             *domain.InsightLevel
	WindowStart       *time.Time
	WindowEnd         *time.Time
	Page              domain.PageRequest
}

type InsightPointQuery struct {
	ConnectionID uuid.UUID
	MetaObjectID string
	Level        domain.InsightLevel
	QueryHash    string
	At           time.Time
}

type InsightRepository struct {
	db *gorm.DB
}

func (r *InsightRepository) Upsert(ctx context.Context, snapshot *domain.InsightSnapshot) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "connection_id"},
			{Name: "meta_object_id"},
			{Name: "level"},
			{Name: "query_hash"},
			{Name: "window_start"},
			{Name: "window_end"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"ad_account_id", "published_object_id", "date_start", "date_stop",
			"account_timezone", "attribution_setting", "impressions", "reach", "clicks",
			"link_clicks", "landing_page_views", "actions", "conversions", "leads",
			"registrations", "purchases", "spend", "frequency", "ctr", "cpc", "cpm",
			"cost_per_action", "purchase_value", "roas", "breakdowns", "metrics",
			"raw_json", "fetched_at", "updated_at",
		}),
	}, clause.Returning{}).Create(snapshot).Error
}

func (r *InsightRepository) UpsertMany(ctx context.Context, snapshots []domain.InsightSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "connection_id"},
			{Name: "meta_object_id"},
			{Name: "level"},
			{Name: "query_hash"},
			{Name: "window_start"},
			{Name: "window_end"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"ad_account_id", "published_object_id", "date_start", "date_stop",
			"account_timezone", "attribution_setting", "impressions", "reach", "clicks",
			"link_clicks", "landing_page_views", "actions", "conversions", "leads",
			"registrations", "purchases", "spend", "frequency", "ctr", "cpc", "cpm",
			"cost_per_action", "purchase_value", "roas", "breakdowns", "metrics",
			"raw_json", "fetched_at", "updated_at",
		}),
	}).CreateInBatches(&snapshots, 500).Error
}

func (r *InsightRepository) Get(ctx context.Context, id uuid.UUID) (*domain.InsightSnapshot, error) {
	var snapshot domain.InsightSnapshot
	if err := r.db.WithContext(ctx).First(&snapshot, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *InsightRepository) List(ctx context.Context, filter InsightFilter) (domain.Page[domain.InsightSnapshot], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.InsightSnapshot{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *filter.AdAccountID)
	}
	if filter.PublishedObjectID != nil {
		query = query.Where("published_object_id = ?", *filter.PublishedObjectID)
	}
	if filter.MetaObjectID != "" {
		query = query.Where("meta_object_id = ?", filter.MetaObjectID)
	}
	if filter.Level != nil {
		query = query.Where("level = ?", *filter.Level)
	}
	if filter.WindowStart != nil {
		query = query.Where("window_end >= ?", *filter.WindowStart)
	}
	if filter.WindowEnd != nil {
		query = query.Where("window_start <= ?", *filter.WindowEnd)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.InsightSnapshot]{}, err
	}
	var items []domain.InsightSnapshot
	if err := applyPage(query.Order("window_end DESC, fetched_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.InsightSnapshot]{}, err
	}
	return domain.Page[domain.InsightSnapshot]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *InsightRepository) Latest(ctx context.Context, connectionID uuid.UUID, metaObjectID string, level domain.InsightLevel) (*domain.InsightSnapshot, error) {
	var snapshot domain.InsightSnapshot
	if err := r.db.WithContext(ctx).
		Where("connection_id = ? AND meta_object_id = ? AND level = ?", connectionID, metaObjectID, level).
		Order("window_end DESC, fetched_at DESC, id DESC").
		First(&snapshot).Error; err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *InsightRepository) NearestBefore(ctx context.Context, point InsightPointQuery) (*domain.InsightSnapshot, error) {
	query := r.pointQuery(ctx, point).Where("window_end <= ?", point.At)
	var snapshot domain.InsightSnapshot
	if err := query.Order("window_end DESC, fetched_at DESC, id DESC").First(&snapshot).Error; err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *InsightRepository) NearestAfter(ctx context.Context, point InsightPointQuery) (*domain.InsightSnapshot, error) {
	query := r.pointQuery(ctx, point).Where("window_end >= ?", point.At)
	var snapshot domain.InsightSnapshot
	if err := query.Order("window_end ASC, fetched_at ASC, id ASC").First(&snapshot).Error; err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *InsightRepository) pointQuery(ctx context.Context, point InsightPointQuery) *gorm.DB {
	query := r.db.WithContext(ctx).
		Where("connection_id = ? AND meta_object_id = ? AND level = ?", point.ConnectionID, point.MetaObjectID, point.Level)
	if point.QueryHash != "" {
		query = query.Where("query_hash = ?", point.QueryHash)
	}
	return query
}

func (r *InsightRepository) DeleteBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	result := r.db.WithContext(ctx).Exec(`
		DELETE FROM insight_snapshots
		WHERE id IN (
		    SELECT id
		    FROM insight_snapshots
		    WHERE fetched_at < ?
		    ORDER BY fetched_at ASC
		    LIMIT ?
		)`, before, limit)
	return result.RowsAffected, result.Error
}

// LatestForObjects returns the newest snapshot per published object.
func (r *InsightRepository) LatestForObjects(ctx context.Context, objectIDs []uuid.UUID) ([]domain.InsightSnapshot, error) {
	if len(objectIDs) == 0 {
		return nil, nil
	}
	var snapshots []domain.InsightSnapshot
	// Identity and headline counters only: the flattened metric map and raw
	// rows would multiply the scan by orders of magnitude at this fan-out.
	err := r.db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (published_object_id)
		            id, published_object_id, connection_id, meta_object_id, level,
		            spend, impressions, clicks, window_end, fetched_at
		     FROM insight_snapshots
		     WHERE published_object_id IN ?
		     ORDER BY published_object_id, window_end DESC, id DESC`, objectIDs).
		Scan(&snapshots).Error
	return snapshots, err
}
