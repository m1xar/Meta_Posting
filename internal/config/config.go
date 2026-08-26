package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/watchers-factory/raze-ads/internal/meta"
)

type Config struct {
	Environment      string
	HTTPAddress      string
	DatabaseURL      string
	UploadsDir       string
	InternalAPIToken string
	// AllowLegacyInternalToken keeps the shared INTERNAL_API_TOKEN working as
	// an admin credential. It reads every tenant and owns nothing, so it
	// defaults off; enable it only while migrating callers to per-user keys.
	AllowLegacyInternalToken bool
	TokenEncryptionKey       []byte
	Meta                     MetaConfig
	Worker                   WorkerConfig
}

type MetaConfig struct {
	AppID          string
	AppSecret      string
	APIVersion     string
	RedirectURI    string
	LoginConfigID  string
	Scopes         []string
	RequestTimeout time.Duration
	UploadTimeout  time.Duration
}

type WorkerConfig struct {
	Concurrency      int
	PollInterval     time.Duration
	InsightsInterval time.Duration
	RuleInterval     time.Duration
	JobLeaseDuration time.Duration
	MaxAttempts      int

	// Per-level polling cadence for account-wide tracking. Cost rises sharply
	// with depth, because time_increment=1 multiplies rows by days: an
	// account-level poll is one row per day, an ad-level poll is one row per
	// ad per day.
	// FastLaneInterval drives sub-cadence rules and the insight collection
	// they depend on. Evaluating a 60-second rule against 15-minute-old
	// numbers is just a stale decision made more often.
	FastLaneInterval         time.Duration
	FastRuleMaxInterval      int64
	DiscoveryInterval        time.Duration
	EntitySyncInterval       time.Duration
	AccountInsightsInterval  time.Duration
	CampaignInsightsInterval time.Duration
	AdSetInsightsInterval    time.Duration
	AdInsightsInterval       time.Duration

	// AdLevelBatchSize bounds how many ad accounts get an ad-level poll per
	// cycle. The rotation cursor makes the remainder wait their turn rather
	// than being skipped.
	AdLevelBatchSize int

	// InsightsLookbackDays re-fetches recent days to pick up late
	// attribution. 28 because 28d_click is Meta's longest standard window, so
	// a conversion attributed to day D can arrive up to 28 days later.
	InsightsLookbackDays int
	InsightsBackfillDays int
	InsightRetention     time.Duration
	RetentionInterval    time.Duration
	MaintenanceInterval  time.Duration
}

