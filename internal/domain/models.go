package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var LegacyUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

type Model struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// User is the tenant boundary for every operator-visible resource. A Meta
// identity may be connected by more than one User without sharing inventory,
// batches, media, insights, or rules between them.
// UserRole gates cross-tenant access. It is deliberately a small closed set:
// anything finer belongs in per-resource ownership, not in a role.
type UserRole string

const (
	RoleUser  UserRole = "user"
	RoleAdmin UserRole = "admin"
	// RoleDisabled cannot authenticate. It holds the legacy pre-multi-tenant
	// tenant, whose resources an operator may migrate explicitly.
	RoleDisabled UserRole = "disabled"
)

type User struct {
	Model
	Email        string     `gorm:"not null" json:"email"`
	Username     string     `gorm:"not null" json:"username"`
	Role         UserRole   `gorm:"type:text;not null;default:'user'" json:"role"`
	PasswordHash string     `gorm:"not null" json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

func (u User) IsAdmin() bool  { return u.Role == RoleAdmin }
func (u User) CanLogin() bool { return u.Role == RoleUser || u.Role == RoleAdmin }

func (User) TableName() string { return "users" }

type UserSession struct {
	Model
	UserID     uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	TokenHash  []byte    `gorm:"not null" json:"-"`
	CSRFHash   []byte    `gorm:"not null" json:"-"`
	ExpiresAt  time.Time `gorm:"not null" json:"expires_at"`
	LastSeenAt time.Time `gorm:"not null" json:"last_seen_at"`
}

func (UserSession) TableName() string { return "user_sessions" }

func (m *Model) BeforeCreate(_ *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

type MetaConnection struct {
	Model
	UserID                uuid.UUID            `gorm:"type:uuid;not null" json:"user_id"`
	MetaUserID            string               `gorm:"not null" json:"meta_user_id"`
	DisplayName           string               `gorm:"not null;default:''" json:"display_name"`
	Email                 string               `gorm:"not null;default:''" json:"email"`
	Status                MetaConnectionStatus `gorm:"type:text;not null" json:"status"`
	AccessTokenCiphertext []byte               `gorm:"not null" json:"-"`
	AccessTokenNonce      []byte               `gorm:"not null" json:"-"`
	TokenKeyVersion       int16                `gorm:"not null;default:1" json:"-"`
	TokenExpiresAt        *time.Time           `json:"token_expires_at,omitempty"`
	DataAccessExpiresAt   *time.Time           `json:"data_access_expires_at,omitempty"`
	GrantedScopes         JSON                 `gorm:"type:jsonb;not null;default:'[]'" json:"granted_scopes"`
	DeclinedScopes        JSON                 `gorm:"type:jsonb;not null;default:'[]'" json:"declined_scopes"`
	LastValidatedAt       *time.Time           `json:"last_validated_at,omitempty"`
	LastSyncedAt          *time.Time           `json:"last_synced_at,omitempty"`
	LastError             string               `gorm:"not null;default:''" json:"last_error,omitempty"`
	Metadata              JSON                 `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
}

func (MetaConnection) TableName() string { return "meta_connections" }

type OAuthSession struct {
	Model
	UserID                uuid.UUID          `gorm:"type:uuid;not null" json:"user_id"`
	StateHash             []byte             `gorm:"not null" json:"-"`
	RedirectURI           string             `gorm:"not null" json:"redirect_uri"`
	RequestedScopes       JSON               `gorm:"type:jsonb;not null;default:'[]'" json:"requested_scopes"`
	Status                OAuthSessionStatus `gorm:"type:text;not null" json:"status"`
	ExpiresAt             time.Time          `gorm:"not null" json:"expires_at"`
	ConsumedAt            *time.Time         `json:"consumed_at,omitempty"`
	CompletedAt           *time.Time         `json:"completed_at,omitempty"`
	CompletedConnectionID *uuid.UUID         `gorm:"type:uuid" json:"completed_connection_id,omitempty"`
	Error                 string             `gorm:"not null;default:''" json:"error,omitempty"`
	Metadata              JSON               `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
}

func (OAuthSession) TableName() string { return "oauth_sessions" }

type Business struct {
	Model
	ConnectionID       uuid.UUID `gorm:"type:uuid;not null" json:"connection_id"`
	MetaBusinessID     string    `gorm:"not null" json:"meta_business_id"`
	Name               string    `gorm:"not null;default:''" json:"name"`
	VerificationStatus string    `gorm:"not null;default:''" json:"verification_status"`
	Vertical           string    `gorm:"not null;default:''" json:"vertical"`
	PrimaryPageID      string    `gorm:"not null;default:''" json:"primary_page_id"`
	TimezoneID         int       `gorm:"not null;default:0" json:"timezone_id"`
	IsHidden           bool      `gorm:"not null;default:false" json:"is_hidden"`
	RawJSON            JSON      `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	LastSyncedAt       time.Time `gorm:"not null" json:"last_synced_at"`
}

