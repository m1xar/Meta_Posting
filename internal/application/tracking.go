package application

import "strings"

// Tracking macros the guard relies on: sub_id_7 carries the Facebook campaign
// id (the reliable match key) and sub_id_3 the campaign name (the fallback).
const (
	trackingCampaignIDMacro   = "sub_id_7={{campaign.id}}"
	trackingCampaignNameMacro = "sub_id_3={{campaign.name}}"
)

// ensureTrackingTags guarantees the campaign-id and campaign-name macros are
// present in a creative's url_tags, merging with whatever the buyer supplied
// so a forgotten macro cannot silently break tracking. Meta appends url_tags
// to every click URL and resolves the macros, so once these are present the
// tracker receives them as long as the click passes through it.
func ensureTrackingTags(urlTags string) string {
	urlTags = strings.TrimSpace(urlTags)
	parts := make([]string, 0, 4)
	if urlTags != "" {
		parts = append(parts, urlTags)
	}
	if !hasSubID(urlTags, "sub_id_7") {
		parts = append(parts, trackingCampaignIDMacro)
	}
	if !hasSubID(urlTags, "sub_id_3") {
		parts = append(parts, trackingCampaignNameMacro)
	}
	return strings.Join(parts, "&")
}

// trackingLinkPresent reports whether the click will carry the campaign id to
// the tracker: either the destination link itself is tagged, or the url_tags
// are. Guard checkpoints that read tracker metrics require this, otherwise the
// tracker returns zero and the guard would pause a campaign that is actually
// fine.
func trackingLinkPresent(link, urlTags string) bool {
	return hasSubID(link, "sub_id_7") || hasSubID(urlTags, "sub_id_7")
}

func hasSubID(value, key string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(key)+"=")
}

// checkpointsUseTracker reports whether any checkpoint enforces a Keitaro
// minimum (leads, sales, revenue, or tracker clicks).
func checkpointsUseTracker(checkpoints []GuardCheckpoint) bool {
	for _, checkpoint := range checkpoints {
		if checkpoint.MinTrackerClicks > 0 || checkpoint.MinTrackerLeads > 0 ||
			checkpoint.MinTrackerSales > 0 || checkpoint.MinTrackerRevenue > 0 {
			return true
		}
	}
	return false
}
