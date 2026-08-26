package metrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

var arrayDiscriminatorKeys = []string{
	"action_type",
	"conversion_event",
	"event_type",
	"metric",
	"name",
	"type",
}

var arrayValueKeys = []string{
	"value",
	"count",
	"amount",
}

// FlattenInsights converts one Meta Insights row into dotted numeric metrics.
// Meta action-like arrays are flattened as "<field>.<action_type>" and duplicate
// action types are summed. Non-metric metadata is ignored.
func FlattenInsights(payload map[string]any) (map[string]float64, error) {
	metrics := make(map[string]float64)
	var problems []error

	for key, value := range payload {
		if ignoredInsightsKey(key) {
			continue
		}
		flattenInsightsValue(metrics, key, value, &problems)
	}

	return metrics, errors.Join(problems...)
}

// FlattenInsightsJSON decodes a single Insights row while preserving JSON
// numbers, then delegates to FlattenInsights.
func FlattenInsightsJSON(data []byte) (map[string]float64, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Meta Insights payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("decode Meta Insights payload: multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing Meta Insights payload: %w", err)
	}
	return FlattenInsights(payload)
}

func flattenInsightsValue(metrics map[string]float64, path string, raw any, problems *[]error) {
	if raw == nil {
		return
	}

	if value, ok, err := numericValue(raw); ok {
		if err != nil {
			*problems = append(*problems, fmt.Errorf("%s: %w", path, err))
			return
		}
		metrics[path] += value
		return
	}

	switch value := raw.(type) {
	case map[string]any:
		for key, nested := range value {
			if ignoredInsightsKey(key) {
				continue
			}
			flattenInsightsValue(metrics, path+"."+key, nested, problems)
		}
	case []any:
		flattenInsightsArray(metrics, path, value, problems)
	case []map[string]any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, item)
		}
		flattenInsightsArray(metrics, path, items, problems)
	}
}

func flattenInsightsArray(metrics map[string]float64, path string, items []any, problems *[]error) {
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			if value, numeric, err := numericValue(rawItem); numeric {
				if err != nil {
					*problems = append(*problems, fmt.Errorf("%s[%d]: %w", path, index, err))
					continue
				}
				metrics[fmt.Sprintf("%s.%d", path, index)] += value
			}
			continue
		}

		discriminator := firstString(item, arrayDiscriminatorKeys)
		valueRaw, hasValue := firstValue(item, arrayValueKeys)
		if discriminator != "" && hasValue {
			value, numeric, err := numericValue(valueRaw)
			if !numeric {
				continue
			}
			if err != nil {
				*problems = append(*problems, fmt.Errorf("%s[%d].value: %w", path, index, err))
				continue
			}
			metrics[path+"."+discriminator] += value
			for key, attributedRaw := range item {
				if key == "action_type" || key == "value" || key == "count" || key == "amount" {
					continue
				}
				attributed, attributedNumeric, attributedErr := numericValue(attributedRaw)
				if !attributedNumeric {
					continue
				}
				if attributedErr != nil {
					*problems = append(*problems, fmt.Errorf("%s[%d].%s: %w", path, index, key, attributedErr))
					continue
				}
				metrics[path+"."+discriminator+"."+key] += attributed
			}
			continue
		}

		// Preserve numeric data from uncommon future array shapes without
		// guessing a semantic discriminator.
		for key, nested := range item {
			if key == "action_type" || ignoredInsightsKey(key) {
				continue
			}
			flattenInsightsValue(metrics, fmt.Sprintf("%s.%d.%s", path, index, key), nested, problems)
		}
	}
}

func firstString(values map[string]any, keys []string) string {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func firstValue(values map[string]any, keys []string) (any, bool) {
	for _, key := range keys {
		value, ok := values[key]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func numericValue(raw any) (float64, bool, error) {
	var value float64
	switch typed := raw.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, true, fmt.Errorf("invalid number %q", typed)
		}
		value = parsed
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false, nil
		}
		value = parsed
	case float64:
		value = typed
	case float32:
		value = float64(typed)
	case int:
		value = float64(typed)
	case int8:
		value = float64(typed)
	case int16:
		value = float64(typed)
	case int32:
		value = float64(typed)
	case int64:
		value = float64(typed)
	case uint:
		value = float64(typed)
	case uint8:
		value = float64(typed)
	case uint16:
		value = float64(typed)
	case uint32:
		value = float64(typed)
	case uint64:
		value = float64(typed)
	default:
		return 0, false, nil
	}

	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, true, errors.New("number must be finite")
	}
	return value, true, nil
}

func ignoredInsightsKey(key string) bool {
	lower := strings.ToLower(key)
	if lower == "id" || strings.HasSuffix(lower, "_id") {
		return true
	}
	switch lower {
	case "date_start", "date_stop", "account_name", "campaign_name", "adset_name", "ad_name",
		"objective", "buying_type", "account_currency":
		return true
	default:
		return false
	}
}
