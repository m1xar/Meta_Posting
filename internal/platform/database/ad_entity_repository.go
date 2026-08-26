package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const entityUpsertBatchSize = 200

type AdEntityFilter struct {
	Scope           Scope
	ConnectionID    *uuid.UUID
	AdAccountID     *uuid.UUID
	Level           *domain.AdEntityLevel
	CampaignMetaID  string
	AdSetMetaID     string
	MetaObjectID    string
	EffectiveStatus string
	Search          string
	IncludeGone     bool
	OwnedOnly       bool
	// Light drops the raw Graph payload from the select for list surfaces.
	Light bool
	Page  domain.PageRequest
}

type AdEntityRepository struct {
	db *gorm.DB
}

var entityUpdatableColumns = []string{
	"connection_id", "parent_meta_object_id", "campaign_meta_id", "adset_meta_id",
	"name", "status", "configured_status", "effective_status", "objective",
	"buying_type", "optimization_goal", "billing_event", "destination_type",
	"bid_strategy", "daily_budget", "lifetime_budget", "budget_remaining",
	"bid_amount", "spend_cap", "start_time", "stop_time", "meta_created_time",
	"meta_updated_time", "raw_json", "last_seen_at", "updated_at",
}

// UpsertMany writes the observed inventory.
//
// first_seen_at, is_owned and published_object_id are intentionally absent
// from the update list. The first is the moment we discovered the object and
// never changes; the other two record provenance established at publish time,
// which a later inventory sweep has no way to know and must not overwrite.
// disappeared_at is cleared here, so an object that comes back is revived.
func (r *AdEntityRepository) UpsertMany(ctx context.Context, entities []domain.AdEntity) error {
	if len(entities) == 0 {
		return nil
	}
	assignments := append(
		clause.AssignmentColumns(entityUpdatableColumns),
		// Reviving an object that reappears in the account.
		clause.Assignment{Column: clause.Column{Name: "disappeared_at"}, Value: nil},
	)
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "ad_account_id"},
			{Name: "level"},
			{Name: "meta_object_id"},
		},
		DoUpdates: assignments,
	}).CreateInBatches(entities, entityUpsertBatchSize).Error
}

// MarkDisappeared soft-deletes entities of a level that were not present in
// the latest sweep. History is kept: the object's insight rows remain valid
// for the days it ran, and deleting the parent would orphan them.
func (r *AdEntityRepository) MarkDisappeared(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.AdEntityLevel,
	seenMetaIDs []string,
	at time.Time,
) (int64, error) {
	query := r.db.WithContext(ctx).
		Model(&domain.AdEntity{}).
		Where("ad_account_id = ? AND level = ? AND disappeared_at IS NULL", adAccountID, level)
	if len(seenMetaIDs) > 0 {
		query = query.Where("meta_object_id NOT IN ?", seenMetaIDs)
	}
	result := query.Updates(map[string]any{"disappeared_at": at, "updated_at": at})
	return result.RowsAffected, result.Error
}

// LinkPublishedObject records that a published object corresponds to an
// inventory entity, so provenance survives the next inventory sweep.
func (r *AdEntityRepository) LinkPublishedObject(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.AdEntityLevel,
	metaObjectID string,
	publishedObjectID uuid.UUID,
	at time.Time,
) error {
	return r.db.WithContext(ctx).
		Model(&domain.AdEntity{}).
		Where("ad_account_id = ? AND level = ? AND meta_object_id = ?", adAccountID, level, metaObjectID).
		Updates(map[string]any{
			"published_object_id": publishedObjectID,
			"is_owned":            true,
			"updated_at":          at,
		}).Error
}

