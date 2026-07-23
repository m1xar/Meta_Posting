package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type InsightLevel string

const (
	InsightLevelAccount  InsightLevel = "account"
	InsightLevelCampaign InsightLevel = "campaign"
	InsightLevelAdSet    InsightLevel = "adset"
	InsightLevelAd       InsightLevel = "ad"
)

type InsightTimeRange struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

type InsightFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type InsightQuery struct {
	Fields                       []string
	Level                        InsightLevel
	TimeRange                    *InsightTimeRange
	TimeRanges                   []InsightTimeRange
	DatePreset                   string
	TimeIncrement                any
	Filtering                    []InsightFilter
	Breakdowns                   []string
	ActionBreakdowns             []string
	ActionAttributionWindows     []string
	UseAccountAttributionSetting *bool
	UseUnifiedAttributionSetting *bool
	Sort                         []string
	Limit                        int
}

// ActionMetric preserves every attribution-window value in Raw, while exposing
// the fields most commonly used by automated rules.
type ActionMetric struct {
	ActionType          string                     `json:"action_type"`
	Value               string                     `json:"value"`
	OneDayClick         string                     `json:"1d_click,omitempty"`
	OneDayView          string                     `json:"1d_view,omitempty"`
	SevenDayClick       string                     `json:"7d_click,omitempty"`
	SevenDayView        string                     `json:"7d_view,omitempty"`
	TwentyEightDayClick string                     `json:"28d_click,omitempty"`
	TwentyEightDayView  string                     `json:"28d_view,omitempty"`
	Inline              string                     `json:"inline,omitempty"`
	Raw                 map[string]json.RawMessage `json:"-"`
}

func (metric *ActionMetric) UnmarshalJSON(data []byte) error {
	type alias ActionMetric
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*metric = ActionMetric(decoded)
	metric.Raw = raw
	return nil
}

// InsightRow keeps the full original response in Raw, including any metric
// arrays Meta adds after this client ships.
type InsightRow struct {
	AccountID              string `json:"account_id,omitempty"`
	AccountName            string `json:"account_name,omitempty"`
	CampaignID             string `json:"campaign_id,omitempty"`
	CampaignName           string `json:"campaign_name,omitempty"`
	AdSetID                string `json:"adset_id,omitempty"`
	AdSetName              string `json:"adset_name,omitempty"`
	AdID                   string `json:"ad_id,omitempty"`
	AdName                 string `json:"ad_name,omitempty"`
	DateStart              string `json:"date_start,omitempty"`
	DateStop               string `json:"date_stop,omitempty"`
	Spend                  string `json:"spend,omitempty"`
	Impressions            string `json:"impressions,omitempty"`
	Reach                  string `json:"reach,omitempty"`
	Frequency              string `json:"frequency,omitempty"`
	Clicks                 string `json:"clicks,omitempty"`
	UniqueClicks           string `json:"unique_clicks,omitempty"`
	InlineLinkClicks       string `json:"inline_link_clicks,omitempty"`
	UniqueInlineLinkClicks string `json:"unique_inline_link_clicks,omitempty"`
	CTR                    string `json:"ctr,omitempty"`
	UniqueCTR              string `json:"unique_ctr,omitempty"`
	CPC                    string `json:"cpc,omitempty"`
	CPM                    string `json:"cpm,omitempty"`
	CPP                    string `json:"cpp,omitempty"`
	CostPerUniqueClick     string `json:"cost_per_unique_click,omitempty"`
	CostPerInlineLinkClick string `json:"cost_per_inline_link_click,omitempty"`
	QualityRanking         string `json:"quality_ranking,omitempty"`
	EngagementRateRanking  string `json:"engagement_rate_ranking,omitempty"`
	ConversionRateRanking  string `json:"conversion_rate_ranking,omitempty"`

	Actions                     []ActionMetric `json:"actions,omitempty"`
	ActionValues                []ActionMetric `json:"action_values,omitempty"`
	CostPerActionType           []ActionMetric `json:"cost_per_action_type,omitempty"`
	Conversions                 []ActionMetric `json:"conversions,omitempty"`
	ConversionValues            []ActionMetric `json:"conversion_values,omitempty"`
	CostPerConversion           []ActionMetric `json:"cost_per_conversion,omitempty"`
	PurchaseROAS                []ActionMetric `json:"purchase_roas,omitempty"`
	WebsitePurchaseROAS         []ActionMetric `json:"website_purchase_roas,omitempty"`
	MobileAppPurchaseROAS       []ActionMetric `json:"mobile_app_purchase_roas,omitempty"`
	OutboundClicks              []ActionMetric `json:"outbound_clicks,omitempty"`
	OutboundClicksCTR           []ActionMetric `json:"outbound_clicks_ctr,omitempty"`
	CostPerOutboundClick        []ActionMetric `json:"cost_per_outbound_click,omitempty"`
	VideoPlayActions            []ActionMetric `json:"video_play_actions,omitempty"`
	VideoThruPlayWatchedActions []ActionMetric `json:"video_thruplay_watched_actions,omitempty"`
	VideoAvgTimeWatchedActions  []ActionMetric `json:"video_avg_time_watched_actions,omitempty"`

	Raw map[string]json.RawMessage `json:"-"`
}

