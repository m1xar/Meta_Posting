package meta

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Objective includes the six ODAX objectives accepted for new v25 campaigns
// and legacy values that may still appear when reading existing campaigns.
type Objective string

const (
	ObjectiveOutcomeAppPromotion Objective = "OUTCOME_APP_PROMOTION"
	ObjectiveOutcomeAwareness    Objective = "OUTCOME_AWARENESS"
	ObjectiveOutcomeEngagement   Objective = "OUTCOME_ENGAGEMENT"
	ObjectiveOutcomeLeads        Objective = "OUTCOME_LEADS"
	ObjectiveOutcomeSales        Objective = "OUTCOME_SALES"
	ObjectiveOutcomeTraffic      Objective = "OUTCOME_TRAFFIC"

	LegacyObjectiveAppInstalls         Objective = "APP_INSTALLS"
	LegacyObjectiveBrandAwareness      Objective = "BRAND_AWARENESS"
	LegacyObjectiveConversions         Objective = "CONVERSIONS"
	LegacyObjectiveEventResponses      Objective = "EVENT_RESPONSES"
	LegacyObjectiveLeadGeneration      Objective = "LEAD_GENERATION"
	LegacyObjectiveLinkClicks          Objective = "LINK_CLICKS"
	LegacyObjectiveLocalAwareness      Objective = "LOCAL_AWARENESS"
	LegacyObjectiveMessages            Objective = "MESSAGES"
	LegacyObjectiveOfferClaims         Objective = "OFFER_CLAIMS"
	LegacyObjectivePageLikes           Objective = "PAGE_LIKES"
	LegacyObjectivePostEngagement      Objective = "POST_ENGAGEMENT"
	LegacyObjectiveProductCatalogSales Objective = "PRODUCT_CATALOG_SALES"
	LegacyObjectiveReach               Objective = "REACH"
	LegacyObjectiveStoreVisits         Objective = "STORE_VISITS"
	LegacyObjectiveVideoViews          Objective = "VIDEO_VIEWS"
)

func (o Objective) IsODAX() bool {
	switch o {
	case ObjectiveOutcomeAppPromotion,
		ObjectiveOutcomeAwareness,
		ObjectiveOutcomeEngagement,
		ObjectiveOutcomeLeads,
		ObjectiveOutcomeSales,
		ObjectiveOutcomeTraffic:
		return true
	default:
		return false
	}
}

func (o Objective) IsLegacy() bool {
	switch o {
	case LegacyObjectiveAppInstalls,
		LegacyObjectiveBrandAwareness,
		LegacyObjectiveConversions,
		LegacyObjectiveEventResponses,
		LegacyObjectiveLeadGeneration,
		LegacyObjectiveLinkClicks,
		LegacyObjectiveLocalAwareness,
		LegacyObjectiveMessages,
		LegacyObjectiveOfferClaims,
		LegacyObjectivePageLikes,
		LegacyObjectivePostEngagement,
		LegacyObjectiveProductCatalogSales,
		LegacyObjectiveReach,
		LegacyObjectiveStoreVisits,
		LegacyObjectiveVideoViews:
		return true
	default:
		return false
	}
}

type EntityStatus string

const (
	StatusActive   EntityStatus = "ACTIVE"
	StatusPaused   EntityStatus = "PAUSED"
	StatusArchived EntityStatus = "ARCHIVED"
	StatusDeleted  EntityStatus = "DELETED"
)

type SpecialAdCategory string

const (
	SpecialAdCategoryNone                    SpecialAdCategory = "NONE"
	SpecialAdCategoryOnlineGamblingGaming    SpecialAdCategory = "ONLINE_GAMBLING_AND_GAMING"
	SpecialAdCategoryCredit                  SpecialAdCategory = "CREDIT"
	SpecialAdCategoryEmployment              SpecialAdCategory = "EMPLOYMENT"
	SpecialAdCategoryFinancialProducts       SpecialAdCategory = "FINANCIAL_PRODUCTS_SERVICES"
	SpecialAdCategoryHousing                 SpecialAdCategory = "HOUSING"
	SpecialAdCategoryIssuesElectionsPolitics SpecialAdCategory = "ISSUES_ELECTIONS_POLITICS"
)

