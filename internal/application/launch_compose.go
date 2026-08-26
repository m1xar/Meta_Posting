package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/watchers-factory/raze-ads/internal/meta"
)

// LaunchForm is the launcher's field-by-field input.
//
// It exists so the UI does not have to hand-assemble Meta's nested
// specification. The one thing it does not try to replace is targeting: that
// tree is deep, account-specific and easy to get subtly wrong, so a launch
// inherits it from a proven ad set instead of asking anyone to rebuild it.
type LaunchForm struct {
	// SourceAdSet is an existing ad set whose targeting, optimization goal,
	// billing event, promoted object and attribution are inherited.
	SourceAdSet json.RawMessage `json:"source_ad_set,omitempty"`

	Campaign LaunchCampaignForm `json:"campaign"`
	AdSet    LaunchAdSetForm    `json:"ad_set"`
	Creative LaunchCreativeForm `json:"creative"`
	AdName   string             `json:"ad_name,omitempty"`
}

type LaunchCampaignForm struct {
	Name string `json:"name"`
	// Objective must be one of Meta's ODAX objectives. Left empty it is
	// inherited from the source ad set's campaign when known.
	Objective           string   `json:"objective,omitempty"`
	SpecialAdCategories []string `json:"special_ad_categories,omitempty"`
	SpecialAdCountry    string   `json:"special_ad_category_country,omitempty"`
	BidStrategy         string   `json:"bid_strategy,omitempty"`
	// SpendCap is in whole currency units, as typed.
	SpendCap float64 `json:"spend_cap,omitempty"`
}

type LaunchAdSetForm struct {
	Name string `json:"name"`
	// DailyBudget and LifetimeBudget are in whole currency units. Exactly one
	// should be set; Meta rejects both.
	DailyBudget    float64 `json:"daily_budget,omitempty"`
	LifetimeBudget float64 `json:"lifetime_budget,omitempty"`
	BidAmount      float64 `json:"bid_amount,omitempty"`
	StartTime      string  `json:"start_time,omitempty"`
	EndTime        string  `json:"end_time,omitempty"`
	// Overrides for what would otherwise be inherited.
	OptimizationGoal string `json:"optimization_goal,omitempty"`
	BillingEvent     string `json:"billing_event,omitempty"`
}

