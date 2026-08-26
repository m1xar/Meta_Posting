package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/config"
	"github.com/watchers-factory/raze-posting/internal/keitaro"
	"github.com/watchers-factory/raze-posting/internal/meta"
	platformcrypto "github.com/watchers-factory/raze-posting/internal/platform/crypto"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/storage"
	"github.com/watchers-factory/raze-posting/internal/worker"
)

const workerStorageLimit = int64(1 << 30)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	openCtx, cancelOpen := context.WithTimeout(signalCtx, 30*time.Second)
	db, err := database.Open(openCtx, cfg.DatabaseURL)
	cancelOpen()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(db); closeErr != nil {
			logger.Error("close database", "error", closeErr)
		}
	}()

	repositories := database.NewRepositories(db)
	cipher, err := platformcrypto.NewAESGCM(cfg.TokenEncryptionKey)
	if err != nil {
		return fmt.Errorf("initialize token cipher: %w", err)
	}
	metaClient, err := meta.NewClient(meta.ClientConfig{
		AppID:            cfg.Meta.AppID,
		AppSecret:        cfg.Meta.AppSecret,
		APIVersion:       cfg.Meta.APIVersion,
		HTTPClient:       productionHTTPClient(cfg.Meta.RequestTimeout),
		UploadHTTPClient: productionHTTPClient(cfg.Meta.UploadTimeout),
	})
	if err != nil {
		return fmt.Errorf("initialize Meta client: %w", err)
	}
	localStorage, err := storage.NewLocal(cfg.UploadsDir, workerStorageLimit)
	if err != nil {
		return fmt.Errorf("initialize local storage: %w", err)
	}
	service, err := application.NewService(cfg, repositories, metaClient, cipher, localStorage)
	if err != nil {
		return fmt.Errorf("initialize application service: %w", err)
	}
	if cfg.Keitaro.Enabled() {
		trackerClient, trackerErr := keitaro.NewClient(cfg.Keitaro.BaseURL, cfg.Keitaro.APIKey, cfg.Keitaro.RequestTimeout)
		if trackerErr != nil {
			return fmt.Errorf("initialize Keitaro client: %w", trackerErr)
		}
		service.Tracker = trackerClient
	}

	workerID := newWorkerID()
	runners := make([]*worker.Runner, 0, cfg.Worker.Concurrency)
	for index := 0; index < cfg.Worker.Concurrency; index++ {
		runner, runnerErr := worker.NewRunner(service, repositories.Jobs, worker.RunnerOptions{
			WorkerID:      fmt.Sprintf("%s-%02d", workerID, index+1),
			PollInterval:  cfg.Worker.PollInterval,
			LeaseDuration: cfg.Worker.JobLeaseDuration,
			Logger:        logger,
		})
		if runnerErr != nil {
			return runnerErr
		}
		runners = append(runners, runner)
	}
	scheduler, err := worker.NewScheduler(worker.RepositoryScheduleStore{Repositories: repositories}, worker.SchedulerOptions{
		InsightsInterval: cfg.Worker.InsightsInterval,
		GuardInterval:    cfg.Worker.GuardInterval,
		TrackerInterval:  cfg.Worker.TrackerInterval,
		TrackerEnabled:   cfg.Keitaro.Enabled(),
		MaxAttempts:      cfg.Worker.MaxAttempts,
		Logger:           logger,
	})
	if err != nil {
		return err
	}

	runCtx, cancelRun := context.WithCancel(signalCtx)
	defer cancelRun()
	results := make(chan error, len(runners)+1)
	var components sync.WaitGroup
	components.Add(len(runners) + 1)
	for _, runner := range runners {
		go func(runner *worker.Runner) {
			defer components.Done()
			results <- runner.Run(runCtx)
		}(runner)
	}
	go func() {
		defer components.Done()
		results <- scheduler.Run(runCtx)
	}()

	logger.Info("worker started", "worker_id", workerID, "concurrency", len(runners))
	var runErr error
	select {
	case <-signalCtx.Done():
		logger.Info("worker shutdown requested", "worker_id", workerID)
	case runErr = <-results:
		if runErr != nil {
			logger.Error("worker component failed", "error", runErr)
		}
	}
	cancelRun()
	components.Wait()
	close(results)
	for componentErr := range results {
		if componentErr != nil && !errors.Is(componentErr, context.Canceled) {
			runErr = errors.Join(runErr, componentErr)
		}
	}
	return runErr
}

func newWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString()[:8])
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
