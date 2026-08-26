package application

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestContiguousRangesMergesRunsOfMissingDays(t *testing.T) {
	// A week-long outage should cost one request per level, not seven.
	days := []time.Time{
		day(2026, 3, 10), day(2026, 3, 11), day(2026, 3, 12),
		day(2026, 3, 15),
		day(2026, 3, 20), day(2026, 3, 21),
	}
	ranges := contiguousRanges(days, 30)
	require.Len(t, ranges, 3)
	require.Equal(t, [2]time.Time{day(2026, 3, 10), day(2026, 3, 12)}, ranges[0])
	require.Equal(t, [2]time.Time{day(2026, 3, 15), day(2026, 3, 15)}, ranges[1])
	require.Equal(t, [2]time.Time{day(2026, 3, 20), day(2026, 3, 21)}, ranges[2])
}

func TestContiguousRangesSplitsOversizedRuns(t *testing.T) {
	// A long outage must not become one unbounded request.
	var days []time.Time
	for offset := 0; offset < 10; offset++ {
		days = append(days, day(2026, 3, 1).AddDate(0, 0, offset))
	}
	ranges := contiguousRanges(days, 4)
	require.Len(t, ranges, 3)
	require.Equal(t, [2]time.Time{day(2026, 3, 1), day(2026, 3, 4)}, ranges[0])
	require.Equal(t, [2]time.Time{day(2026, 3, 5), day(2026, 3, 8)}, ranges[1])
	require.Equal(t, [2]time.Time{day(2026, 3, 9), day(2026, 3, 10)}, ranges[2])
}

func TestContiguousRangesEdgeCases(t *testing.T) {
	require.Nil(t, contiguousRanges(nil, 30))
	require.Equal(t,
		[][2]time.Time{{day(2026, 3, 1), day(2026, 3, 1)}},
		contiguousRanges([]time.Time{day(2026, 3, 1)}, 30),
	)
	// A zero limit must not loop or produce empty ranges.
	ranges := contiguousRanges([]time.Time{day(2026, 3, 1), day(2026, 3, 2)}, 0)
	require.Len(t, ranges, 2)
}

func TestBackfillChunkDaysShrinkWithRowCount(t *testing.T) {
	// time_increment=1 multiplies rows by days, so the deeper levels take
	// smaller bites to keep a chunk's page count bounded.
	require.Equal(t, 30, backfillChunkDays(domain.InsightAccount))
	require.Equal(t, 30, backfillChunkDays(domain.InsightCampaign))
	require.Equal(t, 14, backfillChunkDays(domain.InsightAdSet))
	require.Equal(t, 7, backfillChunkDays(domain.InsightAd))
}

func TestAttributionModeForSetting(t *testing.T) {
	require.True(t, attributionModeFor("unified").Unified)
	require.True(t, attributionModeFor("").Unified, "an unset value must not silently become account default")

	explicit := attributionModeFor("1d_view,7d_click")
	require.False(t, explicit.Unified)
	require.Equal(t, []string{"1d_view", "7d_click"}, explicit.Windows)

	spaced := attributionModeFor(" 1d_view , 7d_click ")
	require.Equal(t, []string{"1d_view", "7d_click"}, spaced.Windows)

	accountDefault := attributionModeFor("account_default")
	require.False(t, accountDefault.Unified)
	require.Empty(t, accountDefault.Windows)

	// Garbage falls back to unified rather than producing an invalid query.
	require.True(t, attributionModeFor(" , , ").Unified)
}

func TestEffectiveStatusFilterIsOmitted(t *testing.T) {
	// Learned in production: effective_status values are not interchangeable
	// across edges, and Meta fails the whole request with 100/1815001 on one
	// it does not define - so a single wrong value took out every inventory
	// sweep instead of degrading it. The enum also drifts between API
	// versions, so a hardcoded list is an outage waiting for the next
	// upgrade. Omitting the filter avoids both.
	for _, level := range []domain.AdEntityLevel{
		domain.AdEntityCampaign, domain.AdEntityAdSet, domain.AdEntityAd,
	} {
		require.Nil(t, effectiveStatusesFor(level), "level %s", level)
	}
}

