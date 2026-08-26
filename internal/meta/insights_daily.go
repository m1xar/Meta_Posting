package meta

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AttributionMode selects how Meta attributes conversions for a query.
type AttributionMode struct {
	// Unified mirrors what Ads Manager displays. Preferred, because numbers a
	// buyer can see in Meta's own UI are the ones they will argue about.
	Unified bool
	// Windows is an explicit list such as 1d_view, 7d_click. Mutually
	// exclusive with Unified: Meta rejects a query carrying both.
	Windows []string
}

// Setting renders the mode for storage on each row, so a row always records
// how it was produced.
func (a AttributionMode) Setting() string {
	if a.Unified {
		return "unified"
	}
	if len(a.Windows) > 0 {
		return strings.Join(a.Windows, ",")
	}
	return "account_default"
}

func (a AttributionMode) validate() error {
	if a.Unified && len(a.Windows) > 0 {
		return errors.New(
			"meta: unified attribution and explicit attribution windows are mutually exclusive",
		)
	}
	return nil
}

func (a AttributionMode) apply(query *InsightQuery) {
	if a.Unified {
		unified := true
		query.UseUnifiedAttributionSetting = &unified
		return
	}
	if len(a.Windows) > 0 {
		query.ActionAttributionWindows = a.Windows
	}
}

// DailyInsightRequest asks for one row per object per calendar day over an
// explicit range. Since and Until must already be resolved in the ad
// account's timezone - see AccountRange - because Meta interprets them there.
type DailyInsightRequest struct {
	AccountID   string
	Level       InsightLevel
	Since       string
	Until       string
	Fields      []string
	Attribution AttributionMode
	Limit       int
}

func (r DailyInsightRequest) validate() error {
	if strings.TrimSpace(r.AccountID) == "" {
		return errors.New("meta: daily insights require an ad account ID")
	}
	switch r.Level {
	case InsightLevelAccount, InsightLevelCampaign, InsightLevelAdSet, InsightLevelAd:
	default:
		return fmt.Errorf("meta: unsupported insights level %q", r.Level)
	}
	if _, err := ParseAccountDate(r.Since); err != nil {
		return err
	}
	until, err := ParseAccountDate(r.Until)
	if err != nil {
		return err
	}
	since, _ := ParseAccountDate(r.Since)
	if until.Before(since) {
		return fmt.Errorf("meta: insights range %s..%s ends before it starts", r.Since, r.Until)
	}
	return r.Attribution.validate()
}

func (r DailyInsightRequest) query() (InsightQuery, error) {
	if err := r.validate(); err != nil {
		return InsightQuery{}, err
	}
	fields := r.Fields
	if len(fields) == 0 {
		fields = FieldsForLevel(r.Level)
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 500
	}
	query := InsightQuery{
		Fields:    fields,
		Level:     r.Level,
		TimeRange: &InsightTimeRange{Since: r.Since, Until: r.Until},
		// time_increment=1 is what turns a single aggregate into one row per
		// day, which is what makes a re-fetch of any range idempotent.
		TimeIncrement: 1,
		Limit:         limit,
	}
	r.Attribution.apply(&query)
	return query, nil
}

// FetchDailyInsights returns one InsightRow per (object, day) in the request's
// range, following pagination, and the response metadata of the final page so
// the caller can observe rate-limit pressure.
func (c *Client) FetchDailyInsights(
	ctx context.Context,
	accessToken string,
	request DailyInsightRequest,
) ([]InsightRow, ResponseMeta, error) {
	query, err := request.query()
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	values, err := query.values()
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	return CollectPagesWithMeta[InsightRow](
		ctx,
		c,
		"/"+AdAccountNodeID(request.AccountID)+"/insights",
		accessToken,
		values,
	)
}

// WindowedInsightRequest asks for a single aggregate over [Since, Until] with
// time_increment omitted, so Meta deduplicates reach itself. This is the only
// correct way to obtain reach or frequency for a period longer than one day.
type WindowedInsightRequest struct {
	AccountID   string
	Level       InsightLevel
	Since       string
	Until       string
	Attribution AttributionMode
	Limit       int
}

// FetchWindowedInsights returns deduplicated totals for the whole range.
func (c *Client) FetchWindowedInsights(
	ctx context.Context,
	accessToken string,
	request WindowedInsightRequest,
) ([]InsightRow, ResponseMeta, error) {
	daily := DailyInsightRequest{
		AccountID:   request.AccountID,
		Level:       request.Level,
		Since:       request.Since,
		Until:       request.Until,
		Fields:      WindowedInsightFields,
		Attribution: request.Attribution,
		Limit:       request.Limit,
	}
	query, err := daily.query()
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	// Dropping time_increment is the entire point: it makes Meta compute one
	// deduplicated row for the window instead of one row per day.
	query.TimeIncrement = nil
	values, err := query.values()
	if err != nil {
		return nil, ResponseMeta{}, err
	}
	return CollectPagesWithMeta[InsightRow](
		ctx,
		c,
		"/"+AdAccountNodeID(request.AccountID)+"/insights",
		accessToken,
		values,
	)
}
