package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/config"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/rules"
	"gorm.io/gorm"
)

func TestEvaluateObjectSkipsStaleInsightsWithoutPause(t *testing.T) {
	fixture := newAutomationIntegrationFixture(t)
	ctx := fixture.ctx
	repositories := fixture.repositories
	now := fixture.now
	connection := fixture.connection
	account := fixture.account
	batch := fixture.batch

	object := &domain.PublishedObject{
		Model:                domain.Model{CreatedAt: now.Add(-48 * time.Hour)},
		BatchID:              batch.ID,
		BatchAccountResultID: fixture.result.ID,
		ConnectionID:         connection.ID,
		AdAccountID:          account.ID,
		ObjectType:           domain.PublishedCampaign,
		MetaObjectID:         "campaign-" + uuid.NewString(),
		DesiredStatus:        "ACTIVE",
		EffectiveStatus:      "ACTIVE",
		IdempotencyKey:       "published-" + uuid.NewString(),
		RequestJSON:          domain.EmptyJSONObject,
		ResponseJSON:         domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Batches.CheckpointPublishedObject(ctx, object))

	rule := &domain.AutomationRule{
		ConnectionID: connection.ID,
		AdAccountID:  &account.ID,
		Name:         "Would pause on spend",
		Status:       domain.RuleActive,
		ScopeLevel:   domain.InsightCampaign,
		Action:       domain.RuleActionPause,
		Conditions: domain.MustJSON(rules.Group{
			Logic: rules.LogicAll,
			Conditions: []rules.Condition{{
				Metric:    "spend",
				Operator:  rules.OperatorGT,
				Threshold: 1,
			}},
		}),
		LookbackSeconds:           int64((24 * time.Hour) / time.Second),
		EvaluationIntervalSeconds: int64((15 * time.Minute) / time.Second),
		NextEvaluationAt:          now,
		Metadata:                  domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Rules.Create(ctx, rule))

	staleAt := now.Add(-30*time.Minute - time.Second)
	require.NoError(t, repositories.Insights.Upsert(ctx, &domain.InsightSnapshot{
		ConnectionID:      connection.ID,
		AdAccountID:       account.ID,
		PublishedObjectID: &object.ID,
		MetaObjectID:      object.MetaObjectID,
		Level:             domain.InsightCampaign,
		DateStart:         now.Add(-48 * time.Hour),
		DateStop:          now,
		WindowStart:       now.Add(-48 * time.Hour),
		WindowEnd:         staleAt.Add(-24 * time.Hour),
		QueryHash:         LifetimeInsightQueryHash,
		Metrics:           domain.MustJSON(map[string]float64{"spend": 0}),
		Breakdowns:        domain.EmptyJSONObject,
		RawJSON:           domain.EmptyJSONObject,
		FetchedAt:         staleAt.Add(-24 * time.Hour),
	}))
	require.NoError(t, repositories.Insights.Upsert(ctx, &domain.InsightSnapshot{
		ConnectionID:      connection.ID,
		AdAccountID:       account.ID,
		PublishedObjectID: &object.ID,
		MetaObjectID:      object.MetaObjectID,
		Level:             domain.InsightCampaign,
		DateStart:         now.Add(-48 * time.Hour),
		DateStop:          now,
		WindowStart:       now.Add(-48 * time.Hour),
		WindowEnd:         staleAt,
		QueryHash:         LifetimeInsightQueryHash,
		Metrics:           domain.MustJSON(map[string]float64{"spend": 100}),
		Breakdowns:        domain.EmptyJSONObject,
		RawJSON:           domain.EmptyJSONObject,
		FetchedAt:         staleAt,
	}))

	service := &Service{
		Config: config.Config{Worker: config.WorkerConfig{InsightsInterval: 15 * time.Minute}},
		Repos:  repositories,
		Meta:   fixture.metaClient,
		Now:    func() time.Time { return now },
	}
	next := now.Add(15 * time.Minute)
	require.NoError(t, service.evaluateObject(ctx, rule, object, "unused-token", now, next))
	require.Zero(t, fixture.metaCalls.Load(), "a stale snapshot must never call Meta's pause endpoint")

	evaluations, err := repositories.Rules.ListEvaluations(ctx, database.RuleEvaluationFilter{
		RuleID:            rule.ID,
		PublishedObjectID: &object.ID,
	})
	require.NoError(t, err)
	require.Len(t, evaluations.Items, 1)
	require.Equal(t, domain.RuleEvaluationSkipped, evaluations.Items[0].Status)
	require.False(t, evaluations.Items[0].ActionAttempted)
	require.Contains(t, string(evaluations.Items[0].ConditionResults), "stale")

	var storedObject domain.PublishedObject
	require.NoError(t, fixture.tx.First(&storedObject, "id = ?", object.ID).Error)
	require.Equal(t, "ACTIVE", storedObject.EffectiveStatus)
}