type BidStrategy string

const (
	BidStrategyLowestCostWithoutCap BidStrategy = "LOWEST_COST_WITHOUT_CAP"
	BidStrategyLowestCostWithBidCap BidStrategy = "LOWEST_COST_WITH_BID_CAP"
	BidStrategyCostCap              BidStrategy = "COST_CAP"
	BidStrategyLowestCostMinROAS    BidStrategy = "LOWEST_COST_WITH_MIN_ROAS"
)

type BillingEvent string

const (
	BillingEventImpressions BillingEvent = "IMPRESSIONS"
	BillingEventLinkClicks  BillingEvent = "LINK_CLICKS"
	BillingEventClicks      BillingEvent = "CLICKS"
	BillingEventAppInstalls BillingEvent = "APP_INSTALLS"
	BillingEventThruPlay    BillingEvent = "THRUPLAY"
	BillingEventPurchase    BillingEvent = "PURCHASE"
)

type OptimizationGoal string

const (
	OptimizationGoalAdRecallLift                  OptimizationGoal = "AD_RECALL_LIFT"
	OptimizationGoalAppInstalls                   OptimizationGoal = "APP_INSTALLS"
	OptimizationGoalAppInstallsOffsiteConversions OptimizationGoal = "APP_INSTALLS_AND_OFFSITE_CONVERSIONS"
	OptimizationGoalAutomatic                     OptimizationGoal = "AUTOMATIC_OBJECTIVE"
	OptimizationGoalImpressions                   OptimizationGoal = "IMPRESSIONS"
	OptimizationGoalLandingPageViews              OptimizationGoal = "LANDING_PAGE_VIEWS"
	OptimizationGoalLinkClicks                    OptimizationGoal = "LINK_CLICKS"
	OptimizationGoalOffsiteConversions            OptimizationGoal = "OFFSITE_CONVERSIONS"
	OptimizationGoalPageLikes                     OptimizationGoal = "PAGE_LIKES"
	OptimizationGoalPostEngagement                OptimizationGoal = "POST_ENGAGEMENT"
	OptimizationGoalProfileAndPageEngagement      OptimizationGoal = "PROFILE_AND_PAGE_ENGAGEMENT"
	OptimizationGoalReach                         OptimizationGoal = "REACH"
	OptimizationGoalThruPlay                      OptimizationGoal = "THRUPLAY"
	OptimizationGoalValue                         OptimizationGoal = "VALUE"
)

type DestinationType string

const (
	DestinationWebsite           DestinationType = "WEBSITE"
	DestinationApp               DestinationType = "APP"
	DestinationAppLinksAutomatic DestinationType = "APPLINKS_AUTOMATIC"
	DestinationFacebookPage      DestinationType = "FACEBOOK_PAGE"
	DestinationOnPage            DestinationType = "ON_PAGE"
	DestinationOnPost            DestinationType = "ON_POST"
)

// RawFields is a forward-compatibility escape hatch. It is deep-merged over
// typed fields, except for hierarchy IDs and PAUSED status which Publisher
// deliberately forces while creating a safe draft.
type RawFields map[string]any

