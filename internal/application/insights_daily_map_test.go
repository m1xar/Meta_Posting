package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func decodeRow(t *testing.T, payload string) meta.InsightRow {
	t.Helper()
	var row meta.InsightRow
	require.NoError(t, json.Unmarshal([]byte(payload), &row))
	return row
}

func testRowContext(level domain.InsightLevel) dailyRowContext {
	return dailyRowContext{
		ConnectionID:       uuid.New(),
		AdAccountID:        uuid.New(),
		MetaAccountID:      "act_123",
		AccountTimezone:    "America/Los_Angeles",
		Currency:           "USD",
		AttributionSetting: "unified",
		Level:              level,
		FetchedAt:          time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC),
	}
}

func TestDailyInsightFromRowMapsTypedMetrics(t *testing.T) {
	row := decodeRow(t, `{
		"account_id":"123","campaign_id":"c1","campaign_name":"Spring",
		"date_start":"2026-03-11","date_stop":"2026-03-11",
		"spend":"123.45","impressions":"10000","reach":"7500","frequency":"1.3333",
		"clicks":"250","unique_clicks":"210","inline_link_clicks":"180",
		"ctr":"2.5","cpc":"0.4938","cpm":"12.345","cpp":"16.46",
		"quality_ranking":"ABOVE_AVERAGE",
		"account_currency":"EUR"
	}`)

	record, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightCampaign))
	require.NoError(t, err)
	require.True(t, ok)

	require.Equal(t, "c1", record.MetaObjectID)
	require.Equal(t, "Spring", record.ObjectName)
	require.Equal(t, domain.InsightCampaign, record.Level)
	require.Equal(t, time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC), record.Date)
	require.InDelta(t, 123.45, record.Spend, 1e-9)
	require.Equal(t, int64(10000), record.Impressions)
	require.Equal(t, int64(7500), record.Reach)
	require.Equal(t, int64(250), record.Clicks)
	require.Equal(t, "ABOVE_AVERAGE", record.QualityRanking)
	// A currency on the row wins over the account default.
	require.Equal(t, "EUR", record.Currency)
	require.Equal(t, "unified", record.AttributionSetting)
	require.Equal(t, "America/Los_Angeles", record.AccountTimezone)
}

func TestDailyInsightPreservesEveryAttributionWindow(t *testing.T) {
	// The headline value plus each window must survive, so a later question
	// about 1d_view vs 7d_click does not require re-fetching history.
	row := decodeRow(t, `{
		"account_id":"123","ad_id":"a1","date_start":"2026-03-11","date_stop":"2026-03-11",
		"actions":[
			{"action_type":"purchase","value":"12","1d_click":"9","7d_click":"12","1d_view":"3","28d_click":"12"},
			{"action_type":"lead","value":"40","7d_click":"38"}
		],
		"action_values":[{"action_type":"purchase","value":"480.50"}]
	}`)

	record, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightAd))
	require.NoError(t, err)
	require.True(t, ok)

	var actions map[string]map[string]float64
	require.NoError(t, record.Actions.Decode(&actions))

	require.InDelta(t, 12, actions["purchase"]["value"], 1e-9)
	require.InDelta(t, 9, actions["purchase"]["1d_click"], 1e-9)
	require.InDelta(t, 12, actions["purchase"]["7d_click"], 1e-9)
	require.InDelta(t, 3, actions["purchase"]["1d_view"], 1e-9)
	require.InDelta(t, 12, actions["purchase"]["28d_click"], 1e-9)
	require.InDelta(t, 40, actions["lead"]["value"], 1e-9)
	require.NotContains(t, actions["purchase"], "action_type")

	var values map[string]map[string]float64
	require.NoError(t, record.ActionValues.Decode(&values))
	require.InDelta(t, 480.50, values["purchase"]["value"], 1e-9)
}

