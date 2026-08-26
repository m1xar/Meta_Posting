package config

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestMetaUploadTimeoutConfiguration(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("META_APP_ID", "app-id")
	t.Setenv("META_APP_SECRET", "app-secret")
	t.Setenv("META_OAUTH_REDIRECT_URI", "https://example.test/oauth/facebook/callback")
	t.Setenv("META_REQUEST_TIMEOUT", "")
	t.Setenv("META_UPLOAD_TIMEOUT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if cfg.Meta.RequestTimeout != 30*time.Second {
		t.Fatalf("request timeout = %s", cfg.Meta.RequestTimeout)
	}
	if cfg.Meta.UploadTimeout != 30*time.Minute {
		t.Fatalf("upload timeout = %s", cfg.Meta.UploadTimeout)
	}
	if cfg.Worker.Concurrency != 4 {
		t.Fatalf("worker concurrency = %d", cfg.Worker.Concurrency)
	}

	t.Setenv("META_UPLOAD_TIMEOUT", "47m")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load override: %v", err)
	}
	if cfg.Meta.UploadTimeout != 47*time.Minute {
		t.Fatalf("upload timeout override = %s", cfg.Meta.UploadTimeout)
	}
}

func TestConfigurationRejectsMalformedOrNonPositiveRuntimeValues(t *testing.T) {
	key := make([]byte, 32)
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TOKEN_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("META_APP_ID", "app-id")
	t.Setenv("META_APP_SECRET", "app-secret")
	t.Setenv("META_OAUTH_REDIRECT_URI", "https://example.test/oauth/facebook/callback")

	t.Setenv("META_REQUEST_TIMEOUT", "eventually")
	if _, err := Load(); err == nil {
		t.Fatal("malformed duration was accepted")
	}

	t.Setenv("META_REQUEST_TIMEOUT", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("zero request timeout was accepted")
	}

	t.Setenv("META_REQUEST_TIMEOUT", "30s")
	t.Setenv("JOB_MAX_ATTEMPTS", "many")
	if _, err := Load(); err == nil {
		t.Fatal("malformed max attempts was accepted")
	}

	t.Setenv("JOB_MAX_ATTEMPTS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero max attempts was accepted")
	}

	t.Setenv("JOB_MAX_ATTEMPTS", "5")
	t.Setenv("WORKER_CONCURRENCY", "65")
	if _, err := Load(); err == nil {
		t.Fatal("excessive worker concurrency was accepted")
	}
}
