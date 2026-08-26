package httpapi

import (
	"net/http"
	"time"

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

// campaignSummary is the campaign identity a row carries, unified across the
// two places a campaign can come from: launched through this service
// (published_objects) or discovered in the ad account (ad_entities).
type campaignSummary struct {
	ID              uuid.UUID  `json:"id"`
	Source          string     `json:"source"`
	AdAccountID     uuid.UUID  `json:"ad_account_id"`
	BatchID         *uuid.UUID `json:"batch_id,omitempty"`
	MetaObjectID    string     `json:"meta_object_id"`
	Name            string     `json:"name"`
	EffectiveStatus string     `json:"effective_status"`
	Objective       string     `json:"objective,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

const (
	campaignSourceLaunched   = "launched"
	campaignSourceDiscovered = "discovered"
)

// campaignView is one campaign row for the UI: the campaign joined with its
// lifetime insights, tracker roll-up, guard, and checkpoint outcomes.
type campaignView struct {
	Campaign campaignSummary         `json:"campaign"`
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
//
// Launched campaigns carry their lifetime insight snapshot, guard, and
// checkpoint history. Discovered campaigns - launched outside this service -
// get their numbers from the account-wide daily insight rows instead, and
// their tracker roll-up by Meta campaign ID.
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
	var published []domain.PublishedObject
	if err := query.Order("created_at DESC, id DESC").Limit(1000).Find(&published).Error; err != nil {
		return nil, totals, err
	}

	entityQuery := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.AdEntity{}).
		Where("ad_entities.level = ?", domain.AdEntityCampaign)
	entityQuery = scope.Apply(entityQuery, "ad_entities")
	if adAccountID != nil {
		entityQuery = entityQuery.Where("ad_entities.ad_account_id = ?", *adAccountID)
	}
	var entities []domain.AdEntity
	if err := entityQuery.Order("meta_created_time DESC NULLS LAST, created_at DESC").Limit(2000).Find(&entities).Error; err != nil {
		return nil, totals, err
	}

	objectIDs := make([]uuid.UUID, 0, len(published))
	publishedMetaIDs := make(map[string]struct{}, len(published))
	for _, campaign := range published {
		objectIDs = append(objectIDs, campaign.ID)
		publishedMetaIDs[campaign.MetaObjectID] = struct{}{}
	}
	metaIDs := make([]string, 0, len(published)+len(entities))
	for _, campaign := range published {
		metaIDs = append(metaIDs, campaign.MetaObjectID)
	}
	entityStatus := make(map[string]string, len(entities))
	discovered := make([]domain.AdEntity, 0, len(entities))
	for _, entity := range entities {
		entityStatus[entity.MetaObjectID] = entity.EffectiveStatus
		if _, launched := publishedMetaIDs[entity.MetaObjectID]; launched {
			continue
		}
		discovered = append(discovered, entity)
		metaIDs = append(metaIDs, entity.MetaObjectID)
	}

	snapshots, err := s.service.Repos.Insights.LatestForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	dailyTotals, err := s.dailyCampaignTotals(c, metaIDs)
	if err != nil {
		return nil, totals, err
	}
	trackerByMeta, trackerByObject, err := s.trackerFor(c, objectIDs, metaIDs)
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

	views := make([]campaignView, 0, len(published)+len(discovered))
	for index := range published {
		campaign := published[index]
		batchID := campaign.BatchID
		status := campaign.EffectiveStatus
		if fresh, ok := entityStatus[campaign.MetaObjectID]; ok && fresh != "" {
			// The entity sync sees status changes sooner than the hourly
			// published-object refresh.
			status = fresh
		}
		view := campaignView{Campaign: campaignSummary{
			ID:              campaign.ID,
			Source:          campaignSourceLaunched,
			AdAccountID:     campaign.AdAccountID,
			BatchID:         &batchID,
			MetaObjectID:    campaign.MetaObjectID,
			Name:            campaign.Name,
			EffectiveStatus: status,
			CreatedAt:       campaign.CreatedAt,
		}}
		view.Insights = snapshotByObject[campaign.ID]
		if view.Insights == nil {
			view.Insights = dailyTotals[campaign.MetaObjectID]
		}
		if tracker, ok := trackerByObject[campaign.ID]; ok {
			view.Tracker = tracker
		} else {
			view.Tracker = trackerByMeta[campaign.MetaObjectID]
		}
		view.Checks = checksByObject[campaign.ID]
		if guard, ok := guardByObject[campaign.ID]; ok {
			view.Guard = guard
		} else if guard, ok := guardByBatch[campaign.BatchID]; ok {
			view.Guard = guard
		}
		views = append(views, view)
		totals.add(view)
	}
	for index := range discovered {
		entity := discovered[index]
		createdAt := entity.CreatedAt
		if entity.MetaCreatedTime != nil {
			createdAt = *entity.MetaCreatedTime
		}
		view := campaignView{Campaign: campaignSummary{
			ID:              entity.ID,
			Source:          campaignSourceDiscovered,
			AdAccountID:     entity.AdAccountID,
			MetaObjectID:    entity.MetaObjectID,
			Name:            entity.Name,
			EffectiveStatus: entity.EffectiveStatus,
			Objective:       entity.Objective,
			CreatedAt:       createdAt,
		}}
		view.Insights = dailyTotals[entity.MetaObjectID]
		view.Tracker = trackerByMeta[entity.MetaObjectID]
		views = append(views, view)
		totals.add(view)
	}
	return views, totals, nil
}

// dailyCampaignTotals rolls the account-wide daily rows up to one lifetime
// figure per campaign, shaped as a snapshot so rows render the same way.
func (s *Server) dailyCampaignTotals(c fiber.Ctx, metaIDs []string) (map[string]*domain.InsightSnapshot, error) {
	if len(metaIDs) == 0 {
		return map[string]*domain.InsightSnapshot{}, nil
	}
	type rollup struct {
		MetaObjectID string
		Spend        float64
		Impressions  int64
		Clicks       int64
	}
	var rows []rollup
	if err := s.service.Repos.DB().WithContext(c.Context()).
		Raw(`SELECT meta_object_id, SUM(spend) AS spend,
		            SUM(impressions) AS impressions, SUM(clicks) AS clicks
		     FROM ad_insights_daily
		     WHERE level = 'campaign' AND meta_object_id IN ?
		     GROUP BY meta_object_id`, metaIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]*domain.InsightSnapshot, len(rows))
	for index := range rows {
		row := rows[index]
		result[row.MetaObjectID] = &domain.InsightSnapshot{
			MetaObjectID: row.MetaObjectID,
			Level:        domain.InsightCampaign,
			Spend:        row.Spend,
			Impressions:  row.Impressions,
			Clicks:       row.Clicks,
		}
	}
	return result, nil
}

func (s *Server) trackerFor(
	c fiber.Ctx,
	objectIDs []uuid.UUID,
	metaIDs []string,
) (map[string]*domain.TrackerStat, map[uuid.UUID]*domain.TrackerStat, error) {
	byMeta := make(map[string]*domain.TrackerStat)
	byObject := make(map[uuid.UUID]*domain.TrackerStat)
	objectStats, err := s.service.Repos.Tracker.ListForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, nil, err
	}
	for index := range objectStats {
		if objectStats[index].PublishedObjectID != nil {
			byObject[*objectStats[index].PublishedObjectID] = &objectStats[index]
		}
	}
	metaStats, err := s.service.Repos.Tracker.ListForCampaignIDs(c.Context(), metaIDs)
	if err != nil {
		return nil, nil, err
	}
	for index := range metaStats {
		stat := &metaStats[index]
		if existing, ok := byMeta[stat.MetaCampaignID]; !ok || stat.Clicks > existing.Clicks {
			byMeta[stat.MetaCampaignID] = stat
		}
	}
	return byMeta, byObject, nil
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
