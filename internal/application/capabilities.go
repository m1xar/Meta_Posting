package application

import (
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func (s *Service) Capabilities() Capabilities {
	return Capabilities{
		MetaAPIVersion: s.Config.Meta.APIVersion,
		Objectives: []meta.Objective{
			meta.ObjectiveOutcomeAwareness,
			meta.ObjectiveOutcomeTraffic,
			meta.ObjectiveOutcomeEngagement,
			meta.ObjectiveOutcomeLeads,
			meta.ObjectiveOutcomeAppPromotion,
			meta.ObjectiveOutcomeSales,
		},
		Destinations: []meta.DestinationType{
			meta.DestinationWebsite,
			meta.DestinationApp,
			meta.DestinationAppLinksAutomatic,
			meta.DestinationFacebookPage,
			meta.DestinationOnPage,
			meta.DestinationOnPost,
		},
		OptimizationGoals: []meta.OptimizationGoal{
			meta.OptimizationGoalAutomatic,
			meta.OptimizationGoalAdRecallLift,
			meta.OptimizationGoalImpressions,
			meta.OptimizationGoalReach,
			meta.OptimizationGoalLinkClicks,
			meta.OptimizationGoalLandingPageViews,
			meta.OptimizationGoalThruPlay,
			meta.OptimizationGoalOffsiteConversions,
			meta.OptimizationGoalPageLikes,
			meta.OptimizationGoalPostEngagement,
			meta.OptimizationGoalProfileAndPageEngagement,
			meta.OptimizationGoalValue,
			meta.OptimizationGoalAppInstalls,
			meta.OptimizationGoalAppInstallsOffsiteConversions,
		},
		BillingEvents: []meta.BillingEvent{
			meta.BillingEventImpressions,
			meta.BillingEventLinkClicks,
			meta.BillingEventClicks,
			meta.BillingEventAppInstalls,
			meta.BillingEventThruPlay,
			meta.BillingEventPurchase,
		},
		BidStrategies: []meta.BidStrategy{
			meta.BidStrategyLowestCostWithoutCap,
			meta.BidStrategyLowestCostWithBidCap,
			meta.BidStrategyCostCap,
			meta.BidStrategyLowestCostMinROAS,
		},
		SpecialAdCategories: []meta.SpecialAdCategory{
			meta.SpecialAdCategoryNone,
			meta.SpecialAdCategoryOnlineGamblingGaming,
			meta.SpecialAdCategoryCredit,
			meta.SpecialAdCategoryEmployment,
			meta.SpecialAdCategoryFinancialProducts,
			meta.SpecialAdCategoryHousing,
			meta.SpecialAdCategoryIssuesElectionsPolitics,
		},
		CreativeFormats: []string{
			"single_image",
			"single_video",
			"carousel",
			"flexible_asset_feed",
			"existing_facebook_post",
			"existing_instagram_media",
		},
		GuardMetrics: []string{
			"spend",
			"impressions",
			"clicks",
			"tracker_clicks",
			"tracker_leads",
			"tracker_sales",
			"tracker_revenue",
		},
		RawFieldsSupported: true,
		ExcludedCapabilities: []string{
			"instant_forms",
			"messaging_destinations",
			"catalog_sales",
		},
	}
}
