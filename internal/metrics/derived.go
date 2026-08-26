package metrics

import (
	"math"
	"strings"
)

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

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
