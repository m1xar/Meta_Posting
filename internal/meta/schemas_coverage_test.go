package meta

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// The insight field sets are a contract with Meta, not an implementation
// detail: one invalid field fails an entire query with (#100), so a field
// silently appearing or disappearing stops ingestion rather than degrading
// it. These golden lists make such a change a failing test with a diff,
// instead of a production incident.
//
// To change a set: run cmd/fieldaudit against a live account, confirm the
// level accepts the field, then update the list here in the same commit.

func TestDefaultInsightFieldsGolden(t *testing.T) {
	golden := []string{
		"account_id", "account_name",
		"campaign_id", "campaign_name",
		"adset_id", "adset_name",
		"ad_id", "ad_name",
		"date_start", "date_stop",
		"spend", "impressions", "reach", "frequency",
		"clicks", "unique_clicks", "inline_link_clicks", "unique_inline_link_clicks",
		"ctr", "unique_ctr", "cpc", "cpm", "cpp",
		"cost_per_unique_click", "cost_per_inline_link_click",
		"actions", "action_values", "cost_per_action_type",
		"conversions", "conversion_values", "cost_per_conversion",
		"purchase_roas", "website_purchase_roas", "mobile_app_purchase_roas",
		"outbound_clicks", "outbound_clicks_ctr", "cost_per_outbound_click",
		"video_play_actions", "video_thruplay_watched_actions",
		"video_avg_time_watched_actions",
		"quality_ranking", "engagement_rate_ranking", "conversion_rate_ranking",
	}
	requireSameFields(t, golden, DefaultInsightFields, "DefaultInsightFields")
}

func TestAccountInsightFieldsExtendTheDefaults(t *testing.T) {
	// A stored row must know its currency, so account_currency belongs in
	// every set that produces one.
	require.Contains(t, AccountInsightFields, "account_currency")
	require.Contains(t, AccountInsightFields, "objective")
	require.Contains(t, AccountInsightFields, "buying_type")

	for _, field := range DefaultInsightFields {
		require.Contains(t, AccountInsightFields, field,
			"AccountInsightFields must be a superset of DefaultInsightFields")
	}
}

func TestExtendedInsightFieldsAreNotUsedByDefault(t *testing.T) {
	// Extended fields are unverified against the live API. Meta fails a whole
	// query on one invalid field, so shipping them by default would stop
	// ingestion rather than degrade it; the audit promotes them once
	// confirmed.
	extendedOnly := []string{
		"video_p95_watched_actions",
		"cost_per_thruplay",
		"social_spend",
		"estimated_ad_recallers",
	}
	for _, field := range extendedOnly {
		require.Contains(t, ExtendedInsightFields, field)
		for _, level := range []InsightLevel{
			InsightLevelAccount, InsightLevelCampaign, InsightLevelAdSet, InsightLevelAd,
		} {
			require.NotContains(t, FieldsForLevel(level), field,
				"%s must stay opt-in until the field audit confirms it at %s", field, level)
		}
	}
}

func TestFieldsForLevelAlwaysCarriesTheEssentials(t *testing.T) {
	for _, level := range []InsightLevel{
		InsightLevelAccount, InsightLevelCampaign, InsightLevelAdSet, InsightLevelAd,
	} {
		fields := FieldsForLevel(level)
		for _, required := range []string{
			"spend", "impressions", "clicks", "date_start", "date_stop", "account_currency",
		} {
			require.Contains(t, fields, required, "level %s must request %s", level, required)
		}
		require.Equal(t, len(fields), len(uniqueStrings(fields)),
			"level %s requests a duplicate field", level)
	}
}

func TestWindowedInsightFieldsStayMinimal(t *testing.T) {
	// This query runs per level per account every night. Only reach,
	// frequency and their denominators cannot be derived from daily rows, so
	// everything else is waste.
	require.Contains(t, WindowedInsightFields, "reach")
	require.Contains(t, WindowedInsightFields, "frequency")
	require.Contains(t, WindowedInsightFields, "impressions")
	require.Less(t, len(WindowedInsightFields), len(DefaultInsightFields)/2,
		"the windowed set should stay far smaller than the daily set")
	require.NotContains(t, WindowedInsightFields, "actions")
}

func TestInsightFieldSetsShareNoDuplicates(t *testing.T) {
	for name, fields := range map[string][]string{
		"DefaultInsightFields":  DefaultInsightFields,
		"AccountInsightFields":  AccountInsightFields,
		"ExtendedInsightFields": ExtendedInsightFields,
		"WindowedInsightFields": WindowedInsightFields,
	} {
		require.Equal(t, len(fields), len(uniqueStrings(fields)), "%s contains a duplicate", name)
	}
}

func requireSameFields(t *testing.T, golden, actual []string, name string) {
	t.Helper()
	expected := append([]string(nil), golden...)
	got := append([]string(nil), actual...)
	sort.Strings(expected)
	sort.Strings(got)
	require.Equal(t, expected, got,
		"%s changed. Run cmd/fieldaudit against a live account to confirm the new set, "+
			"then update the golden list in the same commit.", name)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
