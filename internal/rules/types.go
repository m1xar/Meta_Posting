package rules

import "time"

// TargetLevel is the Meta object level at which a rule is evaluated and acted on.
type TargetLevel string

const (
	TargetCampaign TargetLevel = "campaign"
	TargetAdSet    TargetLevel = "adset"
	TargetAd       TargetLevel = "ad"
)

// Action is deliberately restricted to safe automation actions supported by this
// service. Additional actions must be added explicitly and reviewed before use.
type Action string

const (
	ActionPause Action = "pause"
)

// Logic controls how the members of a Group are combined.
type Logic string

const (
	LogicAll Logic = "all"
	LogicAny Logic = "any"
)

// Operator compares an observed metric with a condition threshold.
type Operator string

const (
	OperatorGT  Operator = "gt"
	OperatorGTE Operator = "gte"
	OperatorLT  Operator = "lt"
	OperatorLTE Operator = "lte"
	OperatorEQ  Operator = "eq"
	OperatorNEQ Operator = "neq"
)

// RuleMetadata contains scheduling constraints rather than business conditions.
// Durations are represented as seconds so their JSON representation is explicit
// and interoperable with non-Go workers.
type RuleMetadata struct {
	GracePeriodSeconds int64 `json:"grace_period_seconds,omitempty"`
	CooldownSeconds    int64 `json:"cooldown_seconds,omitempty"`
}

// SampleGuard prevents decisions on statistically insignificant data. A guard
// passes when its metric is greater than or equal to Minimum.
type SampleGuard struct {
	Metric        string  `json:"metric"`
	Minimum       float64 `json:"minimum"`
	MissingAsZero bool    `json:"missing_as_zero,omitempty"`
}

// Condition is a single numeric comparison. MinimumSamples applies only to this
// condition; rule- and group-level guards are available on their respective
// types. MissingAsZero should be used only where a missing metric genuinely
// means zero. Count/value arrays returned by Meta (actions.*, conversions.*,
// etc.) already have this behavior by default.
type Condition struct {
	Metric         string        `json:"metric"`
	Operator       Operator      `json:"operator"`
	Threshold      float64       `json:"threshold"`
	MissingAsZero  bool          `json:"missing_as_zero,omitempty"`
	MinimumSamples []SampleGuard `json:"minimum_samples,omitempty"`
}

// Group is a recursive boolean expression. Conditions and child Groups are
// evaluated together according to Logic.
type Group struct {
	Logic          Logic         `json:"logic"`
	Conditions     []Condition   `json:"conditions,omitempty"`
	Groups         []Group       `json:"groups,omitempty"`
	MinimumSamples []SampleGuard `json:"minimum_samples,omitempty"`
}

// Rule is the persisted, JSON-serializable automation rule definition.
type Rule struct {
	ID             string        `json:"id,omitempty"`
	Name           string        `json:"name"`
	TargetLevel    TargetLevel   `json:"target_level"`
	Action         Action        `json:"action"`
	WindowSeconds  int64         `json:"window_seconds"`
	MinimumSamples []SampleGuard `json:"minimum_samples,omitempty"`
	Conditions     Group         `json:"conditions"`
	Metadata       RuleMetadata  `json:"metadata,omitempty"`
}

// Snapshot represents cumulative Meta Insights metrics at a point in time.
type Snapshot struct {
	TakenAt time.Time          `json:"taken_at"`
	Metrics map[string]float64 `json:"metrics"`
}

// WindowDelta is the rolling-window result obtained by subtracting an older
// cumulative snapshot from a newer one.
type WindowDelta struct {
	StartedAt time.Time          `json:"started_at"`
	EndedAt   time.Time          `json:"ended_at"`
	Metrics   map[string]float64 `json:"metrics"`
}

// EvaluationContext supplies the window metrics and timing state needed to
// evaluate one rule for one Meta entity.
type EvaluationContext struct {
	Now             time.Time          `json:"now"`
	ActiveSince     time.Time          `json:"active_since,omitempty"`
	LastTriggeredAt *time.Time         `json:"last_triggered_at,omitempty"`
	WindowStartedAt time.Time          `json:"window_started_at,omitempty"`
	WindowEndedAt   time.Time          `json:"window_ended_at,omitempty"`
	Metrics         map[string]float64 `json:"metrics"`
}

// Observation is the machine-readable explanation for a guard or condition.
type Observation struct {
	Path        string   `json:"path"`
	Kind        string   `json:"kind"`
	Metric      string   `json:"metric"`
	Value       float64  `json:"value"`
	Found       bool     `json:"found"`
	Defaulted   bool     `json:"defaulted,omitempty"`
	Operator    Operator `json:"operator"`
	Threshold   float64  `json:"threshold"`
	Matched     bool     `json:"matched"`
	Explanation string   `json:"explanation"`
}

// Evaluation is safe to persist as the audit record for a rule check.
type Evaluation struct {
	Matched      bool          `json:"matched"`
	Reasons      []string      `json:"reasons"`
	Observations []Observation `json:"observations"`
}
