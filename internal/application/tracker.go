package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/keitaro"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// TrackerClient is the read surface this service needs from Keitaro.
type TrackerClient interface {
	CampaignReport(ctx context.Context, campaignIDs, campaignNames []string) ([]keitaro.ReportRow, error)
}

func metaStatusFor(pause bool) meta.EntityStatus {
	if pause {
		return meta.StatusPaused
	}
	return meta.StatusActive
}

// SyncTrackerStats pulls the all-time Keitaro roll-up for every published
// campaign in the system and stores one row per campaign. Matching prefers the
// campaign ID carried in sub_id_7 and falls back to the campaign name in
// sub_id_3.
func (s *Service) SyncTrackerStats(ctx context.Context) error {
	if s.Tracker == nil {
		return nil
	}
	var campaigns []domain.PublishedObject
	if err := s.Repos.DB().WithContext(ctx).
		Where("object_type = ?", domain.PublishedCampaign).
		Order("created_at ASC").
		Find(&campaigns).Error; err != nil {
		return err
	}
	if len(campaigns) == 0 {
		return nil
	}

	byID := make(map[string]*domain.PublishedObject, len(campaigns))
	byName := make(map[string]*domain.PublishedObject, len(campaigns))
	ids := make([]string, 0, len(campaigns))
	names := make([]string, 0, len(campaigns))
	for index := range campaigns {
		campaign := &campaigns[index]
		if id := strings.TrimSpace(campaign.MetaObjectID); id != "" {
			if _, seen := byID[id]; !seen {
				byID[id] = campaign
				ids = append(ids, id)
			}
		}
		if name := strings.TrimSpace(campaign.Name); name != "" {
			if _, seen := byName[name]; !seen {
				byName[name] = campaign
				names = append(names, name)
			}
		}
	}

	rows, err := s.Tracker.CampaignReport(ctx, ids, names)
	if err != nil {
		return fmt.Errorf("keitaro campaign report: %w", err)
	}
	now := s.Now()
	merged := make(map[string]*domain.TrackerStat)
	for _, row := range rows {
		campaign := byID[strings.TrimSpace(row.SubID7)]
		if campaign == nil {
			campaign = byName[strings.TrimSpace(row.SubID3)]
		}
		if campaign == nil {
			continue
		}
		stat, exists := merged[campaign.ID.String()]
		if !exists {
			stat = &domain.TrackerStat{
				ConnectionID:      &campaign.ConnectionID,
				PublishedObjectID: &campaign.ID,
				MetaCampaignID:    campaign.MetaObjectID,
				CampaignName:      campaign.Name,
				Raw:               domain.MustJSON(map[string]any{}),
				LastSyncedAt:      now,
			}
			merged[campaign.ID.String()] = stat
		}
		// The ID-filtered and name-filtered reports can both return the same
		// visitors, so keep the larger roll-up instead of double counting.
		if row.Clicks > stat.Clicks {
			stat.Clicks = row.Clicks
			stat.UniqueClicks = row.UniqueClicks
			stat.Leads = row.Leads
			stat.Sales = row.Sales
			stat.Revenue = row.Revenue
			stat.Raw = domain.MustJSON(row)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	stats := make([]domain.TrackerStat, 0, len(merged))
	for _, stat := range merged {
		stats = append(stats, *stat)
	}
	if err := s.Repos.Tracker.UpsertMany(ctx, stats); err != nil {
		return fmt.Errorf("store tracker stats: %w", err)
	}
	return nil
}
