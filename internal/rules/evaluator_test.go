package rules

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRuleJSONRoundTripAndValidation(t *testing.T) {
	t.Parallel()

	rule := Rule{
		ID:            "rule-1",
		Name:          "pause expensive traffic",
		TargetLevel:   TargetAdSet,
		Action:        ActionPause,
		WindowSeconds: 86400,
		MinimumSamples: []SampleGuard{
			{Metric: "spend", Minimum: 50},
		},
		Conditions: Group{
			Logic: LogicAll,
			Conditions: []Condition{
				{Metric: "actions.offsite_conversion.fb_pixel_purchase", Operator: OperatorEQ, Threshold: 0},
			},
			Groups: []Group{
				{
					Logic: LogicAny,
					Conditions: []Condition{
						{Metric: "ctr", Operator: OperatorLT, Threshold: 1},
						{Metric: "frequency", Operator: OperatorGT, Threshold: 4},
					},
				},
			},
		},
		Metadata: RuleMetadata{
			GracePeriodSeconds: 86400,
			CooldownSeconds:    3600,
		},
	}

	if err := rule.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded Rule
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Validate() error = %v", err)
	}
	if decoded.Conditions.Groups[0].Logic != LogicAny {
		t.Fatalf("nested logic = %q, want %q", decoded.Conditions.Groups[0].Logic, LogicAny)
	}

	invalid := rule
	invalid.Action = Action("increase_budget")
	invalid.TargetLevel = TargetLevel("account")
	invalid.Conditions.Conditions[0].Metric = "bad metric"
	err = invalid.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want validation failure")
	}
	for _, expected := range []string{"unsupported target_level", "unsupported action", "valid dotted metric"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Validate() error %q does not contain %q", err, expected)
		}
	}
}

func TestEvaluate24HourNoResultRule(t *testing.T) {
	t.Parallel()

	activeSince := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	now := activeSince.Add(24 * time.Hour)
	rule := Rule{
		Name:           "pause after 24h without registration",
		TargetLevel:    TargetCampaign,
		Action:         ActionPause,
		WindowSeconds:  int64((24 * time.Hour).Seconds()),
		MinimumSamples: []SampleGuard{{Metric: "spend", Minimum: 100}},
		Conditions: Group{
			Logic: LogicAll,
			Conditions: []Condition{
				{
					Metric:    "actions.offsite_conversion.fb_pixel_complete_registration",
					Operator:  OperatorEQ,
					Threshold: 0,
				},
			},
		},
		Metadata: RuleMetadata{
			GracePeriodSeconds: int64((24 * time.Hour).Seconds()),
			CooldownSeconds:    int64(time.Hour.Seconds()),
		},
	}
	context := EvaluationContext{
		Now:             now,
		ActiveSince:     activeSince,
		WindowStartedAt: activeSince,
		WindowEndedAt:   now,
		Metrics:         map[string]float64{"spend": 125},
	}

	evaluation, err := Evaluate(rule, context)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !evaluation.Matched {
		t.Fatalf("Matched = false, reasons = %v", evaluation.Reasons)
	}
	if len(evaluation.Observations) != 2 {
		t.Fatalf("len(Observations) = %d, want 2", len(evaluation.Observations))
	}
	actionObservation := evaluation.Observations[1]
	if !actionObservation.Found || !actionObservation.Defaulted || actionObservation.Value != 0 || !actionObservation.Matched {
		t.Errorf("zero-result observation = %+v", actionObservation)
	}
	if _, err := json.Marshal(evaluation); err != nil {
		t.Fatalf("evaluation is not JSON serializable: %v", err)
	}

	t.Run("grace period blocks early pause", func(t *testing.T) {
		early := context
		early.Now = activeSince.Add(23 * time.Hour)
		early.WindowEndedAt = early.Now
		result, err := Evaluate(rule, early)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		if result.Matched || len(result.Observations) != 0 || !containsReason(result.Reasons, "grace period") {
			t.Fatalf("early evaluation = %+v", result)
		}
	})

	t.Run("cooldown blocks repeated action", func(t *testing.T) {
		lastTriggered := now.Add(-30 * time.Minute)
		coolingDown := context
		coolingDown.LastTriggeredAt = &lastTriggered
		result, err := Evaluate(rule, coolingDown)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		if result.Matched || !containsReason(result.Reasons, "cooldown") {
			t.Fatalf("cooldown evaluation = %+v", result)
		}
	})

	t.Run("minimum spend blocks sparse data", func(t *testing.T) {
		sparse := context
		sparse.Metrics = map[string]float64{"spend": 99}
		result, err := Evaluate(rule, sparse)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		if result.Matched || !containsReason(result.Reasons, "minimum sample") {
			t.Fatalf("sparse evaluation = %+v", result)
		}
	})

	t.Run("incomplete window blocks early decision", func(t *testing.T) {
		incomplete := context
		incomplete.ActiveSince = time.Time{}
		incomplete.WindowStartedAt = now.Add(-23 * time.Hour)
		result, err := Evaluate(rule, incomplete)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		if result.Matched || !containsReason(result.Reasons, "window is incomplete") {
			t.Fatalf("incomplete-window evaluation = %+v", result)
		}
	})
}