func TestEvaluateObjectSkipsPreviousOnlyCounterCorrectionWithoutPause(t *testing.T) {
	fixture := newAutomationIntegrationFixture(t)
	ctx := fixture.ctx
	repositories := fixture.repositories
	now := fixture.now

	object := &domain.PublishedObject{
		Model:                domain.Model{CreatedAt: now.Add(-48 * time.Hour)},
		BatchID:              fixture.batch.ID,
		BatchAccountResultID: fixture.result.ID,
		ConnectionID:         fixture.connection.ID,
		AdAccountID:          fixture.account.ID,
		ObjectType:           domain.PublishedCampaign,
		MetaObjectID:         "campaign-" + uuid.NewString(),
		DesiredStatus:        "ACTIVE",
		EffectiveStatus:      "ACTIVE",
		IdempotencyKey:       "published-" + uuid.NewString(),
		RequestJSON:          domain.EmptyJSONObject,
		ResponseJSON:         domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Batches.CheckpointPublishedObject(ctx, object))

	rule := &domain.AutomationRule{
		ConnectionID: fixture.connection.ID,
		AdAccountID:  &fixture.account.ID,
		Name:         "Pause when purchases disappear",
		Status:       domain.RuleActive,
		ScopeLevel:   domain.InsightCampaign,
		Action:       domain.RuleActionPause,
		Conditions: domain.MustJSON(rules.Group{
			Logic: rules.LogicAll,
			Conditions: []rules.Condition{{
				Metric:        "actions.purchase",
				Operator:      rules.OperatorLT,
				Threshold:     1,
				MissingAsZero: true,
			}},
		}),
		LookbackSeconds:           int64((24 * time.Hour) / time.Second),
		EvaluationIntervalSeconds: int64((15 * time.Minute) / time.Second),
		NextEvaluationAt:          now,
		Metadata:                  domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Rules.Create(ctx, rule))

	for _, snapshot := range []domain.InsightSnapshot{
		{
			ConnectionID:      fixture.connection.ID,
			AdAccountID:       fixture.account.ID,
			PublishedObjectID: &object.ID,
			MetaObjectID:      object.MetaObjectID,
			Level:             domain.InsightCampaign,
			DateStart:         now.Add(-48 * time.Hour),
			DateStop:          now,
			WindowStart:       now.Add(-48 * time.Hour),
			WindowEnd:         now.Add(-24 * time.Hour),
			QueryHash:         LifetimeInsightQueryHash,
			Metrics: domain.MustJSON(map[string]float64{
				"spend":            100,
				"actions.purchase": 1,
			}),
			Breakdowns: domain.EmptyJSONObject,
			RawJSON:    domain.EmptyJSONObject,
			FetchedAt:  now.Add(-24 * time.Hour),
		},
		{
			ConnectionID:      fixture.connection.ID,
			AdAccountID:       fixture.account.ID,
			PublishedObjectID: &object.ID,
			MetaObjectID:      object.MetaObjectID,
			Level:             domain.InsightCampaign,
			DateStart:         now.Add(-48 * time.Hour),
			DateStop:          now,
			WindowStart:       now.Add(-48 * time.Hour),
			WindowEnd:         now,
			QueryHash:         LifetimeInsightQueryHash,
			Metrics:           domain.MustJSON(map[string]float64{"spend": 200}),
			Breakdowns:        domain.EmptyJSONObject,
			RawJSON:           domain.EmptyJSONObject,
			FetchedAt:         now,
		},
	} {
		snapshot := snapshot
		require.NoError(t, repositories.Insights.Upsert(ctx, &snapshot))
	}

	service := &Service{
		Config: config.Config{Worker: config.WorkerConfig{InsightsInterval: 15 * time.Minute}},
		Repos:  repositories,
		Meta:   fixture.metaClient,
		Now:    func() time.Time { return now },
	}
	require.NoError(t, service.evaluateObject(
		ctx,
		rule,
		object,
		"unused-token",
		now,
		now.Add(15*time.Minute),
	))
	require.Zero(t, fixture.metaCalls.Load(), "a previous-only counter correction must never call Meta's pause endpoint")

	evaluations, err := repositories.Rules.ListEvaluations(ctx, database.RuleEvaluationFilter{
		RuleID:            rule.ID,
		PublishedObjectID: &object.ID,
	})
	require.NoError(t, err)
	require.Len(t, evaluations.Items, 1)
	require.Equal(t, domain.RuleEvaluationSkipped, evaluations.Items[0].Status)
	require.False(t, evaluations.Items[0].ActionAttempted)
	require.Contains(t, string(evaluations.Items[0].ConditionResults), "actions.purchase")

	var storedObject domain.PublishedObject
	require.NoError(t, fixture.tx.First(&storedObject, "id = ?", object.ID).Error)
	require.Equal(t, "ACTIVE", storedObject.EffectiveStatus)
}

