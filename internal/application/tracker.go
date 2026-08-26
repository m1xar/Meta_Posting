package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
	// Campaigns discovered from the ad accounts count too: buyers launch
	// outside this service as well, and their tracker traffic is tagged with
	// the same campaign name/id macros.
	var entities []domain.AdEntity
	if err := s.Repos.DB().WithContext(ctx).
		Where("level = ?", domain.AdEntityCampaign).
		Order("created_at ASC").
		Find(&entities).Error; err != nil {
		return err
	}
	if len(campaigns) == 0 && len(entities) == 0 {
		return nil
	}

	type trackerTarget struct {
		ObjectID     *uuid.UUID
		ConnectionID uuid.UUID
		MetaID       string
		Name         string
	}
	byID := make(map[string]*trackerTarget, len(campaigns)+len(entities))
	byName := make(map[string]*trackerTarget, len(campaigns)+len(entities))
	ids := make([]string, 0, len(campaigns)+len(entities))
	names := make([]string, 0, len(campaigns)+len(entities))
	addTarget := func(target *trackerTarget) {
		if id := strings.TrimSpace(target.MetaID); id != "" {
			if _, seen := byID[id]; !seen {
				byID[id] = target
				ids = append(ids, id)
			}
		}
		if name := strings.TrimSpace(target.Name); name != "" {
			if _, seen := byName[name]; !seen {
				byName[name] = target
				names = append(names, name)
			}
		}
	}
	// Launched campaigns first so a shared name resolves to ours.
	for index := range campaigns {
		campaign := &campaigns[index]
		objectID := campaign.ID
		addTarget(&trackerTarget{
			ObjectID:     &objectID,
			ConnectionID: campaign.ConnectionID,
			MetaID:       campaign.MetaObjectID,
			Name:         campaign.Name,
		})
	}
	for index := range entities {
		entity := &entities[index]
		addTarget(&trackerTarget{
			ObjectID:     entity.PublishedObjectID,
			ConnectionID: entity.ConnectionID,
			MetaID:       entity.MetaObjectID,
			Name:         entity.Name,
		})
	}

	rows, err := s.Tracker.CampaignReport(ctx, ids, names)
	if err != nil {
		return fmt.Errorf("keitaro campaign report: %w", err)
	}
	now := s.Now()
	merged := make(map[string]*domain.TrackerStat)
	for _, row := range rows {
		target := byID[strings.TrimSpace(row.SubID7)]
		if target == nil {
			target = byName[strings.TrimSpace(row.SubID3)]
		}
		if target == nil {
			continue
		}
		key := target.MetaID + "\x00" + target.Name
		stat, exists := merged[key]
		if !exists {
			connectionID := target.ConnectionID
			stat = &domain.TrackerStat{
				ConnectionID:      &connectionID,
				PublishedObjectID: target.ObjectID,
				MetaCampaignID:    target.MetaID,
				CampaignName:      target.Name,
				Raw:               domain.MustJSON(map[string]any{}),
				LastSyncedAt:      now,
			}
			merged[key] = stat
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
