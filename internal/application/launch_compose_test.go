package application

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func validForm() LaunchForm {
	return LaunchForm{
		Campaign: LaunchCampaignForm{Name: "Spring", Objective: "OUTCOME_SALES"},
		AdSet:    LaunchAdSetForm{Name: "Set 1", DailyBudget: 10},
		Creative: LaunchCreativeForm{
			PageID: "123456", Link: "https://example.com",
			Headline: "Try it", Message: "Body", CallToActionType: "LEARN_MORE",
		},
	}
}

func TestComposeConvertsMoneyToMinorUnits(t *testing.T) {
	form := validForm()
	form.AdSet.DailyBudget = 10.99
	form.Campaign.SpendCap = 250

	hierarchy, err := form.Compose()
	require.NoError(t, err)
	// Rounding, not truncation: 10.99 must not become 1098.
	require.Equal(t, int64(1099), hierarchy.AdSet.DailyBudget)
	require.Equal(t, int64(25000), hierarchy.Campaign.SpendCap)
}

func TestComposeInheritsTargetingFromTheSourceAdSet(t *testing.T) {
	// Targeting is the part most likely to be wrong when retyped and the part
	// a buyer has already proven, so it is copied verbatim rather than rebuilt.
	form := validForm()
	form.SourceAdSet = json.RawMessage(`{
		"optimization_goal":"OFFSITE_CONVERSIONS",
		"billing_event":"IMPRESSIONS",
		"targeting":{"geo_locations":{"countries":["IT"]},"age_min":25},
		"promoted_object":{"pixel_id":"999","custom_event_type":"PURCHASE"},
		"attribution_spec":[{"event_type":"CLICK_THROUGH","window_days":7}]
	}`)

	hierarchy, err := form.Compose()
	require.NoError(t, err)

	require.Equal(t, "OFFSITE_CONVERSIONS", string(hierarchy.AdSet.OptimizationGoal))
	require.Equal(t, "IMPRESSIONS", string(hierarchy.AdSet.BillingEvent))
	require.Contains(t, hierarchy.AdSet.Raw, "targeting")
	// The pixel arrives only through the source ad set; there is nowhere else
	// to get it from.
	require.Contains(t, hierarchy.AdSet.Raw, "promoted_object")
	require.Contains(t, hierarchy.AdSet.Raw, "attribution_spec")
}

func TestFormOverridesBeatInheritance(t *testing.T) {
	// "Copy this ad set but optimise for something else" must be one field.
	form := validForm()
	form.SourceAdSet = json.RawMessage(`{"optimization_goal":"OFFSITE_CONVERSIONS"}`)
	form.AdSet.OptimizationGoal = "LINK_CLICKS"

	hierarchy, err := form.Compose()
	require.NoError(t, err)
	require.Equal(t, "LINK_CLICKS", string(hierarchy.AdSet.OptimizationGoal))
}

func TestComposeBuildsALinkCreative(t *testing.T) {
	hierarchy, err := validForm().Compose()
	require.NoError(t, err)

	story := hierarchy.Creative.ObjectStorySpec
	require.NotNil(t, story)
	require.Equal(t, "123456", story.PageID)
	require.NotNil(t, story.LinkData)
	require.Equal(t, "https://example.com", story.LinkData.Link)
	require.Equal(t, "Try it", story.LinkData.Name)
	require.NotNil(t, story.LinkData.CallToAction)
	require.Equal(t, "LEARN_MORE", story.LinkData.CallToAction.Type)
	require.Equal(t, "https://example.com", story.LinkData.CallToAction.Value["link"])
}

func TestComposeBuildsAVideoCreativeInstead(t *testing.T) {
	form := validForm()
	form.Creative.VideoID = "78910"
	hierarchy, err := form.Compose()
	require.NoError(t, err)

	story := hierarchy.Creative.ObjectStorySpec
	require.NotNil(t, story.VideoData)
	require.Equal(t, "78910", story.VideoData.VideoID)
	require.Nil(t, story.LinkData, "a video ad must not also carry link data")
}

func TestComposeRejectsWhatMetaWouldRejectLater(t *testing.T) {
	for name, mutate := range map[string]func(*LaunchForm){
		"no campaign name": func(f *LaunchForm) { f.Campaign.Name = "" },
		"no ad set name":   func(f *LaunchForm) { f.AdSet.Name = "" },
		"no page":          func(f *LaunchForm) { f.Creative.PageID = "" },
		"no link or video": func(f *LaunchForm) { f.Creative.Link = "" },
		"no budget":        func(f *LaunchForm) { f.AdSet.DailyBudget = 0 },
		"both budgets": func(f *LaunchForm) {
			f.AdSet.LifetimeBudget = 100
		},
		"lifetime without end": func(f *LaunchForm) {
			f.AdSet.DailyBudget = 0
			f.AdSet.LifetimeBudget = 100
		},
	} {
		form := validForm()
		mutate(&form)
		_, err := form.Compose()
		require.Error(t, err, name)
	}
}

func TestLifetimeBudgetWithEndTimeIsAccepted(t *testing.T) {
	form := validForm()
	form.AdSet.DailyBudget = 0
	form.AdSet.LifetimeBudget = 100
	form.AdSet.EndTime = "2026-12-31T23:59:59+0000"

	hierarchy, err := form.Compose()
	require.NoError(t, err)
	require.Equal(t, int64(10000), hierarchy.AdSet.LifetimeBudget)
	require.Zero(t, hierarchy.AdSet.DailyBudget)
}

