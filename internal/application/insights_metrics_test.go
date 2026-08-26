package application

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

func day(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC)
}

func dailyRow(date time.Time, spend float64, impressions, clicks, reach int64) domain.AdInsightDaily {
	return domain.AdInsightDaily{
		Date:        date,
		Currency:    "USD",
		Spend:       spend,
		Impressions: impressions,
		Clicks:      clicks,
		Reach:       reach,
		Frequency:   1.5,
		Actions:     domain.MustJSON(map[string]map[string]float64{"purchase": {"value": 2}}),
		Metrics:     domain.MustJSON(map[string]float64{"spend": spend, "reach": float64(reach)}),
	}
}

func TestSumDailyRefusesToSumReachAcrossDays(t *testing.T) {
	// This is the whole point of the guard. Meta_Tracking sums reach and then
	// derives frequency from the sum; both numbers are wrong by construction,
	// and they are presented to a buyer as fact.
	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 10), 100, 10000, 200, 7000),
		dailyRow(day(2026, 3, 11), 150, 12000, 240, 8000),
	}
	rollup := SumDaily(rows)

	require.Equal(t, 2, rollup.Days)
	require.InDelta(t, 250, rollup.Spend, 1e-9)
	require.Equal(t, int64(22000), rollup.Impressions)
	require.Equal(t, int64(440), rollup.Clicks)

	// Absent, not zero: a zero would be read as "nobody was reached".
	require.Nil(t, rollup.Reach)
	require.Nil(t, rollup.Frequency)
	require.Contains(t, rollup.NonAdditiveOmitted, "reach")
	require.Contains(t, rollup.NonAdditiveOmitted, "frequency")
}

func TestSumDailyReportsReachForASingleDay(t *testing.T) {
	// One day's stored reach is exactly Meta's deduplicated figure, so it can
	// be reported as-is.
	rollup := SumDaily([]domain.AdInsightDaily{dailyRow(day(2026, 3, 10), 100, 10000, 200, 7000)})

	require.Equal(t, 1, rollup.Days)
	require.NotNil(t, rollup.Reach)
	require.Equal(t, int64(7000), *rollup.Reach)
	require.NotNil(t, rollup.Frequency)
	require.InDelta(t, 1.5, *rollup.Frequency, 1e-9)
	require.Empty(t, rollup.NonAdditiveOmitted)
}

func TestSumDailyDerivesRatiosFromTotalsNotAverages(t *testing.T) {
	// The mean of daily CTRs is not the CTR of the period.
	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 10), 100, 10000, 100, 0), // 1.0% CTR
		dailyRow(day(2026, 3, 11), 100, 1000, 100, 0),  // 10.0% CTR
	}
	rollup := SumDaily(rows)

	// 200 clicks / 11000 impressions = 1.818%, not the 5.5% mean.
	require.InDelta(t, 1.8181818, rollup.CTR, 1e-6)
	require.InDelta(t, 200.0/11000.0*1000, rollup.CPM, 1e-9)
	require.InDelta(t, 1.0, rollup.CPC, 1e-9)
}

func TestSumDailyAggregatesActionTotals(t *testing.T) {
	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 10), 100, 1000, 10, 500),
		dailyRow(day(2026, 3, 11), 100, 1000, 10, 500),
	}
	rollup := SumDaily(rows)
	require.InDelta(t, 4, rollup.Actions["purchase"], 1e-9)
}

func TestSumDailyBoundsAndEmptyInput(t *testing.T) {
	rollup := SumDaily(nil)
	require.Zero(t, rollup.Rows)
	require.Zero(t, rollup.Days)
	require.Nil(t, rollup.Reach)

	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 12), 1, 1, 1, 1),
		dailyRow(day(2026, 3, 10), 1, 1, 1, 1),
		dailyRow(day(2026, 3, 11), 1, 1, 1, 1),
	}
	rollup = SumDaily(rows)
	require.Equal(t, day(2026, 3, 10), rollup.Since)
	require.Equal(t, day(2026, 3, 12), rollup.Until)
	require.Equal(t, 3, rollup.Days)
	require.Equal(t, "USD", rollup.Currency)
}

func TestSumDailyCountsDistinctDaysNotRows(t *testing.T) {
	// At campaign level many objects report on the same day.
	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 10), 10, 100, 1, 50),
		dailyRow(day(2026, 3, 10), 20, 200, 2, 60),
		dailyRow(day(2026, 3, 11), 30, 300, 3, 70),
	}
	rollup := SumDaily(rows)
	require.Equal(t, 3, rollup.Rows)
	require.Equal(t, 2, rollup.Days)
	// Two rows on one day is still multi-object, so reach stays absent.
	require.Nil(t, rollup.Reach)
}

func TestIsAdditive(t *testing.T) {
	for _, metric := range []string{"spend", "impressions", "clicks", "actions.purchase"} {
		require.True(t, IsAdditive(metric), metric)
	}
	for _, metric := range []string{
		"reach", "frequency", "cpp", "unique_clicks", "unique_ctr",
		"cost_per_unique_click", "quality_ranking",
	} {
		require.False(t, IsAdditive(metric), metric)
	}
}

func TestSumMetricRefusesNonAdditiveMetrics(t *testing.T) {
	rows := []domain.AdInsightDaily{
		dailyRow(day(2026, 3, 10), 100, 1000, 10, 500),
		dailyRow(day(2026, 3, 11), 150, 1000, 10, 600),
	}

	total, err := SumMetric(rows, "spend")
	require.NoError(t, err)
	require.InDelta(t, 250, total, 1e-9)

	_, err = SumMetric(rows, "reach")
	require.ErrorIs(t, err, ErrNonAdditiveMetric)
	require.ErrorContains(t, err, "reach")
}
