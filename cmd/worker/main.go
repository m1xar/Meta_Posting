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

	// Ship the timezone database in the binary. Insights dates are resolved in
	// each ad account's timezone, and a container without tzdata would make
	// time.LoadLocation fail into UTC silently.
	_ "time/tzdata"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/config"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
	platformcrypto "github.com/watchers-factory/raze-ads/internal/platform/crypto"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
	"github.com/watchers-factory/raze-ads/internal/storage"
	"github.com/watchers-factory/raze-ads/internal/worker"
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
	scheduleStore := worker.RepositoryScheduleStore{Repositories: repositories}
	scheduler, err := worker.NewScheduler(scheduleStore, worker.SchedulerOptions{
		InsightsInterval:    cfg.Worker.InsightsInterval,
		RuleInterval:        cfg.Worker.RuleInterval,
		MaintenanceInterval: cfg.Worker.MaintenanceInterval,
		MaxAttempts:         cfg.Worker.MaxAttempts,
		Logger:              logger,

		// Cadence falls off with cost. Account and campaign levels are one
		// row per object per day and can be polled for every account each
		// cycle; ad level multiplies by the number of ads, so it rotates
		// through a slice of accounts at a time.
		AccountLevelPlans: []worker.AccountLevelPlan{
			{
				Level:        domain.InsightAccount,
				Interval:     cfg.Worker.AccountInsightsInterval,
				SinceDaysAgo: 1,
				Priority:     18,
			},
			{
				Level:        domain.InsightCampaign,
				Interval:     cfg.Worker.CampaignInsightsInterval,
				SinceDaysAgo: 1,
				Priority:     16,
			},
			{
				Level:        domain.InsightAdSet,
				Interval:     cfg.Worker.AdSetInsightsInterval,
				SinceDaysAgo: 1,
				Priority:     14,
			},
			{
				Level:        domain.InsightAd,
				Interval:     cfg.Worker.AdInsightsInterval,
				BatchSize:    cfg.Worker.AdLevelBatchSize,
				SinceDaysAgo: 1,
				Priority:     12,
			},
		},
		FastLaneInterval:    cfg.Worker.FastLaneInterval,
		FastRuleMaxInterval: cfg.Worker.FastRuleMaxInterval,
		DiscoveryInterval:   cfg.Worker.DiscoveryInterval,
		EntitySyncInterval:  cfg.Worker.EntitySyncInterval,
		AdLevelBatchSize:    cfg.Worker.AdLevelBatchSize,

		// The lookback re-reads the attribution window at campaign level.
		// Running it at ad level too would multiply the cost by the number of
		// ads for a restatement that is visible in the aggregate anyway.
		LookbackLevel:    domain.InsightCampaign,
		LookbackDays:     cfg.Worker.InsightsLookbackDays,
		LookbackInterval: 24 * time.Hour,

		RetentionInterval: cfg.Worker.RetentionInterval,
		InsightRetention:  cfg.Worker.InsightRetention,
	})
	if err != nil {
		return err
	}
	scheduler.WithAccountStore(worker.RepositoryAccountScheduleStore{
		RepositoryScheduleStore: scheduleStore,
	})

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
