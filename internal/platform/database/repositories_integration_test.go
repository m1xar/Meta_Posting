package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

func TestRepositoriesPostgresIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := NewRepositories(tx)

	now := time.Now().UTC().Truncate(time.Microsecond)
	metaUserID := "test-" + uuid.NewString()
	connection := &domain.MetaConnection{
		MetaUserID:            metaUserID,
		DisplayName:           "First profile",
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 32),
		AccessTokenNonce:      make([]byte, 12),
		TokenKeyVersion:       1,
		GrantedScopes:         domain.MustJSON([]string{"ads_management"}),
		DeclinedScopes:        domain.EmptyJSONArray,
		Metadata:              domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Upsert(ctx, connection))
	require.NotEqual(t, uuid.Nil, connection.ID)

	reconnected := &domain.MetaConnection{
		MetaUserID:            metaUserID,
		DisplayName:           "Updated profile",
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 33),
		AccessTokenNonce:      make([]byte, 12),
		TokenKeyVersion:       1,
		GrantedScopes:         domain.MustJSON([]string{"ads_management", "ads_read"}),
		DeclinedScopes:        domain.EmptyJSONArray,
		Metadata:              domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.MetaConnections.Upsert(ctx, reconnected))
	require.Equal(t, connection.ID, reconnected.ID)

	oauthSession := &domain.OAuthSession{
		StateHash:       make([]byte, 32),
		RedirectURI:     "https://example.test/oauth/facebook/callback",
		RequestedScopes: domain.MustJSON([]string{"ads_management"}),
		Status:          domain.OAuthSessionPending,
		ExpiresAt:       now.Add(time.Hour),
		Metadata:        domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.OAuthSessions.Create(ctx, oauthSession))
	oauthNow := time.Now().UTC().Truncate(time.Microsecond)
	consumed, err := repositories.OAuthSessions.Consume(ctx, oauthSession.StateHash, oauthNow)
	require.NoError(t, err)
	require.Equal(t, oauthSession.ID, consumed.ID)
	_, err = repositories.OAuthSessions.Consume(ctx, oauthSession.StateHash, oauthNow)
	require.ErrorIs(t, err, ErrOAuthSessionUnavailable)
	require.NoError(t, repositories.OAuthSessions.Complete(ctx, oauthSession.ID, connection.ID, oauthNow))

	account := &domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		Name:            "Test account",
		Currency:        "USD",
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now,
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, account))
	secondAccount := &domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		Name:            "Second account",
		Currency:        "USD",
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now,
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, secondAccount))

	video := &domain.Media{
		ConnectionID: &connection.ID,
		Kind:         domain.MediaVideo,
		Status:       domain.MediaReady,
		OriginalName: "checkpoint.mp4",
		LocalPath:    uuid.NewString() + "/checkpoint.mp4",
		MIMEType:     "video/mp4",
		SHA256:       strings.Repeat("a", 64),
		SizeBytes:    1024,
		Metadata:     domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Media.Create(ctx, video))
	accountUpload, err := repositories.Media.CheckpointAccountUpload(ctx, &domain.MediaAccountUpload{
		MediaID:      video.ID,
		AdAccountID:  account.ID,
		Status:       domain.MediaProcessing,
		MetaVideoID:  "video-" + uuid.NewString(),
		ResponseJSON: domain.MustJSON(map[string]any{"status": "processing"}),
	})
	require.NoError(t, err)
	require.Equal(t, domain.MediaProcessing, accountUpload.Status)
	require.NotEmpty(t, accountUpload.MetaVideoID)

	conflictingUpload, err := repositories.Media.CheckpointAccountUpload(ctx, &domain.MediaAccountUpload{
		MediaID:      video.ID,
		AdAccountID:  account.ID,
		Status:       domain.MediaProcessing,
		MetaVideoID:  "duplicate-" + uuid.NewString(),
		ResponseJSON: domain.EmptyJSONObject,
	})
	require.NoError(t, err)
	require.Equal(t, accountUpload.ID, conflictingUpload.ID)
	require.Equal(t, accountUpload.MetaVideoID, conflictingUpload.MetaVideoID)

	videoCheckedAt := now.Add(time.Minute)
	require.NoError(t, repositories.Media.UpdateAccountUploadStatus(
		ctx,
		accountUpload.ID,
		domain.MediaReady,
		domain.MustJSON(map[string]any{"status": "ready"}),
		"",
		videoCheckedAt,
	))
	accountUpload, err = repositories.Media.GetAccountUpload(ctx, video.ID, account.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaReady, accountUpload.Status)
	require.NotNil(t, accountUpload.LastCheckedAt)
	require.WithinDuration(t, videoCheckedAt, *accountUpload.LastCheckedAt, time.Microsecond)

	sharedAssetID := "pixel-" + uuid.NewString()
	for _, accountID := range []uuid.UUID{account.ID, secondAccount.ID} {
		require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &domain.Asset{
			ConnectionID: connection.ID,
			AdAccountID:  &accountID,
			AssetType:    domain.AssetPixel,
			MetaAssetID:  sharedAssetID,
			Name:         "Shared pixel",
			IsActive:     true,
			Normalized:   domain.EmptyJSONObject,
			RawJSON:      domain.EmptyJSONObject,
			LastSyncedAt: now,
		}))
	}
	for _, accountID := range []uuid.UUID{account.ID, secondAccount.ID} {
		assets, err := repositories.Inventory.ListAssets(ctx, AssetFilter{
			ConnectionID: connection.ID,
			AdAccountID:  &accountID,
		})
		require.NoError(t, err)
		require.Len(t, assets.Items, 1)
		require.Equal(t, sharedAssetID, assets.Items[0].MetaAssetID)
	}

	staleAccount := &domain.AdAccount{
		ConnectionID:    connection.ID,
		MetaAdAccountID: "act_" + uuid.NewString(),
		Name:            "No longer accessible",
		Currency:        "USD",
		IsActive:        true,
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now.Add(-time.Hour),
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, staleAccount))
	stalePixel := inventoryAssetFixture(
		connection.ID,
		&account.ID,
		domain.AssetPixel,
		"stale-pixel-"+uuid.NewString(),
		now.Add(-time.Hour),
	)
	require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &stalePixel))
	preservedApplication := inventoryAssetFixture(
		connection.ID,
		&account.ID,
		domain.AssetMetaApp,
		"preserved-app-"+uuid.NewString(),
		now.Add(-time.Hour),
	)
	require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &preservedApplication))
	staleAccountInstagram := inventoryAssetFixture(
		connection.ID,
		&staleAccount.ID,
		domain.AssetInstagramAccount,
		"stale-account-instagram-"+uuid.NewString(),
		now.Add(-time.Hour),
	)
	require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &staleAccountInstagram))

	currentPage := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetPage,
		"current-page-"+uuid.NewString(),
		now,
	)
	stalePage := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetPage,
		"stale-page-"+uuid.NewString(),
		now.Add(-time.Hour),
	)
	currentPageInstagram := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetInstagramAccount,
		"current-page-instagram-"+uuid.NewString(),
		now,
	)
	staleGlobalInstagram := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetInstagramAccount,
		"stale-global-instagram-"+uuid.NewString(),
		now.Add(-time.Hour),
	)
	for _, asset := range []*domain.Asset{
		&currentPage,
		&stalePage,
		&currentPageInstagram,
		&staleGlobalInstagram,
	} {
		require.NoError(t, repositories.Inventory.UpsertAsset(ctx, asset))
	}

	require.NoError(t, repositories.Inventory.Reconcile(ctx, InventoryReconciliation{
		ConnectionID: connection.ID,
		SeenAdAccountMetaIDs: []string{
			account.MetaAdAccountID,
			secondAccount.MetaAdAccountID,
		},
		AccountAssetScopes: []AssetAccessScope{
			{
				AdAccountID: account.ID,
				AssetType:   domain.AssetPixel,
				SeenMetaIDs: []string{sharedAssetID},
			},
			{
				AdAccountID: secondAccount.ID,
				AssetType:   domain.AssetPixel,
				SeenMetaIDs: []string{sharedAssetID},
			},
		},
		PagesComplete:        true,
		SeenPageMetaIDs:      []string{currentPage.MetaAssetID},
		SeenPageInstagramIDs: []string{currentPageInstagram.MetaAssetID},
		ReconciledAt:         now.Add(time.Minute),
	}))

	staleAccountAfter, err := repositories.Inventory.GetAdAccount(ctx, staleAccount.ID)
	require.NoError(t, err)
	require.False(t, staleAccountAfter.IsActive)
	activeAccounts, err := repositories.Inventory.ListAdAccounts(ctx, AdAccountFilter{
		ConnectionID: &connection.ID,
		ActiveOnly:   true,
	})
	require.NoError(t, err)
	require.Len(t, activeAccounts.Items, 2)

	for _, expectation := range []struct {
		id     uuid.UUID
		active bool
	}{
		{id: stalePixel.ID, active: false},
		{id: preservedApplication.ID, active: true},
		{id: staleAccountInstagram.ID, active: false},
		{id: currentPage.ID, active: true},
		{id: stalePage.ID, active: false},
		{id: currentPageInstagram.ID, active: true},
		{id: staleGlobalInstagram.ID, active: false},
	} {
		asset, assetErr := repositories.Inventory.GetAsset(ctx, expectation.id)
		require.NoError(t, assetErr)
		require.Equal(t, expectation.active, asset.IsActive, asset.MetaAssetID)
	}
	staleAccountAssets, err := repositories.Inventory.ListAssets(ctx, AssetFilter{
		ConnectionID: connection.ID,
		AdAccountID:  &staleAccount.ID,
	})
	require.NoError(t, err)
	require.Empty(t, staleAccountAssets.Items)

	preservedPage := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetPage,
		"page-preserved-after-partial-"+uuid.NewString(),
		now,
	)
	preservedPageInstagram := inventoryAssetFixture(
		connection.ID,
		nil,
		domain.AssetInstagramAccount,
		"instagram-preserved-after-partial-"+uuid.NewString(),
		now,
	)
	require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &preservedPage))
	require.NoError(t, repositories.Inventory.UpsertAsset(ctx, &preservedPageInstagram))
	require.NoError(t, repositories.Inventory.Reconcile(ctx, InventoryReconciliation{
		ConnectionID: connection.ID,
		SeenAdAccountMetaIDs: []string{
			account.MetaAdAccountID,
			secondAccount.MetaAdAccountID,
		},
		PagesComplete: false,
		ReconciledAt:  now.Add(2 * time.Minute),
	}))
	for _, assetID := range []uuid.UUID{preservedPage.ID, preservedPageInstagram.ID} {
		asset, assetErr := repositories.Inventory.GetAsset(ctx, assetID)
		require.NoError(t, assetErr)
		require.True(t, asset.IsActive, asset.MetaAssetID)
	}

	result := domain.BatchAccountResult{
		AdAccountID:  account.ID,
		Status:       domain.BatchAccountPending,
		RequestJSON:  domain.EmptyJSONObject,
		ResponseJSON: domain.EmptyJSONObject,
	}
	batch := &domain.Batch{
		ConnectionID:   connection.ID,
		Name:           "Integration test",
		Status:         domain.BatchRunning,
		IdempotencyKey: uuid.NewString(),
		Specification:  domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Batches.Create(ctx, batch, []domain.BatchAccountResult{result}))
	results, err := repositories.Batches.ListAccountResults(ctx, BatchAccountResultFilter{BatchID: batch.ID})
	require.NoError(t, err)
	require.Len(t, results.Items, 1)
	batchStartedAt := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, repositories.Batches.MarkAccountRunning(ctx, results.Items[0].ID, batchStartedAt))
	runningBatch, err := repositories.Batches.Get(ctx, batch.ID)
	require.NoError(t, err)
	require.Equal(t, domain.BatchRunning, runningBatch.Status)
	require.NotNil(t, runningBatch.StartedAt)
	require.WithinDuration(t, batchStartedAt, *runningBatch.StartedAt, time.Microsecond)

	finishedAt := time.Now().UTC().Truncate(time.Microsecond)
	published := domain.PublishedObject{
		ObjectType:      domain.PublishedCampaign,
		MetaObjectID:    "campaign-" + uuid.NewString(),
		Name:            "Campaign",
		DesiredStatus:   "ACTIVE",
		EffectiveStatus: "ACTIVE",
		IdempotencyKey:  uuid.NewString(),
		RequestJSON:     domain.EmptyJSONObject,
		ResponseJSON:    domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Batches.FinishAccountResult(
		ctx,
		results.Items[0].ID,
		AccountResultCompletion{
			Status:       domain.BatchAccountSucceeded,
			ResponseJSON: domain.EmptyJSONObject,
		},
		[]domain.PublishedObject{published},
		finishedAt,
	))
	objects, err := repositories.Batches.ListPublishedObjects(ctx, PublishedObjectFilter{BatchID: &batch.ID})
	require.NoError(t, err)
	require.Len(t, objects.Items, 1)
	statusCheckedAt := finishedAt.Add(time.Minute)
	require.NoError(t, repositories.Batches.MarkPublishedStatusChecked(ctx, objects.Items[0].ID, statusCheckedAt))
	statusCheckedObject, err := repositories.Batches.GetPublishedObject(ctx, objects.Items[0].ID)
	require.NoError(t, err)
	require.NotNil(t, statusCheckedObject.LastSyncedAt)
	require.WithinDuration(t, statusCheckedAt, *statusCheckedObject.LastSyncedAt, time.Microsecond)
	require.Equal(t, "ACTIVE", statusCheckedObject.EffectiveStatus)

	dedupeKey := uuid.NewString()
	job := &domain.Job{
		ConnectionID: &connection.ID,
		Type:         "integration",
		Payload:      domain.EmptyJSONObject,
		DedupeKey:    &dedupeKey,
		MaxAttempts:  2,
		AvailableAt:  now,
	}
	enqueued, created, err := repositories.Jobs.Enqueue(ctx, job)
	require.NoError(t, err)
	require.True(t, created)
	duplicate, created, err := repositories.Jobs.Enqueue(ctx, &domain.Job{
		Type:        job.Type,
		Payload:     domain.EmptyJSONObject,
		DedupeKey:   &dedupeKey,
		MaxAttempts: 2,
		AvailableAt: now,
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, enqueued.ID, duplicate.ID)

	claimed, err := repositories.Jobs.Claim(ctx, "worker-1", time.Minute, now)
	require.NoError(t, err)
	require.Equal(t, enqueued.ID, claimed.ID)
	retried, err := repositories.Jobs.Fail(ctx, claimed.ID, "worker-1", "temporary", time.Second, now.Add(time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, domain.JobPending, retried.Status)
	claimed, err = repositories.Jobs.Claim(ctx, "worker-2", time.Minute, now.Add(2*time.Second))
	require.NoError(t, err)
	require.NoError(t, repositories.Jobs.Complete(ctx, claimed.ID, "worker-2", now.Add(3*time.Second)))

	expiredLock := now.Add(-time.Minute)
	expiredJob := &domain.Job{
		ConnectionID: &connection.ID,
		Type:         "expired-final-attempt",
		Status:       domain.JobRunning,
		Payload:      domain.EmptyJSONObject,
		Attempts:     1,
		MaxAttempts:  1,
		AvailableAt:  now.Add(-time.Hour),
		LockedBy:     "crashed-worker",
		LockedAt:     &expiredLock,
		LockedUntil:  &expiredLock,
	}
	require.NoError(t, repositories.DB().WithContext(ctx).Create(expiredJob).Error)
	_, err = repositories.Jobs.Claim(ctx, "worker-3", time.Minute, now)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	expiredJob, err = repositories.Jobs.Get(ctx, expiredJob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.JobDead, expiredJob.Status)

	queryHash := uuid.NewString()
	firstSnapshot := insightFixture(connection.ID, account.ID, objects.Items[0].ID, objects.Items[0].MetaObjectID, queryHash, now, 10)
	secondSnapshot := insightFixture(connection.ID, account.ID, objects.Items[0].ID, objects.Items[0].MetaObjectID, queryHash, now.Add(time.Hour), 25)
	require.NoError(t, repositories.Insights.UpsertMany(ctx, []domain.InsightSnapshot{firstSnapshot, secondSnapshot}))
	before, err := repositories.Insights.NearestBefore(ctx, InsightPointQuery{
		ConnectionID: connection.ID,
		MetaObjectID: objects.Items[0].MetaObjectID,
		Level:        domain.InsightCampaign,
		QueryHash:    queryHash,
		At:           now.Add(30 * time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, float64(10), before.Spend)
	after, err := repositories.Insights.NearestAfter(ctx, InsightPointQuery{
		ConnectionID: connection.ID,
		MetaObjectID: objects.Items[0].MetaObjectID,
		Level:        domain.InsightCampaign,
		QueryHash:    queryHash,
		At:           now.Add(30 * time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, float64(25), after.Spend)

	rule := &domain.AutomationRule{
		ConnectionID:              connection.ID,
		AdAccountID:               &account.ID,
		Name:                      "Pause no conversions",
		Status:                    domain.RuleActive,
		ScopeLevel:                domain.InsightCampaign,
		Action:                    domain.RuleActionPause,
		Conditions:                domain.MustJSON(map[string]any{"logic": "all", "conditions": []any{map[string]any{"metric": "spend", "operator": "gt", "threshold": 10}}}),
		LookbackSeconds:           86400,
		EvaluationIntervalSeconds: 900,
		NextEvaluationAt:          now,
		Metadata:                  domain.EmptyJSONObject,
	}
	require.NoError(t, repositories.Rules.Create(ctx, rule))
	evaluation := &domain.RuleEvaluation{
		RuleID:            rule.ID,
		PublishedObjectID: &objects.Items[0].ID,
		MetaObjectID:      objects.Items[0].MetaObjectID,
		Status:            domain.RuleEvaluationNoMatch,
		WindowStart:       now.Add(-time.Hour),
		WindowEnd:         now,
		ObservedMetrics:   domain.MustJSON(map[string]float64{"spend": 10}),
		ConditionResults:  domain.EmptyJSONObject,
		ActionResponse:    domain.EmptyJSONObject,
		EvaluatedAt:       now,
	}
	require.NoError(t, repositories.Rules.SaveEvaluation(ctx, evaluation, now.Add(15*time.Minute)))
	evaluations, err := repositories.Rules.ListEvaluations(ctx, RuleEvaluationFilter{RuleID: rule.ID})
	require.NoError(t, err)
	require.Len(t, evaluations.Items, 1)

	require.NoError(t, repositories.Audit.Append(ctx, &domain.AuditEvent{
		ConnectionID: &connection.ID,
		ActorType:    "worker",
		ActorID:      "worker-2",
		Action:       "campaign.publish",
		EntityType:   "batch",
		EntityID:     batch.ID.String(),
		Severity:     domain.AuditInfo,
		Before:       domain.EmptyJSONObject,
		After:        domain.EmptyJSONObject,
		Metadata:     domain.EmptyJSONObject,
		CreatedAt:    now,
	}))

	err = NewRepositories(nil).Transaction(ctx, func(*Repositories) error { return nil })
	require.Error(t, err)
	require.False(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func inventoryAssetFixture(
	connectionID uuid.UUID,
	accountID *uuid.UUID,
	assetType domain.AssetType,
	metaID string,
	syncedAt time.Time,
) domain.Asset {
	return domain.Asset{
		ConnectionID: connectionID,
		AdAccountID:  accountID,
		AssetType:    assetType,
		MetaAssetID:  metaID,
		Name:         metaID,
		IsActive:     true,
		Normalized:   domain.EmptyJSONObject,
		RawJSON:      domain.EmptyJSONObject,
		LastSyncedAt: syncedAt,
	}
}

func TestPublishJobDeadFinalizesBatchPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := NewRepositories(tx)

	connection := &domain.MetaConnection{
		MetaUserID:            "dead-publish-" + uuid.NewString(),
		DisplayName:           "Dead publish test",
		Status:                domain.MetaConnectionActive,
		AccessTokenCiphertext: make([]byte, 32),
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
		Name:            "Dead publish test account",
		Currency:        "USD",
		IsActive:        true,
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    time.Now().UTC(),
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, account))

	newRunningResult := func(label string) (*domain.Batch, *domain.BatchAccountResult) {
		batch := &domain.Batch{
			ConnectionID:   connection.ID,
			Name:           label,
			Status:         domain.BatchQueued,
			IdempotencyKey: uuid.NewString(),
			Specification:  domain.EmptyJSONObject,
		}
		require.NoError(t, repositories.Batches.Create(ctx, batch, []domain.BatchAccountResult{{
			AdAccountID:  account.ID,
			Status:       domain.BatchAccountPending,
			RequestJSON:  domain.EmptyJSONObject,
			ResponseJSON: domain.EmptyJSONObject,
		}}))
		page, listErr := repositories.Batches.ListAccountResults(ctx, BatchAccountResultFilter{BatchID: batch.ID})
		require.NoError(t, listErr)
		require.Len(t, page.Items, 1)
		result := &page.Items[0]
		require.NoError(t, repositories.Batches.MarkAccountRunning(ctx, result.ID, time.Now().UTC()))
		return batch, result
	}

	t.Run("retry attempt stays pending until the publish job exhausts", func(t *testing.T) {
		batch, accountResult := newRunningResult("retry backoff")
		now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
		response := domain.MustJSON(map[string]any{
			"success": false,
			"stages": []any{
				map[string]any{"name": "create_campaign", "retryable": true},
			},
		})
		checkpoint := domain.PublishedObject{
			ObjectType:      domain.PublishedCampaign,
			MetaObjectID:    "campaign-" + uuid.NewString(),
			Name:            "Retry campaign",
			DesiredStatus:   "ACTIVE",
			EffectiveStatus: "PAUSED",
			IdempotencyKey:  uuid.NewString(),
			RequestJSON:     domain.EmptyJSONObject,
			ResponseJSON:    response,
		}
		require.NoError(t, repositories.Batches.RecordAccountRetry(
			ctx,
			accountResult.ID,
			AccountResultRetry{
				ResponseJSON: response,
				ErrorCode:    "2",
				ErrorMessage: "temporary Meta failure",
			},
			[]domain.PublishedObject{checkpoint},
			now,
		))

		storedResult, getErr := repositories.Batches.GetAccountResult(ctx, accountResult.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchAccountPending, storedResult.Status)
		require.Equal(t, "2", storedResult.ErrorCode)
		require.Equal(t, "temporary Meta failure", storedResult.ErrorMessage)
		require.JSONEq(t, string(response), string(storedResult.ResponseJSON))
		require.Nil(t, storedResult.CompletedAt)

		storedBatch, getErr := repositories.Batches.Get(ctx, batch.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchRunning, storedBatch.Status)
		require.Zero(t, storedBatch.FailedAccounts)
		require.Nil(t, storedBatch.CompletedAt)

		objects, listErr := repositories.Batches.ListPublishedObjects(
			ctx,
			PublishedObjectFilter{BatchID: &batch.ID},
		)
		require.NoError(t, listErr)
		require.Len(t, objects.Items, 1)
		require.Equal(t, checkpoint.MetaObjectID, objects.Items[0].MetaObjectID)
		require.Equal(t, accountResult.ID, objects.Items[0].BatchAccountResultID)

		job := &domain.Job{
			ConnectionID: &connection.ID,
			Type:         publishAccountJobType,
			Payload:      domain.MustJSON(map[string]any{"result_id": accountResult.ID}),
			MaxAttempts:  1,
			AvailableAt:  now.Add(-time.Minute),
		}
		_, created, enqueueErr := repositories.Jobs.Enqueue(ctx, job)
		require.NoError(t, enqueueErr)
		require.True(t, created)
		claimed, claimErr := repositories.Jobs.Claim(ctx, "worker-retry-exhausted", time.Minute, now)
		require.NoError(t, claimErr)
		dead, failErr := repositories.Jobs.Fail(
			ctx,
			claimed.ID,
			"worker-retry-exhausted",
			"retryable Meta publish failure",
			time.Second,
			now.Add(time.Millisecond),
		)
		require.NoError(t, failErr)
		require.Equal(t, domain.JobDead, dead.Status)

		storedResult, getErr = repositories.Batches.GetAccountResult(ctx, accountResult.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchAccountFailed, storedResult.Status)
		require.Equal(t, deadPublishAccountErrorCode, storedResult.ErrorCode)
		require.NotNil(t, storedResult.CompletedAt)
		storedBatch, getErr = repositories.Batches.Get(ctx, batch.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchFailed, storedBatch.Status)
		require.Equal(t, 1, storedBatch.FailedAccounts)
		require.NotNil(t, storedBatch.CompletedAt)
	})

	t.Run("failure on final attempt is atomic with result and batch finalization", func(t *testing.T) {
		batch, accountResult := newRunningResult("explicit final failure")
		now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
		job := &domain.Job{
			ConnectionID: &connection.ID,
			Type:         publishAccountJobType,
			Payload:      domain.MustJSON(map[string]any{"result_id": accountResult.ID}),
			MaxAttempts:  1,
			AvailableAt:  now.Add(-time.Minute),
		}
		_, created, enqueueErr := repositories.Jobs.Enqueue(ctx, job)
		require.NoError(t, enqueueErr)
		require.True(t, created)

		claimed, claimErr := repositories.Jobs.Claim(ctx, "worker-final-error", time.Minute, now)
		require.NoError(t, claimErr)
		require.Equal(t, job.ID, claimed.ID)
		dead, failErr := repositories.Jobs.Fail(
			ctx,
			claimed.ID,
			"worker-final-error",
			"database checkpoint failed",
			time.Second,
			now.Add(time.Millisecond),
		)
		require.NoError(t, failErr)
		require.Equal(t, domain.JobDead, dead.Status)

		storedResult, getErr := repositories.Batches.GetAccountResult(ctx, accountResult.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchAccountFailed, storedResult.Status)
		require.Equal(t, deadPublishAccountErrorCode, storedResult.ErrorCode)
		require.Equal(t, "database checkpoint failed", storedResult.ErrorMessage)
		require.NotNil(t, storedResult.CompletedAt)

		storedBatch, getErr := repositories.Batches.Get(ctx, batch.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchFailed, storedBatch.Status)
		require.Equal(t, 1, storedBatch.FailedAccounts)
		require.NotNil(t, storedBatch.CompletedAt)
	})

	t.Run("unrelated dead job does not mutate a batch result", func(t *testing.T) {
		batch, accountResult := newRunningResult("unrelated job")
		now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
		job := &domain.Job{
			ConnectionID: &connection.ID,
			Type:         "unrelated_job",
			Payload:      domain.MustJSON(map[string]any{"result_id": accountResult.ID}),
			MaxAttempts:  1,
			AvailableAt:  now.Add(-time.Minute),
		}
		_, created, enqueueErr := repositories.Jobs.Enqueue(ctx, job)
		require.NoError(t, enqueueErr)
		require.True(t, created)
		claimed, claimErr := repositories.Jobs.Claim(ctx, "worker-unrelated", time.Minute, now)
		require.NoError(t, claimErr)
		_, failErr := repositories.Jobs.Fail(
			ctx,
			claimed.ID,
			"worker-unrelated",
			"unrelated terminal error",
			0,
			now.Add(time.Millisecond),
		)
		require.NoError(t, failErr)

		storedResult, getErr := repositories.Batches.GetAccountResult(ctx, accountResult.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchAccountRunning, storedResult.Status)
		storedBatch, getErr := repositories.Batches.Get(ctx, batch.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchRunning, storedBatch.Status)
	})

	t.Run("expired final lease also finalizes publish result", func(t *testing.T) {
		batch, accountResult := newRunningResult("expired final lease")
		now := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
		expiredAt := now.Add(-time.Minute)
		job := &domain.Job{
			ConnectionID: &connection.ID,
			Type:         publishAccountJobType,
			Status:       domain.JobRunning,
			Payload:      domain.MustJSON(map[string]any{"result_id": accountResult.ID}),
			Attempts:     1,
			MaxAttempts:  1,
			AvailableAt:  expiredAt,
			LockedBy:     "crashed-worker",
			LockedAt:     &expiredAt,
			LockedUntil:  &expiredAt,
		}
		require.NoError(t, repositories.DB().WithContext(ctx).Create(job).Error)

		_, claimErr := repositories.Jobs.Claim(ctx, "recovery-worker", time.Minute, now)
		require.ErrorIs(t, claimErr, gorm.ErrRecordNotFound)
		storedJob, getErr := repositories.Jobs.Get(ctx, job.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.JobDead, storedJob.Status)
		require.Equal(t, expiredFinalAttemptErrorText, storedJob.LastError)

		storedResult, getErr := repositories.Batches.GetAccountResult(ctx, accountResult.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchAccountFailed, storedResult.Status)
		require.Equal(t, deadPublishAccountErrorCode, storedResult.ErrorCode)
		require.Equal(t, expiredFinalAttemptErrorText, storedResult.ErrorMessage)
		storedBatch, getErr := repositories.Batches.Get(ctx, batch.ID)
		require.NoError(t, getErr)
		require.Equal(t, domain.BatchFailed, storedBatch.Status)
		require.Equal(t, 1, storedBatch.FailedAccounts)
		require.NotNil(t, storedBatch.CompletedAt)
	})
}

func TestRuleRepositoryListDueFiltersConnectionBeforeLimit(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })
	repositories := NewRepositories(tx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	newConnectionWithAccount := func(label string) (*domain.MetaConnection, *domain.AdAccount) {
		connection := &domain.MetaConnection{
			MetaUserID:            label + "-" + uuid.NewString(),
			DisplayName:           label,
			Status:                domain.MetaConnectionActive,
			AccessTokenCiphertext: make([]byte, 32),
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
			Name:            label,
			Currency:        "USD",
			Capabilities:    domain.EmptyJSONArray,
			RawJSON:         domain.EmptyJSONObject,
			LastSyncedAt:    now,
		}
		require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, account))
		return connection, account
	}

	firstConnection, firstAccount := newConnectionWithAccount("first")
	targetConnection, targetAccount := newConnectionWithAccount("target")
	newRule := func(connectionID, accountID uuid.UUID, name string, dueAt time.Time) *domain.AutomationRule {
		rule := &domain.AutomationRule{
			ConnectionID:              connectionID,
			AdAccountID:               &accountID,
			Name:                      name,
			Status:                    domain.RuleActive,
			ScopeLevel:                domain.InsightCampaign,
			Action:                    domain.RuleActionPause,
			Conditions:                domain.MustJSON(map[string]any{"logic": "all", "conditions": []any{map[string]any{"metric": "spend", "operator": "gt", "threshold": 1}}}),
			LookbackSeconds:           3600,
			EvaluationIntervalSeconds: 900,
			NextEvaluationAt:          dueAt,
			Metadata:                  domain.EmptyJSONObject,
		}
		require.NoError(t, repositories.Rules.Create(ctx, rule))
		return rule
	}
	firstRule := newRule(firstConnection.ID, firstAccount.ID, "globally earliest", now.Add(-2*time.Hour))
	targetRule := newRule(targetConnection.ID, targetAccount.ID, "target connection", now.Add(-time.Hour))

	globalDue, err := repositories.Rules.ListDue(ctx, nil, now, 1)
	require.NoError(t, err)
	require.Len(t, globalDue, 1)
	require.Equal(t, firstRule.ID, globalDue[0].ID)

	connectionDue, err := repositories.Rules.ListDue(ctx, &targetConnection.ID, now, 1)
	require.NoError(t, err)
	require.Len(t, connectionDue, 1)
	require.Equal(t, targetRule.ID, connectionDue[0].ID)
}

func insightFixture(
	connectionID uuid.UUID,
	accountID uuid.UUID,
	objectID uuid.UUID,
	metaObjectID string,
	queryHash string,
	windowEnd time.Time,
	spend float64,
) domain.InsightSnapshot {
	return domain.InsightSnapshot{
		ConnectionID:      connectionID,
		AdAccountID:       accountID,
		PublishedObjectID: &objectID,
		MetaObjectID:      metaObjectID,
		Level:             domain.InsightCampaign,
		DateStart:         windowEnd.Add(-time.Hour),
		DateStop:          windowEnd,
		WindowStart:       windowEnd.Add(-time.Hour),
		WindowEnd:         windowEnd,
		QueryHash:         queryHash,
		Spend:             spend,
		Breakdowns:        domain.EmptyJSONObject,
		Metrics:           domain.MustJSON(map[string]float64{"spend": spend}),
		RawJSON:           domain.EmptyJSONObject,
		FetchedAt:         windowEnd,
	}
}
