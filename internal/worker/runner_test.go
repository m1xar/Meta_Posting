package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"gorm.io/gorm"
)

func TestRunnerDispatchesSupportedJobs(t *testing.T) {
	connectionID := uuid.New()
	resultID := uuid.New()
	tests := []struct {
		name       string
		jobType    string
		payload    domain.JSON
		connection *uuid.UUID
		assertCall func(*testing.T, *fakeJobService)
	}{
		{
			name:       "sync",
			jobType:    application.JobSyncConnection,
			payload:    domain.MustJSON(application.SyncJobPayload{ConnectionID: connectionID}),
			connection: &connectionID,
			assertCall: func(t *testing.T, service *fakeJobService) {
				require.Equal(t, []uuid.UUID{connectionID}, service.syncCalls)
			},
		},
		{
			name:       "publish",
			jobType:    application.JobPublishAccount,
			payload:    domain.MustJSON(application.PublishJobPayload{ResultID: resultID}),
			connection: &connectionID,
			assertCall: func(t *testing.T, service *fakeJobService) {
				require.Equal(t, []uuid.UUID{resultID}, service.publishCalls)
			},
		},
		{
			name:       "insights",
			jobType:    application.JobCollectInsights,
			payload:    domain.MustJSON(application.InsightsJobPayload{ConnectionID: connectionID}),
			connection: &connectionID,
			assertCall: func(t *testing.T, service *fakeJobService) {
				require.Equal(t, []uuid.UUID{connectionID}, service.insightCalls)
			},
		},
		{
			name:       "rules",
			jobType:    application.JobEvaluateGuards,
			payload:    domain.MustJSON(application.EvaluateGuardsJobPayload{ConnectionID: &connectionID}),
			connection: &connectionID,
			assertCall: func(t *testing.T, service *fakeJobService) {
				require.Len(t, service.ruleCalls, 1)
				require.NotNil(t, service.ruleCalls[0])
				require.Equal(t, connectionID, *service.ruleCalls[0])
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := claimedJob(test.jobType, test.payload)
			job.ConnectionID = test.connection
			store := &fakeJobStore{claims: []*domain.Job{job}}
			service := &fakeJobService{}
			runner := newTestRunner(t, service, store, RunnerOptions{})

			processed, err := runner.ProcessOne(context.Background())
			require.NoError(t, err)
			require.True(t, processed)
			require.Equal(t, []uuid.UUID{job.ID}, store.completed)
			require.Empty(t, store.failed)
			test.assertCall(t, service)
		})
	}
}