type LaunchCreativeForm struct {
	Name             string `json:"name,omitempty"`
	PageID           string `json:"page_id"`
	InstagramActorID string `json:"instagram_actor_id,omitempty"`
	Link             string `json:"link"`
	Message          string `json:"message,omitempty"`
	Headline         string `json:"headline,omitempty"`
	Description      string `json:"description,omitempty"`
	CallToActionType string `json:"call_to_action_type,omitempty"`
	// One of these identifies the media. ImageHash and VideoID refer to
	// something already uploaded to the ad account.
	ImageHash string `json:"image_hash,omitempty"`
	VideoID   string `json:"video_id,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
	URLTags   string `json:"url_tags,omitempty"`
	// UseVideo builds a video creative even when VideoID is still empty,
	// which is the case when the video arrives through a media binding: the
	// publisher uploads it per ad account and writes each account's own video
	// ID into the spec. Without this the binding has no video_data to write
	// into and the launch fails after the campaign already exists.
	UseVideo bool `json:"use_video,omitempty"`
	// ObjectStoryID reuses an existing published post as the creative,
	// "<page_id>_<post_id>". When set, the creative is that post as-is and no
	// new object_story_spec is built - which also means no page-publishing
	// permission is exercised, so a post that already runs can be relaunched
	// without page write access. All other creative fields are ignored.
	ObjectStoryID string `json:"object_story_id,omitempty"`
}

// minorUnits converts a typed amount to the integer minor units Meta expects.
// Rounding rather than truncating: 10.99 must not become 1098.
func minorUnits(amount float64) int64 {
	if amount <= 0 {
		return 0
	}
	return int64(amount*100 + 0.5)
}

// Compose renders the form as the publish hierarchy.
//
// Inheritance is explicit and one-directional: anything the form states wins,
// anything it leaves empty falls back to the source ad set. That ordering is
// what makes "copy this ad set but spend less" a one-field change.
func (f LaunchForm) Compose() (meta.HierarchySpec, error) {
	if err := f.validate(); err != nil {
		return meta.HierarchySpec{}, err
	}

	var source map[string]any
	if len(f.SourceAdSet) > 0 {
		if err := json.Unmarshal(f.SourceAdSet, &source); err != nil {
			return meta.HierarchySpec{}, invalid("source_ad_set", "is not valid JSON")
		}
	}

	categories := make([]meta.SpecialAdCategory, 0, len(f.Campaign.SpecialAdCategories))
	for _, category := range f.Campaign.SpecialAdCategories {
		categories = append(categories, meta.SpecialAdCategory(category))
	}
	campaign := meta.CampaignSpec{
		Name:      strings.TrimSpace(f.Campaign.Name),
		Objective: meta.Objective(f.Campaign.Objective),
		// Meta requires special_ad_categories on every campaign create.
		// Leaving this empty is correct: meta.campaignPayload substitutes
		// ["NONE"] at request time, and injecting an empty array here would
		// suppress that default and send [] instead.
		SpecialAdCategories: categories,
		BidStrategy:         meta.BidStrategy(f.Campaign.BidStrategy),
	}
	if country := strings.TrimSpace(f.Campaign.SpecialAdCountry); country != "" {
		campaign.SpecialAdCategoryCountry = []string{country}
	}
	if cap := minorUnits(f.Campaign.SpendCap); cap > 0 {
		campaign.SpendCap = cap
	}
	// Meta rejects a campaign that carries no budget of its own unless this
	// is stated explicitly - "You must specify True or False in the field
	// is_adset_budget_sharing_enabled if you are not using campaign budget".
	// The launcher always budgets at ad set level, so the answer is always
	// false; leaving it unset fails validation with a message that does not
	// mention the launcher's own fields at all.
	sharing := false
	campaign.IsAdSetBudgetSharingEnabled = &sharing

	// The bid strategy has to live wherever the budget lives. This launcher
	// budgets at ad set level, and Meta refuses to set a strategy on a
	// campaign that carries no budget of its own - "This campaign doesn't
	// have a budget. Add a budget to edit the bid strategy." So it is set on
	// the ad set below, and deliberately left off the campaign.
	campaign.BidStrategy = ""

	adSet := meta.AdSetSpec{
		Name: strings.TrimSpace(f.AdSet.Name),
		OptimizationGoal: meta.OptimizationGoal(
			firstNonEmpty(f.AdSet.OptimizationGoal, stringField(source, "optimization_goal"))),
		BillingEvent: meta.BillingEvent(
			firstNonEmpty(f.AdSet.BillingEvent, stringField(source, "billing_event"))),
		StartTime: f.AdSet.StartTime,
		EndTime:   f.AdSet.EndTime,
	}
	if budget := minorUnits(f.AdSet.DailyBudget); budget > 0 {
		adSet.DailyBudget = budget
	}
	if budget := minorUnits(f.AdSet.LifetimeBudget); budget > 0 {
		adSet.LifetimeBudget = budget
	}
	if bid := minorUnits(f.AdSet.BidAmount); bid > 0 {
		adSet.BidAmount = bid
	}
	// Unset does not mean "no strategy": Meta falls back to the ad account's
	// default, and an account configured for a bid cap then rejects the ad
	// set for carrying no bid amount. Highest volume needs none, so an
	// unspecified launch stays launchable.
	adSet.BidStrategy = meta.BidStrategy(f.Campaign.BidStrategy)
	if adSet.BidStrategy == "" {
		adSet.BidStrategy = meta.BidStrategyLowestCostWithoutCap
	}

	// Targeting, the promoted object and attribution come from the source ad
	// set verbatim. They are the parts most likely to be wrong when retyped,
	// and the parts a buyer has already proven.
	inherited := meta.RawFields{}
	for _, key := range []string{"targeting", "promoted_object", "attribution_spec", "destination_type"} {
		if value, ok := source[key]; ok && value != nil {
			inherited[key] = value
		}
	}
	if len(inherited) > 0 {
		adSet.Raw = inherited
	}

	creative, err := f.composeCreative()
	if err != nil {
		return meta.HierarchySpec{}, err
	}

	return meta.HierarchySpec{
		Campaign: campaign,
		AdSet:    adSet,
		Creative: creative,
		Ad:       meta.AdSpec{Name: firstNonEmpty(f.AdName, f.Creative.Name, f.AdSet.Name)},
	}, nil
}

func (f LaunchForm) composeCreative() (meta.CreativeSpec, error) {
	if storyID := strings.TrimSpace(f.Creative.ObjectStoryID); storyID != "" {
		// An existing post is the whole creative; object_story_id and
		// object_story_spec are mutually exclusive in Meta's schema.
		return meta.CreativeSpec{
			Name:          firstNonEmpty(f.Creative.Name, "Creative"),
			ObjectStoryID: storyID,
			URLTags:       ensureTrackingTags(f.Creative.URLTags),
		}, nil
	}
	link := meta.LinkData{
		Link:        strings.TrimSpace(f.Creative.Link),
		Message:     f.Creative.Message,
		Name:        f.Creative.Headline,
		Description: f.Creative.Description,
		ImageHash:   f.Creative.ImageHash,
		Picture:     f.Creative.ImageURL,
	}
	if action := strings.TrimSpace(f.Creative.CallToActionType); action != "" {
		link.CallToAction = &meta.CallToAction{
			Type:  action,
			Value: map[string]any{"link": link.Link},
		}
	}

	story := &meta.ObjectStorySpec{
		PageID:          strings.TrimSpace(f.Creative.PageID),
		InstagramUserID: strings.TrimSpace(f.Creative.InstagramActorID),
	}
	if f.Creative.VideoID != "" || f.Creative.UseVideo {
		story.VideoData = &meta.VideoData{
			VideoID:         f.Creative.VideoID,
			Message:         f.Creative.Message,
			Title:           f.Creative.Headline,
			LinkDescription: f.Creative.Description,
			CallToAction:    link.CallToAction,
		}
	} else {
		story.LinkData = &link
	}

	return meta.CreativeSpec{
		Name:            firstNonEmpty(f.Creative.Name, f.Creative.Headline, "Creative"),
		ObjectStorySpec: story,
		URLTags:         ensureTrackingTags(f.Creative.URLTags),
	}, nil
}

func (f LaunchForm) validate() error {
	if strings.TrimSpace(f.Campaign.Name) == "" {
		return invalid("campaign.name", "is required")
	}
	if strings.TrimSpace(f.AdSet.Name) == "" {
		return invalid("ad_set.name", "is required")
	}
	// An existing post carries its own page and media, so the per-field
	// creative requirements do not apply.
	if strings.TrimSpace(f.Creative.ObjectStoryID) == "" {
		if strings.TrimSpace(f.Creative.PageID) == "" {
			return invalid("creative.page_id", "is required: an ad has to be published by a Page")
		}
		if f.Creative.VideoID == "" && !f.Creative.UseVideo && strings.TrimSpace(f.Creative.Link) == "" {
			return invalid("creative.link", "is required for a link ad")
		}
	}
	if f.AdSet.DailyBudget > 0 && f.AdSet.LifetimeBudget > 0 {
		return invalid("ad_set", "set either a daily or a lifetime budget, not both")
	}
	if f.AdSet.DailyBudget <= 0 && f.AdSet.LifetimeBudget <= 0 {
		return invalid("ad_set", "a budget is required")
	}
	// A capped strategy without a bid amount fails at the ad set stage, one
	// step after the campaign has been created and has to be cleaned up.
	if requiresBidAmount(f.Campaign.BidStrategy) && f.AdSet.BidAmount <= 0 {
		return invalid("ad_set.bid_amount",
			fmt.Sprintf("is required with the %s bid strategy", f.Campaign.BidStrategy))
	}
	if f.AdSet.LifetimeBudget > 0 && strings.TrimSpace(f.AdSet.EndTime) == "" {
		// Meta rejects a lifetime budget without one, with a less obvious
		// message than this.
		return invalid("ad_set.end_time", "is required when using a lifetime budget")
	}
	return nil
}

func stringField(source map[string]any, key string) string {
	if source == nil {
		return ""
	}
	value, ok := source[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}

// requiresBidAmount reports whether a bid strategy needs an explicit bid.
func requiresBidAmount(strategy string) bool {
	switch strategy {
	case "LOWEST_COST_WITH_BID_CAP", "COST_CAP":
		return true
	default:
		return false
	}
}
