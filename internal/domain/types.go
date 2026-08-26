package domain

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// JSON stores schema-flexible Meta payloads while retaining their native JSONB
// representation in PostgreSQL. A nil value is persisted as SQL NULL; use
// EmptyJSONObject or EmptyJSONArray when a non-null empty value is required.
type JSON json.RawMessage

var (
	EmptyJSONObject = JSON(`{}`)
	EmptyJSONArray  = JSON(`[]`)
)

func NewJSON(value any) (JSON, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}
	return JSON(raw), nil
}

func MustJSON(value any) JSON {
	raw, err := NewJSON(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	if !json.Valid(j) {
		return nil, errors.New("invalid JSON value")
	}
	return []byte(j), nil
}

func (j *JSON) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}

	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("scan JSON from %T", value)
	}
	if !json.Valid(raw) {
		return errors.New("database returned invalid JSON")
	}

	*j = append((*j)[:0], raw...)
	return nil
}

func (j JSON) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	if !json.Valid(j) {
		return nil, errors.New("invalid JSON value")
	}
	return j, nil
}

func (j *JSON) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*j = nil
		return nil
	}
	if !json.Valid(data) {
		return errors.New("invalid JSON value")
	}
	*j = append((*j)[:0], data...)
	return nil
}

func (j JSON) Decode(target any) error {
	if j == nil {
		return errors.New("cannot decode null JSON")
	}
	return json.Unmarshal(j, target)
}

type PageRequest struct {
	Limit  int
	Offset int
}

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 500
)

func (p PageRequest) Normalized() PageRequest {
	if p.Limit <= 0 {
		p.Limit = DefaultPageLimit
	}
	if p.Limit > MaxPageLimit {
		p.Limit = MaxPageLimit
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type MetaConnectionStatus string

const (
	MetaConnectionActive       MetaConnectionStatus = "active"
	MetaConnectionExpired      MetaConnectionStatus = "expired"
	MetaConnectionRevoked      MetaConnectionStatus = "revoked"
	MetaConnectionDisconnected MetaConnectionStatus = "disconnected"
	MetaConnectionError        MetaConnectionStatus = "error"
)

type OAuthSessionStatus string

const (
	OAuthSessionPending   OAuthSessionStatus = "pending"
	OAuthSessionConsumed  OAuthSessionStatus = "consumed"
	OAuthSessionCompleted OAuthSessionStatus = "completed"
	OAuthSessionFailed    OAuthSessionStatus = "failed"
	OAuthSessionExpired   OAuthSessionStatus = "expired"
)

type AssetType string

const (
	AssetPage              AssetType = "page"
	AssetInstagramAccount  AssetType = "instagram_account"
	AssetPixel             AssetType = "pixel"
	AssetDataset           AssetType = "dataset"
	AssetCustomConversion  AssetType = "custom_conversion"
	AssetCustomAudience    AssetType = "custom_audience"
	AssetLookalikeAudience AssetType = "lookalike_audience"
	AssetMetaApp           AssetType = "meta_app"
	AssetPost              AssetType = "post"
)

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

type MediaStatus string

const (
	MediaPending    MediaStatus = "pending"
	MediaProcessing MediaStatus = "processing"
	MediaReady      MediaStatus = "ready"
	MediaFailed     MediaStatus = "failed"
)

type BatchStatus string

const (
	BatchDraft              BatchStatus = "draft"
	BatchQueued             BatchStatus = "queued"
	BatchRunning            BatchStatus = "running"
	BatchPartiallySucceeded BatchStatus = "partially_succeeded"
	BatchSucceeded          BatchStatus = "succeeded"
	BatchFailed             BatchStatus = "failed"
	BatchCancelled          BatchStatus = "cancelled"
)

type BatchAccountStatus string

const (
	BatchAccountPending   BatchAccountStatus = "pending"
	BatchAccountRunning   BatchAccountStatus = "running"
	BatchAccountSucceeded BatchAccountStatus = "succeeded"
	BatchAccountFailed    BatchAccountStatus = "failed"
	BatchAccountSkipped   BatchAccountStatus = "skipped"
)

type PublishedObjectType string

const (
	PublishedCampaign PublishedObjectType = "campaign"
	PublishedAdSet    PublishedObjectType = "ad_set"
	PublishedCreative PublishedObjectType = "creative"
	PublishedAd       PublishedObjectType = "ad"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobDead      JobStatus = "dead"
	JobCancelled JobStatus = "cancelled"
)

type InsightLevel string

const (
	InsightAccount  InsightLevel = "account"
	InsightCampaign InsightLevel = "campaign"
	InsightAdSet    InsightLevel = "adset"
	InsightAd       InsightLevel = "ad"
)

type GuardStatus string

const (
	GuardActive   GuardStatus = "active"
	GuardDisabled GuardStatus = "disabled"
)

type GuardCheckStatus string

const (
	GuardCheckPassed     GuardCheckStatus = "passed"
	GuardCheckFailed     GuardCheckStatus = "failed"
	GuardCheckOverridden GuardCheckStatus = "overridden"
)

type AuditSeverity string

const (
	AuditInfo     AuditSeverity = "info"
	AuditWarning  AuditSeverity = "warning"
	AuditCritical AuditSeverity = "critical"
)

// RetryDelay applies capped exponential backoff. Attempts is the number of
// claims already made, so the first failed attempt receives base.
func RetryDelay(attempts int, base, maximum time.Duration) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if maximum <= 0 {
		maximum = time.Hour
	}
	if attempts < 1 {
		attempts = 1
	}

	delay := base
	for i := 1; i < attempts && delay < maximum; i++ {
		if delay > maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if delay > maximum {
		return maximum
	}
	return delay
}
