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

func hasSubID(value, key string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(key)+"=")
}
