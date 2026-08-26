package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

const defaultGuardInterval = 300

func (s *Service) CreateGuard(ctx context.Context, request CreateGuardRequest) (*domain.CampaignGuard, error) {
	if request.ConnectionID == uuid.Nil {
		return nil, invalid("connection_id", "is required")
	}
	if request.Name == "" {
		return nil, invalid("name", "is required")
	}
	if request.BatchID == nil && request.PublishedObjectID == nil {
		return nil, invalid("scope", "batch_id or published_object_id is required")
	}
	if _, err := s.Repos.MetaConnections.Get(ctx, request.ConnectionID); err != nil {
		return nil, err
	}
	if request.BatchID != nil {
		batch, err := s.Repos.Batches.Get(ctx, *request.BatchID)
		if err != nil {
			return nil, err
		}
		if batch.ConnectionID != request.ConnectionID {
			return nil, invalid("batch_id", "belongs to another connection")
		}
	}
	if request.Status == "" {
		request.Status = domain.GuardActive
	}
	if request.Status != domain.GuardActive && request.Status != domain.GuardDisabled {
		return nil, invalid("status", "must be active or disabled")
	}
	checkpoints, err := normalizeCheckpoints(request.Checkpoints)
	if err != nil {
		return nil, err
	}
	interval := request.EvaluationIntervalSeconds
	if interval == 0 {
		interval = defaultGuardInterval
	}
	if interval < 60 {
		return nil, invalid("evaluation_interval_seconds", "must be at least 60")
	}
	guard := &domain.CampaignGuard{
		ConnectionID:              request.ConnectionID,
		BatchID:                   request.BatchID,
		PublishedObjectID:         request.PublishedObjectID,
		Name:                      request.Name,
		Status:                    request.Status,
		Checkpoints:               domain.MustJSON(checkpoints),
		EvaluationIntervalSeconds: interval,
		NextEvaluationAt:          s.Now(),
	}
	if err := s.Repos.Guards.Create(ctx, guard); err != nil {
		return nil, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &guard.ConnectionID,
		ActorType:    "user",
		Action:       "campaign_guard.created",
		EntityType:   "campaign_guard",
		EntityID:     guard.ID.String(),
		After:        domain.MustJSON(guard),
	})
	return guard, nil
}

func (s *Service) UpdateGuard(ctx context.Context, id uuid.UUID, request UpdateGuardRequest) (*domain.CampaignGuard, error) {
	existing, err := s.Repos.Guards.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *existing
	if request.Name != "" {
		updated.Name = request.Name
	}
	if request.Status != "" {
		if request.Status != domain.GuardActive && request.Status != domain.GuardDisabled {
			return nil, invalid("status", "must be active or disabled")
		}
		updated.Status = request.Status
	}
	if request.Checkpoints != nil {
		checkpoints, err := normalizeCheckpoints(request.Checkpoints)
		if err != nil {
			return nil, err
		}
		updated.Checkpoints = domain.MustJSON(checkpoints)
	}
	if request.EvaluationIntervalSeconds != 0 {
		if request.EvaluationIntervalSeconds < 60 {
			return nil, invalid("evaluation_interval_seconds", "must be at least 60")
		}
		updated.EvaluationIntervalSeconds = request.EvaluationIntervalSeconds
	}
	if err := s.Repos.Guards.UpdateConfiguration(ctx, &updated); err != nil {
		return nil, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &updated.ConnectionID,
		ActorType:    "user",
		Action:       "campaign_guard.updated",
		EntityType:   "campaign_guard",
		EntityID:     updated.ID.String(),
		Before:       domain.MustJSON(existing),
		After:        domain.MustJSON(updated),
	})
	return s.Repos.Guards.Get(ctx, id)
}

