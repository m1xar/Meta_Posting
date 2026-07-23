package application

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

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
