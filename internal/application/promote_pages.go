package application

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

// PromotablePage is a page an ad account may advertise, joined to our stored
// page asset (when we have one) so the caller can load its posts.
type PromotablePage struct {
	MetaID  string     `json:"meta_id"`
	Name    string     `json:"name"`
	AssetID *uuid.UUID `json:"asset_id,omitempty"`
}

// PromotablePages returns the pages the given ad account may run ads for,
// matched against our synced page assets so their posts can be loaded.
func (s *Service) PromotablePages(ctx context.Context, adAccountID uuid.UUID) ([]PromotablePage, error) {
	account, err := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
	if err != nil {
		return nil, err
	}
	_, token, err := s.accessToken(ctx, account.ConnectionID)
	if err != nil {
		return nil, err
	}
	rawAccountID := firstNonEmpty(account.MetaAdAccountID, account.AccountID)
	pages, err := s.Meta.PromotablePages(ctx, token, rawAccountID)
	if err != nil {
		return nil, err
	}
	// Match Meta page ids to our page assets on this connection so the UI can
	// load posts by asset uuid.
	var assets []domain.Asset
	if err := s.Repos.DB().WithContext(ctx).
		Select("id, meta_asset_id").
		Where("connection_id = ? AND asset_type = ?", account.ConnectionID, domain.AssetPage).
		Find(&assets).Error; err != nil {
		return nil, err
	}
	assetByMeta := make(map[string]uuid.UUID, len(assets))
	for i := range assets {
		assetByMeta[assets[i].MetaAssetID] = assets[i].ID
	}
	result := make([]PromotablePage, 0, len(pages))
	for _, page := range pages {
		metaID := strings.TrimSpace(page.ID)
		if metaID == "" {
			continue
		}
		item := PromotablePage{MetaID: metaID, Name: page.Name}
		if id, ok := assetByMeta[metaID]; ok {
			item.AssetID = &id
		}
		result = append(result, item)
	}
	return result, nil
}
