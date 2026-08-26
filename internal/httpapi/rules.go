package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
	"github.com/watchers-factory/raze-ads/internal/rules"
)

type patchRuleRequest struct {
	AdAccountID               nullableUUID         `json:"ad_account_id,omitempty"`
	BatchID                   nullableUUID         `json:"batch_id,omitempty"`
	Name                      *string              `json:"name,omitempty"`
	Status                    *domain.RuleStatus   `json:"status,omitempty"`
	ScopeLevel                *domain.InsightLevel `json:"scope_level,omitempty"`
	Action                    *domain.RuleAction   `json:"action,omitempty"`
	Conditions                *rules.Group         `json:"conditions,omitempty"`
	LookbackSeconds           *int64               `json:"lookback_seconds,omitempty"`
	EvaluationIntervalSeconds *int64               `json:"evaluation_interval_seconds,omitempty"`
	GracePeriodSeconds        *int64               `json:"grace_period_seconds,omitempty"`
	CooldownSeconds           *int64               `json:"cooldown_seconds,omitempty"`
	MinimumSpend              *float64             `json:"minimum_spend,omitempty"`
	MinimumImpressions        *int64               `json:"minimum_impressions,omitempty"`
	Timezone                  *string              `json:"timezone,omitempty"`
	Metadata                  map[string]any       `json:"metadata,omitempty"`
}

type nullableUUID struct {
	Set   bool
	Value *uuid.UUID
}

func (value *nullableUUID) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var id uuid.UUID
	if err := json.Unmarshal(data, &id); err != nil {
		return invalidField("uuid", "must be a UUID or null")
	}
	if id == uuid.Nil {
		return invalidField("uuid", "must not be an empty UUID")
	}
	value.Value = &id
	return nil
}

func (s *Server) createRule(c fiber.Ctx) error {
	var request application.CreateRuleRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	item, err := s.service.CreateRule(c.Context(), request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, item)
}

