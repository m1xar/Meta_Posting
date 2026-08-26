package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/config"
	"github.com/watchers-factory/raze-posting/internal/httpapi"
	"github.com/watchers-factory/raze-posting/internal/meta"
	platformcrypto "github.com/watchers-factory/raze-posting/internal/platform/crypto"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/storage"
)

const maxUploadBytes = int64(1 << 30)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancelStartup()
	db, err := database.Open(startupContext, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close(db)

	migrationsDir, err := firstExistingDirectory("/app/migrations", "./migrations")
	if err != nil {
		return err
	}
	if err := database.RunMigrations(startupContext, db, migrationsDir); err != nil {
		return err
	}

	cipher, err := platformcrypto.NewAESGCM(cfg.TokenEncryptionKey)
	if err != nil {
		return fmt.Errorf("initialize token encryption: %w", err)
	}
	graphClient, err := meta.NewClient(meta.ClientConfig{
		AppID:            cfg.Meta.AppID,
		AppSecret:        cfg.Meta.AppSecret,
		APIVersion:       cfg.Meta.APIVersion,
		HTTPClient:       productionHTTPClient(cfg.Meta.RequestTimeout),
		UploadHTTPClient: productionHTTPClient(cfg.Meta.UploadTimeout),
	})
	if err != nil {
		return fmt.Errorf("initialize Meta client: %w", err)
	}
	localStorage, err := storage.NewLocal(cfg.UploadsDir, maxUploadBytes)
	if err != nil {
		return fmt.Errorf("initialize upload storage: %w", err)
	}
	repositories := database.NewRepositories(db)
	service, err := application.NewService(cfg, repositories, graphClient, cipher, localStorage)
	if err != nil {
		return err
	}

	openAPI, err := readFirstFile("/app/openapi/openapi.yaml", "./openapi/openapi.yaml")
	if err != nil {
		return err
	}
	sqlDB, err := database.SQLDB(db)
	if err != nil {
		return err
	}
	server, err := httpapi.New(service, httpapi.Config{
		Environment: cfg.Environment,
		OpenAPI:     openAPI,
		Logger:      logger,
		BodyLimit:   int(maxUploadBytes + (8 << 20)),
		Ready:       sqlDB.PingContext,
	})
	if err != nil {
		return err
	}

	listenErrors := make(chan error, 1)
	go func() {
		logger.Info("Raze Posting API listening", "address", cfg.HTTPAddress, "environment", cfg.Environment)
		listenErrors <- server.App.Listen(cfg.HTTPAddress)
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case received := <-signals:
		logger.Info("shutdown requested", "signal", received.String())
	case listenErr := <-listenErrors:
		if listenErr != nil {
			return fmt.Errorf("listen: %w", listenErr)
		}
		return nil
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := server.App.ShutdownWithContext(shutdownContext); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func productionHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = timeout
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

func firstExistingDirectory(candidates ...string) (string, error) {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", fmt.Errorf("none of the required directories exist: %v", candidates)
}

func readFirstFile(candidates ...string) ([]byte, error) {
	var failures []error
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if err == nil {
			return content, nil
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("read OpenAPI document: %w", errors.Join(failures...))
}