type CampaignSpec struct {
	Name                        string              `json:"name"`
	Objective                   Objective           `json:"objective"`
	BuyingType                  string              `json:"buying_type,omitempty"`
	SpecialAdCategories         []SpecialAdCategory `json:"special_ad_categories,omitempty"`
	SpecialAdCategoryCountry    []string            `json:"special_ad_category_country,omitempty"`
	DailyBudget                 int64               `json:"daily_budget,omitempty"`
	LifetimeBudget              int64               `json:"lifetime_budget,omitempty"`
	BidStrategy                 BidStrategy         `json:"bid_strategy,omitempty"`
	SpendCap                    int64               `json:"spend_cap,omitempty"`
	StartTime                   string              `json:"start_time,omitempty"`
	StopTime                    string              `json:"stop_time,omitempty"`
	BudgetScheduleSpecs         []map[string]any    `json:"budget_schedule_specs,omitempty"`
	IsAdSetBudgetSharingEnabled *bool               `json:"is_adset_budget_sharing_enabled,omitempty"`
	AdLabels                    []ObjectRef         `json:"adlabels,omitempty"`
	Raw                         RawFields           `json:"raw,omitempty"`
}

type AttributionSpec struct {
	EventType  string `json:"event_type"`
	WindowDays int    `json:"window_days"`
}

type PromotedObject struct {
	PixelID                    string    `json:"pixel_id,omitempty"`
	CustomEventType            string    `json:"custom_event_type,omitempty"`
	CustomEventStr             string    `json:"custom_event_str,omitempty"`
	ApplicationID              string    `json:"application_id,omitempty"`
	ObjectStoreURL             string    `json:"object_store_url,omitempty"`
	OfflineConversionDataSetID string    `json:"offline_conversion_data_set_id,omitempty"`
	PageID                     string    `json:"page_id,omitempty"`
	SmartPSEEnabled            *bool     `json:"smart_pse_enabled,omitempty"`
	Raw                        RawFields `json:"raw,omitempty"`
}

type Targeting struct {
	AgeMin                         int              `json:"age_min,omitempty"`
	AgeMax                         int              `json:"age_max,omitempty"`
	Genders                        []int            `json:"genders,omitempty"`
	GeoLocations                   map[string]any   `json:"geo_locations,omitempty"`
	ExcludedGeoLocations           map[string]any   `json:"excluded_geo_locations,omitempty"`
	Locales                        []int            `json:"locales,omitempty"`
	Interests                      []map[string]any `json:"interests,omitempty"`
	Behaviors                      []map[string]any `json:"behaviors,omitempty"`
	CustomAudiences                []map[string]any `json:"custom_audiences,omitempty"`
	ExcludedCustomAudiences        []map[string]any `json:"excluded_custom_audiences,omitempty"`
	FlexibleSpec                   []map[string]any `json:"flexible_spec,omitempty"`
	Exclusions                     map[string]any   `json:"exclusions,omitempty"`
	DevicePlatforms                []string         `json:"device_platforms,omitempty"`
	PublisherPlatforms             []string         `json:"publisher_platforms,omitempty"`
	FacebookPositions              []string         `json:"facebook_positions,omitempty"`
	InstagramPositions             []string         `json:"instagram_positions,omitempty"`
	AudienceNetworkPositions       []string         `json:"audience_network_positions,omitempty"`
	MessengerPositions             []string         `json:"messenger_positions,omitempty"`
	UserOS                         []string         `json:"user_os,omitempty"`
	UserDevice                     []string         `json:"user_device,omitempty"`
	ExcludedPublisherCategories    []string         `json:"excluded_publisher_categories,omitempty"`
	BrandSafetyContentFilterLevels []string         `json:"brand_safety_content_filter_levels,omitempty"`
	AdvantageAudience              int              `json:"advantage_audience,omitempty"`
	TargetingAutomation            map[string]any   `json:"targeting_automation,omitempty"`
	ThreadsPositions               []string         `json:"threads_positions,omitempty"`
	WhatsAppPositions              []string         `json:"whatsapp_positions,omitempty"`
	Raw                            RawFields        `json:"raw,omitempty"`
}

type FrequencyControlSpec struct {
	Event        string `json:"event"`
	IntervalDays int    `json:"interval_days"`
	MaxFrequency int    `json:"max_frequency"`
}