func (s *Server) listRules(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	batchID, err := optionalID(c, "batch_id")
	if err != nil {
		return err
	}
	statuses := make([]domain.RuleStatus, 0)
	for _, raw := range splitQuery(c, "statuses") {
		value := domain.RuleStatus(raw)
		switch value {
		case domain.RuleActive, domain.RuleDisabled, domain.RuleArchived:
			statuses = append(statuses, value)
		default:
			return invalidField("statuses", "contains an unsupported rule status")
		}
	}
	page, err := s.service.Repos.Rules.List(c.Context(), database.RuleFilter{
		ConnectionID: connectionID,
		AdAccountID:  adAccountID,
		BatchID:      batchID,
		Statuses:     statuses,
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) getRule(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	item, err := s.service.Repos.Rules.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func (s *Server) updateRule(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	existing, err := s.service.Repos.Rules.Get(c.Context(), id)
	if err != nil {
		return err
	}
	request, err := requestFromRule(existing)
	if err != nil {
		return err
	}
	var patch patchRuleRequest
	if err := decodeJSON(c, &patch); err != nil {
		return err
	}
	applyRulePatch(&request, patch)
	item, err := s.service.UpdateRule(c.Context(), id, request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func (s *Server) enableRule(c fiber.Ctx) error {
	return s.setRuleStatus(c, domain.RuleActive)
}

func (s *Server) disableRule(c fiber.Ctx) error {
	return s.setRuleStatus(c, domain.RuleDisabled)
}

func (s *Server) setRuleStatus(c fiber.Ctx, status domain.RuleStatus) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	var body struct{}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	item, err := s.service.SetRuleStatus(c.Context(), id, status)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func (s *Server) listRuleEvaluations(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if _, err := s.service.Repos.Rules.Get(c.Context(), id); err != nil {
		return err
	}
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	publishedObjectID, err := optionalID(c, "published_object_id")
	if err != nil {
		return err
	}
	statuses := make([]domain.RuleEvaluationStatus, 0)
	for _, raw := range splitQuery(c, "statuses") {
		value := domain.RuleEvaluationStatus(raw)
		switch value {
		case domain.RuleEvaluationNoMatch, domain.RuleEvaluationMatched, domain.RuleEvaluationActionSucceeded,
			domain.RuleEvaluationActionFailed, domain.RuleEvaluationSkipped, domain.RuleEvaluationError:
			statuses = append(statuses, value)
		default:
			return invalidField("statuses", "contains an unsupported evaluation status")
		}
	}
	page, err := s.service.Repos.Rules.ListEvaluations(c.Context(), database.RuleEvaluationFilter{
		RuleID:            id,
		PublishedObjectID: publishedObjectID,
		Statuses:          statuses,
		Page:              domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) capabilities(c fiber.Ctx) error {
	return jsonOK(c, http.StatusOK, s.service.Capabilities())
}

func requestFromRule(record *domain.AutomationRule) (application.UpdateRuleRequest, error) {
	var conditions rules.Group
	if err := record.Conditions.Decode(&conditions); err != nil {
		return application.UpdateRuleRequest{}, err
	}
	metadata := make(map[string]any)
	if len(record.Metadata) > 0 {
		if err := json.Unmarshal(record.Metadata, &metadata); err != nil {
			return application.UpdateRuleRequest{}, err
		}
	}
	cooldown := int64FromAny(metadata["cooldown_seconds"])
	delete(metadata, "cooldown_seconds")
	return application.UpdateRuleRequest{
		ConnectionID:              record.ConnectionID,
		AdAccountID:               record.AdAccountID,
		BatchID:                   record.BatchID,
		Name:                      record.Name,
		Status:                    record.Status,
		ScopeLevel:                record.ScopeLevel,
		Action:                    record.Action,
		Conditions:                conditions,
		LookbackSeconds:           record.LookbackSeconds,
		EvaluationIntervalSeconds: record.EvaluationIntervalSeconds,
		GracePeriodSeconds:        record.GracePeriodSeconds,
		CooldownSeconds:           cooldown,
		MinimumSpend:              record.MinimumSpend,
		MinimumImpressions:        record.MinimumImpressions,
		Timezone:                  record.Timezone,
		Metadata:                  metadata,
	}, nil
}

func applyRulePatch(request *application.UpdateRuleRequest, patch patchRuleRequest) {
	if patch.AdAccountID.Set {
		request.AdAccountID = patch.AdAccountID.Value
	}
	if patch.BatchID.Set {
		request.BatchID = patch.BatchID.Value
	}
	if patch.Name != nil {
		request.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Status != nil {
		request.Status = *patch.Status
	}
	if patch.ScopeLevel != nil {
		request.ScopeLevel = *patch.ScopeLevel
	}
	if patch.Action != nil {
		request.Action = *patch.Action
	}
	if patch.Conditions != nil {
		request.Conditions = *patch.Conditions
	}
	if patch.LookbackSeconds != nil {
		request.LookbackSeconds = *patch.LookbackSeconds
	}
	if patch.EvaluationIntervalSeconds != nil {
		request.EvaluationIntervalSeconds = *patch.EvaluationIntervalSeconds
	}
	if patch.GracePeriodSeconds != nil {
		request.GracePeriodSeconds = *patch.GracePeriodSeconds
	}
	if patch.CooldownSeconds != nil {
		request.CooldownSeconds = *patch.CooldownSeconds
	}
	if patch.MinimumSpend != nil {
		request.MinimumSpend = *patch.MinimumSpend
	}
	if patch.MinimumImpressions != nil {
		request.MinimumImpressions = *patch.MinimumImpressions
	}
	if patch.Timezone != nil {
		request.Timezone = strings.TrimSpace(*patch.Timezone)
	}
	if patch.Metadata != nil {
		request.Metadata = patch.Metadata
	}
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return result
	default:
		return 0
	}
}
