package rules

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

var ErrCounterCorrection = errors.New("cumulative insight counter decreased")

const counterCorrectionEpsilon = 1e-9

// CounterCorrectionError means Meta revised at least one cumulative counter
// down between snapshots. A fixed Meta object cannot reset its lifetime
// counters, so treating the current lifetime value as a rolling-window value
// could trigger an unsafe automation action.
type CounterCorrectionError struct {
	Metrics []string
}

func (e *CounterCorrectionError) Error() string {
	if e == nil || len(e.Metrics) == 0 {
		return ErrCounterCorrection.Error()
	}
	return fmt.Sprintf("%s: %s", ErrCounterCorrection, strings.Join(e.Metrics, ", "))
}

func (e *CounterCorrectionError) Unwrap() error {
	return ErrCounterCorrection
}

// RollingWindowDelta subtracts two cumulative snapshots. Meta's already-derived
// rate/cost metrics are carried from the current snapshot; additive rates are
// recomputed from window counters when possible. Non-additive reach, frequency,
// and CPP are intentionally absent because lifetime snapshots cannot produce
// correct rolling-window values for them.
func RollingWindowDelta(older, newer Snapshot) (WindowDelta, error) {
	if newer.TakenAt.Before(older.TakenAt) {
		return WindowDelta{}, errors.New("newer snapshot cannot precede older snapshot")
	}
	if older.Metrics == nil || newer.Metrics == nil {
		return WindowDelta{}, errors.New("both snapshots must contain metrics")
	}

	metrics, err := DeltaMetrics(older.Metrics, newer.Metrics)
	if err != nil {
		return WindowDelta{}, err
	}
	return WindowDelta{
		StartedAt: older.TakenAt,
		EndedAt:   newer.TakenAt,
		Metrics:   WithDerivedMetrics(metrics),
	}, nil
}

// DeltaMetrics computes a delta for additive cumulative metrics. A material
// decrease is returned as CounterCorrectionError so the caller can skip the
// unsafe evaluation. The input maps are never mutated.
func DeltaMetrics(previous, current map[string]float64) (map[string]float64, error) {
	delta := make(map[string]float64, len(current))
	var correctedMetrics []string

	for metric, currentValue := range current {
		if !finite(currentValue) {
			continue
		}
		if !isAdditiveRollingMetric(metric) {
			// A lifetime rate/cost/average is not the rate for the requested
			// rolling interval. Safe derived values are rebuilt below from
			// additive window counters; unavailable ratios remain absent.
			continue
		}

		previousValue, found := previous[metric]
		if !found || !finite(previousValue) {
			delta[metric] = currentValue
			continue
		}
		if currentValue >= previousValue || math.Abs(currentValue-previousValue) <= counterCorrectionEpsilon {
			delta[metric] = normalizeZero(math.Max(0, currentValue-previousValue))
			continue
		}

		correctedMetrics = append(correctedMetrics, metric)
	}

	// Meta may omit a previously reported action/conversion (or any other
	// additive counter) from a later lifetime snapshot. Treat an absent current
	// key exactly like an explicit zero. Otherwise the key silently disappears
	// from the rolling delta and a missing-as-zero condition could act on stale
	// or corrected data.
	for metric, previousValue := range previous {
		if _, found := current[metric]; found {
			continue
		}
		if !finite(previousValue) || !isAdditiveRollingMetric(metric) {
			continue
		}
		if previousValue > counterCorrectionEpsilon {
			correctedMetrics = append(correctedMetrics, metric)
			continue
		}
		// Preserve the same epsilon and zero-normalization behavior as an
		// explicitly reported current value of zero.
		delta[metric] = normalizeZero(math.Max(0, -previousValue))
	}

	if len(correctedMetrics) > 0 {
		sort.Strings(correctedMetrics)
		return nil, &CounterCorrectionError{Metrics: correctedMetrics}
	}
	return delta, nil
}