func (Business) TableName() string { return "businesses" }

type AdAccount struct {
	Model
	ConnectionID      uuid.UUID  `gorm:"type:uuid;not null" json:"connection_id"`
	BusinessID        *uuid.UUID `gorm:"type:uuid" json:"business_id,omitempty"`
	MetaAdAccountID   string     `gorm:"not null" json:"meta_ad_account_id"`
	AccountID         string     `gorm:"not null;default:''" json:"account_id"`
	Name              string     `gorm:"not null;default:''" json:"name"`
	Currency          string     `gorm:"not null;default:''" json:"currency"`
	TimezoneName      string     `gorm:"not null;default:''" json:"timezone_name"`
	TimezoneOffsetUTC float64    `gorm:"not null;default:0" json:"timezone_offset_utc"`
	AccountStatus     int        `gorm:"not null;default:0" json:"account_status"`
	DisableReason     int        `gorm:"not null;default:0" json:"disable_reason"`
	BusinessName      string     `gorm:"not null;default:''" json:"business_name"`
	AmountSpent       int64      `gorm:"not null;default:0" json:"amount_spent_minor"`
	Balance           int64      `gorm:"not null;default:0" json:"balance_minor"`
	SpendCap          int64      `gorm:"not null;default:0" json:"spend_cap_minor"`
	Capabilities      JSON       `gorm:"type:jsonb;not null;default:'[]'" json:"capabilities"`
	UserTasks         JSON       `gorm:"type:jsonb;not null;default:'[]'" json:"user_tasks"`
	// FundingSource is empty when no payment instrument is attached, which is
	// the only reliable signal that an account cannot be launched into.
	// Balance is the amount due, not funds available.
	FundingSource        string    `gorm:"not null;default:''" json:"funding_source,omitempty"`
	FundingSourceDetails JSON      `gorm:"type:jsonb;not null;default:'{}'" json:"funding_source_details"`
	IsPrepayAccount      bool      `gorm:"not null;default:false" json:"is_prepay_account"`
	IsActive             bool      `gorm:"not null;default:true" json:"is_active"`
	RawJSON              JSON      `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	LastSyncedAt         time.Time `gorm:"not null" json:"last_synced_at"`
}

func (AdAccount) TableName() string { return "ad_accounts" }

type Asset struct {
	Model
	ConnectionID uuid.UUID  `gorm:"type:uuid;not null" json:"connection_id"`
	BusinessID   *uuid.UUID `gorm:"type:uuid" json:"business_id,omitempty"`
	AdAccountID  *uuid.UUID `gorm:"type:uuid" json:"ad_account_id,omitempty"`
	AssetType    AssetType  `gorm:"type:text;not null" json:"asset_type"`
	MetaAssetID  string     `gorm:"not null" json:"meta_asset_id"`
	ParentMetaID string     `gorm:"not null;default:''" json:"parent_meta_id"`
	Name         string     `gorm:"not null;default:''" json:"name"`
	Status       string     `gorm:"not null;default:''" json:"status"`
	IsActive     bool       `gorm:"not null;default:true" json:"is_active"`
	Normalized   JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"normalized"`
	RawJSON      JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	LastSyncedAt time.Time  `gorm:"not null" json:"last_synced_at"`
}

func (Asset) TableName() string { return "assets" }

