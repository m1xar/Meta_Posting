package application

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/rules"
)

const (
	JobSyncConnection  = "sync_connection"
	JobPublishAccount  = "publish_account"
	JobCollectInsights = "collect_insights"
	JobEvaluateRules   = "evaluate_rules"
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

type EvaluateRulesJobPayload struct {
	ConnectionID *uuid.UUID `json:"connection_id,omitempty"`
}

type CreateRuleRequest struct {
	ConnectionID              uuid.UUID           `json:"connection_id"`
	AdAccountID               *uuid.UUID          `json:"ad_account_id,omitempty"`
	BatchID                   *uuid.UUID          `json:"batch_id,omitempty"`
	Name                      string              `json:"name"`
	Status                    domain.RuleStatus   `json:"status,omitempty"`
	ScopeLevel                domain.InsightLevel `json:"scope_level"`
	Action                    domain.RuleAction   `json:"action,omitempty"`
	Conditions                rules.Group         `json:"conditions"`
	LookbackSeconds           int64               `json:"lookback_seconds"`
	EvaluationIntervalSeconds int64               `json:"evaluation_interval_seconds,omitempty"`
	GracePeriodSeconds        int64               `json:"grace_period_seconds,omitempty"`
	CooldownSeconds           int64               `json:"cooldown_seconds,omitempty"`
	MinimumSpend              float64             `json:"minimum_spend,omitempty"`
	MinimumImpressions        int64               `json:"minimum_impressions,omitempty"`
	Timezone                  string              `json:"timezone,omitempty"`
	Metadata                  map[string]any      `json:"metadata,omitempty"`
}

type UpdateRuleRequest = CreateRuleRequest

type Capabilities struct {
	MetaAPIVersion       string                   `json:"meta_api_version"`
	Objectives           []meta.Objective         `json:"objectives"`
	Destinations         []meta.DestinationType   `json:"destinations"`
	OptimizationGoals    []meta.OptimizationGoal  `json:"optimization_goals"`
	BillingEvents        []meta.BillingEvent      `json:"billing_events"`
	BidStrategies        []meta.BidStrategy       `json:"bid_strategies"`
	SpecialAdCategories  []meta.SpecialAdCategory `json:"special_ad_categories"`
	CreativeFormats      []string                 `json:"creative_formats"`
	RuleOperators        []rules.Operator         `json:"rule_operators"`
	RuleActions          []rules.Action           `json:"rule_actions"`
	RuleTargetLevels     []rules.TargetLevel      `json:"rule_target_levels"`
	CommonRuleMetrics    []string                 `json:"common_rule_metrics"`
	RawFieldsSupported   bool                     `json:"raw_fields_supported"`
	ExcludedCapabilities []string                 `json:"excluded_capabilities"`
}