type automationIntegrationFixture struct {
	ctx          context.Context
	tx           *gorm.DB
	repositories *database.Repositories
	metaClient   *meta.Client
	metaCalls    *atomic.Int64
	now          time.Time
	connection   *domain.MetaConnection
	account      *domain.AdAccount
	batch        *domain.Batch
	result       domain.BatchAccountResult
}

func newAutomationIntegrationFixture(t *testing.T) automationIntegrationFixture {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := database.NewRepositories(tx)

	metaCalls := &atomic.Int64{}
	metaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		metaCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(metaServer.Close)
	metaClient, err := meta.NewClient(meta.ClientConfig{
		AppID:        "test-app",
		AppSecret:    "test-secret",
		BaseURL:      metaServer.URL,
		OAuthBaseURL: metaServer.URL,
		HTTPClient:   metaServer.Client(),
		MaxRetries:   1,
	})
	require.NoError(t, err)

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	connection := &domain.MetaConnection{
		MetaUserID:            "automation-insights-" + uuid.NewString(),
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 17),
		AccessTokenNonce:      make([]byte, 12),
		TokenKeyVersion:       1,
		GrantedScopes:         domain.EmptyJSONArray,
		DeclinedScopes:        domain.EmptyJSONArray,
		Metadata:              domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Create(ctx, connection))

	account := &domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		AccountID:       uuid.NewString(),
		Name:            "Automation Insights account",
		Currency:        "USD",
		IsActive:        true,
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now,
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, account))

	results := []domain.BatchAccountResult{{
		AdAccountID:  account.ID,
		Status:       domain.BatchAccountSucceeded,
		RequestJSON:  domain.EmptyJSONObject,
		ResponseJSON: domain.EmptyJSONObject,
	}}
	batch := &domain.Batch{
		ConnectionID:   connection.ID,
		Name:           "Automation Insights batch",
		Status:         domain.BatchSucceeded,
		IdempotencyKey: "automation-insights-" + uuid.NewString(),
		Specification:  domain.EmptyJSONObject,
		CreatedBy:      "test",
	}
	require.NoError(t, repositories.Batches.Create(ctx, batch, results))

	return automationIntegrationFixture{
		ctx:          ctx,
		tx:           tx,
		repositories: repositories,
		metaClient:   metaClient,
		metaCalls:    metaCalls,
		now:          now,
		connection:   connection,
		account:      account,
		batch:        batch,
		result:       results[0],
	}
}
