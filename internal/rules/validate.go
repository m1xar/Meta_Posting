package rules

import (
	"errors"
	"fmt"
	"math"
	"regexp"
)

var metricNamePattern = regexp.MustCompile(`^[A-Za-z0-9_:-]+(?:\.[A-Za-z0-9_:-]+)*$`)

// Validate checks that the rule can be safely evaluated and can only pause a
// supported Meta object.
func (r Rule) Validate() error {
	var problems []error

	switch r.TargetLevel {
	case TargetCampaign, TargetAdSet, TargetAd:
	default:
		problems = append(problems, fmt.Errorf("unsupported target_level %q", r.TargetLevel))
	}

	if r.Action != ActionPause {
		problems = append(problems, fmt.Errorf("unsupported action %q: only %q is allowed", r.Action, ActionPause))
	}
	if r.WindowSeconds <= 0 {
		problems = append(problems, errors.New("window_seconds must be greater than zero"))
	}
	if r.Metadata.GracePeriodSeconds < 0 {
		problems = append(problems, errors.New("metadata.grace_period_seconds cannot be negative"))
	}
	if r.Metadata.CooldownSeconds < 0 {
		problems = append(problems, errors.New("metadata.cooldown_seconds cannot be negative"))
	}

	problems = append(problems, validateGuards("minimum_samples", r.MinimumSamples)...)
	problems = append(problems, validateGroup("conditions", r.Conditions)...)

	return errors.Join(problems...)
}

func validateGroup(path string, group Group) []error {
	var problems []error

	switch group.Logic {
	case LogicAll, LogicAny:
	default:
		problems = append(problems, fmt.Errorf("%s.logic must be %q or %q", path, LogicAll, LogicAny))
	}
	if len(group.Conditions) == 0 && len(group.Groups) == 0 {
		problems = append(problems, fmt.Errorf("%s must contain at least one condition or group", path))
	}
	problems = append(problems, validateGuards(path+".minimum_samples", group.MinimumSamples)...)

	for index, condition := range group.Conditions {
		conditionPath := fmt.Sprintf("%s.conditions[%d]", path, index)
		if !validMetricName(condition.Metric) {
			problems = append(problems, fmt.Errorf("%s.metric %q is not a valid dotted metric name", conditionPath, condition.Metric))
		}
		switch condition.Operator {
		case OperatorGT, OperatorGTE, OperatorLT, OperatorLTE, OperatorEQ, OperatorNEQ:
		default:
			problems = append(problems, fmt.Errorf("%s.operator %q is unsupported", conditionPath, condition.Operator))
		}
		if !finite(condition.Threshold) {
			problems = append(problems, fmt.Errorf("%s.threshold must be finite", conditionPath))
		}
		problems = append(problems, validateGuards(conditionPath+".minimum_samples", condition.MinimumSamples)...)
	}

	for index, child := range group.Groups {
		problems = append(problems, validateGroup(fmt.Sprintf("%s.groups[%d]", path, index), child)...)
	}

	return problems
}

func validateGuards(path string, guards []SampleGuard) []error {
	var problems []error
	for index, guard := range guards {
		guardPath := fmt.Sprintf("%s[%d]", path, index)
		if !validMetricName(guard.Metric) {
			problems = append(problems, fmt.Errorf("%s.metric %q is not a valid dotted metric name", guardPath, guard.Metric))
		}
		if !finite(guard.Minimum) || guard.Minimum < 0 {
			problems = append(problems, fmt.Errorf("%s.minimum must be finite and non-negative", guardPath))
		}
	}
	return problems
}

func validMetricName(metric string) bool {
	return metricNamePattern.MatchString(metric)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
