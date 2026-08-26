package httpapi

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

func (s *Server) capabilities(c fiber.Ctx) error {
	return jsonOK(c, http.StatusOK, s.service.Capabilities())
}

// scopedGuard fetches one guard the caller may see.
func (s *Server) scopedGuard(c fiber.Ctx, id uuid.UUID) (*domain.CampaignGuard, error) {
	scope, err := scopeFor(c)
	if err != nil {
		return nil, err
	}
	var guard domain.CampaignGuard
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.CampaignGuard{}).Where("campaign_guards.id = ?", id)
	if err := scope.Apply(query, "campaign_guards").First(&guard).Error; err != nil {
		return nil, err
	}
	return &guard, nil
}

// scopedCampaign fetches one published campaign the caller may see.
func (s *Server) scopedCampaign(c fiber.Ctx, id uuid.UUID) (*domain.PublishedObject, error) {
	scope, err := scopeFor(c)
	if err != nil {
		return nil, err
	}
	var campaign domain.PublishedObject
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.PublishedObject{}).
		Where("published_objects.id = ? AND published_objects.object_type = ?", id, domain.PublishedCampaign)
	if err := scope.Apply(query, "published_objects").First(&campaign).Error; err != nil {
		return nil, err
	}
	return &campaign, nil
}

func (s *Server) listGuards(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	batchID, err := optionalID(c, "batch_id")
	if err != nil {
		return err
	}
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.CampaignGuard{})
	query = scope.Apply(query, "campaign_guards")
	if batchID != nil {
		query = query.Where("batch_id = ?", *batchID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return err
	}
	var items []domain.CampaignGuard
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) getGuard(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	guard, err := s.scopedGuard(c, id)
	if err != nil {
		return err
	}
	checks, err := s.service.Repos.Guards.ListChecks(c.Context(), guard.ID, nil)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"guard": guard, "checks": checks})
}

func (s *Server) updateGuard(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if _, err := s.scopedGuard(c, id); err != nil {
		return err
	}
	var request application.UpdateGuardRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	guard, err := s.service.UpdateGuard(c.Context(), id, request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, guard)
}

func (s *Server) enableGuard(c fiber.Ctx) error  { return s.setGuardStatus(c, domain.GuardActive) }
func (s *Server) disableGuard(c fiber.Ctx) error { return s.setGuardStatus(c, domain.GuardDisabled) }

func (s *Server) setGuardStatus(c fiber.Ctx, status domain.GuardStatus) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if _, err := s.scopedGuard(c, id); err != nil {
		return err
	}
	var body struct{}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	if err := s.service.Repos.Guards.SetStatus(c.Context(), id, status, s.service.Now()); err != nil {
		return err
	}
	guard, err := s.service.Repos.Guards.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, guard)
}

// campaignView is one campaign row for the UI: the published object joined
// with its latest lifetime insights, tracker roll-up, guard, and checks.
type campaignView struct {
	Campaign domain.PublishedObject  `json:"campaign"`
	Insights *domain.InsightSnapshot `json:"insights,omitempty"`
	Tracker  *domain.TrackerStat     `json:"tracker,omitempty"`
	Guard    *domain.CampaignGuard   `json:"guard,omitempty"`
	Checks   []domain.GuardCheck     `json:"checks,omitempty"`
}

type campaignTotals struct {
	Campaigns      int     `json:"campaigns"`
	Live           int     `json:"live"`
	Paused         int     `json:"paused"`
	Spend          float64 `json:"spend"`
	Clicks         int64   `json:"clicks"`
	Impressions    int64   `json:"impressions"`
	TrackerClicks  int64   `json:"tracker_clicks"`
	TrackerLeads   float64 `json:"tracker_leads"`
	TrackerSales   float64 `json:"tracker_sales"`
	TrackerRevenue float64 `json:"tracker_revenue"`
}

func campaignIsLive(status string) bool {
	switch status {
	case "ACTIVE", "IN_PROCESS", "WITH_ISSUES":
		return true
	}
	return false
}

func campaignIsPaused(status string) bool {
	return status == "PAUSED" || status == "CAMPAIGN_PAUSED" || status == "ADSET_PAUSED"
}

func (totals *campaignTotals) add(view campaignView) {
	totals.Campaigns++
	if campaignIsLive(view.Campaign.EffectiveStatus) {
		totals.Live++
	} else if campaignIsPaused(view.Campaign.EffectiveStatus) {
		totals.Paused++
	}
	if view.Insights != nil {
		totals.Spend += view.Insights.Spend
		totals.Clicks += view.Insights.Clicks
		totals.Impressions += view.Insights.Impressions
	}
	if view.Tracker != nil {
		totals.TrackerClicks += view.Tracker.Clicks
		totals.TrackerLeads += view.Tracker.Leads
		totals.TrackerSales += view.Tracker.Sales
		totals.TrackerRevenue += view.Tracker.Revenue
	}
}

