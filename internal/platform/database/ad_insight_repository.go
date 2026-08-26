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

// dailyUpsertBatchSize bounds the parameter count of a single multi-row
// INSERT. Postgres caps a statement at 65535 bound parameters and these rows
// are wide, so batching keeps a large backfill chunk from failing outright.
const dailyUpsertBatchSize = 200

type AdInsightDailyFilter struct {
	Scope          Scope
	ConnectionID   *uuid.UUID
	AdAccountID    *uuid.UUID
	Level          *domain.InsightLevel
	MetaObjectID   string
	CampaignMetaID string
	AdSetMetaID    string
	Since          *time.Time
	Until          *time.Time
	Page           domain.PageRequest
}

type AdInsightRepository struct {
	db *gorm.DB
}

var dailyUpdatableColumns = []string{
	"connection_id", "meta_account_id", "campaign_meta_id", "adset_meta_id",
	"object_name", "account_timezone", "currency", "attribution_setting",
	"spend", "impressions", "reach", "frequency", "clicks", "unique_clicks",
	"inline_link_clicks", "unique_inline_link_clicks", "ctr", "unique_ctr",
	"cpc", "cpm", "cpp", "cost_per_unique_click", "cost_per_inline_link_click",
	"quality_ranking", "engagement_rate_ranking", "conversion_rate_ranking",
	"actions", "action_values", "cost_per_action", "conversions", "roas",
	"video", "metrics", "raw_json", "fetched_at", "updated_at",
}

// UpsertDaily writes daily rows, replacing any existing row for the same
// (ad account, level, object, date). Re-fetching a range is therefore always
// safe, which is what backfill, gap repair and the 28-day attribution
// lookback all depend on.
func (r *AdInsightRepository) UpsertDaily(ctx context.Context, rows []domain.AdInsightDaily) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ad_account_id"},
			{Name: "level"},
			{Name: "meta_object_id"},
			{Name: "date"},
		},
		DoUpdates: clause.AssignmentColumns(dailyUpdatableColumns),
	}).CreateInBatches(rows, dailyUpsertBatchSize).Error
}

// UpsertWindowed stores deduplicated totals for an explicit window.
func (r *AdInsightRepository) UpsertWindowed(ctx context.Context, rows []domain.AdInsightWindowed) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ad_account_id"},
			{Name: "level"},
			{Name: "meta_object_id"},
			{Name: "since"},
			{Name: "until"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"connection_id", "account_timezone", "attribution_setting",
			"reach", "frequency", "impressions", "spend", "raw_json",
			"fetched_at", "updated_at",
		}),
	}).CreateInBatches(rows, dailyUpsertBatchSize).Error
}

// MarkCoverage records that each date was fetched, and with how many rows.
// A zero count is meaningful: it says the day was checked and nothing ran,
// which is what distinguishes a quiet day from a hole.
func (r *AdInsightRepository) MarkCoverage(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
	counts map[time.Time]int,
	at time.Time,
) error {
	if len(counts) == 0 {
		return nil
	}
	rows := make([]domain.AdInsightCoverage, 0, len(counts))
	for date, count := range counts {
		rows = append(rows, domain.AdInsightCoverage{
			AdAccountID: adAccountID,
			Level:       level,
			Date:        date.UTC().Truncate(24 * time.Hour),
			RowCount:    count,
			FetchedAt:   at,
		})
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ad_account_id"},
			{Name: "level"},
			{Name: "date"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"row_count", "fetched_at"}),
	}).CreateInBatches(rows, dailyUpsertBatchSize).Error
}