// AssetAdAccount preserves many-to-many access. Meta assets such as pixels,
// datasets, audiences, and Instagram identities can be shared with hundreds of
// ad accounts, so Asset.AdAccountID alone is only a convenient primary context.
type AssetAdAccount struct {
	AssetID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"asset_id"`
	AdAccountID  uuid.UUID `gorm:"type:uuid;primaryKey" json:"ad_account_id"`
	ConnectionID uuid.UUID `gorm:"type:uuid;not null" json:"connection_id"`
	LastSyncedAt time.Time `gorm:"not null" json:"last_synced_at"`
}

func (AssetAdAccount) TableName() string { return "asset_ad_accounts" }

type Media struct {
	Model
	ConnectionID  *uuid.UUID  `gorm:"type:uuid" json:"connection_id,omitempty"`
	AdAccountID   *uuid.UUID  `gorm:"type:uuid" json:"ad_account_id,omitempty"`
	Kind          MediaKind   `gorm:"type:text;not null" json:"kind"`
	Status        MediaStatus `gorm:"type:text;not null" json:"status"`
	OriginalName  string      `gorm:"not null" json:"original_name"`
	LocalPath     string      `gorm:"not null" json:"local_path"`
	MIMEType      string      `gorm:"not null" json:"mime_type"`
	SHA256        string      `gorm:"not null" json:"sha256"`
	SizeBytes     int64       `gorm:"not null" json:"size_bytes"`
	Width         int         `gorm:"not null;default:0" json:"width"`
	Height        int         `gorm:"not null;default:0" json:"height"`
	DurationMS    int64       `gorm:"not null;default:0" json:"duration_ms"`
	MetaImageHash string      `gorm:"not null;default:''" json:"meta_image_hash,omitempty"`
	MetaVideoID   string      `gorm:"not null;default:''" json:"meta_video_id,omitempty"`
	LastError     string      `gorm:"not null;default:''" json:"last_error,omitempty"`
	Metadata      JSON        `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
}

func (Media) TableName() string { return "media" }

// MediaAccountUpload is the durable per-ad-account Meta upload checkpoint.
// Image hashes and video IDs are account-scoped and must be persisted before a
// creative can reference them.
type MediaAccountUpload struct {
	Model
	MediaID       uuid.UUID   `gorm:"type:uuid;not null" json:"media_id"`
	AdAccountID   uuid.UUID   `gorm:"type:uuid;not null" json:"ad_account_id"`
	Status        MediaStatus `gorm:"type:text;not null" json:"status"`
	MetaImageHash string      `gorm:"not null;default:''" json:"meta_image_hash,omitempty"`
	MetaVideoID   string      `gorm:"not null;default:''" json:"meta_video_id,omitempty"`
	ResponseJSON  JSON        `gorm:"type:jsonb;not null;default:'{}'" json:"response"`
	LastError     string      `gorm:"not null;default:''" json:"last_error,omitempty"`
	LastCheckedAt *time.Time  `json:"last_checked_at,omitempty"`
}

func (MediaAccountUpload) TableName() string { return "media_account_uploads" }

type Batch struct {
	Model
	ConnectionID      uuid.UUID   `gorm:"type:uuid;not null" json:"connection_id"`
	Name              string      `gorm:"not null;default:''" json:"name"`
	Status            BatchStatus `gorm:"type:text;not null" json:"status"`
	IdempotencyKey    string      `gorm:"not null" json:"idempotency_key"`
	Specification     JSON        `gorm:"type:jsonb;not null;default:'{}'" json:"specification"`
	TotalAccounts     int         `gorm:"not null;default:0" json:"total_accounts"`
	SucceededAccounts int         `gorm:"not null;default:0" json:"succeeded_accounts"`
	FailedAccounts    int         `gorm:"not null;default:0" json:"failed_accounts"`
	StartedAt         *time.Time  `json:"started_at,omitempty"`
	CompletedAt       *time.Time  `json:"completed_at,omitempty"`
	CreatedBy         string      `gorm:"not null;default:''" json:"created_by"`
}

func (Batch) TableName() string { return "batches" }