func TestEvaluateDerivedCPA_ROAS_CTRAndFrequency(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Name:           "derived metrics",
		TargetLevel:    TargetAd,
		Action:         ActionPause,
		WindowSeconds:  3600,
		MinimumSamples: []SampleGuard{{Metric: "impressions", Minimum: 1000}},
		Conditions: Group{
			Logic: LogicAll,
			Conditions: []Condition{
				{Metric: "cpa.purchase", Operator: OperatorGTE, Threshold: 50},
				{Metric: "roas.purchase", Operator: OperatorGTE, Threshold: 3},
				{Metric: "ctr", Operator: OperatorLT, Threshold: 3},
			},
			Groups: []Group{
				{
					Logic: LogicAny,
					Conditions: []Condition{
						{Metric: "frequency", Operator: OperatorGTE, Threshold: 2},
						{Metric: "cpm", Operator: OperatorGT, Threshold: 100},
					},
				},
			},
		},
	}
	context := EvaluationContext{
		Metrics: map[string]float64{
			"spend":                  200,
			"impressions":            10000,
			"reach":                  5000,
			"clicks":                 200,
			"actions.purchase":       4,
			"action_values.purchase": 600,
		},
	}

	evaluation, err := Evaluate(rule, context)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !evaluation.Matched {
		t.Fatalf("Matched = false, reasons = %v", evaluation.Reasons)
	}

	values := make(map[string]float64)
	for _, observation := range evaluation.Observations {
		values[observation.Metric] = observation.Value
	}
	for metric, want := range map[string]float64{
		"cpa.purchase":  50,
		"roas.purchase": 3,
		"ctr":           2,
		"frequency":     2,
	} {
		if got := values[metric]; !almostEqual(got, want) {
			t.Errorf("%s = %v, want %v", metric, got, want)
		}
	}
}

func TestEvaluateOperators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operator  Operator
		threshold float64
		matched   bool
	}{
		{name: "gt", operator: OperatorGT, threshold: 9, matched: true},
		{name: "gte", operator: OperatorGTE, threshold: 10, matched: true},
		{name: "lt", operator: OperatorLT, threshold: 11, matched: true},
		{name: "lte", operator: OperatorLTE, threshold: 10, matched: true},
		{name: "eq", operator: OperatorEQ, threshold: 10 + 1e-10, matched: true},
		{name: "neq", operator: OperatorNEQ, threshold: 9, matched: true},
		{name: "failed", operator: OperatorGT, threshold: 10, matched: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rule := Rule{
				Name:          test.name,
				TargetLevel:   TargetAd,
				Action:        ActionPause,
				WindowSeconds: 60,
				Conditions: Group{
					Logic: LogicAll,
					Conditions: []Condition{
						{Metric: "spend", Operator: test.operator, Threshold: test.threshold},
					},
				},
			}
			result, err := Evaluate(rule, EvaluationContext{Metrics: map[string]float64{"spend": 10}})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if result.Matched != test.matched {
				t.Fatalf("Matched = %v, want %v; reasons = %v", result.Matched, test.matched, result.Reasons)
			}
		})
	}
}

func TestEvaluateDoesNotDefaultMissingScalarMetricToZero(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Name:          "missing CTR",
		TargetLevel:   TargetAd,
		Action:        ActionPause,
		WindowSeconds: 60,
		Conditions: Group{
			Logic: LogicAll,
			Conditions: []Condition{
				{Metric: "ctr", Operator: OperatorEQ, Threshold: 0},
			},
		},
	}
	result, err := Evaluate(rule, EvaluationContext{Metrics: map[string]float64{}})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Matched || result.Observations[0].Found {
		t.Fatalf("missing scalar evaluation = %+v", result)
	}
}

func containsReason(reasons []string, fragment string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, fragment) {
			return true
		}
	}
	return false
}