func TestSpecialAdCategoryDefaultsToNone(t *testing.T) {
	// Meta requires the field to be present; omitting it is not the same as
	// declaring no category.
	hierarchy, err := validForm().Compose()
	require.NoError(t, err)
	require.NotNil(t, hierarchy.Campaign.SpecialAdCategories)
	require.Empty(t, hierarchy.Campaign.SpecialAdCategories)
}

func TestSpecialAdCategoriesReachMetaEvenWhenEmpty(t *testing.T) {
	// Meta requires the field on every campaign create, and the typed field
	// carries omitempty - so this only works because the publisher
	// substitutes ["NONE"] when the list is empty. Asserting it through the
	// real payload builder rather than a bare marshal, because a bare marshal
	// is not what reaches Meta.
	hierarchy, err := validForm().Compose()
	require.NoError(t, err)
	require.Empty(t, hierarchy.Campaign.SpecialAdCategories)

	payload, err := meta.CampaignPayloadForTest(hierarchy.Campaign)
	require.NoError(t, err)
	require.Equal(t, []string{"NONE"}, payload["special_ad_categories"])
}

func TestSpecialAdCategoryIsCarriedWhenSet(t *testing.T) {
	form := validForm()
	form.Campaign.SpecialAdCategories = []string{"ONLINE_GAMBLING_AND_GAMING"}
	form.Campaign.SpecialAdCountry = "IT"

	hierarchy, err := form.Compose()
	require.NoError(t, err)
	require.Equal(t, []string{"IT"}, hierarchy.Campaign.SpecialAdCategoryCountry)

	payload, err := meta.CampaignPayloadForTest(hierarchy.Campaign)
	require.NoError(t, err)
	require.Contains(t, fmt.Sprint(payload["special_ad_categories"]), "ONLINE_GAMBLING_AND_GAMING")
}

func TestCampaignDeclaresBudgetSharingExplicitly(t *testing.T) {
	// Found by the first dry run against Meta: a campaign that carries no
	// budget of its own is rejected unless this is stated. The launcher
	// always budgets at ad set level, so the answer is always false - but
	// omitting it fails validation with a message that never mentions any
	// field the launcher itself exposes.
	hierarchy, err := validForm().Compose()
	require.NoError(t, err)
	require.NotNil(t, hierarchy.Campaign.IsAdSetBudgetSharingEnabled)
	require.False(t, *hierarchy.Campaign.IsAdSetBudgetSharingEnabled)

	payload, err := meta.CampaignPayloadForTest(hierarchy.Campaign)
	require.NoError(t, err)
	require.Equal(t, false, payload["is_adset_budget_sharing_enabled"])
}

func TestBidStrategyLivesWhereTheBudgetLives(t *testing.T) {
	// Two live launches taught this from both sides. Leaving the strategy
	// unset makes Meta fall back to the account default, which on a bid-cap
	// account rejects the ad set for having no bid amount. Setting it on the
	// campaign instead fails with "This campaign doesn't have a budget. Add a
	// budget to edit the bid strategy", because this launcher budgets at ad
	// set level. So it belongs on the ad set.
	hierarchy, err := validForm().Compose()
	require.NoError(t, err)
	require.Empty(t, string(hierarchy.Campaign.BidStrategy),
		"a campaign with no budget of its own must not carry a bid strategy")
	require.Equal(t, "LOWEST_COST_WITHOUT_CAP", string(hierarchy.AdSet.BidStrategy))
}

func TestExplicitBidStrategyIsKept(t *testing.T) {
	form := validForm()
	form.Campaign.BidStrategy = "COST_CAP"
	form.AdSet.BidAmount = 2

	hierarchy, err := form.Compose()
	require.NoError(t, err)
	require.Equal(t, "COST_CAP", string(hierarchy.AdSet.BidStrategy))
	require.Empty(t, string(hierarchy.Campaign.BidStrategy))
	require.Equal(t, int64(200), hierarchy.AdSet.BidAmount)
}

func TestCappedBidStrategyWithoutABidIsRejectedLocally(t *testing.T) {
	// Rejecting here costs nothing. Letting it through costs a created
	// campaign that then has to be cleaned up by hand.
	for _, strategy := range []string{"LOWEST_COST_WITH_BID_CAP", "COST_CAP"} {
		form := validForm()
		form.Campaign.BidStrategy = strategy
		_, err := form.Compose()
		require.ErrorContains(t, err, "bid_amount", strategy)
	}
}

func TestVideoCreativeIsShapedBeforeTheVideoIDExists(t *testing.T) {
	// A video uploaded through the launcher has no ID at compose time: the
	// publisher uploads it into each ad account and writes that account's own
	// ID in. Without video_data already present the binding has nowhere to
	// write, and the launch fails after the campaign exists.
	form := validForm()
	form.Creative.UseVideo = true
	form.Creative.VideoID = ""
	form.Creative.Link = ""

	hierarchy, err := form.Compose()
	require.NoError(t, err)

	story := hierarchy.Creative.ObjectStorySpec
	require.NotNil(t, story.VideoData, "the binding needs a video_data object to fill")
	require.Empty(t, story.VideoData.VideoID)
	require.Nil(t, story.LinkData)
}

func TestLinkIsStillRequiredForALinkAd(t *testing.T) {
	form := validForm()
	form.Creative.Link = ""
	_, err := form.Compose()
	require.ErrorContains(t, err, "link")
}