func normalizeCheckpoints(checkpoints []GuardCheckpoint) ([]GuardCheckpoint, error) {
	if len(checkpoints) == 0 {
		return nil, invalid("checkpoints", "at least one checkpoint is required")
	}
	if len(checkpoints) > 20 {
		return nil, invalid("checkpoints", "at most 20 checkpoints are supported")
	}
	normalized := append([]GuardCheckpoint(nil), checkpoints...)
	sort.SliceStable(normalized, func(i, j int) bool { return normalized[i].Spend < normalized[j].Spend })
	for index, checkpoint := range normalized {
		if checkpoint.Spend <= 0 {
			return nil, invalid("checkpoints", "every checkpoint needs spend greater than zero")
		}
		if index > 0 && checkpoint.Spend == normalized[index-1].Spend {
			return nil, invalid("checkpoints", "checkpoint spend values must be unique")
		}
		if checkpoint.MinClicks < 0 || checkpoint.MinImpressions < 0 || checkpoint.MinTrackerClicks < 0 ||
			checkpoint.MinTrackerLeads < 0 || checkpoint.MinTrackerSales < 0 || checkpoint.MinTrackerRevenue < 0 {
			return nil, invalid("checkpoints", "thresholds cannot be negative")
		}
		if checkpoint.MinClicks == 0 && checkpoint.MinImpressions == 0 && checkpoint.MinTrackerClicks == 0 &&
			checkpoint.MinTrackerLeads == 0 && checkpoint.MinTrackerSales == 0 && checkpoint.MinTrackerRevenue == 0 {
			return nil, invalid("checkpoints", "every checkpoint needs at least one threshold")
		}
	}
	return normalized, nil
}