// WithDerivedMetrics returns a copy enriched with common metrics that can be
// computed from the counters present in source.
func WithDerivedMetrics(source map[string]float64) map[string]float64 {
	metrics := make(map[string]float64, len(source)+16)
	for metric, value := range source {
		if finite(value) {
			metrics[metric] = value
		}
	}

	spend, hasSpend := metrics["spend"]
	impressions, hasImpressions := metrics["impressions"]
	clicks, hasClicks := metrics["clicks"]
	reach, hasReach := metrics["reach"]

	setRatio(metrics, "ctr", clicks*100, impressions, hasClicks && hasImpressions)
	setRatio(metrics, "cpc", spend, clicks, hasSpend && hasClicks)
	setRatio(metrics, "cpm", spend*1000, impressions, hasSpend && hasImpressions)
	setRatio(metrics, "frequency", impressions, reach, hasImpressions && hasReach)
	setRatio(metrics, "cpp", spend*1000, reach, hasSpend && hasReach)

	if inlineClicks, ok := metrics["inline_link_clicks"]; ok && hasImpressions {
		setRatio(metrics, "inline_link_click_ctr", inlineClicks*100, impressions, true)
	}
	if linkClicks, ok := metrics["link_clicks"]; ok && hasImpressions {
		setRatio(metrics, "link_ctr", linkClicks*100, impressions, true)
	}
	for metric, outboundClicks := range metrics {
		if !strings.HasPrefix(metric, "outbound_clicks.") || !hasImpressions {
			continue
		}
		suffix := strings.TrimPrefix(metric, "outbound_clicks.")
		if suffix != "" {
			setRatio(metrics, "outbound_clicks_ctr."+suffix, outboundClicks*100, impressions, true)
		}
	}
	if uniqueClicks, ok := metrics["unique_clicks"]; ok && hasSpend {
		setRatio(metrics, "cost_per_unique_click", spend, uniqueClicks, true)
	}
	if uniqueInlineClicks, ok := metrics["unique_inline_link_clicks"]; ok && hasSpend {
		setRatio(metrics, "cost_per_inline_link_click", spend, uniqueInlineClicks, true)
	}

	if hasSpend {
		deriveCostMetrics(metrics, "actions.", "cost_per_action_type.", "cpa.", spend)
		deriveCostMetrics(metrics, "conversions.", "cost_per_conversion.", "conversion_cpa.", spend)
		deriveValueMetrics(metrics, "action_values.", "roas.", spend)
		deriveValueMetrics(metrics, "conversion_values.", "conversion_roas.", spend)
	}

	return metrics
}

func deriveCostMetrics(metrics map[string]float64, sourcePrefix, canonicalPrefix, shortPrefix string, spend float64) {
	for metric, count := range metrics {
		if !strings.HasPrefix(metric, sourcePrefix) {
			continue
		}
		suffix := strings.TrimPrefix(metric, sourcePrefix)
		if suffix == "" || count <= 0 {
			continue
		}
		setRatio(metrics, canonicalPrefix+suffix, spend, count, true)
		setRatio(metrics, shortPrefix+suffix, spend, count, true)
	}
}

func deriveValueMetrics(metrics map[string]float64, sourcePrefix, destinationPrefix string, spend float64) {
	for metric, value := range metrics {
		if !strings.HasPrefix(metric, sourcePrefix) {
			continue
		}
		suffix := strings.TrimPrefix(metric, sourcePrefix)
		if suffix == "" {
			continue
		}
		setRatio(metrics, destinationPrefix+suffix, value, spend, true)
	}
}

func setRatio(metrics map[string]float64, metric string, numerator, denominator float64, inputsAvailable bool) {
	if !inputsAvailable || denominator <= 0 {
		return
	}
	value := numerator / denominator
	if finite(value) {
		metrics[metric] = value
	}
}

func isDerivedMetric(metric string) bool {
	lower := strings.ToLower(metric)
	base := lower
	if separator := strings.IndexByte(base, '.'); separator >= 0 {
		base = base[:separator]
	}
	if strings.Contains(lower, "roas") ||
		strings.HasPrefix(lower, "cost_per_") ||
		strings.HasPrefix(lower, "cpa.") ||
		strings.HasSuffix(base, "_ctr") ||
		strings.HasSuffix(base, "_rate") ||
		strings.Contains(base, "_avg_") {
		return true
	}
	switch lower {
	case "ctr", "unique_ctr", "inline_link_click_ctr", "link_ctr", "frequency", "cpc", "cpm", "cpp":
		return true
	default:
		return false
	}
}

func isNonAdditiveRollingMetric(metric string) bool {
	switch strings.ToLower(metric) {
	case "reach", "frequency", "cpp":
		return true
	default:
		return false
	}
}

func isAdditiveRollingMetric(metric string) bool {
	return !isNonAdditiveRollingMetric(metric) && !isDerivedMetric(metric)
}

func normalizeZero(value float64) float64 {
	if math.Abs(value) < 1e-12 {
		return 0
	}
	return value
}

func (delta WindowDelta) DurationSeconds() int64 {
	return int64(delta.EndedAt.Sub(delta.StartedAt).Seconds())
}

func (delta WindowDelta) String() string {
	return fmt.Sprintf("%s..%s (%d seconds, %d metrics)", delta.StartedAt.UTC(), delta.EndedAt.UTC(), delta.DurationSeconds(), len(delta.Metrics))
}
