package rules

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	observationKindCondition = "condition"
	observationKindGuard     = "minimum_sample"
)

// Evaluate evaluates one rule against rolling-window metrics. The returned
// reasons and observations are intended to be persisted even when no match
// occurs.
func Evaluate(rule Rule, context EvaluationContext) (Evaluation, error) {
	if err := rule.Validate(); err != nil {
		return Evaluation{}, err
	}

	result := Evaluation{
		Reasons:      make([]string, 0),
		Observations: make([]Observation, 0),
	}
	now := evaluationTime(context)

	if context.WindowStartedAt.IsZero() != context.WindowEndedAt.IsZero() {
		return Evaluation{}, errors.New("window_started_at and window_ended_at must either both be set or both be omitted")
	}
	if !context.WindowStartedAt.IsZero() && context.WindowEndedAt.Before(context.WindowStartedAt) {
		return Evaluation{}, errors.New("window_ended_at cannot precede window_started_at")
	}

	if !context.ActiveSince.IsZero() && rule.Metadata.GracePeriodSeconds > 0 {
		eligibleAt := context.ActiveSince.Add(time.Duration(rule.Metadata.GracePeriodSeconds) * time.Second)
		if now.Before(eligibleAt) {
			result.Reasons = append(result.Reasons, fmt.Sprintf("grace period active until %s", eligibleAt.UTC().Format(time.RFC3339)))
			return result, nil
		}
	}

	if context.LastTriggeredAt != nil && rule.Metadata.CooldownSeconds > 0 {
		eligibleAt := context.LastTriggeredAt.Add(time.Duration(rule.Metadata.CooldownSeconds) * time.Second)
		if now.Before(eligibleAt) {
			result.Reasons = append(result.Reasons, fmt.Sprintf("cooldown active until %s", eligibleAt.UTC().Format(time.RFC3339)))
			return result, nil
		}
	}

	if !context.WindowStartedAt.IsZero() && !context.WindowEndedAt.IsZero() {
		actualWindow := context.WindowEndedAt.Sub(context.WindowStartedAt)
		requiredWindow := time.Duration(rule.WindowSeconds) * time.Second

		// A window wider than the object's whole life is already complete:
		// there is no earlier history to wait for, because the object did not
		// exist. Without this, a total-spend cap on a freshly launched
		// campaign waits out its entire lookback before it can ever act -
		// which is the one period the cap exists to cover.
		coversWholeLife := !context.ActiveSince.IsZero() &&
			!context.WindowStartedAt.After(context.ActiveSince)

		if actualWindow < requiredWindow && !coversWholeLife {
			result.Reasons = append(result.Reasons, fmt.Sprintf(
				"window is incomplete: observed %d seconds, requires %d seconds",
				int64(actualWindow.Seconds()),
				rule.WindowSeconds,
			))
			return result, nil
		}
	}

	metrics := WithDerivedMetrics(context.Metrics)
	if !evaluateGuards("minimum_samples", rule.MinimumSamples, metrics, &result) {
		result.Reasons = append(result.Reasons, "rule minimum sample guards were not satisfied")
		return result, nil
	}

	result.Matched = evaluateGroup("conditions", rule.Conditions, metrics, &result)
	if result.Matched {
		result.Reasons = append(result.Reasons, fmt.Sprintf("rule matched; %s target is eligible for pause", rule.TargetLevel))
	} else {
		result.Reasons = append(result.Reasons, "rule conditions did not match")
	}
	return result, nil
}

func evaluateGroup(path string, group Group, metrics map[string]float64, result *Evaluation) bool {
	if !evaluateGuards(path+".minimum_samples", group.MinimumSamples, metrics, result) {
		result.Reasons = append(result.Reasons, path+" minimum sample guards were not satisfied")
		return false
	}

	outcomes := make([]bool, 0, len(group.Conditions)+len(group.Groups))
	for index, condition := range group.Conditions {
		outcomes = append(outcomes, evaluateCondition(
			fmt.Sprintf("%s.conditions[%d]", path, index),
			condition,
			metrics,
			result,
		))
	}
	for index, child := range group.Groups {
		outcomes = append(outcomes, evaluateGroup(
			fmt.Sprintf("%s.groups[%d]", path, index),
			child,
			metrics,
			result,
		))
	}

	if group.Logic == LogicAny {
		for _, outcome := range outcomes {
			if outcome {
				return true
			}
		}
		return false
	}
	for _, outcome := range outcomes {
		if !outcome {
			return false
		}
	}
	return true
}

