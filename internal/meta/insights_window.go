package meta

import (
	"fmt"
	"time"
)

// DateLayout is the format Meta accepts in time_range and returns in
// date_start / date_stop.
const DateLayout = "2006-01-02"

// AccountLocation resolves an ad account's IANA timezone. The second return
// value reports whether the name was usable: callers are expected to surface a
// false, not swallow it. Meta evaluates date_preset and time_range in the ad
// account's own timezone, so silently substituting UTC shifts a day's spend
// across the boundary for every account that is not on UTC - and does so
// invisibly, which is how this class of bug survives for months.
func AccountLocation(timezoneName string) (*time.Location, bool) {
	if timezoneName == "" {
		return time.UTC, false
	}
	location, err := time.LoadLocation(timezoneName)
	if err != nil {
		return time.UTC, false
	}
	return location, true
}

// AccountDate renders the calendar date at instant `at` as observed in the ad
// account's timezone.
func AccountDate(at time.Time, timezoneName string) string {
	location, _ := AccountLocation(timezoneName)
	return at.In(location).Format(DateLayout)
}

// AccountDay truncates `at` to midnight in the account's timezone and returns
// it as a UTC-anchored date, which is how a date column round-trips without
// acquiring an offset.
func AccountDay(at time.Time, timezoneName string) time.Time {
	location, _ := AccountLocation(timezoneName)
	local := at.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// AccountToday is the account's current calendar date.
func AccountToday(now time.Time, timezoneName string) string {
	return AccountDate(now, timezoneName)
}

// AccountRange builds an inclusive time_range ending `untilDaysAgo` days
// before the account's today and starting `sinceDaysAgo` days before it. Both
// offsets count backwards, so AccountRange(now, tz, 1, 0) is "yesterday and
// today" - the standard incremental poll.
func AccountRange(now time.Time, timezoneName string, sinceDaysAgo, untilDaysAgo int) (InsightTimeRange, error) {
	if sinceDaysAgo < untilDaysAgo {
		return InsightTimeRange{}, fmt.Errorf(
			"meta: insights range starts %d days ago but ends %d days ago",
			sinceDaysAgo, untilDaysAgo,
		)
	}
	today := AccountDay(now, timezoneName)
	return InsightTimeRange{
		Since: today.AddDate(0, 0, -sinceDaysAgo).Format(DateLayout),
		Until: today.AddDate(0, 0, -untilDaysAgo).Format(DateLayout),
	}, nil
}

// ParseAccountDate reads a Meta date_start / date_stop value as a UTC-anchored
// date. Meta already rendered it in the account's timezone, so no conversion
// applies here - doing one would shift the day a second time.
func ParseAccountDate(value string) (time.Time, error) {
	parsed, err := time.ParseInLocation(DateLayout, value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("meta: parse insights date %q: %w", value, err)
	}
	return parsed, nil
}

// DaysBetween counts inclusive days in [since, until].
func DaysBetween(since, until time.Time) int {
	if until.Before(since) {
		return 0
	}
	return int(until.Sub(since).Hours()/24) + 1
}

// EachDay yields every date in the inclusive range [since, until].
func EachDay(since, until time.Time) []time.Time {
	var days []time.Time
	for day := since; !day.After(until); day = day.AddDate(0, 0, 1) {
		days = append(days, day)
	}
	return days
}