func TestRunnerFailsWithRetryDelayAndTruncatedError(t *testing.T) {
	connectionID := uuid.New()
	job := claimedJob(
		application.JobCollectInsights,
		domain.MustJSON(application.InsightsJobPayload{ConnectionID: connectionID}),
	)
	job.ConnectionID = &connectionID
	job.Attempts = 3
	job.MaxAttempts = 5
	store := &fakeJobStore{claims: []*domain.Job{job}}
	service := &fakeJobService{insightErr: errors.New(strings.Repeat("é", 100))}
	runner := newTestRunner(t, service, store, RunnerOptions{
		RetryBaseDelay: time.Second,
		RetryMaxDelay:  10 * time.Second,
		MaxErrorBytes:  7,
	})

	processed, err := runner.ProcessOne(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Empty(t, store.completed)
	require.Len(t, store.failed, 1)
	require.Equal(t, 4*time.Second, store.failed[0].delay)
	require.LessOrEqual(t, len(store.failed[0].message), 7)
	require.True(t, utf8.ValidString(store.failed[0].message))
}

func TestRunnerCancelsHandlerWhenLeaseIsLost(t *testing.T) {
	connectionID := uuid.New()
	job := claimedJob(
		application.JobCollectInsights,
		domain.MustJSON(application.InsightsJobPayload{ConnectionID: connectionID}),
	)
	job.ConnectionID = &connectionID
	store := &fakeJobStore{
		claims:    []*domain.Job{job},
		extendErr: database.ErrLeaseLost,
	}
	service := &fakeJobService{blockInsights: true}
	runner := newTestRunner(t, service, store, RunnerOptions{LeaseDuration: 30 * time.Millisecond})

	processed, err := runner.ProcessOne(context.Background())
	require.True(t, processed)
	require.ErrorIs(t, err, database.ErrLeaseLost)
	require.Empty(t, store.completed)
	require.Empty(t, store.failed)
	require.GreaterOrEqual(t, store.extendCalls, 1)
}

func TestRunnerReleasesJobForRetryOnGracefulCancellation(t *testing.T) {
	connectionID := uuid.New()
	job := claimedJob(
		application.JobCollectInsights,
		domain.MustJSON(application.InsightsJobPayload{ConnectionID: connectionID}),
	)
	job.ConnectionID = &connectionID
	store := &fakeJobStore{claims: []*domain.Job{job}}
	started := make(chan struct{})
	service := &fakeJobService{blockInsights: true, insightStarted: started}
	runner := newTestRunner(t, service, store, RunnerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := runner.ProcessOne(ctx)
		result <- err
	}()
	<-started
	cancel()

	require.NoError(t, <-result)
	require.Empty(t, store.completed)
	require.Len(t, store.failed, 1)
	require.Contains(t, store.failed[0].message, context.Canceled.Error())
}

func TestRunnerWaitsWhenQueueIsEmpty(t *testing.T) {
	store := &fakeJobStore{}
	service := &fakeJobService{}
	ctx, cancel := context.WithCancel(context.Background())
	waitCalls := 0
	runner := newTestRunner(t, service, store, RunnerOptions{
		Wait: func(context.Context, time.Duration) error {
			waitCalls++
			cancel()
			return context.Canceled
		},
	})
	require.NoError(t, runner.Run(ctx))
	require.Equal(t, 1, waitCalls)
	require.Equal(t, 1, store.claimCalls)
}

func TestRunnerRejectsMismatchedConnectionPayload(t *testing.T) {
	payloadConnection := uuid.New()
	jobConnection := uuid.New()
	job := claimedJob(
		application.JobSyncConnection,
		domain.MustJSON(application.SyncJobPayload{ConnectionID: payloadConnection}),
	)
	job.ConnectionID = &jobConnection
	store := &fakeJobStore{claims: []*domain.Job{job}}
	runner := newTestRunner(t, &fakeJobService{}, store, RunnerOptions{})

	processed, err := runner.ProcessOne(context.Background())
	require.NoError(t, err)
	require.True(t, processed)
	require.Len(t, store.failed, 1)
	require.Contains(t, store.failed[0].message, "does not match")
}

func newTestRunner(
	t *testing.T,
	service JobService,
	store JobStore,
	options RunnerOptions,
) *Runner {
	t.Helper()
	if options.WorkerID == "" {
		options.WorkerID = "worker-test"
	}
	if options.PollInterval == 0 {
		options.PollInterval = time.Millisecond
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = time.Minute
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	runner, err := NewRunner(service, store, options)
	require.NoError(t, err)
	return runner
}

func claimedJob(jobType string, payload domain.JSON) *domain.Job {
	now := time.Now().UTC()
	return &domain.Job{
		Model:       domain.Model{ID: uuid.New(), CreatedAt: now, UpdatedAt: now},
		Type:        jobType,
		Status:      domain.JobRunning,
		Payload:     payload,
		Attempts:    1,
		MaxAttempts: 5,
		AvailableAt: now,
		LockedBy:    "worker-test",
		LockedAt:    &now,
		LockedUntil: ptrTime(now.Add(time.Minute)),
	}
}

type fakeJobService struct {
	mu             sync.Mutex
	syncCalls      []uuid.UUID
	publishCalls   []uuid.UUID
	insightCalls   []uuid.UUID
	ruleCalls      []*uuid.UUID
	syncErr        error
	publishErr     error
	insightErr     error
	ruleErr        error
	blockInsights  bool
	insightStarted chan struct{}
	startOnce      sync.Once
}

func (s *fakeJobService) SyncConnection(_ context.Context, id uuid.UUID) (application.SyncSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncCalls = append(s.syncCalls, id)
	return application.SyncSummary{}, s.syncErr
}

func (s *fakeJobService) PublishAccountResult(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishCalls = append(s.publishCalls, id)
	return s.publishErr
}

func (s *fakeJobService) CollectInsights(ctx context.Context, id uuid.UUID) error {
	s.mu.Lock()
	s.insightCalls = append(s.insightCalls, id)
	block := s.blockInsights
	started := s.insightStarted
	err := s.insightErr
	s.mu.Unlock()
	if started != nil {
		s.startOnce.Do(func() { close(started) })
	}
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (s *fakeJobService) SyncTrackerStats(_ context.Context) error { return nil }

func (s *fakeJobService) EvaluateDueGuards(_ context.Context, id *uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == nil {
		s.ruleCalls = append(s.ruleCalls, nil)
	} else {
		copyID := *id
		s.ruleCalls = append(s.ruleCalls, &copyID)
	}
	return s.ruleErr
}

type failRecord struct {
	id      uuid.UUID
	message string
	delay   time.Duration
}

type fakeJobStore struct {
	mu          sync.Mutex
	claims      []*domain.Job
	claimErr    error
	claimCalls  int
	completed   []uuid.UUID
	failed      []failRecord
	extendErr   error
	extendCalls int
}

func (s *fakeJobStore) Claim(context.Context, string, time.Duration, time.Time) (*domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimCalls++
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if len(s.claims) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	job := s.claims[0]
	s.claims = s.claims[1:]
	return job, nil
}

func (s *fakeJobStore) ExtendLease(context.Context, uuid.UUID, string, time.Duration, time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extendCalls++
	return s.extendErr
}

func (s *fakeJobStore) Complete(_ context.Context, id uuid.UUID, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, id)
	return nil
}

func (s *fakeJobStore) Fail(
	_ context.Context,
	id uuid.UUID,
	_ string,
	message string,
	delay time.Duration,
	_ time.Time,
) (*domain.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, failRecord{id: id, message: message, delay: delay})
	return &domain.Job{Status: domain.JobPending}, nil
}

func ptrTime(value time.Time) *time.Time { return &value }
