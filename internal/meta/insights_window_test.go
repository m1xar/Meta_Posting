package meta

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountLocationReportsUnusableTimezone(t *testing.T) {
	location, ok := AccountLocation("America/New_York")
	require.True(t, ok)
	require.Equal(t, "America/New_York", location.String())

	// The failure must be visible. A caller that cannot tell UTC-by-fallback
	// from UTC-by-configuration will silently mis-date every row.
	location, ok = AccountLocation("Not/AZone")
	require.False(t, ok)
	require.Equal(t, time.UTC, location)

	location, ok = AccountLocation("")
	require.False(t, ok)
	require.Equal(t, time.UTC, location)
}

func TestAccountDateCrossesTheDayBoundaryWithTheAccount(t *testing.T) {
	// 03:30 UTC on 12 March is still 11 March in Los Angeles. An account
	// there has not rolled over yet, so its spend belongs to the 11th.
	at := time.Date(2026, 3, 12, 3, 30, 0, 0, time.UTC)

	require.Equal(t, "2026-03-12", AccountDate(at, "UTC"))
	require.Equal(t, "2026-03-11", AccountDate(at, "America/Los_Angeles"))
	// And an account far enough east has already started the 12th.
	require.Equal(t, "2026-03-12", AccountDate(at, "Pacific/Auckland"))
}

func TestAccountDateAtExtremeOffsets(t *testing.T) {
	at := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	// UTC+13 during southern winter... Auckland is UTC+12 in June.
	require.Equal(t, "2026-07-01", AccountDate(at, "Pacific/Auckland"))
	// UTC-11.
	require.Equal(t, "2026-06-30", AccountDate(at, "Pacific/Niue"))

	midnightish := time.Date(2026, 6, 30, 23, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-06-30", AccountDate(midnightish, "UTC"))
	require.Equal(t, "2026-06-30", AccountDate(midnightish, "Pacific/Niue"))
	require.Equal(t, "2026-07-01", AccountDate(midnightish, "Pacific/Auckland"))
}

func TestAccountDateAcrossDSTTransition(t *testing.T) {
	// US DST begins 08 March 2026 at 02:00 local. 08:30 UTC is 03:30 EDT,
	// i.e. after the skipped hour, and still the 8th.
	at := time.Date(2026, 3, 8, 8, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-03-08", AccountDate(at, "America/New_York"))

	// 04:30 UTC the same day is 23:30 EST on the 7th, before the transition.
	before := time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-03-07", AccountDate(before, "America/New_York"))
}

func TestAccountDayIsUTCAnchored(t *testing.T) {
	at := time.Date(2026, 3, 12, 3, 30, 0, 0, time.UTC)
	day := AccountDay(at, "America/Los_Angeles")
	require.Equal(t, time.UTC, day.Location())
	require.Equal(t, 2026, day.Year())
	require.Equal(t, time.March, day.Month())
	require.Equal(t, 11, day.Day())
	require.Zero(t, day.Hour())
}

func TestAccountRange(t *testing.T) {
	now := time.Date(2026, 3, 12, 3, 30, 0, 0, time.UTC)

	window, err := AccountRange(now, "UTC", 1, 0)
	require.NoError(t, err)
	require.Equal(t, InsightTimeRange{Since: "2026-03-11", Until: "2026-03-12"}, window)

	lookback, err := AccountRange(now, "UTC", 28, 0)
	require.NoError(t, err)
	require.Equal(t, InsightTimeRange{Since: "2026-02-12", Until: "2026-03-12"}, lookback)

	// The account's own today drives the range.
	shifted, err := AccountRange(now, "America/Los_Angeles", 1, 0)
	require.NoError(t, err)
	require.Equal(t, InsightTimeRange{Since: "2026-03-10", Until: "2026-03-11"}, shifted)

	_, err = AccountRange(now, "UTC", 0, 5)
	require.Error(t, err)
}

func TestDaysBetweenAndEachDay(t *testing.T) {
	since := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)

	require.Equal(t, 3, DaysBetween(since, until))
	require.Equal(t, 1, DaysBetween(since, since))
	require.Equal(t, 0, DaysBetween(until, since))

	days := EachDay(since, until)
	require.Len(t, days, 3)
	require.Equal(t, since, days[0])
	require.Equal(t, until, days[2])
	require.Empty(t, EachDay(until, since))
}

func TestParseAccountDateDoesNotShiftTheDay(t *testing.T) {
	// Meta already rendered this in the account's timezone. Converting again
	// would move it a second time.
	parsed, err := ParseAccountDate("2026-03-11")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 11, 0, 0, 0, 0, time.UTC), parsed)

	_, err = ParseAccountDate("11/03/2026")
	require.Error(t, err)
}