func Load() (Config, error) {
	encryptionKey, err := decodeEncryptionKey(os.Getenv("TOKEN_ENCRYPTION_KEY"))
	if err != nil {
		return Config{}, err
	}
	requestTimeout, err := envDuration("META_REQUEST_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	uploadTimeout, err := envDuration("META_UPLOAD_TIMEOUT", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	pollInterval, err := envDuration("WORKER_POLL_INTERVAL", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	insightsInterval, err := envDuration("INSIGHTS_POLL_INTERVAL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	ruleInterval, err := envDuration("RULE_EVALUATION_INTERVAL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	jobLeaseDuration, err := envDuration("JOB_LEASE_DURATION", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	maxAttempts, err := envInt("JOB_MAX_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	workerConcurrency, err := envInt("WORKER_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	fastLaneInterval, err := envDuration("FAST_RULE_LANE_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	fastRuleMaxInterval, err := envInt("FAST_RULE_MAX_INTERVAL_SECONDS", 300)
	if err != nil {
		return Config{}, err
	}
	discoveryInterval, err := envDuration("DISCOVERY_INTERVAL", 6*time.Hour)
	if err != nil {
		return Config{}, err
	}
	entitySyncInterval, err := envDuration("INSIGHTS_ENTITY_SYNC_INTERVAL", 6*time.Hour)
	if err != nil {
		return Config{}, err
	}
	accountInsightsInterval, err := envDuration("INSIGHTS_ACCOUNT_INTERVAL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	campaignInsightsInterval, err := envDuration("INSIGHTS_CAMPAIGN_INTERVAL", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	adSetInsightsInterval, err := envDuration("INSIGHTS_ADSET_INTERVAL", 2*time.Hour)
	if err != nil {
		return Config{}, err
	}
	adInsightsInterval, err := envDuration("INSIGHTS_AD_INTERVAL", 6*time.Hour)
	if err != nil {
		return Config{}, err
	}
	adLevelBatchSize, err := envInt("INSIGHTS_AD_LEVEL_BATCH", 25)
	if err != nil {
		return Config{}, err
	}
	lookbackDays, err := envInt("INSIGHTS_LOOKBACK_DAYS", 28)
	if err != nil {
		return Config{}, err
	}
	backfillDays, err := envInt("INSIGHTS_BACKFILL_DAYS", 90)
	if err != nil {
		return Config{}, err
	}
	insightRetention, err := envDuration("INSIGHTS_RETENTION", 90*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	retentionInterval, err := envDuration("INSIGHTS_RETENTION_INTERVAL", time.Hour)
	if err != nil {
		return Config{}, err
	}
	maintenanceInterval, err := envDuration("WORKER_MAINTENANCE_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment:              envOr("APP_ENV", "development"),
		HTTPAddress:              envOr("HTTP_ADDRESS", ":8080"),
		DatabaseURL:              strings.TrimSpace(os.Getenv("DATABASE_URL")),
		UploadsDir:               envOr("UPLOADS_DIR", "./uploads"),
		InternalAPIToken:         strings.TrimSpace(os.Getenv("INTERNAL_API_TOKEN")),
		AllowLegacyInternalToken: envBool("ALLOW_LEGACY_INTERNAL_TOKEN", false),
		TokenEncryptionKey:       encryptionKey,
		Meta: MetaConfig{
			AppID:          strings.TrimSpace(os.Getenv("META_APP_ID")),
			AppSecret:      strings.TrimSpace(os.Getenv("META_APP_SECRET")),
			APIVersion:     envOr("META_API_VERSION", meta.DefaultAPIVersion),
			RedirectURI:    strings.TrimSpace(os.Getenv("META_OAUTH_REDIRECT_URI")),
			LoginConfigID:  strings.TrimSpace(os.Getenv("META_LOGIN_CONFIG_ID")),
			Scopes:         splitCSV(envOr("META_OAUTH_SCOPES", "ads_management,ads_read,business_management,pages_show_list,pages_read_engagement")),
			RequestTimeout: requestTimeout,
			UploadTimeout:  uploadTimeout,
		},
		Worker: WorkerConfig{
			Concurrency:      workerConcurrency,
			PollInterval:     pollInterval,
			InsightsInterval: insightsInterval,
			RuleInterval:     ruleInterval,
			JobLeaseDuration: jobLeaseDuration,
			MaxAttempts:      maxAttempts,

			FastLaneInterval:         fastLaneInterval,
			FastRuleMaxInterval:      int64(fastRuleMaxInterval),
			DiscoveryInterval:        discoveryInterval,
			EntitySyncInterval:       entitySyncInterval,
			AccountInsightsInterval:  accountInsightsInterval,
			CampaignInsightsInterval: campaignInsightsInterval,
			AdSetInsightsInterval:    adSetInsightsInterval,
			AdInsightsInterval:       adInsightsInterval,
			AdLevelBatchSize:         adLevelBatchSize,
			InsightsLookbackDays:     lookbackDays,
			InsightsBackfillDays:     backfillDays,
			InsightRetention:         insightRetention,
			RetentionInterval:        retentionInterval,
			MaintenanceInterval:      maintenanceInterval,
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	for name, value := range map[string]string{
		"DATABASE_URL":            c.DatabaseURL,
		"INTERNAL_API_TOKEN":      c.InternalAPIToken,
		"META_APP_ID":             c.Meta.AppID,
		"META_APP_SECRET":         c.Meta.AppSecret,
		"META_OAUTH_REDIRECT_URI": c.Meta.RedirectURI,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(c.TokenEncryptionKey) != 32 {
		missing = append(missing, "TOKEN_ENCRYPTION_KEY(32-byte base64)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing or invalid configuration: %s", strings.Join(missing, ", "))
	}
	if !strings.HasPrefix(c.Meta.APIVersion, "v") {
		return errors.New("META_API_VERSION must start with v")
	}
	for name, value := range map[string]time.Duration{
		"META_REQUEST_TIMEOUT":          c.Meta.RequestTimeout,
		"META_UPLOAD_TIMEOUT":           c.Meta.UploadTimeout,
		"WORKER_POLL_INTERVAL":          c.Worker.PollInterval,
		"INSIGHTS_POLL_INTERVAL":        c.Worker.InsightsInterval,
		"RULE_EVALUATION_INTERVAL":      c.Worker.RuleInterval,
		"JOB_LEASE_DURATION":            c.Worker.JobLeaseDuration,
		"FAST_RULE_LANE_INTERVAL":       c.Worker.FastLaneInterval,
		"DISCOVERY_INTERVAL":            c.Worker.DiscoveryInterval,
		"INSIGHTS_ENTITY_SYNC_INTERVAL": c.Worker.EntitySyncInterval,
		"INSIGHTS_ACCOUNT_INTERVAL":     c.Worker.AccountInsightsInterval,
		"INSIGHTS_CAMPAIGN_INTERVAL":    c.Worker.CampaignInsightsInterval,
		"INSIGHTS_ADSET_INTERVAL":       c.Worker.AdSetInsightsInterval,
		"INSIGHTS_AD_INTERVAL":          c.Worker.AdInsightsInterval,
		"INSIGHTS_RETENTION":            c.Worker.InsightRetention,
		"INSIGHTS_RETENTION_INTERVAL":   c.Worker.RetentionInterval,
		"WORKER_MAINTENANCE_INTERVAL":   c.Worker.MaintenanceInterval,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be greater than zero", name)
		}
	}
	if c.Worker.MaxAttempts <= 0 {
		return errors.New("JOB_MAX_ATTEMPTS must be greater than zero")
	}
	if c.Worker.Concurrency <= 0 || c.Worker.Concurrency > 64 {
		return errors.New("WORKER_CONCURRENCY must be between 1 and 64")
	}
	if c.Worker.AdLevelBatchSize <= 0 {
		return errors.New("INSIGHTS_AD_LEVEL_BATCH must be greater than zero")
	}
	// 28d_click is Meta's longest standard attribution window, so anything
	// shorter silently under-reports conversions that land late.
	if c.Worker.InsightsLookbackDays < 1 || c.Worker.InsightsLookbackDays > 90 {
		return errors.New("INSIGHTS_LOOKBACK_DAYS must be between 1 and 90")
	}
	// Meta retains insights for 37 months; asking for more is a guaranteed
	// wasted sweep to the start of the range.
	if c.Worker.InsightsBackfillDays < 0 || c.Worker.InsightsBackfillDays > 1100 {
		return errors.New("INSIGHTS_BACKFILL_DAYS must be between 0 and 1100")
	}
	// Retention must outlive the rolling windows the automation engine reads,
	// or a sweep would delete the history a rule needs and the rule would
	// silently stop matching. The lookback is the longest window ingestion
	// itself re-reads.
	minimumRetention := time.Duration(c.Worker.InsightsLookbackDays) * 24 * time.Hour * 2
	if c.Worker.InsightRetention < minimumRetention {
		return fmt.Errorf(
			"INSIGHTS_RETENTION must be at least %s, twice the %d-day lookback, "+
				"so a sweep cannot delete history the rules engine still reads",
			minimumRetention, c.Worker.InsightsLookbackDays,
		)
	}
	return nil
}

func decodeEncryptionKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode TOKEN_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("TOKEN_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", name, err)
	}
	return parsed, nil
}

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