// EvaluateDueGuards runs every guard whose next evaluation is due. Campaign
// object-scoped guards shadow the batch guard for that campaign.
func (s *Service) EvaluateDueGuards(ctx context.Context, connectionID *uuid.UUID) error {
	now := s.Now()
	due, err := s.Repos.Guards.ListDue(ctx, connectionID, now, 500)
	if err != nil {
		return err
	}
	var failures []error
	expiredConnections := make(map[uuid.UUID]struct{})
	for index := range due {
		guard := &due[index]
		if _, expired := expiredConnections[guard.ConnectionID]; expired {
			continue
		}
		if err := s.evaluateGuard(ctx, guard, now); err != nil {
			failures = append(failures, fmt.Errorf("guard %s: %w", guard.ID, err))
			expired, statusErr := s.markConnectionExpiredForMetaError(ctx, guard.ConnectionID, err)
			if statusErr != nil {
				failures = append(failures, statusErr)
			}
			if expired {
				expiredConnections[guard.ConnectionID] = struct{}{}
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) evaluateGuard(ctx context.Context, guard *domain.CampaignGuard, now time.Time) error {
	var checkpoints []GuardCheckpoint
	if err := guard.Checkpoints.Decode(&checkpoints); err != nil {
		return fmt.Errorf("decode checkpoints: %w", err)
	}
	next := now.Add(time.Duration(guard.EvaluationIntervalSeconds) * time.Second)
	campaigns, err := s.guardCampaigns(ctx, guard)
	if err != nil {
		return err
	}
	if len(campaigns) == 0 || len(checkpoints) == 0 {
		return s.Repos.Guards.MarkEvaluated(ctx, guard.ID, now, next)
	}
	_, token, err := s.accessToken(ctx, guard.ConnectionID)
	if err != nil {
		return err
	}
	var failures []error
	for index := range campaigns {
		campaign := &campaigns[index]
		if err := s.evaluateGuardCampaign(ctx, guard, checkpoints, campaign, token, now); err != nil {
			failures = append(failures, fmt.Errorf("campaign %s: %w", campaign.MetaObjectID, err))
			if isMetaAccessTokenError(err) {
				break
			}
		}
	}
	if err := s.Repos.Guards.MarkEvaluated(ctx, guard.ID, now, next); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (s *Service) guardCampaigns(ctx context.Context, guard *domain.CampaignGuard) ([]domain.PublishedObject, error) {
	query := s.Repos.DB().WithContext(ctx).
		Where("connection_id = ? AND object_type = ?", guard.ConnectionID, domain.PublishedCampaign)
	switch {
	case guard.PublishedObjectID != nil:
		query = query.Where("id = ?", *guard.PublishedObjectID)
	case guard.BatchID != nil:
		query = query.Where("batch_id = ?", *guard.BatchID)
		// Campaigns with their own guard are evaluated by that guard instead.
		query = query.Where("id NOT IN (?)", s.Repos.DB().WithContext(ctx).
			Model(&domain.CampaignGuard{}).
			Select("published_object_id").
			Where("published_object_id IS NOT NULL AND status = ?", domain.GuardActive))
	default:
		return nil, errors.New("guard has no scope")
	}
	var campaigns []domain.PublishedObject
	err := query.Order("created_at ASC").Find(&campaigns).Error
	return campaigns, err
}

// GuardObservation is the metric set a checkpoint is judged against.
type GuardObservation struct {
	Spend          float64 `json:"spend"`
	Clicks         int64   `json:"clicks"`
	Impressions    int64   `json:"impressions"`
	TrackerClicks  int64   `json:"tracker_clicks"`
	TrackerLeads   float64 `json:"tracker_leads"`
	TrackerSales   float64 `json:"tracker_sales"`
	TrackerRevenue float64 `json:"tracker_revenue"`
}

func checkpointSatisfied(checkpoint GuardCheckpoint, observed GuardObservation) bool {
	return observed.Clicks >= checkpoint.MinClicks &&
		observed.Impressions >= checkpoint.MinImpressions &&
		observed.TrackerClicks >= checkpoint.MinTrackerClicks &&
		observed.TrackerLeads >= checkpoint.MinTrackerLeads &&
		observed.TrackerSales >= checkpoint.MinTrackerSales &&
		observed.TrackerRevenue >= checkpoint.MinTrackerRevenue
}

func (s *Service) evaluateGuardCampaign(
	ctx context.Context,
	guard *domain.CampaignGuard,
	checkpoints []GuardCheckpoint,
	campaign *domain.PublishedObject,
	token string,
	now time.Time,
) error {
	if campaign.EffectiveStatus == "PAUSED" || campaign.EffectiveStatus == "ARCHIVED" || campaign.EffectiveStatus == "DELETED" {
		return nil
	}
	observed, err := s.observeCampaign(ctx, guard.ConnectionID, campaign)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // No lifetime snapshot yet; nothing to judge.
		}
		return err
	}
	existing, err := s.Repos.Guards.ListChecks(ctx, guard.ID, &campaign.ID)
	if err != nil {
		return err
	}
	decided := make(map[int]domain.GuardCheckStatus, len(existing))
	for _, check := range existing {
		decided[check.CheckpointIndex] = check.Status
	}
	for index, checkpoint := range checkpoints {
		if observed.Spend < checkpoint.Spend {
			break // The ladder is sorted; later checkpoints are further away.
		}
		if status, done := decided[index]; done && status != domain.GuardCheckFailed {
			continue
		}
		passed := checkpointSatisfied(checkpoint, *observed)
		check := &domain.GuardCheck{
			GuardID:           guard.ID,
			PublishedObjectID: campaign.ID,
			MetaObjectID:      campaign.MetaObjectID,
			CheckpointIndex:   index,
			CheckpointSpend:   checkpoint.Spend,
			Observed:          domain.MustJSON(observed),
			Thresholds:        domain.MustJSON(checkpoint),
			EvaluatedAt:       now,
		}
		if passed {
			check.Status = domain.GuardCheckPassed
			if err := s.Repos.Guards.SaveCheck(ctx, check); err != nil {
				return err
			}
			continue
		}
		check.Status = domain.GuardCheckFailed
		pauseErr := s.Meta.PauseEntity(ctx, token, campaign.MetaObjectID)
		if pauseErr != nil {
			check.Error = pauseErr.Error()
		} else {
			check.Paused = true
			if err := s.Repos.Batches.UpdatePublishedStatus(
				ctx,
				campaign.ID,
				"PAUSED",
				domain.MustJSON(map[string]any{"paused_by_guard": guard.ID}),
				now,
			); err != nil {
				check.Error = err.Error()
			}
		}
		if err := s.Repos.Guards.SaveCheck(ctx, check); err != nil {
			return errors.Join(pauseErr, err)
		}
		if check.Paused {
			s.audit(ctx, domain.AuditEvent{
				ConnectionID: &guard.ConnectionID,
				ActorType:    "campaign_guard",
				ActorID:      guard.ID.String(),
				Action:       "meta.campaign.paused",
				EntityType:   string(campaign.ObjectType),
				EntityID:     campaign.MetaObjectID,
				Severity:     domain.AuditWarning,
				Before:       domain.MustJSON(map[string]any{"status": campaign.EffectiveStatus}),
				After:        domain.MustJSON(map[string]any{"status": "PAUSED", "checkpoint_index": index}),
				Metadata:     domain.MustJSON(map[string]any{"observed": observed, "thresholds": checkpoint}),
			})
		}
		return pauseErr
	}
	return nil
}

func (s *Service) observeCampaign(
	ctx context.Context,
	connectionID uuid.UUID,
	campaign *domain.PublishedObject,
) (*GuardObservation, error) {
	snapshot, err := s.Repos.Insights.Latest(ctx, connectionID, campaign.MetaObjectID, domain.InsightCampaign)
	if err != nil {
		return nil, err
	}
	observed := &GuardObservation{
		Spend:       snapshot.Spend,
		Clicks:      snapshot.Clicks,
		Impressions: snapshot.Impressions,
	}
	tracker, err := s.Repos.Tracker.ForObject(ctx, campaign.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if tracker != nil {
		observed.TrackerClicks = tracker.Clicks
		observed.TrackerLeads = tracker.Leads
		observed.TrackerSales = tracker.Sales
		observed.TrackerRevenue = tracker.Revenue
	}
	return observed, nil
}

// SetCampaignStatus pauses or resumes one published campaign. Resuming a
// campaign that a guard paused overrides its failed checks so the next
// evaluation does not immediately pause it again; the next checkpoint on the
// ladder still applies.
func (s *Service) SetCampaignStatus(ctx context.Context, campaignID uuid.UUID, pause bool) (*domain.PublishedObject, error) {
	var campaign domain.PublishedObject
	if err := s.Repos.DB().WithContext(ctx).
		Where("id = ? AND object_type = ?", campaignID, domain.PublishedCampaign).
		First(&campaign).Error; err != nil {
		return nil, err
	}
	_, token, err := s.accessToken(ctx, campaign.ConnectionID)
	if err != nil {
		return nil, err
	}
	status := "ACTIVE"
	if pause {
		status = "PAUSED"
	}
	if err := s.Meta.SetEntityStatus(ctx, token, campaign.MetaObjectID, metaStatusFor(pause)); err != nil {
		return nil, err
	}
	now := s.Now()
	if err := s.Repos.Batches.UpdatePublishedStatus(
		ctx,
		campaign.ID,
		status,
		domain.MustJSON(map[string]any{"status": status, "changed_by": "user"}),
		now,
	); err != nil {
		return nil, err
	}
	if !pause {
		if err := s.overrideFailedChecks(ctx, campaign.ID, now); err != nil {
			return nil, err
		}
	}
	campaign.EffectiveStatus = status
	campaign.DesiredStatus = status
	return &campaign, nil
}

func (s *Service) overrideFailedChecks(ctx context.Context, campaignID uuid.UUID, now time.Time) error {
	return s.Repos.DB().WithContext(ctx).Model(&domain.GuardCheck{}).
		Where("published_object_id = ? AND status = ?", campaignID, domain.GuardCheckFailed).
		Updates(map[string]any{"status": domain.GuardCheckOverridden, "updated_at": now}).Error
}
