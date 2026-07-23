package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BusinessFilter struct {
	ConnectionID uuid.UUID
	Search       string
	Page         domain.PageRequest
}

type AdAccountFilter struct {
	ConnectionID  *uuid.UUID
	BusinessID    *uuid.UUID
	AccountStatus *int
	Currency      string
	ActiveOnly    bool
	Search        string
	Page          domain.PageRequest
}

type AssetFilter struct {
	ConnectionID uuid.UUID
	BusinessID   *uuid.UUID
	AdAccountID  *uuid.UUID
	Types        []domain.AssetType
	ActiveOnly   bool
	Search       string
	Page         domain.PageRequest
}

type InventoryRepository struct {
	db *gorm.DB
}

// AssetAccessScope is one successfully completed account-level Meta edge.
// SeenMetaIDs must contain every asset returned by that edge for AssetType.
type AssetAccessScope struct {
	AdAccountID uuid.UUID
	AssetType   domain.AssetType
	SeenMetaIDs []string
}

// InventoryReconciliation contains only discovery scopes that completed
// successfully. Omitted scopes are deliberately preserved because an empty
// partial result cannot prove that access was removed upstream.
type InventoryReconciliation struct {
	ConnectionID         uuid.UUID
	SeenAdAccountMetaIDs []string
	AccountAssetScopes   []AssetAccessScope
	PagesComplete        bool
	SeenPageMetaIDs      []string
	SeenPageInstagramIDs []string
	ReconciledAt         time.Time
}

func (r *InventoryRepository) UpsertBusiness(ctx context.Context, business *domain.Business) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "connection_id"}, {Name: "meta_business_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"name", "verification_status", "vertical", "primary_page_id", "timezone_id",
			"is_hidden", "raw_json", "last_synced_at", "updated_at",
		}),
	}, clause.Returning{}).Create(business).Error
}

func (r *InventoryRepository) GetBusiness(ctx context.Context, id uuid.UUID) (*domain.Business, error) {
	var business domain.Business
	if err := r.db.WithContext(ctx).First(&business, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &business, nil
}

func (r *InventoryRepository) ListBusinesses(ctx context.Context, filter BusinessFilter) (domain.Page[domain.Business], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.Business{}).Where("connection_id = ?", filter.ConnectionID)
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		query = query.Where("name ILIKE ? OR meta_business_id ILIKE ?", pattern, pattern)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.Business]{}, err
	}
	var items []domain.Business
	if err := applyPage(query.Order("name ASC, id ASC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.Business]{}, err
	}
	return domain.Page[domain.Business]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *InventoryRepository) UpsertAdAccount(ctx context.Context, account *domain.AdAccount) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "connection_id"}, {Name: "meta_ad_account_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"business_id", "account_id", "name", "currency", "timezone_name",
			"timezone_offset_utc", "account_status", "disable_reason", "business_name",
			"amount_spent", "balance", "spend_cap", "capabilities", "raw_json",
			"is_active", "last_synced_at", "updated_at",
		}),
	}, clause.Returning{}).Create(account).Error
}

func (r *InventoryRepository) GetAdAccount(ctx context.Context, id uuid.UUID) (*domain.AdAccount, error) {
	var account domain.AdAccount
	if err := r.db.WithContext(ctx).First(&account, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *InventoryRepository) FindAdAccountByMetaID(ctx context.Context, connectionID uuid.UUID, metaID string) (*domain.AdAccount, error) {
	var account domain.AdAccount
	if err := r.db.WithContext(ctx).
		First(&account, "connection_id = ? AND meta_ad_account_id = ?", connectionID, metaID).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *InventoryRepository) ListAdAccounts(ctx context.Context, filter AdAccountFilter) (domain.Page[domain.AdAccount], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.AdAccount{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.BusinessID != nil {
		query = query.Where("business_id = ?", *filter.BusinessID)
	}
	if filter.AccountStatus != nil {
		query = query.Where("account_status = ?", *filter.AccountStatus)
	}
	if filter.Currency != "" {
		query = query.Where("currency = ?", filter.Currency)
	}
	if filter.ActiveOnly {
		query = query.Where("is_active = true")
	}
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		query = query.Where("name ILIKE ? OR meta_ad_account_id ILIKE ? OR account_id ILIKE ?", pattern, pattern, pattern)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.AdAccount]{}, err
	}
	var items []domain.AdAccount
	if err := applyPage(query.Order("name ASC, id ASC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.AdAccount]{}, err
	}
	return domain.Page[domain.AdAccount]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *InventoryRepository) UpsertAsset(ctx context.Context, asset *domain.Asset) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "connection_id"}, {Name: "asset_type"}, {Name: "meta_asset_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"business_id", "ad_account_id", "parent_meta_id", "name", "status",
				"is_active", "normalized", "raw_json", "last_synced_at", "updated_at",
			}),
		}, clause.Returning{}).Create(asset).Error; err != nil {
			return err
		}
		if asset.AdAccountID == nil {
			return nil
		}
		access := domain.AssetAdAccount{
			AssetID:      asset.ID,
			AdAccountID:  *asset.AdAccountID,
			ConnectionID: asset.ConnectionID,
			LastSyncedAt: asset.LastSyncedAt,
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "asset_id"}, {Name: "ad_account_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"connection_id", "last_synced_at"}),
		}).Create(&access).Error
	})
}