type BatchAccountResult struct {
	Model
	BatchID      uuid.UUID          `gorm:"type:uuid;not null" json:"batch_id"`
	AdAccountID  uuid.UUID          `gorm:"type:uuid;not null" json:"ad_account_id"`
	Status       BatchAccountStatus `gorm:"type:text;not null" json:"status"`
	Attempts     int                `gorm:"not null;default:0" json:"attempts"`
	RequestJSON  JSON               `gorm:"type:jsonb;not null;default:'{}'" json:"request"`
	ResponseJSON JSON               `gorm:"type:jsonb;not null;default:'{}'" json:"response"`
	ErrorCode    string             `gorm:"not null;default:''" json:"error_code,omitempty"`
	ErrorSubcode string             `gorm:"not null;default:''" json:"error_subcode,omitempty"`
	ErrorMessage string             `gorm:"not null;default:''" json:"error_message,omitempty"`
	StartedAt    *time.Time         `json:"started_at,omitempty"`
	CompletedAt  *time.Time         `json:"completed_at,omitempty"`
}

func (BatchAccountResult) TableName() string { return "batch_account_results" }

type PublishedObject struct {
	Model
	BatchID              uuid.UUID           `gorm:"type:uuid;not null" json:"batch_id"`
	BatchAccountResultID uuid.UUID           `gorm:"type:uuid;not null" json:"batch_account_result_id"`
	ConnectionID         uuid.UUID           `gorm:"type:uuid;not null" json:"connection_id"`
	AdAccountID          uuid.UUID           `gorm:"type:uuid;not null" json:"ad_account_id"`
	ObjectType           PublishedObjectType `gorm:"type:text;not null" json:"object_type"`
	MetaObjectID         string              `gorm:"not null" json:"meta_object_id"`
	ParentMetaObjectID   string              `gorm:"not null;default:''" json:"parent_meta_object_id"`
	Name                 string              `gorm:"not null;default:''" json:"name"`
	DesiredStatus        string              `gorm:"not null;default:''" json:"desired_status"`
	EffectiveStatus      string              `gorm:"not null;default:''" json:"effective_status"`
	IdempotencyKey       string              `gorm:"not null" json:"idempotency_key"`
	RequestJSON          JSON                `gorm:"type:jsonb;not null;default:'{}'" json:"request"`
	ResponseJSON         JSON                `gorm:"type:jsonb;not null;default:'{}'" json:"response"`
	LastSyncedAt         *time.Time          `json:"last_synced_at,omitempty"`
}

func (PublishedObject) TableName() string { return "published_objects" }