type AdSetSpec struct {
	Name                        string                 `json:"name"`
	BillingEvent                BillingEvent           `json:"billing_event,omitempty"`
	OptimizationGoal            OptimizationGoal       `json:"optimization_goal,omitempty"`
	DestinationType             DestinationType        `json:"destination_type,omitempty"`
	DailyBudget                 int64                  `json:"daily_budget,omitempty"`
	LifetimeBudget              int64                  `json:"lifetime_budget,omitempty"`
	BidStrategy                 BidStrategy            `json:"bid_strategy,omitempty"`
	BidAmount                   int64                  `json:"bid_amount,omitempty"`
	BidConstraints              map[string]any         `json:"bid_constraints,omitempty"`
	StartTime                   string                 `json:"start_time,omitempty"`
	EndTime                     string                 `json:"end_time,omitempty"`
	Targeting                   Targeting              `json:"targeting"`
	PromotedObject              *PromotedObject        `json:"promoted_object,omitempty"`
	AttributionSpec             []AttributionSpec      `json:"attribution_spec,omitempty"`
	PacingType                  []string               `json:"pacing_type,omitempty"`
	FrequencyControlSpecs       []FrequencyControlSpec `json:"frequency_control_specs,omitempty"`
	IsDynamicCreative           *bool                  `json:"is_dynamic_creative,omitempty"`
	OptimizationSubEvent        string                 `json:"optimization_sub_event,omitempty"`
	DailyMinSpendTarget         int64                  `json:"daily_min_spend_target,omitempty"`
	DailySpendCap               int64                  `json:"daily_spend_cap,omitempty"`
	LifetimeMinSpendTarget      int64                  `json:"lifetime_min_spend_target,omitempty"`
	LifetimeSpendCap            int64                  `json:"lifetime_spend_cap,omitempty"`
	DSABeneficiary              string                 `json:"dsa_beneficiary,omitempty"`
	DSAPayor                    string                 `json:"dsa_payor,omitempty"`
	RegionalRegulatedCategories []string               `json:"regional_regulated_categories,omitempty"`
	Raw                         RawFields              `json:"raw,omitempty"`
}

type CallToAction struct {
	Type  string         `json:"type"`
	Value map[string]any `json:"value,omitempty"`
}

type ChildAttachment struct {
	Link         string        `json:"link,omitempty"`
	Name         string        `json:"name,omitempty"`
	Description  string        `json:"description,omitempty"`
	Picture      string        `json:"picture,omitempty"`
	ImageHash    string        `json:"image_hash,omitempty"`
	VideoID      string        `json:"video_id,omitempty"`
	CallToAction *CallToAction `json:"call_to_action,omitempty"`
}

// LinkData represents both single-image/link and carousel creatives.
// ChildAttachments turns it into a carousel.
type LinkData struct {
	Link                string            `json:"link"`
	Message             string            `json:"message,omitempty"`
	Name                string            `json:"name,omitempty"`
	Description         string            `json:"description,omitempty"`
	Caption             string            `json:"caption,omitempty"`
	Picture             string            `json:"picture,omitempty"`
	ImageHash           string            `json:"image_hash,omitempty"`
	CallToAction        *CallToAction     `json:"call_to_action,omitempty"`
	ChildAttachments    []ChildAttachment `json:"child_attachments,omitempty"`
	AttachmentStyle     string            `json:"attachment_style,omitempty"`
	MultiShareOptimized *bool             `json:"multi_share_optimized,omitempty"`
	MultiShareEndCard   *bool             `json:"multi_share_end_card,omitempty"`
}

type VideoData struct {
	VideoID         string        `json:"video_id"`
	ImageHash       string        `json:"image_hash,omitempty"`
	ImageURL        string        `json:"image_url,omitempty"`
	Message         string        `json:"message,omitempty"`
	Title           string        `json:"title,omitempty"`
	LinkDescription string        `json:"link_description,omitempty"`
	CallToAction    *CallToAction `json:"call_to_action,omitempty"`
}

