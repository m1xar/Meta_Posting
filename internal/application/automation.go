package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
	"github.com/watchers-factory/raze-ads/internal/rules"
	"gorm.io/gorm"
)

const (
	defaultRuleBaselineTolerance  = 15 * time.Minute
	ruleInsightFreshnessIntervals = 2
	maxTimeDuration               = time.Duration(1<<63 - 1)
)

func (s *Service) CreateRule(ctx context.Context, request CreateRuleRequest) (*domain.AutomationRule, error) {
	record, err := s.ruleRecord(ctx, uuid.Nil, request)
	if err != nil {
		return nil, err
	}
	if err := s.Repos.Rules.Create(ctx, record); err != nil {
		return nil, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &record.ConnectionID,
		ActorType:    "internal_api",
		Action:       "automation_rule.created",
		EntityType:   "automation_rule",
		EntityID:     record.ID.String(),
		After:        domain.MustJSON(record),
	})
	return record, nil
}

func (s *Service) UpdateRule(ctx context.Context, id uuid.UUID, request UpdateRuleRequest) (*domain.AutomationRule, error) {
	existing, err := s.Repos.Rules.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if request.ConnectionID == uuid.Nil {
		request.ConnectionID = existing.ConnectionID
	}
	if request.ConnectionID != existing.ConnectionID {
		return nil, invalid("connection_id", "cannot be changed")
	}
	record, err := s.ruleRecord(ctx, id, request)
	if err != nil {
		return nil, err
	}
	if err := s.Repos.Rules.UpdateConfiguration(ctx, record); err != nil {
		return nil, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &record.ConnectionID,
		ActorType:    "internal_api",
		Action:       "automation_rule.updated",
		EntityType:   "automation_rule",
		EntityID:     record.ID.String(),
		Before:       domain.MustJSON(existing),
		After:        domain.MustJSON(record),
	})
	return s.Repos.Rules.Get(ctx, id)
}

func (s *Service) SetRuleStatus(ctx context.Context, id uuid.UUID, status domain.RuleStatus) (*domain.AutomationRule, error) {
	switch status {
	case domain.RuleActive, domain.RuleDisabled:
	default:
		return nil, invalid("status", "must be active or disabled")
	}
	if err := s.Repos.Rules.SetStatus(ctx, id, status, s.Now()); err != nil {
		return nil, err
	}
	if status == domain.RuleDisabled {
		// A backstop registered inside Meta would otherwise outlive the
		// intent behind it and keep pausing campaigns nobody is guarding any
		// more. Failing to remove it must not fail the disable itself: the
		// rule is already off here, which is what the caller asked for.
		if err := s.RemoveRuleMirrors(ctx, id); err != nil {
			s.audit(ctx, domain.AuditEvent{
				ActorType:  "user",
				Action:     "rule.mirror_removal_failed",
				EntityType: "automation_rule",
				EntityID:   id.String(),
				Severity:   domain.AuditWarning,
				Metadata:   domain.MustJSON(map[string]string{"error": truncateError(err)}),
			})
		}
	}
	return s.Repos.Rules.Get(ctx, id)
}

