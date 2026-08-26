package domain

import (
	"time"

	"github.com/google/uuid"
)

// AdEntityLevel is the Meta object level of an inventory record. It excludes
// the account level, which is ad_accounts, and creatives, which have no
// Insights level of their own.
type AdEntityLevel string

const (
	AdEntityCampaign AdEntityLevel = "campaign"
	AdEntityAdSet    AdEntityLevel = "adset"
	AdEntityAd       AdEntityLevel = "ad"
)

func (l AdEntityLevel) Valid() bool {
	switch l {
	case AdEntityCampaign, AdEntityAdSet, AdEntityAd:
		return true
	default:
		return false
	}
}

// InsightLevel returns the Insights level this entity is reported at.
func (l AdEntityLevel) InsightLevel() InsightLevel {
	return InsightLevel(l)
}

// AdEntity is one campaign, ad set or ad observed in a connected ad account,
// whether or not this service created it. PublishedObjectID links it back to
// the batch that produced it when it is ours; IsOwned records that fact
// independently, so provenance survives a batch being deleted.
type AdEntity struct {
	Model
	ConnectionID       uuid.UUID     `gorm:"type:uuid;not null" json:"connection_id"`
	AdAccountID        uuid.UUID     `gorm:"type:uuid;not null" json:"ad_account_id"`
	Level              AdEntityLevel `gorm:"type:text;not null" json:"level"`
	MetaObjectID       string        `gorm:"not null" json:"meta_object_id"`
	ParentMetaObjectID string        `gorm:"not null;default:''" json:"parent_meta_object_id"`
	CampaignMetaID     string        `gorm:"not null;default:''" json:"campaign_meta_id"`
	AdSetMetaID        string        `gorm:"column:adset_meta_id;not null;default:''" json:"adset_meta_id"`
	Name               string        `gorm:"not null;default:''" json:"name"`
	Status             string        `gorm:"not null;default:''" json:"status"`
	ConfiguredStatus   string        `gorm:"not null;default:''" json:"configured_status"`
	EffectiveStatus    string        `gorm:"not null;default:''" json:"effective_status"`
	Objective          string        `gorm:"not null;default:''" json:"objective,omitempty"`
	BuyingType         string        `gorm:"not null;default:''" json:"buying_type,omitempty"`
	OptimizationGoal   string        `gorm:"not null;default:''" json:"optimization_goal,omitempty"`
	BillingEvent       string        `gorm:"not null;default:''" json:"billing_event,omitempty"`
	DestinationType    string        `gorm:"not null;default:''" json:"destination_type,omitempty"`
	BidStrategy        string        `gorm:"not null;default:''" json:"bid_strategy,omitempty"`
	DailyBudget        int64         `gorm:"not null;default:0" json:"daily_budget_minor"`
	LifetimeBudget     int64         `gorm:"not null;default:0" json:"lifetime_budget_minor"`
	BudgetRemaining    int64         `gorm:"not null;default:0" json:"budget_remaining_minor"`
	BidAmount          int64         `gorm:"not null;default:0" json:"bid_amount_minor"`
	SpendCap           int64         `gorm:"not null;default:0" json:"spend_cap_minor"`
	StartTime          *time.Time    `json:"start_time,omitempty"`
	StopTime           *time.Time    `json:"stop_time,omitempty"`
	MetaCreatedTime    *time.Time    `json:"meta_created_time,omitempty"`
	MetaUpdatedTime    *time.Time    `json:"meta_updated_time,omitempty"`
	PublishedObjectID  *uuid.UUID    `gorm:"type:uuid" json:"published_object_id,omitempty"`
	IsOwned            bool          `gorm:"not null;default:false" json:"is_owned"`
	RawJSON            JSON          `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	FirstSeenAt        time.Time     `gorm:"not null" json:"first_seen_at"`
	LastSeenAt         time.Time     `gorm:"not null" json:"last_seen_at"`
	DisappearedAt      *time.Time    `json:"disappeared_at,omitempty"`
}

func (AdEntity) TableName() string { return "ad_entities" }

// AdInsightDaily is one object's delivery on one calendar day, in the ad
// account's timezone.
//
// Reach, Frequency, CPP and every Unique* field are deduplicated over the
// query window. They are correct for this single day and must never be summed
// across rows - see application.NonAdditiveMetrics.
type AdInsightDaily struct {
	Model
	ConnectionID       uuid.UUID    `gorm:"type:uuid;not null" json:"connection_id"`
	AdAccountID        uuid.UUID    `gorm:"type:uuid;not null" json:"ad_account_id"`
	Level              InsightLevel `gorm:"type:text;not null" json:"level"`
	MetaObjectID       string       `gorm:"not null" json:"meta_object_id"`
	MetaAccountID      string       `gorm:"not null;default:''" json:"meta_account_id"`
	CampaignMetaID     string       `gorm:"not null;default:''" json:"campaign_meta_id,omitempty"`
	AdSetMetaID        string       `gorm:"column:adset_meta_id;not null;default:''" json:"adset_meta_id,omitempty"`
	ObjectName         string       `gorm:"not null;default:''" json:"object_name"`
	Date               time.Time    `gorm:"type:date;not null" json:"date"`
	AccountTimezone    string       `gorm:"not null;default:''" json:"account_timezone"`
	Currency           string       `gorm:"not null;default:''" json:"currency"`
	AttributionSetting string       `gorm:"not null;default:''" json:"attribution_setting"`

	Spend                  float64 `gorm:"type:numeric(24,8);not null;default:0" json:"spend"`
	Impressions            int64   `gorm:"not null;default:0" json:"impressions"`
	Reach                  int64   `gorm:"not null;default:0" json:"reach"`
	Frequency              float64 `gorm:"type:numeric(24,8);not null;default:0" json:"frequency"`
	Clicks                 int64   `gorm:"not null;default:0" json:"clicks"`
	UniqueClicks           int64   `gorm:"not null;default:0" json:"unique_clicks"`
	InlineLinkClicks       int64   `gorm:"not null;default:0" json:"inline_link_clicks"`
	UniqueInlineLinkClicks int64   `gorm:"not null;default:0" json:"unique_inline_link_clicks"`
	CTR                    float64 `gorm:"type:numeric(24,8);not null;default:0" json:"ctr"`
	UniqueCTR              float64 `gorm:"type:numeric(24,8);not null;default:0" json:"unique_ctr"`
	CPC                    float64 `gorm:"type:numeric(24,8);not null;default:0" json:"cpc"`
	CPM                    float64 `gorm:"type:numeric(24,8);not null;default:0" json:"cpm"`
	CPP                    float64 `gorm:"type:numeric(24,8);not null;default:0" json:"cpp"`
	CostPerUniqueClick     float64 `gorm:"type:numeric(24,8);not null;default:0" json:"cost_per_unique_click"`
	CostPerInlineLinkClick float64 `gorm:"type:numeric(24,8);not null;default:0" json:"cost_per_inline_link_click"`
	QualityRanking         string  `gorm:"not null;default:''" json:"quality_ranking,omitempty"`
	EngagementRateRanking  string  `gorm:"not null;default:''" json:"engagement_rate_ranking,omitempty"`
	ConversionRateRanking  string  `gorm:"not null;default:''" json:"conversion_rate_ranking,omitempty"`

	Actions       JSON `gorm:"type:jsonb;not null;default:'{}'" json:"actions"`
	ActionValues  JSON `gorm:"type:jsonb;not null;default:'{}'" json:"action_values"`
	CostPerAction JSON `gorm:"type:jsonb;not null;default:'{}'" json:"cost_per_action"`
	Conversions   JSON `gorm:"type:jsonb;not null;default:'{}'" json:"conversions"`
	ROAS          JSON `gorm:"type:jsonb;not null;default:'{}'" json:"roas"`
	Video         JSON `gorm:"type:jsonb;not null;default:'{}'" json:"video"`
	Metrics       JSON `gorm:"type:jsonb;not null;default:'{}'" json:"metrics"`
	RawJSON       JSON `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`

	FetchedAt time.Time `gorm:"not null" json:"fetched_at"`
}

func (AdInsightDaily) TableName() string { return "ad_insights_daily" }

// AdInsightWindowed holds deduplicated reach over an explicit window, fetched
// without time_increment so Meta performs the deduplication. Summing daily
// reach is arithmetically wrong; this table is the supported alternative.
type AdInsightWindowed struct {
	Model
	ConnectionID       uuid.UUID    `gorm:"type:uuid;not null" json:"connection_id"`
	AdAccountID        uuid.UUID    `gorm:"type:uuid;not null" json:"ad_account_id"`
	Level              InsightLevel `gorm:"type:text;not null" json:"level"`
	MetaObjectID       string       `gorm:"not null" json:"meta_object_id"`
	Since              time.Time    `gorm:"type:date;not null" json:"since"`
	Until              time.Time    `gorm:"type:date;not null" json:"until"`
	AccountTimezone    string       `gorm:"not null;default:''" json:"account_timezone"`
	AttributionSetting string       `gorm:"not null;default:''" json:"attribution_setting"`
	Reach              int64        `gorm:"not null;default:0" json:"reach"`
	Frequency          float64      `gorm:"type:numeric(24,8);not null;default:0" json:"frequency"`
	Impressions        int64        `gorm:"not null;default:0" json:"impressions"`
	Spend              float64      `gorm:"type:numeric(24,8);not null;default:0" json:"spend"`
	RawJSON            JSON         `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	FetchedAt          time.Time    `gorm:"not null" json:"fetched_at"`
}