func (r *InventoryRepository) GetAsset(ctx context.Context, id uuid.UUID) (*domain.Asset, error) {
	var asset domain.Asset
	if err := r.db.WithContext(ctx).First(&asset, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *InventoryRepository) FindAssetByMetaID(ctx context.Context, connectionID uuid.UUID, assetType domain.AssetType, metaID string) (*domain.Asset, error) {
	var asset domain.Asset
	if err := r.db.WithContext(ctx).
		First(&asset, "connection_id = ? AND asset_type = ? AND meta_asset_id = ?", connectionID, assetType, metaID).Error; err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *InventoryRepository) ListAssets(ctx context.Context, filter AssetFilter) (domain.Page[domain.Asset], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.Asset{}).Where("connection_id = ?", filter.ConnectionID)
	if filter.BusinessID != nil {
		query = query.Where("business_id = ?", *filter.BusinessID)
	}
	if filter.AdAccountID != nil {
		query = query.Where(`
			EXISTS (
			    SELECT 1
			    FROM asset_ad_accounts aaa
			    WHERE aaa.asset_id = assets.id
			      AND aaa.ad_account_id = ?
			)`, *filter.AdAccountID)
	}
	if len(filter.Types) > 0 {
		query = query.Where("asset_type IN ?", filter.Types)
	}
	if filter.ActiveOnly {
		query = query.Where("is_active = true")
	}
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		query = query.Where("name ILIKE ? OR meta_asset_id ILIKE ?", pattern, pattern)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.Asset]{}, err
	}
	var items []domain.Asset
	if err := applyPage(query.Order("asset_type ASC, name ASC, id ASC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.Asset]{}, err
	}
	return domain.Page[domain.Asset]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

// Reconcile removes inventory only where Meta returned a complete edge. It is
// deliberately atomic: on any cleanup error the previous selectable inventory
// remains intact and a later sync can retry safely.
func (r *InventoryRepository) Reconcile(ctx context.Context, input InventoryReconciliation) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := input.ReconciledAt
		if now.IsZero() {
			now = time.Now().UTC()
		}
		removedAssetIDs := make(map[uuid.UUID]struct{})

		missingAccounts := tx.Model(&domain.AdAccount{}).
			Where("connection_id = ? AND is_active = true", input.ConnectionID)
		if len(input.SeenAdAccountMetaIDs) > 0 {
			missingAccounts = missingAccounts.Where("meta_ad_account_id NOT IN ?", input.SeenAdAccountMetaIDs)
		}
		if err := missingAccounts.Updates(map[string]any{
			"is_active":  false,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}

		// A complete /me/adaccounts result proves that associations belonging
		// to a missing account are no longer usable by this connection.
		var missingAccountAccess []struct {
			AssetID uuid.UUID `gorm:"column:asset_id"`
		}
		if err := tx.Raw(`
			DELETE FROM asset_ad_accounts AS aaa
			USING ad_accounts AS aa
			WHERE aaa.ad_account_id = aa.id
			  AND aaa.connection_id = ?
			  AND aa.connection_id = ?
			  AND aa.is_active = false
			RETURNING aaa.asset_id
		`, input.ConnectionID, input.ConnectionID).Scan(&missingAccountAccess).Error; err != nil {
			return err
		}
		for _, access := range missingAccountAccess {
			removedAssetIDs[access.AssetID] = struct{}{}
		}

		for _, scope := range input.AccountAssetScopes {
			if scope.AdAccountID == uuid.Nil || scope.AssetType == "" {
				continue
			}
			staleAssets := tx.Model(&domain.Asset{}).
				Select("id").
				Where("connection_id = ? AND asset_type = ?", input.ConnectionID, scope.AssetType)
			if len(scope.SeenMetaIDs) > 0 {
				staleAssets = staleAssets.Where("meta_asset_id NOT IN ?", scope.SeenMetaIDs)
			}
			var removedAccess []domain.AssetAdAccount
			if err := tx.Clauses(clause.Returning{}).Where(
				"connection_id = ? AND ad_account_id = ? AND asset_id IN (?)",
				input.ConnectionID,
				scope.AdAccountID,
				staleAssets,
			).Delete(&removedAccess).Error; err != nil {
				return err
			}
			for _, access := range removedAccess {
				removedAssetIDs[access.AssetID] = struct{}{}
			}
		}

		if input.PagesComplete {
			stalePages := tx.Model(&domain.Asset{}).
				Where("connection_id = ? AND asset_type = ? AND is_active = true", input.ConnectionID, domain.AssetPage)
			if len(input.SeenPageMetaIDs) > 0 {
				stalePages = stalePages.Where("meta_asset_id NOT IN ?", input.SeenPageMetaIDs)
			}
			if err := stalePages.Updates(map[string]any{
				"is_active":  false,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}

			// Instagram identities can be discovered globally through Pages
			// and through one or more ad-account edges. They become inactive
			// only when absent from the complete Page result and from every
			// retained account association.
			staleInstagram := tx.Model(&domain.Asset{}).
				Where("connection_id = ? AND asset_type = ? AND is_active = true", input.ConnectionID, domain.AssetInstagramAccount).
				Where(`
					NOT EXISTS (
					    SELECT 1
					    FROM asset_ad_accounts AS aaa
					    WHERE aaa.asset_id = assets.id
					)
				`)
			if len(input.SeenPageInstagramIDs) > 0 {
				staleInstagram = staleInstagram.Where("meta_asset_id NOT IN ?", input.SeenPageInstagramIDs)
			}
			if err := staleInstagram.Updates(map[string]any{
				"is_active":  false,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
		}

		// Only assets whose access association was actually removed are
		// candidates for deactivation. This keeps unrelated orphan records
		// untouched when their corresponding Meta edge failed.
		if len(removedAssetIDs) > 0 {
			candidateIDs := make([]uuid.UUID, 0, len(removedAssetIDs))
			for id := range removedAssetIDs {
				candidateIDs = append(candidateIDs, id)
			}
			accountScopedTypes := []domain.AssetType{
				domain.AssetPixel,
				domain.AssetDataset,
				domain.AssetCustomConversion,
				domain.AssetCustomAudience,
				domain.AssetLookalikeAudience,
				domain.AssetMetaApp,
			}
			if err := tx.Model(&domain.Asset{}).
				Where("connection_id = ? AND id IN ? AND asset_type IN ? AND is_active = true", input.ConnectionID, candidateIDs, accountScopedTypes).
				Where(`
					NOT EXISTS (
					    SELECT 1
					    FROM asset_ad_accounts AS aaa
					    WHERE aaa.asset_id = assets.id
					)
				`).
				Updates(map[string]any{
					"is_active":  false,
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}

		// Asset.AdAccountID is a convenience pointer only. Keep it aligned with
		// a retained association so serialized records never imply stale access.
		return tx.Exec(`
			UPDATE assets AS a
			SET ad_account_id = (
			        SELECT aaa.ad_account_id
			        FROM asset_ad_accounts AS aaa
			        JOIN ad_accounts AS aa ON aa.id = aaa.ad_account_id
			        WHERE aaa.asset_id = a.id
			          AND aa.is_active = true
			        ORDER BY aaa.last_synced_at DESC, aaa.ad_account_id
			        LIMIT 1
			    ),
			    updated_at = ?
			WHERE a.connection_id = ?
			  AND a.ad_account_id IS NOT NULL
			  AND NOT EXISTS (
			        SELECT 1
			        FROM asset_ad_accounts AS current_access
			        JOIN ad_accounts AS current_account
			          ON current_account.id = current_access.ad_account_id
			        WHERE current_access.asset_id = a.id
			          AND current_access.ad_account_id = a.ad_account_id
			          AND current_account.is_active = true
			    )
		`, now, input.ConnectionID).Error
	})
}