// MissingDates returns the days in [since, until] that were never fetched.
//
// The anti-join is against ad_insights_coverage, not ad_insights_daily: a day
// with no delivery legitimately produces no daily rows, so checking the data
// table would report every quiet day as a gap and re-fetch it forever.
func (r *AdInsightRepository) MissingDates(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
	since, until time.Time,
) ([]time.Time, error) {
	if until.Before(since) {
		return nil, nil
	}
	var missing []time.Time
	err := r.db.WithContext(ctx).Raw(`
		SELECT day::date
		FROM generate_series($2::date, $3::date, interval '1 day') AS day
		WHERE NOT EXISTS (
			SELECT 1 FROM ad_insights_coverage c
			WHERE c.ad_account_id = $1 AND c.level = $4 AND c.date = day::date
		)
		ORDER BY day
	`, adAccountID, since, until, level).Scan(&missing).Error
	return missing, err
}

// CoverageBounds reports the oldest and newest fetched day for a level.
func (r *AdInsightRepository) CoverageBounds(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
) (oldest, newest *time.Time, err error) {
	var bounds struct {
		Oldest *time.Time
		Newest *time.Time
	}
	err = r.db.WithContext(ctx).
		Model(&domain.AdInsightCoverage{}).
		Select("min(date) AS oldest, max(date) AS newest").
		Where("ad_account_id = ? AND level = ?", adAccountID, level).
		Scan(&bounds).Error
	return bounds.Oldest, bounds.Newest, err
}

func (r *AdInsightRepository) ListDaily(
	ctx context.Context,
	filter AdInsightDailyFilter,
) (domain.Page[domain.AdInsightDaily], error) {
	if !filter.Scope.Valid() {
		return domain.Page[domain.AdInsightDaily]{}, ErrScopeRequired
	}
	page := filter.Page.Normalized()
	query := filter.Scope.Apply(
		r.db.WithContext(ctx).Model(&domain.AdInsightDaily{}),
		"ad_insights_daily",
	)
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *filter.AdAccountID)
	}
	if filter.Level != nil {
		query = query.Where("level = ?", *filter.Level)
	}
	if filter.MetaObjectID != "" {
		query = query.Where("meta_object_id = ?", filter.MetaObjectID)
	}
	if filter.CampaignMetaID != "" {
		query = query.Where("campaign_meta_id = ?", filter.CampaignMetaID)
	}
	if filter.AdSetMetaID != "" {
		query = query.Where("adset_meta_id = ?", filter.AdSetMetaID)
	}
	if filter.Since != nil {
		query = query.Where("date >= ?", *filter.Since)
	}
	if filter.Until != nil {
		query = query.Where("date <= ?", *filter.Until)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.AdInsightDaily]{}, err
	}
	var items []domain.AdInsightDaily
	ordered := query.Order("date DESC, level ASC, meta_object_id ASC, id DESC")
	if err := applyPage(ordered, page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.AdInsightDaily]{}, err
	}
	return domain.Page[domain.AdInsightDaily]{
		Items:  items,
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}, nil
}

// DeleteDailyBefore trims history beyond the retention horizon.
func (r *AdInsightRepository) DeleteDailyBefore(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	result := r.db.WithContext(ctx).Exec(`
		DELETE FROM ad_insights_daily
		WHERE id IN (
			SELECT id FROM ad_insights_daily WHERE date < ? ORDER BY date LIMIT ?
		)
	`, before, limit)
	return result.RowsAffected, result.Error
}

// --- sync state ---

type AdAccountSyncStateRepository struct {
	db *gorm.DB
}

// Ensure creates the bookkeeping row for an ad account if it is absent and
// returns the current state.
func (r *AdAccountSyncStateRepository) Ensure(
	ctx context.Context,
	adAccountID, connectionID uuid.UUID,
	backfillTarget *time.Time,
) (*domain.AdAccountSyncState, error) {
	state := &domain.AdAccountSyncState{
		AdAccountID:        adAccountID,
		ConnectionID:       connectionID,
		AttributionSetting: "unified",
		BackfillTargetDate: backfillTarget,
		LastUsage:          domain.EmptyJSONObject,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ad_account_id"}},
		DoNothing: true,
	}).Create(state).Error
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, adAccountID)
}

