package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultGraphAPIVersion = "v25.0"

type Config struct {
	Environment        string
	HTTPAddress        string
	DatabaseURL        string
	UploadsDir         string
	TokenEncryptionKey []byte
	Meta               MetaConfig
	Keitaro            KeitaroConfig
	Worker             WorkerConfig
}

// KeitaroConfig points the service at the Keitaro tracker Admin API. When the
// base URL or API key is empty the tracker sync stays disabled and guard
// checkpoints only see Facebook metrics.
type KeitaroConfig struct {
	BaseURL        string
	APIKey         string
	RequestTimeout time.Duration
}

func (k KeitaroConfig) Enabled() bool {
	return strings.TrimSpace(k.BaseURL) != "" && strings.TrimSpace(k.APIKey) != ""
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
	GuardInterval    time.Duration
	TrackerInterval  time.Duration
	JobLeaseDuration time.Duration
	MaxAttempts      int
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
	guardInterval, err := envDuration("GUARD_EVALUATION_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	trackerInterval, err := envDuration("KEITARO_POLL_INTERVAL", 10*time.Minute)
	if err != nil {
		return Config{}, err
	}
	keitaroTimeout, err := envDuration("KEITARO_REQUEST_TIMEOUT", 30*time.Second)
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

	cfg := Config{
		Environment:        envOr("APP_ENV", "development"),
		HTTPAddress:        envOr("HTTP_ADDRESS", ":8080"),
		DatabaseURL:        strings.TrimSpace(os.Getenv("DATABASE_URL")),
		UploadsDir:         envOr("UPLOADS_DIR", "./uploads"),
		TokenEncryptionKey: encryptionKey,
		Meta: MetaConfig{
			AppID:          strings.TrimSpace(os.Getenv("META_APP_ID")),
			AppSecret:      strings.TrimSpace(os.Getenv("META_APP_SECRET")),
			APIVersion:     envOr("META_API_VERSION", defaultGraphAPIVersion),
			RedirectURI:    strings.TrimSpace(os.Getenv("META_OAUTH_REDIRECT_URI")),
			LoginConfigID:  strings.TrimSpace(os.Getenv("META_LOGIN_CONFIG_ID")),
			Scopes:         splitCSV(envOr("META_OAUTH_SCOPES", "ads_management,ads_read,business_management,pages_show_list,pages_read_engagement")),
			RequestTimeout: requestTimeout,
			UploadTimeout:  uploadTimeout,
		},
		Keitaro: KeitaroConfig{
			BaseURL:        strings.TrimSpace(os.Getenv("KEITARO_BASE_URL")),
			APIKey:         strings.TrimSpace(os.Getenv("KEITARO_API_KEY")),
			RequestTimeout: keitaroTimeout,
		},
		Worker: WorkerConfig{
			Concurrency:      workerConcurrency,
			PollInterval:     pollInterval,
			InsightsInterval: insightsInterval,
			GuardInterval:    guardInterval,
			TrackerInterval:  trackerInterval,
			JobLeaseDuration: jobLeaseDuration,
			MaxAttempts:      maxAttempts,
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
		"META_REQUEST_TIMEOUT":      c.Meta.RequestTimeout,
		"META_UPLOAD_TIMEOUT":       c.Meta.UploadTimeout,
		"WORKER_POLL_INTERVAL":      c.Worker.PollInterval,
		"INSIGHTS_POLL_INTERVAL":    c.Worker.InsightsInterval,
		"GUARD_EVALUATION_INTERVAL": c.Worker.GuardInterval,
		"KEITARO_POLL_INTERVAL":     c.Worker.TrackerInterval,
		"JOB_LEASE_DURATION":        c.Worker.JobLeaseDuration,
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