func (AdInsightWindowed) TableName() string { return "ad_insights_windowed" }

// AdAccountSyncState tracks ingestion progress and throttling for one ad
// account. Its primary key is the ad account, so it carries no Model.
type AdAccountSyncState struct {
	AdAccountID         uuid.UUID  `gorm:"type:uuid;primaryKey" json:"ad_account_id"`
	ConnectionID        uuid.UUID  `gorm:"type:uuid;not null" json:"connection_id"`
	EntitiesSyncedAt    *time.Time `json:"entities_synced_at,omitempty"`
	AttributionSetting  string     `gorm:"not null;default:'unified'" json:"attribution_setting"`
	BackfillTargetDate  *time.Time `gorm:"type:date" json:"backfill_target_date,omitempty"`
	BackfilledThrough   *time.Time `gorm:"type:date" json:"backfilled_through,omitempty"`
	AccountSyncedThru   *time.Time `gorm:"column:account_synced_through;type:date" json:"account_synced_through,omitempty"`
	CampaignSyncedThru  *time.Time `gorm:"column:campaign_synced_through;type:date" json:"campaign_synced_through,omitempty"`
	AdSetSyncedThrough  *time.Time `gorm:"column:adset_synced_through;type:date" json:"adset_synced_through,omitempty"`
	AdSyncedThrough     *time.Time `gorm:"column:ad_synced_through;type:date" json:"ad_synced_through,omitempty"`
	LastAdLevelRunAt    *time.Time `json:"last_ad_level_run_at,omitempty"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	ThrottledUntil      *time.Time `json:"throttled_until,omitempty"`
	LastUsage           JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"last_usage"`
	LastError           string     `gorm:"not null;default:''" json:"last_error,omitempty"`
	CreatedAt           time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt           time.Time  `gorm:"not null" json:"updated_at"`
}