type ObjectStorySpec struct {
	PageID          string     `json:"page_id"`
	InstagramUserID string     `json:"instagram_user_id,omitempty"`
	LinkData        *LinkData  `json:"link_data,omitempty"`
	VideoData       *VideoData `json:"video_data,omitempty"`
}

type AssetFeedImage struct {
	Hash       string         `json:"hash,omitempty"`
	URL        string         `json:"url,omitempty"`
	URLTags    string         `json:"url_tags,omitempty"`
	ImageCrops map[string]any `json:"image_crops,omitempty"`
	AdLabels   []ObjectRef    `json:"adlabels,omitempty"`
}

type AssetFeedVideo struct {
	VideoID       string      `json:"video_id"`
	ThumbnailURL  string      `json:"thumbnail_url,omitempty"`
	ThumbnailHash string      `json:"thumbnail_hash,omitempty"`
	CaptionIDs    []string    `json:"caption_ids,omitempty"`
	URLTags       string      `json:"url_tags,omitempty"`
	AdLabels      []ObjectRef `json:"adlabels,omitempty"`
}

type AssetFeedText struct {
	Text     string      `json:"text"`
	URLTags  string      `json:"url_tags,omitempty"`
	AdLabels []ObjectRef `json:"adlabels,omitempty"`
}

type AssetFeedLinkURL struct {
	WebsiteURL         string      `json:"website_url"`
	DisplayURL         string      `json:"display_url,omitempty"`
	DeeplinkURL        string      `json:"deeplink_url,omitempty"`
	AndroidURL         string      `json:"android_url,omitempty"`
	IOSURL             string      `json:"ios_url,omitempty"`
	ObjectStoreURLs    []string    `json:"object_store_urls,omitempty"`
	CarouselSeeMoreURL string      `json:"carousel_see_more_url,omitempty"`
	URLTags            string      `json:"url_tags,omitempty"`
	AdLabels           []ObjectRef `json:"adlabels,omitempty"`
}

// AssetFeedSpec is Meta's flexible/dynamic creative payload.
type AssetFeedSpec struct {
	Images                  []AssetFeedImage   `json:"images,omitempty"`
	Videos                  []AssetFeedVideo   `json:"videos,omitempty"`
	Bodies                  []AssetFeedText    `json:"bodies,omitempty"`
	Titles                  []AssetFeedText    `json:"titles,omitempty"`
	Descriptions            []AssetFeedText    `json:"descriptions,omitempty"`
	LinkURLs                []AssetFeedLinkURL `json:"link_urls,omitempty"`
	CallToActionTypes       []string           `json:"call_to_action_types,omitempty"`
	AdFormats               []string           `json:"ad_formats,omitempty"`
	AssetCustomizationRules []map[string]any   `json:"asset_customization_rules,omitempty"`
	OptimizationType        string             `json:"optimization_type,omitempty"`
	Groups                  []map[string]any   `json:"groups,omitempty"`
	AdditionalData          map[string]any     `json:"additional_data,omitempty"`
	CallAdsConfiguration    map[string]any     `json:"call_ads_configuration,omitempty"`
	Raw                     RawFields          `json:"raw,omitempty"`
}

type CreativeSpec struct {
	Name                   string           `json:"name"`
	ObjectStoryID          string           `json:"object_story_id,omitempty"`
	SourceInstagramMediaID string           `json:"source_instagram_media_id,omitempty"`
	ObjectStorySpec        *ObjectStorySpec `json:"object_story_spec,omitempty"`
	AssetFeedSpec          *AssetFeedSpec   `json:"asset_feed_spec,omitempty"`
	URLTags                string           `json:"url_tags,omitempty"`
	PlatformCustomizations map[string]any   `json:"platform_customizations,omitempty"`
	DegreesOfFreedomSpec   map[string]any   `json:"degrees_of_freedom_spec,omitempty"`
	AuthorizationCategory  string           `json:"authorization_category,omitempty"`
	ApplinkTreatment       string           `json:"applink_treatment,omitempty"`
	Raw                    RawFields        `json:"raw,omitempty"`
}

