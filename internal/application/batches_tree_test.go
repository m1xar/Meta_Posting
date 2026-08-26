package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestCampaignTreeOverrideMarkerAndMediaPointer(t *testing.T) {
	t.Parallel()
	base := validHierarchy()
	tree := meta.CampaignTreeSpec{
		Campaign: base.Campaign,
		AdSets: []meta.AdSetTreeSpec{{
			AdSet: base.AdSet,
			Ads: []meta.AdTreeSpec{{
				Creative: base.Creative,
				Ad:       base.Ad,
			}},
		}},
	}
	override := json.RawMessage(`{
		"campaign":{"daily_budget":2500},
		"ad_sets":[{"ad_set":{"name":"Override set","targeting":{"geo_locations":{"countries":["PK"]}}},"ads":[{"creative":{"name":"Override creative","object_story_spec":{"page_id":"page","link_data":{"link":"https://example.com"}}},"ad":{"name":"Override ad"}}]}]
	}`)
	merged, err := specForAccount(tree, override)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Campaign.DailyBudget != 2500 ||
		merged.AdSets[0].AdSet.Name != "Override set" ||
		merged.AdSets[0].Ads[0].Creative.Name != "Override creative" {
		t.Fatalf("merged tree = %#v", merged)
	}
	if err := setSpecJSONPointer(
		&merged,
		"/ad_sets/0/ads/0/creative/object_story_spec/link_data/image_hash",
		"hash-1",
	); err != nil {
		t.Fatal(err)
	}
	if merged.AdSets[0].Ads[0].Creative.ObjectStorySpec.LinkData.ImageHash != "hash-1" {
		t.Fatalf("image hash = %q", merged.AdSets[0].Ads[0].Creative.ObjectStorySpec.LinkData.ImageHash)
	}

	tagCampaignTree(&merged, uuid.MustParse("2ee8ded6-ddbf-46f8-9e73-efb6f6c85ec8"))
	for _, name := range []string{
		merged.Campaign.Name,
		merged.AdSets[0].AdSet.Name,
		merged.AdSets[0].Ads[0].Creative.Name,
		merged.AdSets[0].Ads[0].Ad.Name,
	} {
		if !strings.Contains(name, "[RP:") {
			t.Fatalf("untagged name %q", name)
		}
	}
}
