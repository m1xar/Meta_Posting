package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Meta's own automated rules, created through an ad account's
// adrules_library edge.
//
// These exist as a backstop, not as the primary mechanism. Meta evaluates
// them on its own schedule - roughly every half hour - which is far slower
// than this service's own loop, but it keeps running when this service does
// not. A guard that only lives here would react late; a guard that only lives
// in our worker stops existing the moment the worker does.

// AdRuleEvaluationType selects when Meta checks the rule.
type AdRuleEvaluationType string

const (
	// AdRuleSchedule evaluates on Meta's own cadence.
	AdRuleSchedule AdRuleEvaluationType = "SCHEDULE"
	// AdRuleTrigger evaluates when the watched metric changes.
	AdRuleTrigger AdRuleEvaluationType = "TRIGGER"
)

// AdRuleFilter is one clause of the rule's entity selector or its evaluation.
type AdRuleFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}

// AdRuleEvaluationSpec is what Meta tests.
type AdRuleEvaluationSpec struct {
	EvaluationType AdRuleEvaluationType `json:"evaluation_type"`
	Filters        []AdRuleFilter       `json:"filters"`
	TriggerSpec    map[string]any       `json:"trigger,omitempty"`
}

// AdRuleExecutionSpec is what Meta does when the evaluation holds.
type AdRuleExecutionSpec struct {
	ExecutionType    string         `json:"execution_type"`
	ExecutionOptions map[string]any `json:"execution_options,omitempty"`
}

// AdRuleSpec is the payload for creating a rule.
type AdRuleSpec struct {
	Name           string               `json:"name"`
	EvaluationSpec AdRuleEvaluationSpec `json:"evaluation_spec"`
	ExecutionSpec  AdRuleExecutionSpec  `json:"execution_spec"`
	Schedule       []map[string]any     `json:"schedule_spec,omitempty"`
	Status         string               `json:"status,omitempty"`
}

// AdRule is a rule as Meta reports it.
type AdRule struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

// CreateAdRule registers a rule on an ad account.
//
// Uses the no-retry post: rule creation is not idempotent, and a retry after
// an ambiguous transport failure would leave two rules pausing the same
// campaign.
func (c *Client) CreateAdRule(
	ctx context.Context,
	accessToken string,
	accountID string,
	spec AdRuleSpec,
) (AdRule, error) {
	if strings.TrimSpace(accountID) == "" {
		return AdRule{}, errors.New("meta: ad rule requires an ad account ID")
	}
	evaluation, err := json.Marshal(spec.EvaluationSpec)
	if err != nil {
		return AdRule{}, err
	}
	execution, err := json.Marshal(spec.ExecutionSpec)
	if err != nil {
		return AdRule{}, err
	}
	// The edge takes these as JSON-encoded strings in form fields rather than
	// as a nested JSON body.
	payload := map[string]any{
		"name":            spec.Name,
		"evaluation_spec": string(evaluation),
		"execution_spec":  string(execution),
	}
	if spec.Status != "" {
		payload["status"] = spec.Status
	}
	if len(spec.Schedule) > 0 {
		encoded, scheduleErr := json.Marshal(spec.Schedule)
		if scheduleErr != nil {
			return AdRule{}, scheduleErr
		}
		payload["schedule_spec"] = string(encoded)
	}

	var rule AdRule
	err = c.PostJSONNoRetry(
		ctx,
		"/"+AdAccountNodeID(accountID)+"/adrules_library",
		accessToken,
		nil,
		payload,
		&rule,
	)
	if err != nil {
		return AdRule{}, err
	}
	return rule, nil
}

// DeleteAdRule removes a rule.
func (c *Client) DeleteAdRule(ctx context.Context, accessToken, ruleID string) error {
	if strings.TrimSpace(ruleID) == "" {
		return errors.New("meta: ad rule ID is required")
	}
	var response map[string]any
	return c.Delete(ctx, "/"+strings.TrimPrefix(ruleID, "/"), accessToken, nil, &response)
}

// ListAdRules returns the rules registered on an account.
func (c *Client) ListAdRules(
	ctx context.Context,
	accessToken string,
	accountID string,
) ([]AdRule, error) {
	query := url.Values{"fields": {"id,name,status"}, "limit": {"100"}}
	return CollectPages[AdRule](
		ctx, c, "/"+AdAccountNodeID(accountID)+"/adrules_library", accessToken, query,
	)
}

// SpendCapAdRule mirrors a spend cap guard.
//
// Meta's own field vocabulary is narrower than the internal DSL, so only the
// guards it can express are mirrored. A guard it cannot express stays with
// this service alone rather than being silently approximated - a backstop
// that stops the wrong thing is worse than no backstop.
func SpendCapAdRule(name string, entityType string, spend float64) AdRuleSpec {
	return AdRuleSpec{
		Name: truncateRuleName(name),
		EvaluationSpec: AdRuleEvaluationSpec{
			EvaluationType: AdRuleSchedule,
			Filters: []AdRuleFilter{
				{Field: "entity_type", Operator: "EQUAL", Value: entityType},
				{Field: "spent", Operator: "GREATER_THAN", Value: int64(spend * 100)},
			},
		},
		ExecutionSpec: AdRuleExecutionSpec{ExecutionType: "PAUSE"},
		Status:        "ENABLED",
	}
}

// SpendCheckAdRule mirrors a "spend then judge" guard.
func SpendCheckAdRule(name, entityType string, spend float64, field string, minimum float64) AdRuleSpec {
	return AdRuleSpec{
		Name: truncateRuleName(name),
		EvaluationSpec: AdRuleEvaluationSpec{
			EvaluationType: AdRuleSchedule,
			Filters: []AdRuleFilter{
				{Field: "entity_type", Operator: "EQUAL", Value: entityType},
				{Field: "spent", Operator: "GREATER_THAN", Value: int64(spend * 100)},
				{Field: field, Operator: "LESS_THAN", Value: minimum},
			},
		},
		ExecutionSpec: AdRuleExecutionSpec{ExecutionType: "PAUSE"},
		Status:        "ENABLED",
	}
}

// AdRuleEntityType maps an insight level to Meta's rule vocabulary.
func AdRuleEntityType(level string) (string, error) {
	switch level {
	case "campaign":
		return "CAMPAIGN", nil
	case "adset":
		return "ADSET", nil
	case "ad":
		return "AD", nil
	default:
		return "", fmt.Errorf("meta: %q has no ad rule entity type", level)
	}
}

// truncateRuleName keeps within Meta's name limit.
func truncateRuleName(name string) string {
	const limit = 60
	name = strings.TrimSpace(name)
	if len(name) <= limit {
		return name
	}
	return name[:limit]
}