func (r *AdAccountSyncStateRepository) Get(
	ctx context.Context,
	adAccountID uuid.UUID,
) (*domain.AdAccountSyncState, error) {
	var state domain.AdAccountSyncState
	if err := r.db.WithContext(ctx).
		Where("ad_account_id = ?", adAccountID).
		First(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

// AdvanceSyncedThrough moves one level's watermark forward. It never moves
// backwards: a gap-repair job for an older range must not rewind the live
// polling watermark.
func (r *AdAccountSyncStateRepository) AdvanceSyncedThrough(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
	through time.Time,
	at time.Time,
) error {
	column := domain.SyncedThroughColumn(level)
	if column == "" {
		return errors.New("database: unsupported insights level")
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE ad_account_sync_state
		SET `+column+` = GREATEST(COALESCE(`+column+`, $2::date), $2::date),
		    consecutive_failures = 0,
		    last_error = '',
		    updated_at = $3
		WHERE ad_account_id = $1
	`, adAccountID, through, at).Error
}

// SetBackfilledThrough moves the backfill watermark backwards, which is the
// direction backfill walks.
func (r *AdAccountSyncStateRepository) SetBackfilledThrough(
	ctx context.Context,
	adAccountID uuid.UUID,
	through time.Time,
	at time.Time,
) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE ad_account_sync_state
		SET backfilled_through = LEAST(COALESCE(backfilled_through, $2::date), $2::date),
		    updated_at = $3
		WHERE ad_account_id = $1
	`, adAccountID, through, at).Error
}

func (r *AdAccountSyncStateRepository) MarkEntitiesSynced(
	ctx context.Context,
	adAccountID uuid.UUID,
	at time.Time,
) error {
	return r.db.WithContext(ctx).
		Model(&domain.AdAccountSyncState{}).
		Where("ad_account_id = ?", adAccountID).
		Updates(map[string]any{"entities_synced_at": at, "updated_at": at}).Error
}

// RecordFailure increments the failure counter and stores the message.
func (r *AdAccountSyncStateRepository) RecordFailure(
	ctx context.Context,
	adAccountID uuid.UUID,
	message string,
	at time.Time,
) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE ad_account_sync_state
		SET consecutive_failures = consecutive_failures + 1,
		    last_error = $2,
		    updated_at = $3
		WHERE ad_account_id = $1
	`, adAccountID, message, at).Error
}

// RecordUsage persists the last observed rate-limit reading and any block
// Meta imposed, so throttling survives a worker restart instead of being
// relearned by getting blocked a second time.
func (r *AdAccountSyncStateRepository) RecordUsage(
	ctx context.Context,
	adAccountID uuid.UUID,
	usage domain.JSON,
	throttledUntil *time.Time,
	at time.Time,
) error {
	updates := map[string]any{"last_usage": usage, "updated_at": at}
	if throttledUntil != nil {
		updates["throttled_until"] = *throttledUntil
	}
	return r.db.WithContext(ctx).
		Model(&domain.AdAccountSyncState{}).
		Where("ad_account_id = ?", adAccountID).
		Updates(updates).Error
}

func (r *AdAccountSyncStateRepository) ClearThrottle(
	ctx context.Context,
	adAccountID uuid.UUID,
	at time.Time,
) error {
	return r.db.WithContext(ctx).
		Model(&domain.AdAccountSyncState{}).
		Where("ad_account_id = ?", adAccountID).
		Updates(map[string]any{"throttled_until": nil, "updated_at": at}).Error
}

func (r *AdAccountSyncStateRepository) SetAttribution(
	ctx context.Context,
	adAccountID uuid.UUID,
	setting string,
	at time.Time,
) error {
	return r.db.WithContext(ctx).
		Model(&domain.AdAccountSyncState{}).
		Where("ad_account_id = ?", adAccountID).
		Updates(map[string]any{"attribution_setting": setting, "updated_at": at}).Error
}