func TestDailyInsightMetricsAreRuleReadable(t *testing.T) {
	// The automation DSL reads dotted names off the flat map. If this shape
	// drifts, every rule silently stops matching.
	row := decodeRow(t, `{
		"account_id":"123","campaign_id":"c1","date_start":"2026-03-11","date_stop":"2026-03-11",
		"spend":"100.00","impressions":"5000","clicks":"50",
		"actions":[{"action_type":"complete_registration","value":"7"}]
	}`)

	record, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightCampaign))
	require.NoError(t, err)
	require.True(t, ok)

	var metrics map[string]float64
	require.NoError(t, record.Metrics.Decode(&metrics))
	require.InDelta(t, 100.0, metrics["spend"], 1e-9)
	require.InDelta(t, 5000, metrics["impressions"], 1e-9)
	require.InDelta(t, 7, metrics["actions.complete_registration"], 1e-9)
}

func TestDailyInsightParentIdentifiersByLevel(t *testing.T) {
	payload := `{
		"account_id":"123","campaign_id":"c1","adset_id":"s1","ad_id":"a1",
		"adset_name":"Set","ad_name":"Ad",
		"date_start":"2026-03-11","date_stop":"2026-03-11","spend":"1"
	}`

	campaign, ok, err := dailyInsightFromRow(decodeRow(t, payload), testRowContext(domain.InsightCampaign))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "c1", campaign.MetaObjectID)
	require.Empty(t, campaign.CampaignMetaID)
	require.Empty(t, campaign.AdSetMetaID)

	adset, ok, err := dailyInsightFromRow(decodeRow(t, payload), testRowContext(domain.InsightAdSet))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "s1", adset.MetaObjectID)
	require.Equal(t, "c1", adset.CampaignMetaID)
	require.Empty(t, adset.AdSetMetaID)

	ad, ok, err := dailyInsightFromRow(decodeRow(t, payload), testRowContext(domain.InsightAd))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "a1", ad.MetaObjectID)
	require.Equal(t, "c1", ad.CampaignMetaID)
	require.Equal(t, "s1", ad.AdSetMetaID)
	require.Equal(t, "Ad", ad.ObjectName)
}

func TestDailyInsightSkipsRowWithoutIdentifierAtLevel(t *testing.T) {
	// Meta occasionally emits rows with no ID at the requested level for
	// deleted objects. Storing them would violate the unique key.
	row := decodeRow(t, `{"account_id":"123","date_start":"2026-03-11","date_stop":"2026-03-11","spend":"1"}`)
	_, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightAd))
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDailyInsightRejectsUnparseableDate(t *testing.T) {
	row := decodeRow(t, `{"account_id":"123","campaign_id":"c1","date_start":"not-a-date","spend":"1"}`)
	_, _, err := dailyInsightFromRow(row, testRowContext(domain.InsightCampaign))
	require.Error(t, err)
}

func TestDailyInsightKeepsUnknownFieldsInRaw(t *testing.T) {
	// A metric Meta adds after this ships must still be recoverable.
	row := decodeRow(t, `{
		"account_id":"123","campaign_id":"c1","date_start":"2026-03-11","date_stop":"2026-03-11",
		"spend":"1","some_future_metric":"42"
	}`)
	record, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightCampaign))
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, string(record.RawJSON), "some_future_metric")
}

func TestROASMergesWithoutLosingEitherSource(t *testing.T) {
	// purchase_roas and website_purchase_roas both report action_type
	// "purchase"; naive map assignment drops one of them.
	row := decodeRow(t, `{
		"account_id":"123","ad_id":"a1","date_start":"2026-03-11","date_stop":"2026-03-11",
		"purchase_roas":[{"action_type":"purchase","value":"3.5"}],
		"website_purchase_roas":[{"action_type":"purchase","value":"3.5","7d_click":"3.2"}]
	}`)
	record, ok, err := dailyInsightFromRow(row, testRowContext(domain.InsightAd))
	require.NoError(t, err)
	require.True(t, ok)

	var roas map[string]map[string]float64
	require.NoError(t, record.ROAS.Decode(&roas))
	require.InDelta(t, 3.5, roas["purchase"]["value"], 1e-9)
	require.InDelta(t, 3.2, roas["purchase"]["7d_click"], 1e-9)
}
