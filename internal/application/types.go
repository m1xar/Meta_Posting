package application

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

const (
	JobSyncConnection  = "sync_connection"
	JobPublishAccount  = "publish_account"
	JobCollectInsights = "collect_insights"
	JobEvaluateGuards  = "evaluate_guards"
	JobSyncTracker     = "sync_tracker"
)

const LifetimeInsightQueryHash = "lifetime-default-v2"

type OAuthStart struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type OAuthCompletion struct {
	ConnectionID uuid.UUID `json:"connection_id"`
	MetaUserID   string    `json:"meta_user_id"`
	DisplayName  string    `json:"display_name"`
	SyncJobID    uuid.UUID `json:"sync_job_id"`
}

type SyncSummary struct {
	ConnectionID uuid.UUID               `json:"connection_id"`
	Businesses   int                     `json:"businesses"`
	AdAccounts   int                     `json:"ad_accounts"`
	Assets       int                     `json:"assets"`
	Failures     []meta.DiscoveryFailure `json:"failures,omitempty"`
	SyncedAt     time.Time               `json:"synced_at"`
}

// MediaBinding uploads one local media item to every selected ad account and
// writes the resulting image hash or video ID at Target. Target is an RFC 6901
// JSON pointer relative to the hierarchy, for example
// /creative/object_story_spec/link_data/image_hash.
type MediaBinding struct {
	MediaID uuid.UUID `json:"media_id"`
	Target  string    `json:"target"`
}

type CreateBatchRequest struct {
	ConnectionID     uuid.UUID                  `json:"connection_id"`
	Name             string                     `json:"name"`
	AdAccountIDs     []uuid.UUID                `json:"ad_account_ids"`
	IdempotencyKey   string                     `json:"idempotency_key"`
	Hierarchy        meta.HierarchySpec         `json:"hierarchy"`
	Tree             *meta.CampaignTreeSpec     `json:"tree,omitempty"`
	AccountOverrides map[string]json.RawMessage `json:"account_overrides,omitempty"`
	MediaBindings    []MediaBinding             `json:"media_bindings,omitempty"`
	ValidateOnly     bool                       `json:"validate_only,omitempty"`
	LeavePaused      bool                       `json:"leave_paused,omitempty"`
	CreatedBy        string                     `json:"created_by,omitempty"`
}

type AccountPublishPlan struct {
	Hierarchy     meta.HierarchySpec     `json:"hierarchy"`
	Tree          *meta.CampaignTreeSpec `json:"tree,omitempty"`
	MediaBindings []MediaBinding         `json:"media_bindings,omitempty"`
	ValidateOnly  bool                   `json:"validate_only,omitempty"`
	LeavePaused   bool                   `json:"leave_paused,omitempty"`
}

type PublishJobPayload struct {
	ResultID uuid.UUID `json:"result_id"`
}

type SyncJobPayload struct {
	ConnectionID uuid.UUID `json:"connection_id"`
}

type InsightsJobPayload struct {
	ConnectionID uuid.UUID `json:"connection_id"`
}

type EvaluateGuardsJobPayload struct {
	ConnectionID *uuid.UUID `json:"connection_id,omitempty"`
}

type SyncTrackerJobPayload struct{}

// GuardCheckpoint is one rung of the spend ladder. When a campaign's lifetime
// spend reaches Spend, every non-zero minimum below must already be met or the
// campaign is paused. Clicks and impressions come from Facebook Insights;
// tracker minimums come from Keitaro (leads are registrations, sales are
// deposits).
type GuardCheckpoint struct {
	Spend             float64 `json:"spend"`
	MinClicks         int64   `json:"min_clicks,omitempty"`
	MinImpressions    int64   `json:"min_impressions,omitempty"`
	MinTrackerClicks  int64   `json:"min_tracker_clicks,omitempty"`
	MinTrackerLeads   float64 `json:"min_tracker_leads,omitempty"`
	MinTrackerSales   float64 `json:"min_tracker_sales,omitempty"`
	MinTrackerRevenue float64 `json:"min_tracker_revenue,omitempty"`
}

type CreateGuardRequest struct {
	ConnectionID              uuid.UUID          `json:"connection_id"`
	BatchID                   *uuid.UUID         `json:"batch_id,omitempty"`
	PublishedObjectID         *uuid.UUID         `json:"published_object_id,omitempty"`
	Name                      string             `json:"name"`
	Status                    domain.GuardStatus `json:"status,omitempty"`
	Checkpoints               []GuardCheckpoint  `json:"checkpoints"`
	EvaluationIntervalSeconds int64              `json:"evaluation_interval_seconds,omitempty"`
}

type UpdateGuardRequest struct {
	Name                      string             `json:"name,omitempty"`
	Status                    domain.GuardStatus `json:"status,omitempty"`
	Checkpoints               []GuardCheckpoint  `json:"checkpoints"`
	EvaluationIntervalSeconds int64              `json:"evaluation_interval_seconds,omitempty"`
}

type Capabilities struct {
	MetaAPIVersion       string                   `json:"meta_api_version"`
	Objectives           []meta.Objective         `json:"objectives"`
	Destinations         []meta.DestinationType   `json:"destinations"`
	OptimizationGoals    []meta.OptimizationGoal  `json:"optimization_goals"`
	BillingEvents        []meta.BillingEvent      `json:"billing_events"`
	BidStrategies        []meta.BidStrategy       `json:"bid_strategies"`
	SpecialAdCategories  []meta.SpecialAdCategory `json:"special_ad_categories"`
	CreativeFormats      []string                 `json:"creative_formats"`
	GuardMetrics         []string                 `json:"guard_metrics"`
	RawFieldsSupported   bool                     `json:"raw_fields_supported"`
	ExcludedCapabilities []string                 `json:"excluded_capabilities"`
}