func (r *AdEntityRepository) List(
	ctx context.Context,
	filter AdEntityFilter,
) (domain.Page[domain.AdEntity], error) {
	if !filter.Scope.Valid() {
		return domain.Page[domain.AdEntity]{}, ErrScopeRequired
	}
	page := filter.Page.Normalized()
	query := filter.Scope.Apply(
		r.db.WithContext(ctx).Model(&domain.AdEntity{}),
		"ad_entities",
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
	if filter.CampaignMetaID != "" {
		query = query.Where("campaign_meta_id = ?", filter.CampaignMetaID)
	}
	if filter.AdSetMetaID != "" {
		query = query.Where("adset_meta_id = ?", filter.AdSetMetaID)
	}
	if filter.MetaObjectID != "" {
		query = query.Where("meta_object_id = ?", filter.MetaObjectID)
	}
	if filter.EffectiveStatus != "" {
		query = query.Where("effective_status = ?", filter.EffectiveStatus)
	}
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		query = query.Where("name ILIKE ? OR meta_object_id LIKE ?", pattern, pattern)
	}
	if !filter.IncludeGone {
		query = query.Where("disappeared_at IS NULL")
	}
	if filter.OwnedOnly {
		query = query.Where("is_owned")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.AdEntity]{}, err
	}
	var items []domain.AdEntity
	if filter.Light {
		// Everything but the raw Graph payload: a few hundred templates with
		// their full targeting trees is tens of megabytes nobody scrolls.
		query = query.Select("id, created_at, updated_at, connection_id, ad_account_id, level, meta_object_id, parent_meta_object_id, campaign_meta_id, adset_meta_id, name, status, configured_status, effective_status, objective, buying_type, optimization_goal, billing_event, destination_type, bid_strategy, daily_budget, lifetime_budget, budget_remaining, bid_amount, spend_cap, start_time, stop_time, meta_created_time, meta_updated_time, published_object_id, is_owned")
	}
	ordered := query.Order("level ASC, name ASC, meta_object_id ASC")
	if err := applyPage(ordered, page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.AdEntity]{}, err
	}
	return domain.Page[domain.AdEntity]{
		Items:  items,
		Total:  total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}, nil
}

// ActiveMetaIDs returns the live object IDs at a level, used to scope an
// insights query to objects that still exist.
func (r *AdEntityRepository) ActiveMetaIDs(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.AdEntityLevel,
) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&domain.AdEntity{}).
		Where("ad_account_id = ? AND level = ? AND disappeared_at IS NULL", adAccountID, level).
		Order("meta_object_id ASC").
		Pluck("meta_object_id", &ids).Error
	return ids, err
}

// EffectiveStatusFor looks up the current status of one object, so a
// published object's status can be derived from the inventory sweep instead
// of costing a Graph request each.
func (r *AdEntityRepository) EffectiveStatusFor(
	ctx context.Context,
	adAccountID uuid.UUID,
	metaObjectID string,
) (string, error) {
	var status string
	err := r.db.WithContext(ctx).
		Model(&domain.AdEntity{}).
		Where("ad_account_id = ? AND meta_object_id = ?", adAccountID, metaObjectID).
		Limit(1).
		Pluck("effective_status", &status).Error
	return status, err
}

// EffectiveStatusesForConnection returns the current status of every live
// entity of a connection, keyed by Meta object ID.
//
// One query replaces one Graph request per published object per hour, which
// at a few thousand objects is a rate-limit problem on its own, before any
// account-wide ingestion is added.
func (r *AdEntityRepository) EffectiveStatusesForConnection(
	ctx context.Context,
	connectionID uuid.UUID,
) (map[string]string, error) {
	var rows []struct {
		MetaObjectID    string
		EffectiveStatus string
	}
	err := r.db.WithContext(ctx).
		Model(&domain.AdEntity{}).
		Select("meta_object_id, effective_status").
		Where("connection_id = ? AND disappeared_at IS NULL AND effective_status <> ''", connectionID).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	statuses := make(map[string]string, len(rows))
	for _, row := range rows {
		statuses[row.MetaObjectID] = row.EffectiveStatus
	}
	return statuses, nil
}
