package rules

import (
	"testing"
)

func TestFlattenInsightsActionValueConversionROASAndVideoArrays(t *testing.T) {
	t.Parallel()

	payload := []byte(`{
		"account_id": "123456789",
		"campaign_id": "987654321",
		"date_start": "2026-07-20",
		"spend": "120.50",
		"impressions": "10000",
		"actions": [
			{"action_type": "purchase", "value": "2", "1d_click": "1", "1d_view": "0"},
			{"action_type": "purchase", "value": "3"},
			{"action_type": "link_click", "value": "80"}
		],
		"action_values": [
			{"action_type": "purchase", "value": "725.25"}
		],
		"cost_per_action_type": [
			{"action_type": "purchase", "value": "24.10"}
		],
		"conversions": [
			{"action_type": "offsite_conversion.fb_pixel_complete_registration", "value": "17"}
		],
		"conversion_values": [
			{"action_type": "offsite_conversion.fb_pixel_purchase", "value": "810.75"}
		],
		"purchase_roas": [
			{"action_type": "omni_purchase", "value": "6.02"}
		],
		"website_purchase_roas": [
			{"action_type": "offsite_conversion.fb_pixel_purchase", "value": "6.73"}
		],
		"video_p25_watched_actions": [
			{"action_type": "video_view", "value": "900"}
		],
		"video_avg_time_watched_actions": [
			{"action_type": "video_view", "value": "8.5"}
		]
	}`)

	metrics, err := FlattenInsightsJSON(payload)
	if err != nil {
		t.Fatalf("FlattenInsightsJSON() error = %v", err)
	}

	expected := map[string]float64{
		"spend":                         120.50,
		"impressions":                   10000,
		"actions.purchase":              5,
		"actions.purchase.1d_click":     1,
		"actions.purchase.1d_view":      0,
		"actions.link_click":            80,
		"action_values.purchase":        725.25,
		"cost_per_action_type.purchase": 24.10,
		"conversions.offsite_conversion.fb_pixel_complete_registration": 17,
		"conversion_values.offsite_conversion.fb_pixel_purchase":        810.75,
		"purchase_roas.omni_purchase":                                   6.02,
		"website_purchase_roas.offsite_conversion.fb_pixel_purchase":    6.73,
		"video_p25_watched_actions.video_view":                          900,
		"video_avg_time_watched_actions.video_view":                     8.5,
	}
	for metric, want := range expected {
		if got, ok := metrics[metric]; !ok || !almostEqual(got, want) {
			t.Errorf("%s = %v (present %v), want %v", metric, got, ok, want)
		}
	}
	for _, ignored := range []string{"account_id", "campaign_id", "date_start"} {
		if _, exists := metrics[ignored]; exists {
			t.Errorf("metadata field %s must not be flattened", ignored)
		}
	}
}

func TestFlattenInsightsRejectsNonFiniteNumericValue(t *testing.T) {
	t.Parallel()

	metrics, err := FlattenInsights(map[string]any{
		"spend":       "NaN",
		"impressions": "100",
	})
	if err == nil {
		t.Fatal("FlattenInsights() error = nil, want invalid number error")
	}
	if got := metrics["impressions"]; got != 100 {
		t.Errorf("impressions = %v, want 100", got)
	}
	if _, exists := metrics["spend"]; exists {
		t.Error("non-finite spend must not be included")
	}
}

func TestFlattenInsightsJSONRejectsTrailingValue(t *testing.T) {
	t.Parallel()

	if _, err := FlattenInsightsJSON([]byte(`{"spend":"1"} {"spend":"2"}`)); err == nil {
		t.Fatal("FlattenInsightsJSON() error = nil, want trailing value error")
	}
}
