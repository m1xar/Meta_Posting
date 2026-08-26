package application

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
)

// DuplicateResult reports the outcome of cloning a campaign.
type DuplicateResult struct {
	SourceMetaID string    `json:"source_meta_id"`
	NewMetaID    string    `json:"new_meta_id"`
	AdAccountID  uuid.UUID `json:"ad_account_id"`
}

// DuplicateCampaign clones a live campaign in place through Meta's native
// deep copy: the new campaign carries the same ad sets, ads, creatives and
// targeting, and is created paused. It works for both campaigns launched
// through this service and campaigns discovered in the ad account.
//
// The copy lands in Meta directly, so it appears in the workspace as a
// discovered campaign after the next inventory sync, which is enqueued here so
// it shows up within a minute rather than at the next hourly sweep.
func (s *Service) DuplicateCampaign(ctx context.Context, campaignID uuid.UUID) (DuplicateResult, error) {
	connectionID, metaID, accountID, err := s.resolveCampaign(ctx, campaignID)
	if err != nil {
		return DuplicateResult{}, err
	}
	_, token, err := s.accessToken(ctx, connectionID)
	if err != nil {
		return DuplicateResult{}, err
	}
	copyResult, err := s.Meta.CopyCampaign(ctx, token, metaID, true)
	if err != nil {
		return DuplicateResult{}, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &connectionID,
		ActorType:    "user",
		Action:       "meta.campaign.duplicated",
		EntityType:   "campaign",
		EntityID:     metaID,
		After:        domain.MustJSON(map[string]any{"new_campaign_id": copyResult.CopiedCampaignID}),
	})
	// Surface the clone quickly as discovered inventory.
	_, _ = s.EnqueueConnectionSync(ctx, connectionID, "duplicate:"+metaID)
	return DuplicateResult{
		SourceMetaID: metaID,
		NewMetaID:    copyResult.CopiedCampaignID,
		AdAccountID:  accountID,
	}, nil
}

// resolveCampaign finds a campaign by our UUID whether it was launched through
// this service (published_objects) or discovered in the account (ad_entities),
// returning the identifiers a Meta call needs.
func (s *Service) resolveCampaign(ctx context.Context, campaignID uuid.UUID) (connectionID uuid.UUID, metaID string, accountID uuid.UUID, err error) {
	var published domain.PublishedObject
	pubErr := s.Repos.DB().WithContext(ctx).
		Where("id = ? AND object_type = ?", campaignID, domain.PublishedCampaign).
		First(&published).Error
	if pubErr == nil {
		return published.ConnectionID, published.MetaObjectID, published.AdAccountID, nil
	}
	if !errors.Is(pubErr, gorm.ErrRecordNotFound) {
		return uuid.Nil, "", uuid.Nil, pubErr
	}
	var entity domain.AdEntity
	if err := s.Repos.DB().WithContext(ctx).
		Where("id = ? AND level = ?", campaignID, domain.AdEntityCampaign).
		First(&entity).Error; err != nil {
		return uuid.Nil, "", uuid.Nil, err
	}
	return entity.ConnectionID, entity.MetaObjectID, entity.AdAccountID, nil
}
