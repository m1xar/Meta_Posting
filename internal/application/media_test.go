package application

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestResolveAccountMediaUploadReusesReadyCheckpoint(t *testing.T) {
	t.Parallel()

	service := &Service{}
	imageHash, err := service.resolveAccountMediaUpload(
		context.Background(),
		"unused",
		&domain.Media{Kind: domain.MediaImage},
		&domain.MediaAccountUpload{
			Status:        domain.MediaReady,
			MetaImageHash: "saved-image-hash",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "saved-image-hash", imageHash)

	videoID, err := service.resolveAccountMediaUpload(
		context.Background(),
		"unused",
		&domain.Media{Kind: domain.MediaVideo},
		&domain.MediaAccountUpload{
			Status:      domain.MediaReady,
			MetaVideoID: "saved-video-id",
		},
	)
	require.NoError(t, err)
	require.Equal(t, "saved-video-id", videoID)
}

func TestResolveAccountMediaUploadPreservesTerminalProcessingFailure(t *testing.T) {
	t.Parallel()

	_, err := (&Service{}).resolveAccountMediaUpload(
		context.Background(),
		"unused",
		&domain.Media{Kind: domain.MediaVideo},
		&domain.MediaAccountUpload{
			Status:      domain.MediaFailed,
			MetaVideoID: "failed-video-id",
			LastError:   "unsupported video codec",
		},
	)
	require.EqualError(t, err, "unsupported video codec")
	require.False(t, meta.IsRetryableError(err))
}

func TestMediaCheckpointPersistenceErrorIsRetryable(t *testing.T) {
	t.Parallel()

	cause := errors.New("database unavailable")
	err := retryableMediaCheckpointError("checkpoint video ID", cause)
	require.ErrorIs(t, err, cause)
	require.True(t, meta.IsRetryableError(err))
}

func TestAccountMediaVideoTitleIsDeterministicAndAccountScoped(t *testing.T) {
	t.Parallel()

	mediaID := uuid.MustParse("11c133fe-ee83-4904-bcff-482377e972e4")
	firstAccountID := uuid.MustParse("9cba3575-fbdf-40b0-ac03-22ee36e621c1")
	secondAccountID := uuid.MustParse("340a8cca-43ea-4cf0-8253-24618d24e0a7")

	first := accountMediaVideoTitle(mediaID, firstAccountID)
	require.Equal(t, first, accountMediaVideoTitle(mediaID, firstAccountID))
	require.NotEqual(t, first, accountMediaVideoTitle(mediaID, secondAccountID))
	require.Contains(t, first, mediaID.String())
	require.Contains(t, first, firstAccountID.String())
}