func TestAdEntityFromGraphPromotesFieldsAndKeepsRaw(t *testing.T) {
	account := &domain.AdAccount{Model: domain.Model{ID: uuid.New()}, ConnectionID: uuid.New()}
	now := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)

	entity, ok := adEntityFromGraph(map[string]any{
		"id":               "23847",
		"name":             "Spring campaign",
		"status":           "ACTIVE",
		"effective_status": "ACTIVE",
		"objective":        "OUTCOME_SALES",
		"buying_type":      "AUCTION",
		"daily_budget":     "50000",
		"created_time":     "2026-03-01T10:00:00+0000",
		"future_field":     "kept",
	}, domain.AdEntityCampaign, account, now)

	require.True(t, ok)
	require.Equal(t, "23847", entity.MetaObjectID)
	require.Equal(t, "Spring campaign", entity.Name)
	require.Equal(t, "OUTCOME_SALES", entity.Objective)
	// Budgets arrive as strings of minor units.
	require.Equal(t, int64(50000), entity.DailyBudget)
	require.NotNil(t, entity.MetaCreatedTime)
	require.Equal(t, 2026, entity.MetaCreatedTime.Year())
	// A field this service does not model must still be recoverable.
	require.Contains(t, string(entity.RawJSON), "future_field")
}

func TestAdEntityFromGraphSetsParentByLevel(t *testing.T) {
	account := &domain.AdAccount{Model: domain.Model{ID: uuid.New()}, ConnectionID: uuid.New()}
	now := time.Now().UTC()
	record := map[string]any{"id": "a1", "campaign_id": "c1", "adset_id": "s1"}

	campaign, ok := adEntityFromGraph(record, domain.AdEntityCampaign, account, now)
	require.True(t, ok)
	require.Empty(t, campaign.ParentMetaObjectID)

	adset, ok := adEntityFromGraph(record, domain.AdEntityAdSet, account, now)
	require.True(t, ok)
	require.Equal(t, "c1", adset.ParentMetaObjectID)

	ad, ok := adEntityFromGraph(record, domain.AdEntityAd, account, now)
	require.True(t, ok)
	require.Equal(t, "s1", ad.ParentMetaObjectID)
}

func TestAdEntityFromGraphRejectsRecordWithoutID(t *testing.T) {
	account := &domain.AdAccount{Model: domain.Model{ID: uuid.New()}, ConnectionID: uuid.New()}
	_, ok := adEntityFromGraph(map[string]any{"name": "no id"},
		domain.AdEntityCampaign, account, time.Now().UTC())
	require.False(t, ok)
}

func TestGraphInt64AcceptsStringAndNumber(t *testing.T) {
	require.Equal(t, int64(50000), graphInt64(map[string]any{"v": "50000"}, "v"))
	require.Equal(t, int64(50000), graphInt64(map[string]any{"v": float64(50000)}, "v"))
	require.Zero(t, graphInt64(map[string]any{"v": nil}, "v"))
	require.Zero(t, graphInt64(map[string]any{}, "v"))
	require.Zero(t, graphInt64(map[string]any{"v": "not a number"}, "v"))
}

func TestGraphTimeAcceptsMetaLayouts(t *testing.T) {
	// Meta returns +0000 without a colon, which is not RFC3339.
	parsed := graphTime(map[string]any{"created_time": "2026-03-01T10:00:00+0000"}, "created_time")
	require.NotNil(t, parsed)
	require.Equal(t, time.March, parsed.Month())

	rfc := graphTime(map[string]any{"created_time": "2026-03-01T10:00:00Z"}, "created_time")
	require.NotNil(t, rfc)

	// Ad sets carry end_time where campaigns carry stop_time.
	fallback := graphTime(map[string]any{"end_time": "2026-03-05T10:00:00+0000"}, "stop_time", "end_time")
	require.NotNil(t, fallback)

	require.Nil(t, graphTime(map[string]any{"created_time": "nonsense"}, "created_time"))
	require.Nil(t, graphTime(map[string]any{}, "created_time"))
}

func TestTruncateErrorBoundsStoredMessage(t *testing.T) {
	require.Empty(t, truncateError(nil))
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	require.Len(t, truncateError(errorString(string(long))), 1000)
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestMetaDateLayoutRoundTrip(t *testing.T) {
	parsed, err := meta.ParseAccountDate("2026-03-11")
	require.NoError(t, err)
	require.Equal(t, "2026-03-11", parsed.Format(meta.DateLayout))
}
