package application

import (
	"errors"
	"fmt"
	"time"

	"github.com/watchers-factory/raze-ads/internal/domain"
)

// NonAdditiveMetrics may never be summed across ad_insights_daily rows.
//
// Each of these is deduplicated by Meta over the window it was queried for.
// reach is a count of distinct people, so two days of reach are not two
// disjoint sets; frequency and cpp are derived from reach and inherit the
// problem; the unique_* family is deduplicated the same way. Adding them
// produces a number that looks plausible, is always too high, and is
// presented to a buyer as fact. Meta_Tracking sums reach and then derives
// frequency from the sum, which is where this list comes from.
//
// The supported way to obtain these over a period is a windowed query with
// time_increment omitted - domain.AdInsightWindowed.
var NonAdditiveMetrics = map[string]struct{}{
	"reach":                     {},
	"frequency":                 {},
	"cpp":                       {},
	"unique_clicks":             {},
	"unique_ctr":                {},
	"unique_inline_link_clicks": {},
	"cost_per_unique_click":     {},
	"unique_outbound_clicks":    {},
	"unique_link_clicks_ctr":    {},
	"unique_actions":            {},
	"estimated_ad_recallers":    {},
	"estimated_ad_recall_rate":  {},
	"full_view_reach":           {},
	"quality_ranking":           {},
	"engagement_rate_ranking":   {},
	"conversion_rate_ranking":   {},
}

// IsAdditive reports whether a metric may be summed across daily rows.
func IsAdditive(metric string) bool {
	_, blocked := NonAdditiveMetrics[metric]
	return !blocked
}

// ErrNonAdditiveMetric is returned when a caller asks to aggregate a metric
// that cannot be aggregated.
var ErrNonAdditiveMetric = errors.New("metric cannot be summed across days")

// Rollup is the aggregate of a set of daily rows.
//
// Reach and Frequency are pointers so that "we cannot know this" is
// representable and serializes as absent, rather than collapsing to a
// confident zero. They are populated only when the rollup covers exactly one
// day, where the stored value is genuinely correct.
type Rollup struct {
	Since            time.Time `json:"since"`
	Until            time.Time `json:"until"`
	Days             int       `json:"days"`
	Rows             int       `json:"rows"`
	Currency         string    `json:"currency,omitempty"`
	Spend            float64   `json:"spend"`
	Impressions      int64     `json:"impressions"`
	Clicks           int64     `json:"clicks"`
	InlineLinkClicks int64     `json:"inline_link_clicks"`

	// Nil whenever the window spans more than one day. Use
	// domain.AdInsightWindowed for a real multi-day figure.
	Reach     *int64   `json:"reach,omitempty"`
	Frequency *float64 `json:"frequency,omitempty"`

	// Derived from the summed counters, never averaged from the per-day
	// ratios: the mean of daily CTRs is not the CTR of the period.
	CTR float64 `json:"ctr"`
	CPC float64 `json:"cpc"`
	CPM float64 `json:"cpm"`

	Actions      map[string]float64 `json:"actions,omitempty"`
	ActionValues map[string]float64 `json:"action_values,omitempty"`

	// NonAdditiveOmitted names the metrics deliberately left out, so an API
	// consumer can say why a field is missing instead of guessing.
	NonAdditiveOmitted []string `json:"non_additive_omitted,omitempty"`
}

// SumDaily aggregates daily rows into a Rollup.
func SumDaily(rows []domain.AdInsightDaily) Rollup {
	rollup := Rollup{Rows: len(rows)}
	if len(rows) == 0 {
		return rollup
	}

	days := map[string]struct{}{}
	actions := map[string]float64{}
	actionValues := map[string]float64{}

	for index := range rows {
		row := &rows[index]
		if index == 0 || row.Date.Before(rollup.Since) {
			rollup.Since = row.Date
		}
		if index == 0 || row.Date.After(rollup.Until) {
			rollup.Until = row.Date
		}
		days[row.Date.Format(time.DateOnly)] = struct{}{}
		if rollup.Currency == "" {
			rollup.Currency = row.Currency
		}
		rollup.Spend += row.Spend
		rollup.Impressions += row.Impressions
		rollup.Clicks += row.Clicks
		rollup.InlineLinkClicks += row.InlineLinkClicks

		accumulateMetricMap(actions, row.Actions)
		accumulateMetricMap(actionValues, row.ActionValues)
	}
	rollup.Days = len(days)

	if rollup.Impressions > 0 {
		rollup.CTR = float64(rollup.Clicks) / float64(rollup.Impressions) * 100
		rollup.CPM = rollup.Spend / float64(rollup.Impressions) * 1000
	}
	if rollup.Clicks > 0 {
		rollup.CPC = rollup.Spend / float64(rollup.Clicks)
	}

	// A single-day rollup may report reach: the stored value is exactly the
	// deduplicated figure for that day. Any wider window may not.
	if rollup.Days == 1 && len(rows) == 1 {
		reach := rows[0].Reach
		frequency := rows[0].Frequency
		rollup.Reach = &reach
		rollup.Frequency = &frequency
	} else {
		rollup.NonAdditiveOmitted = []string{"reach", "frequency", "cpp"}
	}

	if len(actions) > 0 {
		rollup.Actions = actions
	}
	if len(actionValues) > 0 {
		rollup.ActionValues = actionValues
	}
	return rollup
}

// SumMetric adds one named metric across rows. It refuses metrics that cannot
// be summed rather than returning a wrong number.
func SumMetric(rows []domain.AdInsightDaily, metric string) (float64, error) {
	if !IsAdditive(metric) {
		return 0, fmt.Errorf("%q: %w", metric, ErrNonAdditiveMetric)
	}
	var total float64
	for index := range rows {
		flat, err := decodeMetricMap(rows[index].Metrics)
		if err != nil {
			return 0, err
		}
		total += flat[metric]
	}
	return total, nil
}

func accumulateMetricMap(target map[string]float64, encoded domain.JSON) {
	for key, value := range decodeActionTotals(encoded) {
		target[key] += value
	}
}

// decodeActionTotals reads the stored per-action-type map and returns the
// headline value of each. Attribution-window breakdowns stay in the stored
// JSON; only the value Meta itself reports as the total is aggregated.
func decodeActionTotals(encoded domain.JSON) map[string]float64 {
	var decoded map[string]map[string]float64
	if len(encoded) == 0 || encoded.Decode(&decoded) != nil {
		return nil
	}
	totals := make(map[string]float64, len(decoded))
	for actionType, windows := range decoded {
		if value, ok := windows["value"]; ok {
			totals[actionType] = value
		}
	}
	return totals
}