func evaluateCondition(path string, condition Condition, metrics map[string]float64, result *Evaluation) bool {
	if !evaluateGuards(path+".minimum_samples", condition.MinimumSamples, metrics, result) {
		result.Reasons = append(result.Reasons, path+" minimum sample guards were not satisfied")
		return false
	}

	value, found, defaulted := metricValue(metrics, condition.Metric, condition.MissingAsZero)
	matched := found && compare(value, condition.Operator, condition.Threshold)
	explanation := comparisonExplanation(condition.Metric, value, found, defaulted, condition.Operator, condition.Threshold, matched)
	result.Observations = append(result.Observations, Observation{
		Path:        path,
		Kind:        observationKindCondition,
		Metric:      condition.Metric,
		Value:       value,
		Found:       found,
		Defaulted:   defaulted,
		Operator:    condition.Operator,
		Threshold:   condition.Threshold,
		Matched:     matched,
		Explanation: explanation,
	})
	result.Reasons = append(result.Reasons, explanation)
	return matched
}

func evaluateGuards(path string, guards []SampleGuard, metrics map[string]float64, result *Evaluation) bool {
	allMatched := true
	for index, guard := range guards {
		value, found, defaulted := metricValue(metrics, guard.Metric, guard.MissingAsZero)
		matched := found && value >= guard.Minimum
		explanation := comparisonExplanation(guard.Metric, value, found, defaulted, OperatorGTE, guard.Minimum, matched)
		result.Observations = append(result.Observations, Observation{
			Path:        fmt.Sprintf("%s[%d]", path, index),
			Kind:        observationKindGuard,
			Metric:      guard.Metric,
			Value:       value,
			Found:       found,
			Defaulted:   defaulted,
			Operator:    OperatorGTE,
			Threshold:   guard.Minimum,
			Matched:     matched,
			Explanation: explanation,
		})
		result.Reasons = append(result.Reasons, explanation)
		allMatched = allMatched && matched
	}
	return allMatched
}

func metricValue(metrics map[string]float64, metric string, explicitlyMissingAsZero bool) (value float64, found, defaulted bool) {
	value, found = metrics[metric]
	if found && finite(value) {
		return value, true, false
	}
	if explicitlyMissingAsZero || implicitlyZeroWhenMissing(metric) {
		return 0, true, true
	}
	return 0, false, false
}

func implicitlyZeroWhenMissing(metric string) bool {
	for _, prefix := range []string{
		"actions.",
		"action_values.",
		"conversions.",
		"conversion_values.",
	} {
		if strings.HasPrefix(metric, prefix) {
			return true
		}
	}
	return strings.HasPrefix(metric, "video_") && strings.Contains(metric, ".")
}

func compare(value float64, operator Operator, threshold float64) bool {
	switch operator {
	case OperatorGT:
		return value > threshold
	case OperatorGTE:
		return value >= threshold
	case OperatorLT:
		return value < threshold
	case OperatorLTE:
		return value <= threshold
	case OperatorEQ:
		return nearlyEqual(value, threshold)
	case OperatorNEQ:
		return !nearlyEqual(value, threshold)
	default:
		return false
	}
}

func nearlyEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= 1e-9*scale
}

func comparisonExplanation(metric string, value float64, found, defaulted bool, operator Operator, threshold float64, matched bool) string {
	if !found {
		return fmt.Sprintf("%s is unavailable; condition did not match", metric)
	}
	defaultNote := ""
	if defaulted {
		defaultNote = " (defaulted to zero)"
	}
	return fmt.Sprintf(
		"%s=%s%s %s %s: %s",
		metric,
		formatNumber(value),
		defaultNote,
		operator,
		formatNumber(threshold),
		map[bool]string{true: "matched", false: "did not match"}[matched],
	)
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func evaluationTime(context EvaluationContext) time.Time {
	if !context.Now.IsZero() {
		return context.Now
	}
	if !context.WindowEndedAt.IsZero() {
		return context.WindowEndedAt
	}
	return time.Now().UTC()
}
