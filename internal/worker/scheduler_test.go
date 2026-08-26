package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

func TestSchedulerEnqueuesPerConnectionWithStableBucket(t *testing.T) {
	first := uuid.New()
	second := uuid.New()
	store := &fakeScheduleStore{connectionIDs: []uuid.UUID{first, second}}
	scheduler := newTestScheduler(t, store)
	now := time.Date(2026, 7, 23, 12, 7, 30, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleInsights(context.Background(), now))
	require.NoError(t, scheduler.ScheduleGuardEvaluations(context.Background(), now))
	require.Len(t, store.jobs, 4)

	for index, job := range store.jobs {
		require.Equal(t, domain.JobPending, job.Status)
		require.Equal(t, 7, job.MaxAttempts)
		require.Equal(t, now, job.AvailableAt)
		require.NotNil(t, job.DedupeKey)
		connectionID := first
		if index%2 == 1 {
			connectionID = second
		}
		require.Equal(t, connectionID, *job.ConnectionID)
		require.Equal(t, connectionID.String()+":1784808000", *job.DedupeKey)
	}
	require.Equal(t, application.JobCollectInsights, store.jobs[0].Type)
	require.Equal(t, application.JobCollectInsights, store.jobs[1].Type)
	require.Equal(t, application.JobEvaluateGuards, store.jobs[2].Type)
	require.Equal(t, application.JobEvaluateGuards, store.jobs[3].Type)
	require.Equal(t, 20, store.jobs[0].Priority)
	require.Equal(t, 10, store.jobs[2].Priority)

	var insights application.InsightsJobPayload
	require.NoError(t, store.jobs[0].Payload.Decode(&insights))
	require.Equal(t, first, insights.ConnectionID)
	var rules application.EvaluateGuardsJobPayload
	require.NoError(t, store.jobs[2].Payload.Decode(&rules))
	require.NotNil(t, rules.ConnectionID)
	require.Equal(t, first, *rules.ConnectionID)
}

func TestSchedulerUsesSameDedupeKeyWithinBucket(t *testing.T) {
	connectionID := uuid.New()
	store := &fakeScheduleStore{connectionIDs: []uuid.UUID{connectionID}}
	scheduler := newTestScheduler(t, store)
	first := time.Date(2026, 7, 23, 12, 1, 0, 0, time.UTC)
	second := time.Date(2026, 7, 23, 12, 14, 59, 0, time.UTC)

	require.NoError(t, scheduler.ScheduleInsights(context.Background(), first))
	require.NoError(t, scheduler.ScheduleInsights(context.Background(), second))
	require.Len(t, store.jobs, 2)
	require.Equal(t, *store.jobs[0].DedupeKey, *store.jobs[1].DedupeKey)
}

func TestSchedulerExpiresOAuthSessions(t *testing.T) {
	store := &fakeScheduleStore{expired: 3}
	scheduler := newTestScheduler(t, store)
	now := time.Now().UTC()
	expired, err := scheduler.ExpireOAuthSessions(context.Background(), now)
	require.NoError(t, err)
	require.EqualValues(t, 3, expired)
	require.Equal(t, []time.Time{now}, store.expiryCalls)
}

func TestScheduleBucket(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 17, 0, 0, time.UTC)
	require.Equal(t, "1784808900", scheduleBucket(now, 15*time.Minute))
	require.Equal(t, "1784809800", scheduleBucket(now.Add(15*time.Minute), 15*time.Minute))
}

func newTestScheduler(t *testing.T, store ScheduleStore) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(store, SchedulerOptions{
		InsightsInterval:    15 * time.Minute,
		RuleInterval:        15 * time.Minute,
		MaintenanceInterval: time.Minute,
		MaxAttempts:         7,
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	return scheduler
}

type fakeScheduleStore struct {
	mu            sync.Mutex
	connectionIDs []uuid.UUID
	jobs          []*domain.Job
	expired       int64
	expiryCalls   []time.Time
	activeErr     error
	enqueueErr    error
	expireErr     error
}

func (s *fakeScheduleStore) ActiveConnectionIDs(context.Context) ([]uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uuid.UUID(nil), s.connectionIDs...), s.activeErr
}

func (s *fakeScheduleStore) ExpirePendingOAuth(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expiryCalls = append(s.expiryCalls, now)
	return s.expired, s.expireErr
}

func (s *fakeScheduleStore) Enqueue(_ context.Context, job *domain.Job) (*domain.Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyJob := *job
	s.jobs = append(s.jobs, &copyJob)
	return &copyJob, true, s.enqueueErr
}
