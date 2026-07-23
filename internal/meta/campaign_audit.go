package meta

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

type AdAccountCampaignAudit struct {
	AccountID string           `json:"account_id"`
	Campaigns []map[string]any `json:"campaigns"`
	AdSets    []map[string]any `json:"ad_sets"`
	Ads       []map[string]any `json:"ads"`
}

func (c *Client) AuditAdAccount(
	ctx context.Context,
	accessToken string,
	accountID string,
	effectiveStatuses []string,
	limit int,
) (AdAccountCampaignAudit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	accountID = strings.TrimPrefix(strings.TrimSpace(accountID), "act_")
	baseQuery := url.Values{
		"limit": {strconv.Itoa(limit)},
	}
	if len(effectiveStatuses) > 0 {
		encoded, err := json.Marshal(effectiveStatuses)
		if err != nil {
			return AdAccountCampaignAudit{}, err
		}
		baseQuery.Set("effective_status", string(encoded))
	}

	campaignQuery := cloneValues(baseQuery)
	campaignQuery.Set("fields", strings.Join([]string{
		"id", "name", "account_id", "objective", "buying_type",
		"status", "configured_status", "effective_status",
		"special_ad_categories", "special_ad_category_country",
		"daily_budget", "lifetime_budget", "budget_remaining",
		"bid_strategy", "spend_cap", "start_time", "stop_time",
		"created_time", "updated_time", "issues_info",
	}, ","))
	campaigns, err := CollectPages[map[string]any](
		ctx, c, "act_"+accountID+"/campaigns", accessToken, campaignQuery,
	)
	if err != nil {
		return AdAccountCampaignAudit{}, err
	}

	adSetQuery := cloneValues(baseQuery)
	adSetQuery.Set("fields", strings.Join([]string{
		"id", "name", "account_id", "campaign_id",
		"status", "configured_status", "effective_status",
		"billing_event", "optimization_goal", "destination_type",
		"daily_budget", "lifetime_budget", "budget_remaining",
		"bid_amount", "bid_strategy", "bid_constraints",
		"attribution_spec", "promoted_object", "targeting",
		"start_time", "end_time", "created_time", "updated_time",
		"learning_stage_info", "issues_info",
	}, ","))
	adSets, err := CollectPages[map[string]any](
		ctx, c, "act_"+accountID+"/adsets", accessToken, adSetQuery,
	)
	if err != nil {
		return AdAccountCampaignAudit{}, err
	}

	adQuery := cloneValues(baseQuery)
	adQuery.Set("fields", strings.Join([]string{
		"id", "name", "account_id", "campaign_id", "adset_id",
		"status", "configured_status", "effective_status",
		"bid_amount", "conversion_domain", "tracking_specs",
		"created_time", "updated_time", "issues_info",
		"creative{id,name,object_story_id,effective_object_story_id,object_type,status,actor_id,instagram_actor_id,object_story_spec,asset_feed_spec,url_tags,thumbnail_url}",
	}, ","))
	ads, err := CollectPages[map[string]any](
		ctx, c, "act_"+accountID+"/ads", accessToken, adQuery,
	)
	if err != nil {
		return AdAccountCampaignAudit{}, err
	}

	return AdAccountCampaignAudit{
		AccountID: accountID,
		Campaigns: campaigns,
		AdSets:    adSets,
		Ads:       ads,
	}, nil
}