func (AdAccountSyncState) TableName() string { return "ad_account_sync_state" }

// SyncedThrough returns the per-level watermark pointer for level.
func (s *AdAccountSyncState) SyncedThrough(level InsightLevel) *time.Time {
	switch level {
	case InsightAccount:
		return s.AccountSyncedThru
	case InsightCampaign:
		return s.CampaignSyncedThru
	case InsightAdSet:
		return s.AdSetSyncedThrough
	case InsightAd:
		return s.AdSyncedThrough
	default:
		return nil
	}
}

// SyncedThroughColumn maps a level to its watermark column, for targeted updates.
func SyncedThroughColumn(level InsightLevel) string {
	switch level {
	case InsightAccount:
		return "account_synced_through"
	case InsightCampaign:
		return "campaign_synced_through"
	case InsightAdSet:
		return "adset_synced_through"
	case InsightAd:
		return "ad_synced_through"
	default:
		return ""
	}
}

// InsightsSyncCursor is the round-robin position for one (connection, level).
type InsightsSyncCursor struct {
	ConnectionID uuid.UUID `gorm:"type:uuid;primaryKey" json:"connection_id"`
	Level        string    `gorm:"primaryKey" json:"level"`
	NextOffset   int       `gorm:"not null;default:0" json:"next_offset"`
	UpdatedAt    time.Time `gorm:"not null" json:"updated_at"`
}

func (InsightsSyncCursor) TableName() string { return "insights_sync_cursors" }

// CursorLevelEntities is the pseudo-level used to rotate entity inventory
// syncs, which are not tied to a single Insights level.
const CursorLevelEntities = "entities"

// AdInsightCoverage records that a day was fetched and how many rows it
// produced. RowCount zero is a real answer - the object did not deliver that
// day - and is what stops gap repair from re-fetching quiet days forever.
type AdInsightCoverage struct {
	AdAccountID uuid.UUID    `gorm:"type:uuid;primaryKey" json:"ad_account_id"`
	Level       InsightLevel `gorm:"type:text;primaryKey" json:"level"`
	Date        time.Time    `gorm:"type:date;primaryKey" json:"date"`
	RowCount    int          `gorm:"not null;default:0" json:"row_count"`
	FetchedAt   time.Time    `gorm:"not null" json:"fetched_at"`
}

func (AdInsightCoverage) TableName() string { return "ad_insights_coverage" }
