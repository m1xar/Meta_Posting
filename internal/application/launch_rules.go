package application

import (
	"fmt"
	"strings"

	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/rules"
)

// GuardKind is one of the stop conditions the launcher offers.
type GuardKind string

const (
	// GuardSpendCap stops an object once it has spent more than Spend.
	GuardSpendCap GuardKind = "spend_cap"
	// GuardSpendCheck stops an object that has spent Spend without reaching
	// Minimum of Metric. This is the "give it a chance, then judge it" shape:
	// below the spend threshold nothing happens, above it the object has to
	// justify itself.
	GuardSpendCheck GuardKind = "spend_check"
)

// LaunchGuard is a stop condition expressed the way a buyer states it, rather
// than as the boolean tree the engine evaluates.
type LaunchGuard struct {
	Kind GuardKind `json:"kind"`
	// Spend is the threshold in account currency, not minor units: it is
	// typed by a person, and Meta reports spend the same way.
	Spend float64 `json:"spend"`
	// Metric and Minimum apply to GuardSpendCheck. Metric accepts anything
	// the flattened insight map exposes, so actions.purchase works alongside
	// impressions and clicks.
	Metric  string  `json:"metric,omitempty"`
	Minimum float64 `json:"minimum,omitempty"`
}

// Validate reports whether the guard is expressible.
func (g LaunchGuard) Validate() error {
	if g.Spend <= 0 {
		return invalid("spend", "must be greater than zero")
	}
	switch g.Kind {
	case GuardSpendCap:
		return nil
	case GuardSpendCheck:
		if strings.TrimSpace(g.Metric) == "" {
			return invalid("metric", "is required for a spend check")
		}
		if g.Minimum <= 0 {
			return invalid("minimum", "must be greater than zero")
		}
		return nil
	default:
		return invalid("kind", fmt.Sprintf("%q is not a supported guard", g.Kind))
	}
}

// Conditions renders the guard as the engine's boolean tree.
func (g LaunchGuard) Conditions() rules.Group {
	spend := rules.Condition{
		Metric:    "spend",
		Operator:  rules.OperatorGTE,
		Threshold: g.Spend,
	}
	if g.Kind == GuardSpendCap {
		return rules.Group{Logic: rules.LogicAll, Conditions: []rules.Condition{spend}}
	}
	// Both halves must hold: enough spent to judge on, and the result still
	// short. MissingAsZero matters because an object that produced nothing
	// reports no metric at all rather than a zero, and that is precisely the
	// case worth stopping.
	return rules.Group{
		Logic: rules.LogicAll,
		Conditions: []rules.Condition{
			spend,
			{
				Metric:        g.Metric,
				Operator:      rules.OperatorLT,
				Threshold:     g.Minimum,
				MissingAsZero: true,
			},
		},
	}
}

// Describe renders the guard as a sentence, for the UI and the audit trail.
func (g LaunchGuard) Describe(currency string) string {
	symbol := strings.ToUpper(strings.TrimSpace(currency))
	if symbol == "" {
		symbol = "USD"
	}
	if g.Kind == GuardSpendCap {
		return fmt.Sprintf("Pause once spend reaches %.2f %s", g.Spend, symbol)
	}
	return fmt.Sprintf(
		"After %.2f %s, pause unless %s reached %g",
		g.Spend, symbol, g.Metric, g.Minimum,
	)
}

// LaunchRuleRequest is a guard plus the scheduling around it.
type LaunchRuleRequest struct {
	Name  string      `json:"name,omitempty"`
	Guard LaunchGuard `json:"guard"`
	// ScopeLevel defaults to campaign: stopping the campaign stops everything
	// under it, which is what a spend guard is usually meant to do.
	ScopeLevel domain.InsightLevel `json:"scope_level,omitempty"`
	// EvaluationIntervalSeconds defaults to 60. The fast lane keeps insights
	// fresh for anything at or below FastRuleMaxInterval.
	EvaluationIntervalSeconds int64 `json:"evaluation_interval_seconds,omitempty"`
	LookbackSeconds           int64 `json:"lookback_seconds,omitempty"`
	GracePeriodSeconds        int64 `json:"grace_period_seconds,omitempty"`
}

// ToCreateRule expands the request into the engine's rule shape.
func (r LaunchRuleRequest) ToCreateRule(currency string) (CreateRuleRequest, error) {
	if err := r.Guard.Validate(); err != nil {
		return CreateRuleRequest{}, err
	}
	scope := r.ScopeLevel
	if scope == "" {
		scope = domain.InsightCampaign
	}
	interval := r.EvaluationIntervalSeconds
	if interval == 0 {
		interval = 60
	}
	lookback := r.LookbackSeconds
	if lookback == 0 {
		// A launch guard judges the campaign's whole life so far, not a
		// rolling window: "it spent 100 in total" is the intent, and a short
		// window would let a slow burner escape the cap indefinitely.
		lookback = 30 * 24 * 3600
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = r.Guard.Describe(currency)
	}
	return CreateRuleRequest{
		Name:                      name,
		ScopeLevel:                scope,
		Action:                    domain.RuleActionPause,
		Conditions:                r.Guard.Conditions(),
		LookbackSeconds:           lookback,
		EvaluationIntervalSeconds: interval,
		GracePeriodSeconds:        r.GracePeriodSeconds,
		Metadata: map[string]any{
			"source":      "launcher",
			"guard_kind":  string(r.Guard.Kind),
			"guard_spend": r.Guard.Spend,
			"description": r.Guard.Describe(currency),
		},
	}, nil
}