func (s *Service) ruleRecord(ctx context.Context, id uuid.UUID, request CreateRuleRequest) (*domain.AutomationRule, error) {
	if request.ConnectionID == uuid.Nil {
		return nil, invalid("connection_id", "is required")
	}
	if request.Name == "" {
		return nil, invalid("name", "is required")
	}
	if _, err := s.Repos.MetaConnections.Get(ctx, request.ConnectionID); err != nil {
		return nil, err
	}
	if request.AdAccountID == nil && request.BatchID == nil {
		return nil, invalid("scope", "ad_account_id or batch_id is required")
	}
	if request.AdAccountID != nil {
		account, err := s.Repos.Inventory.GetAdAccount(ctx, *request.AdAccountID)
		if err != nil {
			return nil, err
		}
		if account.ConnectionID != request.ConnectionID {
			return nil, invalid("ad_account_id", "belongs to another connection")
		}
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
		request.Status = domain.RuleActive
	}
	switch request.Status {
	case domain.RuleActive, domain.RuleDisabled:
	default:
		return nil, invalid("status", "must be active or disabled")
	}
	if request.Action == "" {
		request.Action = domain.RuleActionPause
	}
	if request.Action != domain.RuleActionPause {
		return nil, invalid("action", "only pause is supported")
	}
	if request.EvaluationIntervalSeconds == 0 {
		request.EvaluationIntervalSeconds = 900
	}
	if request.EvaluationIntervalSeconds < 60 {
		return nil, invalid("evaluation_interval_seconds", "must be at least 60")
	}
	if request.LookbackSeconds <= 0 {
		return nil, invalid("lookback_seconds", "must be greater than zero")
	}
	if request.GracePeriodSeconds < 0 || request.CooldownSeconds < 0 {
		return nil, invalid("grace_period_seconds", "grace and cooldown cannot be negative")
	}
	target := rules.TargetLevel(request.ScopeLevel)
	engineRule := rules.Rule{
		Name:          request.Name,
		TargetLevel:   target,
		Action:        rules.Action(request.Action),
		WindowSeconds: request.LookbackSeconds,
		Conditions:    request.Conditions,
		Metadata: rules.RuleMetadata{
			GracePeriodSeconds: request.GracePeriodSeconds,
			CooldownSeconds:    request.CooldownSeconds,
		},
	}
	if request.MinimumSpend > 0 {
		engineRule.MinimumSamples = append(engineRule.MinimumSamples, rules.SampleGuard{Metric: "spend", Minimum: request.MinimumSpend})
	}
	if request.MinimumImpressions > 0 {
		engineRule.MinimumSamples = append(engineRule.MinimumSamples, rules.SampleGuard{Metric: "impressions", Minimum: float64(request.MinimumImpressions)})
	}
	if err := engineRule.Validate(); err != nil {
		return nil, invalid("conditions", err.Error())
	}
	metadata := request.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["cooldown_seconds"] = request.CooldownSeconds
	record := &domain.AutomationRule{
		ConnectionID:              request.ConnectionID,
		AdAccountID:               request.AdAccountID,
		BatchID:                   request.BatchID,
		Name:                      request.Name,
		Status:                    request.Status,
		ScopeLevel:                request.ScopeLevel,
		Action:                    request.Action,
		Conditions:                domain.MustJSON(request.Conditions),
		LookbackSeconds:           request.LookbackSeconds,
		EvaluationIntervalSeconds: request.EvaluationIntervalSeconds,
		GracePeriodSeconds:        request.GracePeriodSeconds,
		MinimumSpend:              request.MinimumSpend,
		MinimumImpressions:        request.MinimumImpressions,
		Timezone:                  request.Timezone,
		NextEvaluationAt:          s.Now(),
		Metadata:                  domain.MustJSON(metadata),
	}
	record.ID = id
	return record, nil
}