type AdSpec struct {
	Name               string           `json:"name"`
	TrackingSpecs      []map[string]any `json:"tracking_specs,omitempty"`
	ConversionDomain   string           `json:"conversion_domain,omitempty"`
	BidAmount          int64            `json:"bid_amount,omitempty"`
	DisplaySequence    int              `json:"display_sequence,omitempty"`
	EngagementAudience bool             `json:"engagement_audience,omitempty"`
	Raw                RawFields        `json:"raw,omitempty"`
}

type HierarchySpec struct {
	Campaign CampaignSpec `json:"campaign"`
	AdSet    AdSetSpec    `json:"ad_set"`
	Creative CreativeSpec `json:"creative"`
	Ad       AdSpec       `json:"ad"`
}

// CampaignTreeSpec models the shape buyers use in Ads Manager: one campaign
// with multiple ad sets and multiple creative/ad pairs under each ad set.
type CampaignTreeSpec struct {
	Campaign CampaignSpec    `json:"campaign"`
	AdSets   []AdSetTreeSpec `json:"ad_sets"`
}

type AdSetTreeSpec struct {
	AdSet AdSetSpec    `json:"ad_set"`
	Ads   []AdTreeSpec `json:"ads"`
}

type AdTreeSpec struct {
	Creative CreativeSpec `json:"creative"`
	Ad       AdSpec       `json:"ad"`
}

func (t CampaignTreeSpec) Validate() error {
	if len(t.AdSets) == 0 {
		return errors.New("campaign tree requires at least one ad set")
	}
	if len(t.AdSets) > 100 {
		return errors.New("campaign tree cannot contain more than 100 ad sets")
	}
	var validationErrors []error
	for adSetIndex, adSet := range t.AdSets {
		if len(adSet.Ads) == 0 {
			validationErrors = append(validationErrors, fmt.Errorf(
				"ad_sets[%d] requires at least one ad", adSetIndex,
			))
			continue
		}
		if len(adSet.Ads) > 100 {
			validationErrors = append(validationErrors, fmt.Errorf(
				"ad_sets[%d] cannot contain more than 100 ads", adSetIndex,
			))
			continue
		}
		for adIndex, ad := range adSet.Ads {
			hierarchy := HierarchySpec{
				Campaign: t.Campaign,
				AdSet:    adSet.AdSet,
				Creative: ad.Creative,
				Ad:       ad.Ad,
			}
			if err := hierarchy.Validate(); err != nil {
				validationErrors = append(validationErrors, fmt.Errorf(
					"ad_sets[%d].ads[%d]: %w", adSetIndex, adIndex, err,
				))
			}
		}
	}
	return errors.Join(validationErrors...)
}

