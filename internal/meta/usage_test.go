package meta

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func usageHeaders(t *testing.T, app, account, business string) ResponseMeta {
	t.Helper()
	response := ResponseMeta{}
	if app != "" {
		require.NoError(t, json.Unmarshal([]byte(app), &response.AppUsage))
	}
	if account != "" {
		require.NoError(t, json.Unmarshal([]byte(account), &response.AdAccountUsage))
	}
	if business != "" {
		require.NoError(t, json.Unmarshal([]byte(business), &response.BusinessUsage))
	}
	return response
}

func TestParseAppUsage(t *testing.T) {
	response := usageHeaders(t, `{"call_count":25,"total_cputime":12,"total_time":8}`, "", "")
	usage, ok := ParseAppUsage(response)
	require.True(t, ok)
	require.InDelta(t, 25, usage.CallCount, 1e-9)
	require.InDelta(t, 12, usage.TotalCPUTime, 1e-9)
	// Pressure is the worst counter, not the average: Meta blocks on any one.
	require.InDelta(t, 0.25, usage.Pressure(), 1e-9)

	_, ok = ParseAppUsage(ResponseMeta{})
	require.False(t, ok)
}

func TestParseAppUsageTakesTheWorstCounter(t *testing.T) {
	response := usageHeaders(t, `{"call_count":10,"total_cputime":97,"total_time":30}`, "", "")
	usage, _ := ParseAppUsage(response)
	require.InDelta(t, 0.97, usage.Pressure(), 1e-9)
}

func TestParseAdAccountUsageHasItsOwnShape(t *testing.T) {
	response := usageHeaders(t, "",
		`{"acc_id_util_pct":9.67,"reset_time_duration":100,"ads_api_access_tier":"standard_access"}`, "")
	usage, ok := ParseAdAccountUsage(response)
	require.True(t, ok)
	require.InDelta(t, 9.67, usage.CallCount, 1e-9)
	require.Equal(t, 100, usage.ResetTimeDuration)
	require.Equal(t, "standard_access", usage.Tier)
	require.InDelta(t, 0.0967, usage.Pressure(), 1e-9)
}

func TestParseBusinessUsageIsAnArrayKeyedByBusiness(t *testing.T) {
	response := usageHeaders(t, "", "", `{
		"17841400000000000":[
			{"type":"ads_management","call_count":10,"total_cputime":5,"total_time":5,
			 "estimated_time_to_regain_access":0},
			{"type":"ads_insights","call_count":88,"total_cputime":40,"total_time":30,
			 "estimated_time_to_regain_access":0}
		]
	}`)
	usage, ok := ParseBusinessUsage(response)
	require.True(t, ok)
	// The worst use case wins; any one of them can block.
	require.InDelta(t, 0.88, usage.Pressure(), 1e-9)
	require.Equal(t, "ads_insights", usage.Type)
}

func TestBusinessUsageSurfacesAnActiveBlock(t *testing.T) {
	response := usageHeaders(t, "", "", `{
		"1784140":[
			{"type":"ads_insights","call_count":100,"estimated_time_to_regain_access":12}
		]
	}`)
	usage, ok := ParseBusinessUsage(response)
	require.True(t, ok)
	require.True(t, usage.Blocked())
	require.Equal(t, 12, usage.EstimatedTimeToRegainAccess)

	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	until, blocked := usage.BlockedUntil(now)
	require.True(t, blocked)
	require.Equal(t, now.Add(12*time.Minute), until)
}

func TestWorstUsageCombinesAllThreeHeaders(t *testing.T) {
	response := usageHeaders(t,
		`{"call_count":10}`,
		`{"acc_id_util_pct":45}`,
		`{"178":[{"type":"ads_insights","call_count":92,"estimated_time_to_regain_access":0}]}`,
	)
	usage, ok := WorstUsage(response)
	require.True(t, ok)
	require.InDelta(t, 0.92, usage.Pressure(), 1e-9)
}

func TestWorstUsageKeepsABlockFromALowerPressureHeader(t *testing.T) {
	// A block must never be lost just because another header reports a higher
	// percentage; being blocked is strictly more important than being busy.
	response := usageHeaders(t,
		`{"call_count":95}`,
		"",
		`{"178":[{"type":"ads_insights","call_count":10,"estimated_time_to_regain_access":30}]}`,
	)
	usage, ok := WorstUsage(response)
	require.True(t, ok)
	require.InDelta(t, 0.95, usage.Pressure(), 1e-9)
	require.True(t, usage.Blocked())
	require.Equal(t, 30, usage.EstimatedTimeToRegainAccess)
}

func TestUsageParsingToleratesMalformedHeaders(t *testing.T) {
	// Meta sends numbers as strings in places, and adds fields without an API
	// version change. Neither may break ingestion.
	response := usageHeaders(t, `{"call_count":"42","unknown_field":{"a":1}}`, "", "")
	usage, ok := ParseAppUsage(response)
	require.True(t, ok)
	require.InDelta(t, 42, usage.CallCount, 1e-9)

	garbage := ResponseMeta{BusinessUsage: map[string]json.RawMessage{"x": json.RawMessage(`"not an array"`)}}
	_, ok = ParseBusinessUsage(garbage)
	require.False(t, ok)

	_, ok = WorstUsage(ResponseMeta{})
	require.False(t, ok)
}
