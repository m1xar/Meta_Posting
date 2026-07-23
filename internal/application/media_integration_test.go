package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/storage"
	"gorm.io/gorm"
)

func TestVideoUploadResponseLossReconcilesAndCheckpointsPerAccountPostgres(t *testing.T) {
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

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	connection := &domain.MetaConnection{
		MetaUserID:            "media-checkpoint-" + uuid.NewString(),
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
		MetaAdAccountID: "act_123",
		AccountID:       "123",
		Name:            "Media checkpoint account",
		Currency:        "USD",
		IsActive:        true,
		Capabilities:    domain.EmptyJSONArray,
		RawJSON:         domain.EmptyJSONObject,
		LastSyncedAt:    now,
	}
	require.NoError(t, repositories.Inventory.UpsertAdAccount(ctx, account))

	localStorage, err := storage.NewLocal(t.TempDir(), 1<<20)
	require.NoError(t, err)

	var expectedTitle string
	var uploaded atomic.Bool
	var preflightCalls atomic.Int32
	var uploadCalls atomic.Int32
	var statusCalls atomic.Int32
	metaServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v25.0/act_123/advideos":
			preflightCalls.Add(1)
			if uploaded.Load() {
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"data": []map[string]string{{
						"id":    "video-1",
						"title": expectedTitle,
					}},
				})
				return
			}
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/v25.0/act_123/advideos":
			uploadCalls.Add(1)
			require.NoError(t, request.ParseMultipartForm(1<<20))
			require.Equal(t, expectedTitle, request.FormValue("title"))
			file, _, fileErr := request.FormFile("source")
			require.NoError(t, fileErr)
			_, _ = io.Copy(io.Discard, file)
			_ = file.Close()
			uploaded.Store(true)
			// Meta accepted the upload, but the successful response was lost.
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"id":`))
		case request.Method == http.MethodGet && request.URL.Path == "/v25.0/video-1":
			statusCalls.Add(1)
			_, _ = writer.Write([]byte(`{"id":"video-1","status":{"video_status":"ready"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(metaServer.Close)
	metaClient, err := meta.NewClient(meta.ClientConfig{
		AppID:          "test-app",
		AppSecret:      "test-secret",
		BaseURL:        metaServer.URL,
		OAuthBaseURL:   metaServer.URL,
		HTTPClient:     metaServer.Client(),
		MaxRetries:     1,
		BaseRetryDelay: time.Millisecond,
		MaxRetryDelay:  time.Millisecond,
	})
	require.NoError(t, err)
	service := &Service{
		Repos:   repositories,
		Meta:    metaClient,
		Storage: localStorage,
		Now:     func() time.Time { return now },
	}
	mediaItem, err := service.SaveMedia(
		ctx,
		&connection.ID,
		&account.ID,
		domain.MediaVideo,
		"clip.mp4",
		"video/mp4",
		strings.NewReader("video bytes"),
	)
	require.NoError(t, err)
	expectedTitle = accountMediaVideoTitle(mediaItem.ID, account.ID)

	_, err = service.uploadMediaForAccount(ctx, "token", account, mediaItem.ID)
	require.Error(t, err)
	require.True(t, meta.IsRetryableError(err))
	_, checkpointErr := repositories.Media.GetAccountUpload(ctx, mediaItem.ID, account.ID)
	require.ErrorIs(t, checkpointErr, gorm.ErrRecordNotFound)

	videoID, err := service.uploadMediaForAccount(ctx, "token", account, mediaItem.ID)
	require.NoError(t, err)
	require.Equal(t, "video-1", videoID)
	require.Equal(t, int32(2), preflightCalls.Load())
	require.Equal(t, int32(1), uploadCalls.Load())
	require.Equal(t, int32(1), statusCalls.Load())

	checkpoint, err := repositories.Media.GetAccountUpload(ctx, mediaItem.ID, account.ID)
	require.NoError(t, err)
	require.Equal(t, domain.MediaReady, checkpoint.Status)
	require.Equal(t, "video-1", checkpoint.MetaVideoID)

	videoID, err = service.uploadMediaForAccount(ctx, "token", account, mediaItem.ID)
	require.NoError(t, err)
	require.Equal(t, "video-1", videoID)
	require.Equal(t, int32(2), preflightCalls.Load())
	require.Equal(t, int32(1), uploadCalls.Load())
	require.Equal(t, int32(1), statusCalls.Load())
}