func (row *InsightRow) UnmarshalJSON(data []byte) error {
	type alias InsightRow
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*row = InsightRow(decoded)
	row.Raw = raw
	return nil
}

var DefaultInsightFields = []string{
	"account_id",
	"account_name",
	"campaign_id",
	"campaign_name",
	"adset_id",
	"adset_name",
	"ad_id",
	"ad_name",
	"date_start",
	"date_stop",
	"spend",
	"impressions",
	"reach",
	"frequency",
	"clicks",
	"unique_clicks",
	"inline_link_clicks",
	"unique_inline_link_clicks",
	"ctr",
	"unique_ctr",
	"cpc",
	"cpm",
	"cpp",
	"cost_per_unique_click",
	"cost_per_inline_link_click",
	"actions",
	"action_values",
	"cost_per_action_type",
	"conversions",
	"conversion_values",
	"cost_per_conversion",
	"purchase_roas",
	"website_purchase_roas",
	"mobile_app_purchase_roas",
	"outbound_clicks",
	"outbound_clicks_ctr",
	"cost_per_outbound_click",
	"video_play_actions",
	"video_thruplay_watched_actions",
	"video_avg_time_watched_actions",
	"quality_ranking",
	"engagement_rate_ranking",
	"conversion_rate_ranking",
}

func (c *Client) FetchInsights(
	ctx context.Context,
	accessToken string,
	entityID string,
	query InsightQuery,
) ([]InsightRow, error) {
	if strings.TrimSpace(entityID) == "" {
		return nil, errors.New("meta: insights entity ID is required")
	}
	values, err := query.values()
	if err != nil {
		return nil, err
	}
	return CollectPages[InsightRow](
		ctx,
		c,
		"/"+strings.TrimPrefix(strings.TrimSpace(entityID), "/")+"/insights",
		accessToken,
		values,
	)
}

func (c *Client) FetchAccountInsights(
	ctx context.Context,
	accessToken string,
	accountID string,
	query InsightQuery,
) ([]InsightRow, error) {
	return c.FetchInsights(ctx, accessToken, AdAccountNodeID(accountID), query)
}

func (query InsightQuery) values() (url.Values, error) {
	values := make(url.Values)
	fields := query.Fields
	if len(fields) == 0 {
		fields = DefaultInsightFields
	}
	values.Set("fields", strings.Join(fields, ","))
	if query.Level == "" {
		query.Level = InsightLevelAd
	}
	switch query.Level {
	case InsightLevelAccount, InsightLevelCampaign, InsightLevelAdSet, InsightLevelAd:
		values.Set("level", string(query.Level))
	default:
		return nil, fmt.Errorf("meta: unsupported insights level %q", query.Level)
	}
	if query.TimeRange != nil && len(query.TimeRanges) > 0 {
		return nil, errors.New("meta: insights time_range and time_ranges are mutually exclusive")
	}
	if query.TimeRange != nil {
		if err := setJSONQuery(values, "time_range", query.TimeRange); err != nil {
			return nil, err
		}
	}
	if len(query.TimeRanges) > 0 {
		if err := setJSONQuery(values, "time_ranges", query.TimeRanges); err != nil {
			return nil, err
		}
	}
	if query.DatePreset != "" {
		values.Set("date_preset", query.DatePreset)
	}
	if query.TimeIncrement != nil {
		switch increment := query.TimeIncrement.(type) {
		case string:
			values.Set("time_increment", increment)
		case int:
			values.Set("time_increment", strconv.Itoa(increment))
		case int64:
			values.Set("time_increment", strconv.FormatInt(increment, 10))
		default:
			if err := setJSONQuery(values, "time_increment", increment); err != nil {
				return nil, err
			}
		}
	}
	if len(query.Filtering) > 0 {
		if err := setJSONQuery(values, "filtering", query.Filtering); err != nil {
			return nil, err
		}
	}
	if len(query.Breakdowns) > 0 {
		values.Set("breakdowns", strings.Join(query.Breakdowns, ","))
	}
	if len(query.ActionBreakdowns) > 0 {
		values.Set("action_breakdowns", strings.Join(query.ActionBreakdowns, ","))
	}
	if len(query.ActionAttributionWindows) > 0 {
		if err := setJSONQuery(values, "action_attribution_windows", query.ActionAttributionWindows); err != nil {
			return nil, err
		}
	}
	if query.UseAccountAttributionSetting != nil {
		values.Set("use_account_attribution_setting", strconv.FormatBool(*query.UseAccountAttributionSetting))
	}
	if query.UseUnifiedAttributionSetting != nil {
		values.Set("use_unified_attribution_setting", strconv.FormatBool(*query.UseUnifiedAttributionSetting))
	}
	if len(query.Sort) > 0 {
		values.Set("sort", strings.Join(query.Sort, ","))
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 500
	}
	values.Set("limit", strconv.Itoa(limit))
	return values, nil
}

func setJSONQuery(values url.Values, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("meta: encode insights %s: %w", key, err)
	}
	values.Set(key, string(encoded))
	return nil
}
