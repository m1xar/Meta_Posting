package meta

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// CopyResult is what a deep copy returns: the new campaign and, for a deep
// copy, the ad set and ad IDs Meta created underneath it.
type CopyResult struct {
	CopiedCampaignID string   `json:"copied_campaign_id"`
	CopiedAdSetID    string   `json:"copied_adset_id"`
	AdObjectIDs      []AdCopy `json:"ad_object_ids"`
}

type AdCopy struct {
	SourceID string `json:"source_id"`
	CopiedID string `json:"copied_id"`
}

// CopyCampaign duplicates a whole campaign inside the same ad account via
// Meta's native `/copies` edge. deep_copy pulls the ad sets, ads and their
// creatives along, so the copy runs the exact same thing as the source
// without this service re-uploading anything or re-publishing from a Page.
//
// The copy is created paused: a duplicate that started spending the instant
// it was made would be a launch nobody confirmed.
func (c *Client) CopyCampaign(ctx context.Context, accessToken, campaignID string, deepCopy bool) (CopyResult, error) {
	campaignID = strings.TrimSpace(campaignID)
	if campaignID == "" {
		return CopyResult{}, errors.New("meta: campaign ID is required")
	}
	payload := map[string]any{
		"deep_copy":     deepCopy,
		"status_option": "PAUSED",
	}
	var result CopyResult
	if err := c.PostJSON(
		ctx,
		"/"+campaignID+"/copies",
		accessToken,
		url.Values{},
		payload,
		&result,
	); err != nil {
		return CopyResult{}, err
	}
	if result.CopiedCampaignID == "" {
		return CopyResult{}, errors.New("meta: campaign copy returned no id")
	}
	return result, nil
}
