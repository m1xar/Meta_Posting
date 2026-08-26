package meta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDailyInsightRequestBuildsQuery(t *testing.T) {
	request := DailyInsightRequest{
		AccountID:   "123456",
		Level:       InsightLevelCampaign,
		Since:       "2026-03-01",
		Until:       "2026-03-07",
		Attribution: AttributionMode{Unified: true},
	}
	query, err := request.query()
	require.NoError(t, err)

	values, err := query.values()
	require.NoError(t, err)

	// time_increment=1 is what produces one row per day; without it Meta
	// returns a single aggregate and the daily upsert loses its meaning.
	require.Equal(t, "1", values.Get("time_increment"))
	require.Equal(t, "campaign", values.Get("level"))
	require.JSONEq(t, `{"since":"2026-03-01","until":"2026-03-07"}`, values.Get("time_range"))
	require.Equal(t, "true", values.Get("use_unified_attribution_setting"))
	require.Empty(t, values.Get("action_attribution_windows"))
	require.Empty(t, values.Get("date_preset"))
	require.Contains(t, values.Get("fields"), "account_currency")
}

func TestDailyInsightRequestExplicitAttributionWindows(t *testing.T) {
	request := DailyInsightRequest{
		AccountID:   "act_123456",
		Level:       InsightLevelAd,
		Since:       "2026-03-01",
		Until:       "2026-03-01",
		Attribution: AttributionMode{Windows: []string{"1d_view", "7d_click"}},
	}
	query, err := request.query()
	require.NoError(t, err)
	values, err := query.values()
	require.NoError(t, err)

	require.JSONEq(t, `["1d_view","7d_click"]`, values.Get("action_attribution_windows"))
	require.Empty(t, values.Get("use_unified_attribution_setting"))
	require.Equal(t, "1d_view,7d_click", request.Attribution.Setting())
}

func TestAttributionModeRejectsConflictingSettings(t *testing.T) {
	// Sending both makes Meta reject the whole query with an opaque (#100).
	_, err := DailyInsightRequest{
		AccountID:   "123",
		Level:       InsightLevelAd,
		Since:       "2026-03-01",
		Until:       "2026-03-01",
		Attribution: AttributionMode{Unified: true, Windows: []string{"7d_click"}},
	}.query()
	require.ErrorContains(t, err, "mutually exclusive")

	// The same conflict assembled directly on the query is also caught.
	unified := true
	_, err = InsightQuery{
		Level:                        InsightLevelAd,
		UseUnifiedAttributionSetting: &unified,
		ActionAttributionWindows:     []string{"7d_click"},
	}.values()
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestDailyInsightRequestValidation(t *testing.T) {
	base := DailyInsightRequest{
		AccountID: "123",
		Level:     InsightLevelAd,
		Since:     "2026-03-01",
		Until:     "2026-03-07",
	}

	missing := base
	missing.AccountID = "  "
	_, err := missing.query()
	require.ErrorContains(t, err, "ad account ID")

	badLevel := base
	badLevel.Level = "creative"
	_, err = badLevel.query()
	require.ErrorContains(t, err, "unsupported insights level")

	reversed := base
	reversed.Since, reversed.Until = "2026-03-07", "2026-03-01"
	_, err = reversed.query()
	require.ErrorContains(t, err, "ends before it starts")

	malformed := base
	malformed.Since = "01-03-2026"
	_, err = malformed.query()
	require.Error(t, err)
}

func TestAttributionModeSetting(t *testing.T) {
	require.Equal(t, "unified", AttributionMode{Unified: true}.Setting())
	require.Equal(t, "1d_view,7d_click", AttributionMode{Windows: []string{"1d_view", "7d_click"}}.Setting())
	require.Equal(t, "account_default", AttributionMode{}.Setting())
}

func TestFieldsForLevel(t *testing.T) {
	// account_currency must be present everywhere: a stored row has to know
	// what currency its spend is in.
	for _, level := range []InsightLevel{
		InsightLevelAccount, InsightLevelCampaign, InsightLevelAdSet, InsightLevelAd,
	} {
		require.Contains(t, FieldsForLevel(level), "account_currency", "level %s", level)
		require.Contains(t, FieldsForLevel(level), "spend", "level %s", level)
	}

	// Extended fields stay opt-in until the field audit confirms them: one
	// invalid field fails the entire query, so a guess would stop ingestion.
	require.NotContains(t, FieldsForLevel(InsightLevelAd), "video_p95_watched_actions")
	require.Contains(t, ExtendedInsightFields, "video_p95_watched_actions")
}

func TestWindowedRequestOmitsTimeIncrement(t *testing.T) {
	// Reach is deduplicated per query window. Asking for it per day and
	// summing is wrong; the windowed query lets Meta do the deduplication.
	daily := DailyInsightRequest{
		AccountID:   "123",
		Level:       InsightLevelCampaign,
		Since:       "2026-03-01",
		Until:       "2026-03-28",
		Fields:      WindowedInsightFields,
		Attribution: AttributionMode{Unified: true},
	}
	query, err := daily.query()
	require.NoError(t, err)
	query.TimeIncrement = nil

	values, err := query.values()
	require.NoError(t, err)
	require.Empty(t, values.Get("time_increment"))
	require.Contains(t, values.Get("fields"), "reach")
}