// campaignViews joins every campaign the scope may see with its metrics.
func (s *Server) campaignViews(c fiber.Ctx, adAccountID *uuid.UUID) ([]campaignView, campaignTotals, error) {
	totals := campaignTotals{}
	scope, err := scopeFor(c)
	if err != nil {
		return nil, totals, err
	}
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.PublishedObject{}).
		Where("published_objects.object_type = ?", domain.PublishedCampaign)
	query = scope.Apply(query, "published_objects")
	if adAccountID != nil {
		query = query.Where("published_objects.ad_account_id = ?", *adAccountID)
	}
	var campaigns []domain.PublishedObject
	if err := query.Order("created_at DESC, id DESC").Limit(1000).Find(&campaigns).Error; err != nil {
		return nil, totals, err
	}
	objectIDs := make([]uuid.UUID, 0, len(campaigns))
	batchIDs := make(map[uuid.UUID]struct{})
	for _, campaign := range campaigns {
		objectIDs = append(objectIDs, campaign.ID)
		batchIDs[campaign.BatchID] = struct{}{}
	}
	snapshots, err := s.service.Repos.Insights.LatestForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	trackerStats, err := s.service.Repos.Tracker.ListForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	checks, err := s.service.Repos.Guards.ListChecksForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	guardsQuery := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.CampaignGuard{})
	guardsQuery = scope.Apply(guardsQuery, "campaign_guards")
	var guards []domain.CampaignGuard
	if err := guardsQuery.Order("created_at ASC").Limit(2000).Find(&guards).Error; err != nil {
		return nil, totals, err
	}

	snapshotByObject := make(map[uuid.UUID]*domain.InsightSnapshot, len(snapshots))
	for index := range snapshots {
		if snapshots[index].PublishedObjectID != nil {
			snapshotByObject[*snapshots[index].PublishedObjectID] = &snapshots[index]
		}
	}
	trackerByObject := make(map[uuid.UUID]*domain.TrackerStat, len(trackerStats))
	for index := range trackerStats {
		if trackerStats[index].PublishedObjectID != nil {
			trackerByObject[*trackerStats[index].PublishedObjectID] = &trackerStats[index]
		}
	}
	checksByObject := make(map[uuid.UUID][]domain.GuardCheck)
	for _, check := range checks {
		checksByObject[check.PublishedObjectID] = append(checksByObject[check.PublishedObjectID], check)
	}
	guardByBatch := make(map[uuid.UUID]*domain.CampaignGuard)
	guardByObject := make(map[uuid.UUID]*domain.CampaignGuard)
	for index := range guards {
		guard := &guards[index]
		if guard.PublishedObjectID != nil {
			guardByObject[*guard.PublishedObjectID] = guard
		} else if guard.BatchID != nil {
			if _, exists := guardByBatch[*guard.BatchID]; !exists {
				guardByBatch[*guard.BatchID] = guard
			}
		}
	}

	views := make([]campaignView, 0, len(campaigns))
	for index := range campaigns {
		campaign := campaigns[index]
		view := campaignView{Campaign: campaign}
		view.Insights = snapshotByObject[campaign.ID]
		view.Tracker = trackerByObject[campaign.ID]
		view.Checks = checksByObject[campaign.ID]
		if guard, ok := guardByObject[campaign.ID]; ok {
			view.Guard = guard
		} else if guard, ok := guardByBatch[campaign.BatchID]; ok {
			view.Guard = guard
		}
		views = append(views, view)
		totals.add(view)
	}
	return views, totals, nil
}

func (s *Server) listCampaignViews(c fiber.Ctx) error {
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	views, totals, err := s.campaignViews(c, adAccountID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"campaigns": views, "totals": totals})
}

func (s *Server) accountStats(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	var account domain.AdAccount
	accountQuery := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.AdAccount{}).
		Where("ad_accounts.id = ?", id)
	if err := scope.Apply(accountQuery, "ad_accounts").First(&account).Error; err != nil {
		return err
	}
	views, totals, err := s.campaignViews(c, &id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"account": account, "campaigns": views, "totals": totals})
}

func (s *Server) pauseCampaign(c fiber.Ctx) error  { return s.setCampaignStatus(c, true) }
func (s *Server) resumeCampaign(c fiber.Ctx) error { return s.setCampaignStatus(c, false) }

func (s *Server) setCampaignStatus(c fiber.Ctx, pause bool) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if _, err := s.scopedCampaign(c, id); err != nil {
		return err
	}
	var body struct{}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	campaign, err := s.service.SetCampaignStatus(c.Context(), id, pause)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, campaign)
}

// createCampaignGuard gives one campaign its own ladder, shadowing the batch
// guard.
func (s *Server) createCampaignGuard(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	campaign, err := s.scopedCampaign(c, id)
	if err != nil {
		return err
	}
	var request application.UpdateGuardRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	name := request.Name
	if name == "" {
		name = "Guard " + campaign.Name
	}
	guard, err := s.service.CreateGuard(c.Context(), application.CreateGuardRequest{
		ConnectionID:              campaign.ConnectionID,
		PublishedObjectID:         &campaign.ID,
		Name:                      name,
		Checkpoints:               request.Checkpoints,
		EvaluationIntervalSeconds: request.EvaluationIntervalSeconds,
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, guard)
}