func (s *Service) EvaluateDueRules(ctx context.Context, connectionID *uuid.UUID) error {
	now := s.Now()
	due, err := s.Repos.Rules.ListDue(ctx, connectionID, now, 500)
	if err != nil {
		return err
	}
	var failures []error
	expiredConnections := make(map[uuid.UUID]struct{})
	for index := range due {
		rule := &due[index]
		if _, expired := expiredConnections[rule.ConnectionID]; expired {
			continue
		}
		if err := s.evaluateRule(ctx, rule, now); err != nil {
			failures = append(failures, fmt.Errorf("rule %s: %w", rule.ID, err))
			expired, statusErr := s.markConnectionExpiredForMetaError(ctx, rule.ConnectionID, err)
			if statusErr != nil {
				failures = append(failures, statusErr)
			}
			if expired {
				expiredConnections[rule.ConnectionID] = struct{}{}
				if connectionID != nil {
					break
				}
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) evaluateRule(ctx context.Context, rule *domain.AutomationRule, now time.Time) error {
	objectType, err := objectTypeForLevel(rule.ScopeLevel)
	if err != nil {
		return err
	}
	query := s.Repos.DB().WithContext(ctx).
		Where("connection_id = ? AND object_type = ? AND effective_status <> ?", rule.ConnectionID, objectType, "PAUSED")
	if rule.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *rule.AdAccountID)
	}
	if rule.BatchID != nil {
		query = query.Where("batch_id = ?", *rule.BatchID)
	}
	var objects []domain.PublishedObject
	if err := query.Order("created_at ASC").Find(&objects).Error; err != nil {
		return err
	}
	next := now.Add(time.Duration(rule.EvaluationIntervalSeconds) * time.Second)
	if len(objects) == 0 {
		return s.Repos.DB().WithContext(ctx).Model(&domain.AutomationRule{}).
			Where("id = ?", rule.ID).
			Updates(map[string]any{
				"last_evaluated_at":  now,
				"next_evaluation_at": next,
				"updated_at":         now,
			}).Error
	}
	_, token, err := s.accessToken(ctx, rule.ConnectionID)
	if err != nil {
		return err
	}
	var failures []error
	for index := range objects {
		object := &objects[index]
		if err := s.evaluateObject(ctx, rule, object, token, now, next); err != nil {
			failures = append(failures, fmt.Errorf("object %s: %w", object.MetaObjectID, err))
			if isMetaAccessTokenError(err) {
				break
			}
		}
	}
	return errors.Join(failures...)
}

func (s *Service) evaluateObject(
	ctx context.Context,
	record *domain.AutomationRule,
	object *domain.PublishedObject,
	token string,
	now, next time.Time,
) error {
	latest, err := s.Repos.Insights.Latest(ctx, record.ConnectionID, object.MetaObjectID, record.ScopeLevel)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.saveSkippedEvaluation(ctx, record, object, now, next, "no insight snapshot is available")
	}
	if err != nil {
		return err
	}
	freshnessLimit := ruleInsightFreshnessLimit(s.Config.Worker.InsightsInterval)
	if !insightSnapshotFresh(now, latest.WindowEnd, freshnessLimit) {
		return s.saveSkippedEvaluation(
			ctx,
			record,
			object,
			now,
			next,
			fmt.Sprintf(
				"the latest insight snapshot at %s is stale; it must be no more than %s old",
				latest.WindowEnd.UTC().Format(time.RFC3339),
				freshnessLimit,
			),
		)
	}
	if latest.QueryHash != LifetimeInsightQueryHash {
		return s.saveSkippedEvaluation(ctx, record, object, now, next, "the latest insight snapshot uses an incompatible query schema")
	}
	targetStart := latest.WindowEnd.Add(-time.Duration(record.LookbackSeconds) * time.Second)
	older, err := s.Repos.Insights.NearestBefore(ctx, database.InsightPointQuery{
		ConnectionID: record.ConnectionID,
		MetaObjectID: object.MetaObjectID,
		Level:        record.ScopeLevel,
		QueryHash:    LifetimeInsightQueryHash,
		At:           targetStart,
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// No snapshot that far back can mean two very different things.
		//
		// If the object already existed when the window opened, history is
		// genuinely missing and judging it now would compare against nothing.
		//
		// But if the object was created *after* the window opened, the window
		// is complete: it started before the object existed, and something
		// that did not exist had spent nothing. Skipping here is what made a
		// launch guard inert - a campaign minutes old can never have a
		// snapshot from a month ago, so a lifetime spend cap would never
		// engage on exactly the campaigns it was attached to.
		if !object.CreatedAt.After(targetStart) {
			return s.saveSkippedEvaluation(ctx, record, object, now, next,
				"the requested rolling window is not complete")
		}
		older = &domain.InsightSnapshot{
			ConnectionID: record.ConnectionID,
			MetaObjectID: object.MetaObjectID,
			Level:        record.ScopeLevel,
			QueryHash:    LifetimeInsightQueryHash,
			WindowEnd:    object.CreatedAt,
			Metrics:      domain.MustJSON(map[string]float64{}),
		}
	} else if err != nil {
		return err
	}
	// A baseline taken at the object's birth is exact by construction, so the
	// staleness tolerance - which exists to reject a baseline that drifted
	// away from the requested window - does not apply to it.
	bornInsideWindow := object.CreatedAt.After(targetStart)
	tolerance := ruleBaselineTolerance(s.Config.Worker.InsightsInterval)
	if !bornInsideWindow && !baselineWithinTolerance(targetStart, older.WindowEnd, tolerance) {
		return s.saveSkippedEvaluation(
			ctx,
			record,
			object,
			now,
			next,
			fmt.Sprintf(
				"the closest insight baseline at %s is outside the %s tolerance around requested window start %s",
				older.WindowEnd.UTC().Format(time.RFC3339),
				tolerance,
				targetStart.UTC().Format(time.RFC3339),
			),
		)
	}
	olderMetrics, err := decodeMetricMap(older.Metrics)
	if err != nil {
		return err
	}
	latestMetrics, err := decodeMetricMap(latest.Metrics)
	if err != nil {
		return err
	}
	delta, err := rules.RollingWindowDelta(
		rules.Snapshot{TakenAt: older.WindowEnd, Metrics: olderMetrics},
		rules.Snapshot{TakenAt: latest.WindowEnd, Metrics: latestMetrics},
	)
	if err != nil {
		var correction *rules.CounterCorrectionError
		if errors.As(err, &correction) {
			return s.saveSkippedEvaluation(ctx, record, object, now, next, "rolling insight window is invalid: "+correction.Error())
		}
		return err
	}
	engineRule, err := engineRuleFromRecord(record)
	if err != nil {
		return err
	}
	lastTriggered, err := s.lastObjectTrigger(ctx, record.ID, object.ID)
	if err != nil {
		return err
	}
	evaluation, err := rules.Evaluate(engineRule, rules.EvaluationContext{
		Now:             now,
		ActiveSince:     object.CreatedAt,
		LastTriggeredAt: lastTriggered,
		WindowStartedAt: delta.StartedAt,
		WindowEndedAt:   delta.EndedAt,
		Metrics:         delta.Metrics,
	})
	if err != nil {
		return err
	}
	status := domain.RuleEvaluationNoMatch
	actionAttempted := false
	actionResponse := emptyObject()
	var actionErr error
	if evaluation.Matched {
		status = domain.RuleEvaluationMatched
		actionAttempted = true
		actionErr = s.Meta.PauseEntity(ctx, token, object.MetaObjectID)
		if actionErr != nil {
			status = domain.RuleEvaluationActionFailed
			actionResponse = domain.MustJSON(map[string]any{"success": false, "error": actionErr.Error()})
		} else {
			status = domain.RuleEvaluationActionSucceeded
			actionResponse = domain.MustJSON(map[string]any{"success": true, "status": "PAUSED"})
			if err := s.Repos.Batches.UpdatePublishedStatus(ctx, object.ID, "PAUSED", actionResponse, now); err != nil {
				actionErr = err
				status = domain.RuleEvaluationActionFailed
			}
		}
	}
	evaluationRecord := &domain.RuleEvaluation{
		RuleID:            record.ID,
		PublishedObjectID: &object.ID,
		MetaObjectID:      object.MetaObjectID,
		Status:            status,
		WindowStart:       delta.StartedAt,
		WindowEnd:         delta.EndedAt,
		ObservedMetrics:   domain.MustJSON(delta.Metrics),
		ConditionResults:  domain.MustJSON(evaluation),
		ActionAttempted:   actionAttempted,
		ActionResponse:    actionResponse,
		EvaluatedAt:       now,
	}
	if actionErr != nil {
		evaluationRecord.Error = actionErr.Error()
	}
	if err := s.Repos.Rules.SaveEvaluation(ctx, evaluationRecord, next); err != nil {
		return err
	}
	if status == domain.RuleEvaluationActionSucceeded {
		s.audit(ctx, domain.AuditEvent{
			ConnectionID: &record.ConnectionID,
			ActorType:    "automation_rule",
			ActorID:      record.ID.String(),
			Action:       "meta.object.paused",
			EntityType:   string(object.ObjectType),
			EntityID:     object.MetaObjectID,
			Severity:     domain.AuditWarning,
			Before:       domain.MustJSON(map[string]any{"status": object.EffectiveStatus}),
			After:        actionResponse,
			Metadata:     domain.MustJSON(map[string]any{"rule_evaluation_id": evaluationRecord.ID}),
		})
	}
	return actionErr
}

func ruleBaselineTolerance(insightsInterval time.Duration) time.Duration {
	if insightsInterval <= 0 {
		return defaultRuleBaselineTolerance
	}
	return insightsInterval
}

func ruleInsightFreshnessLimit(insightsInterval time.Duration) time.Duration {
	if insightsInterval <= 0 {
		insightsInterval = defaultRuleBaselineTolerance
	}
	if insightsInterval > maxTimeDuration/ruleInsightFreshnessIntervals {
		return maxTimeDuration
	}
	return insightsInterval * ruleInsightFreshnessIntervals
}

func insightSnapshotFresh(now, windowEnd time.Time, freshnessLimit time.Duration) bool {
	if freshnessLimit < 0 {
		return false
	}
	return !windowEnd.Before(now.Add(-freshnessLimit))
}

func baselineWithinTolerance(target, baseline time.Time, tolerance time.Duration) bool {
	if tolerance < 0 {
		return false
	}
	skew := target.Sub(baseline)
	return skew >= 0 && skew <= tolerance
}

func (s *Service) saveSkippedEvaluation(
	ctx context.Context,
	rule *domain.AutomationRule,
	object *domain.PublishedObject,
	now, next time.Time,
	reason string,
) error {
	return s.Repos.Rules.SaveEvaluation(ctx, &domain.RuleEvaluation{
		RuleID:            rule.ID,
		PublishedObjectID: &object.ID,
		MetaObjectID:      object.MetaObjectID,
		Status:            domain.RuleEvaluationSkipped,
		WindowStart:       now.Add(-time.Duration(rule.LookbackSeconds) * time.Second),
		WindowEnd:         now,
		ObservedMetrics:   emptyObject(),
		ConditionResults:  domain.MustJSON(map[string]any{"matched": false, "reasons": []string{reason}}),
		ActionResponse:    emptyObject(),
		EvaluatedAt:       now,
	}, next)
}

func engineRuleFromRecord(record *domain.AutomationRule) (rules.Rule, error) {
	var conditions rules.Group
	if err := record.Conditions.Decode(&conditions); err != nil {
		return rules.Rule{}, err
	}
	var metadata map[string]any
	_ = record.Metadata.Decode(&metadata)
	cooldown := int64(0)
	switch value := metadata["cooldown_seconds"].(type) {
	case float64:
		cooldown = int64(value)
	case int64:
		cooldown = value
	}
	engineRule := rules.Rule{
		ID:            record.ID.String(),
		Name:          record.Name,
		TargetLevel:   rules.TargetLevel(record.ScopeLevel),
		Action:        rules.Action(record.Action),
		WindowSeconds: record.LookbackSeconds,
		Conditions:    conditions,
		Metadata: rules.RuleMetadata{
			GracePeriodSeconds: record.GracePeriodSeconds,
			CooldownSeconds:    cooldown,
		},
	}
	if record.MinimumSpend > 0 {
		engineRule.MinimumSamples = append(engineRule.MinimumSamples, rules.SampleGuard{Metric: "spend", Minimum: record.MinimumSpend})
	}
	if record.MinimumImpressions > 0 {
		engineRule.MinimumSamples = append(engineRule.MinimumSamples, rules.SampleGuard{Metric: "impressions", Minimum: float64(record.MinimumImpressions)})
	}
	return engineRule, engineRule.Validate()
}

func objectTypeForLevel(level domain.InsightLevel) (domain.PublishedObjectType, error) {
	switch level {
	case domain.InsightCampaign:
		return domain.PublishedCampaign, nil
	case domain.InsightAdSet:
		return domain.PublishedAdSet, nil
	case domain.InsightAd:
		return domain.PublishedAd, nil
	default:
		return "", fmt.Errorf("unsupported automation scope %q", level)
	}
}

func decodeMetricMap(raw domain.JSON) (map[string]float64, error) {
	var metrics map[string]float64
	if err := raw.Decode(&metrics); err != nil {
		return nil, err
	}
	if metrics == nil {
		metrics = make(map[string]float64)
	}
	return metrics, nil
}

func (s *Service) lastObjectTrigger(ctx context.Context, ruleID, objectID uuid.UUID) (*time.Time, error) {
	var evaluation domain.RuleEvaluation
	err := s.Repos.DB().WithContext(ctx).
		Where("rule_id = ? AND published_object_id = ? AND status = ?", ruleID, objectID, domain.RuleEvaluationActionSucceeded).
		Order("evaluated_at DESC").
		First(&evaluation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value := evaluation.EvaluatedAt
	return &value, nil
}