type Job struct {
	Model
	ConnectionID *uuid.UUID `gorm:"type:uuid" json:"connection_id,omitempty"`
	Type         string     `gorm:"not null" json:"type"`
	Status       JobStatus  `gorm:"type:text;not null" json:"status"`
	Priority     int        `gorm:"not null;default:0" json:"priority"`
	Payload      JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"payload"`
	DedupeKey    *string    `json:"dedupe_key,omitempty"`
	Attempts     int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts  int        `gorm:"not null;default:5" json:"max_attempts"`
	AvailableAt  time.Time  `gorm:"not null" json:"available_at"`
	LockedBy     string     `gorm:"not null;default:''" json:"locked_by,omitempty"`
	LockedAt     *time.Time `json:"locked_at,omitempty"`
	LockedUntil  *time.Time `json:"locked_until,omitempty"`
	LastError    string     `gorm:"not null;default:''" json:"last_error,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func (Job) TableName() string { return "jobs" }

type InsightSnapshot struct {
	Model
	ConnectionID       uuid.UUID    `gorm:"type:uuid;not null" json:"connection_id"`
	AdAccountID        uuid.UUID    `gorm:"type:uuid;not null" json:"ad_account_id"`
	PublishedObjectID  *uuid.UUID   `gorm:"type:uuid" json:"published_object_id,omitempty"`
	MetaObjectID       string       `gorm:"not null" json:"meta_object_id"`
	Level              InsightLevel `gorm:"type:text;not null" json:"level"`
	DateStart          time.Time    `gorm:"type:date;not null" json:"date_start"`
	DateStop           time.Time    `gorm:"type:date;not null" json:"date_stop"`
	WindowStart        time.Time    `gorm:"not null" json:"window_start"`
	WindowEnd          time.Time    `gorm:"not null" json:"window_end"`
	AccountTimezone    string       `gorm:"not null;default:''" json:"account_timezone"`
	AttributionSetting string       `gorm:"not null;default:''" json:"attribution_setting"`
	QueryHash          string       `gorm:"not null" json:"query_hash"`
	Impressions        int64        `gorm:"not null;default:0" json:"impressions"`
	Reach              int64        `gorm:"not null;default:0" json:"reach"`
	Clicks             int64        `gorm:"not null;default:0" json:"clicks"`
	LinkClicks         int64        `gorm:"not null;default:0" json:"link_clicks"`
	LandingPageViews   float64      `gorm:"not null;default:0" json:"landing_page_views"`
	Actions            float64      `gorm:"not null;default:0" json:"actions"`
	Conversions        float64      `gorm:"not null;default:0" json:"conversions"`
	Leads              float64      `gorm:"not null;default:0" json:"leads"`
	Registrations      float64      `gorm:"not null;default:0" json:"registrations"`
	Purchases          float64      `gorm:"not null;default:0" json:"purchases"`
	Spend              float64      `gorm:"type:numeric(24,8);not null;default:0" json:"spend"`
	Frequency          float64      `gorm:"type:numeric(24,8);not null;default:0" json:"frequency"`
	CTR                float64      `gorm:"type:numeric(24,8);not null;default:0" json:"ctr"`
	CPC                float64      `gorm:"type:numeric(24,8);not null;default:0" json:"cpc"`
	CPM                float64      `gorm:"type:numeric(24,8);not null;default:0" json:"cpm"`
	CostPerAction      float64      `gorm:"type:numeric(24,8);not null;default:0" json:"cost_per_action"`
	PurchaseValue      float64      `gorm:"type:numeric(24,8);not null;default:0" json:"purchase_value"`
	ROAS               float64      `gorm:"type:numeric(24,8);not null;default:0" json:"roas"`
	Breakdowns         JSON         `gorm:"type:jsonb;not null;default:'{}'" json:"breakdowns"`
	Metrics            JSON         `gorm:"type:jsonb;not null;default:'{}'" json:"metrics"`
	RawJSON            JSON         `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	FetchedAt          time.Time    `gorm:"not null" json:"fetched_at"`
}

func (InsightSnapshot) TableName() string { return "insight_snapshots" }

// CampaignGuard is the spend-checkpoint ladder that replaces free-form
// automation rules. Every guarded campaign is re-checked when its lifetime
// spend crosses the next checkpoint; a checkpoint whose thresholds are not met
// pauses the campaign. A guard scoped to a single published campaign takes
// precedence over its batch-wide guard.
type CampaignGuard struct {
	Model
	ConnectionID              uuid.UUID   `gorm:"type:uuid;not null" json:"connection_id"`
	BatchID                   *uuid.UUID  `gorm:"type:uuid" json:"batch_id,omitempty"`
	PublishedObjectID         *uuid.UUID  `gorm:"type:uuid" json:"published_object_id,omitempty"`
	Name                      string      `gorm:"not null" json:"name"`
	Status                    GuardStatus `gorm:"type:text;not null" json:"status"`
	Checkpoints               JSON        `gorm:"type:jsonb;not null;default:'[]'" json:"checkpoints"`
	EvaluationIntervalSeconds int64       `gorm:"not null" json:"evaluation_interval_seconds"`
	NextEvaluationAt          time.Time   `gorm:"not null" json:"next_evaluation_at"`
	LastEvaluatedAt           *time.Time  `json:"last_evaluated_at,omitempty"`
}

func (CampaignGuard) TableName() string { return "campaign_guards" }

// GuardCheck is the durable outcome of one checkpoint for one campaign. At
// most one row exists per (guard, campaign, checkpoint index); an operator can
// override a failed check to let the campaign continue to the next checkpoint.
type GuardCheck struct {
	Model
	GuardID           uuid.UUID        `gorm:"type:uuid;not null" json:"guard_id"`
	PublishedObjectID uuid.UUID        `gorm:"type:uuid;not null" json:"published_object_id"`
	MetaObjectID      string           `gorm:"not null" json:"meta_object_id"`
	CheckpointIndex   int              `gorm:"not null" json:"checkpoint_index"`
	CheckpointSpend   float64          `gorm:"type:numeric(24,8);not null" json:"checkpoint_spend"`
	Status            GuardCheckStatus `gorm:"type:text;not null" json:"status"`
	Observed          JSON             `gorm:"type:jsonb;not null;default:'{}'" json:"observed"`
	Thresholds        JSON             `gorm:"type:jsonb;not null;default:'{}'" json:"thresholds"`
	Paused            bool             `gorm:"not null;default:false" json:"paused"`
	Error             string           `gorm:"not null;default:''" json:"error,omitempty"`
	EvaluatedAt       time.Time        `gorm:"not null" json:"evaluated_at"`
}

func (GuardCheck) TableName() string { return "guard_checks" }

// TrackerStat is the latest Keitaro roll-up for one Facebook campaign.
// Rows are matched by campaign ID (sub_id_7) first and campaign name
// (sub_id_3) otherwise. Leads are tracker registrations and sales are
// deposits.
type TrackerStat struct {
	Model
	ConnectionID      *uuid.UUID `gorm:"type:uuid" json:"connection_id,omitempty"`
	PublishedObjectID *uuid.UUID `gorm:"type:uuid" json:"published_object_id,omitempty"`
	MetaCampaignID    string     `gorm:"not null;default:''" json:"meta_campaign_id"`
	CampaignName      string     `gorm:"not null;default:''" json:"campaign_name"`
	Clicks            int64      `gorm:"not null;default:0" json:"clicks"`
	UniqueClicks      int64      `gorm:"not null;default:0" json:"unique_clicks"`
	Leads             float64    `gorm:"type:numeric(24,8);not null;default:0" json:"leads"`
	Sales             float64    `gorm:"type:numeric(24,8);not null;default:0" json:"sales"`
	Revenue           float64    `gorm:"type:numeric(24,8);not null;default:0" json:"revenue"`
	Raw               JSON       `gorm:"type:jsonb;not null;default:'{}'" json:"raw"`
	LastSyncedAt      time.Time  `gorm:"not null" json:"last_synced_at"`
}

func (TrackerStat) TableName() string { return "tracker_stats" }

type AuditEvent struct {
	ID           uuid.UUID     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ConnectionID *uuid.UUID    `gorm:"type:uuid" json:"connection_id,omitempty"`
	ActorType    string        `gorm:"not null" json:"actor_type"`
	ActorID      string        `gorm:"not null;default:''" json:"actor_id"`
	Action       string        `gorm:"not null" json:"action"`
	EntityType   string        `gorm:"not null" json:"entity_type"`
	EntityID     string        `gorm:"not null;default:''" json:"entity_id"`
	Severity     AuditSeverity `gorm:"type:text;not null" json:"severity"`
	RequestID    string        `gorm:"not null;default:''" json:"request_id,omitempty"`
	IPAddress    string        `gorm:"not null;default:''" json:"ip_address,omitempty"`
	Before       JSON          `gorm:"type:jsonb;not null;default:'{}'" json:"before"`
	After        JSON          `gorm:"type:jsonb;not null;default:'{}'" json:"after"`
	Metadata     JSON          `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt    time.Time     `gorm:"not null" json:"created_at"`
}

func (AuditEvent) TableName() string { return "audit_events" }

func (a *AuditEvent) BeforeCreate(_ *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// APIKey is a per-user bearer credential. Only the SHA-256 hash is stored,
// matching UserSession: a database disclosure must not yield usable
// credentials.
type APIKey struct {
	Model
	UserID     uuid.UUID  `gorm:"type:uuid;not null" json:"user_id"`
	Name       string     `gorm:"not null;default:''" json:"name"`
	TokenHash  []byte     `gorm:"not null" json:"-"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func (APIKey) TableName() string { return "api_keys" }
