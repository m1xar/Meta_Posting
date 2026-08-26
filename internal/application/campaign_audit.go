package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

func (s *Service) AuditPagePosts(ctx context.Context, assetID uuid.UUID, limit int) ([]map[string]any, error) {
	asset, err := s.Repos.Inventory.GetAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if asset.AssetType != domain.AssetPage {
		return nil, invalid("id", fmt.Sprintf("asset %s is not a page", assetID))
	}
	_, token, err := s.accessToken(ctx, asset.ConnectionID)
	if err != nil {
		return nil, err
	}
	posts, err := s.Meta.AuditPagePosts(ctx, token, asset.MetaAssetID, limit)
	if err != nil {
		expired, statusErr := s.markConnectionExpiredForMetaError(ctx, asset.ConnectionID, err)
		if expired {
			return nil, errors.Join(err, statusErr)
		}
		return nil, err
	}
	return posts, nil
}

func (s *Service) AuditAdAccount(
	ctx context.Context,
	adAccountID uuid.UUID,
	effectiveStatuses []string,
	limit int,
) (meta.AdAccountCampaignAudit, error) {
	account, err := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
	if err != nil {
		return meta.AdAccountCampaignAudit{}, err
	}
	_, token, err := s.accessToken(ctx, account.ConnectionID)
	if err != nil {
		return meta.AdAccountCampaignAudit{}, err
	}
	result, err := s.Meta.AuditAdAccount(
		ctx,
		token,
		account.AccountID,
		effectiveStatuses,
		limit,
	)
	if err != nil {
		expired, statusErr := s.markConnectionExpiredForMetaError(ctx, account.ConnectionID, err)
		if expired {
			return meta.AdAccountCampaignAudit{}, errors.Join(err, statusErr)
		}
		return meta.AdAccountCampaignAudit{}, err
	}
	return result, nil
}