func (h HierarchySpec) Validate() error {
	var validationErrors []error
	if strings.TrimSpace(h.Campaign.Name) == "" {
		validationErrors = append(validationErrors, errors.New("campaign.name is required"))
	}
	if !h.Campaign.Objective.IsODAX() {
		validationErrors = append(validationErrors, fmt.Errorf(
			"campaign.objective %q is not one of the six ODAX objectives",
			h.Campaign.Objective,
		))
	}
	if strings.TrimSpace(h.AdSet.Name) == "" {
		validationErrors = append(validationErrors, errors.New("ad_set.name is required"))
	}
	if len(h.AdSet.Targeting.GeoLocations) == 0 && len(h.AdSet.Targeting.Raw) == 0 && len(h.AdSet.Raw) == 0 {
		validationErrors = append(validationErrors, errors.New("ad_set.targeting.geo_locations or raw targeting is required"))
	}
	if strings.TrimSpace(h.Creative.Name) == "" {
		validationErrors = append(validationErrors, errors.New("creative.name is required"))
	}
	if h.Creative.ObjectStoryID == "" && h.Creative.SourceInstagramMediaID == "" && h.Creative.ObjectStorySpec == nil {
		validationErrors = append(validationErrors, errors.New(
			"creative requires object_story_id, source_instagram_media_id, or object_story_spec",
		))
	}
	if h.Creative.ObjectStoryID != "" && (h.Creative.ObjectStorySpec != nil || h.Creative.AssetFeedSpec != nil) {
		validationErrors = append(validationErrors, errors.New(
			"creative.object_story_id cannot be combined with object_story_spec or asset_feed_spec",
		))
	}
	if h.Creative.ObjectStorySpec != nil && h.Creative.ObjectStorySpec.PageID == "" {
		validationErrors = append(validationErrors, errors.New("creative.object_story_spec.page_id is required"))
	}
	if h.Creative.ObjectStorySpec != nil &&
		h.Creative.ObjectStorySpec.LinkData == nil &&
		h.Creative.ObjectStorySpec.VideoData == nil &&
		h.Creative.AssetFeedSpec == nil {
		validationErrors = append(validationErrors, errors.New(
			"creative.object_story_spec requires link_data, video_data, or asset_feed_spec",
		))
	}
	if strings.TrimSpace(h.Ad.Name) == "" {
		validationErrors = append(validationErrors, errors.New("ad.name is required"))
	}
	return errors.Join(validationErrors...)
}

func campaignPayload(spec CampaignSpec) (map[string]any, error) {
	payload, err := structPayload(spec, spec.Raw)
	if err != nil {
		return nil, err
	}
	if len(spec.SpecialAdCategories) == 0 && payload["special_ad_categories"] == nil {
		payload["special_ad_categories"] = []string{string(SpecialAdCategoryNone)}
	}
	return payload, nil
}

func adSetPayload(spec AdSetSpec) (map[string]any, error) {
	payload, err := structPayload(spec, spec.Raw)
	if err != nil {
		return nil, err
	}
	targeting, ok := payload["targeting"].(map[string]any)
	if ok {
		delete(targeting, "raw")
		deepMerge(targeting, spec.Targeting.Raw)
		payload["targeting"] = targeting
	}
	if spec.PromotedObject != nil {
		promoted, ok := payload["promoted_object"].(map[string]any)
		if ok {
			delete(promoted, "raw")
			deepMerge(promoted, spec.PromotedObject.Raw)
			payload["promoted_object"] = promoted
		}
	}
	return payload, nil
}

func creativePayload(spec CreativeSpec) (map[string]any, error) {
	payload, err := structPayload(spec, spec.Raw)
	if err != nil {
		return nil, err
	}
	if spec.AssetFeedSpec != nil {
		feed, ok := payload["asset_feed_spec"].(map[string]any)
		if ok {
			delete(feed, "raw")
			deepMerge(feed, spec.AssetFeedSpec.Raw)
			payload["asset_feed_spec"] = feed
		}
	}
	return payload, nil
}

func adPayload(spec AdSpec) (map[string]any, error) {
	return structPayload(spec, spec.Raw)
}

func structPayload(value any, raw RawFields) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("meta: encode payload: %w", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("meta: normalize payload: %w", err)
	}
	delete(payload, "raw")
	deepMerge(payload, raw)
	return payload, nil
}

func deepMerge(destination map[string]any, override map[string]any) {
	for key, value := range override {
		overrideMap, overrideIsMap := stringAnyMap(value)
		currentMap, currentIsMap := stringAnyMap(destination[key])
		if overrideIsMap && currentIsMap {
			deepMerge(currentMap, overrideMap)
			continue
		}
		destination[key] = value
	}
}

func stringAnyMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case RawFields:
		return map[string]any(typed), true
	default:
		return nil, false
	}
}
