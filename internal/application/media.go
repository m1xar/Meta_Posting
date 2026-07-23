package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"gorm.io/gorm"
)

func (s *Service) SaveMedia(
	ctx context.Context,
	connectionID *uuid.UUID,
	adAccountID *uuid.UUID,
	kind domain.MediaKind,
	originalName, contentType string,
	source io.Reader,
) (*domain.Media, error) {
	switch kind {
	case domain.MediaImage, domain.MediaVideo:
	default:
		return nil, invalid("kind", "must be image or video")
	}
	if strings.TrimSpace(originalName) == "" {
		return nil, invalid("file", "file name is required")
	}
	if connectionID != nil {
		if _, err := s.Repos.MetaConnections.Get(ctx, *connectionID); err != nil {
			return nil, err
		}
	}
	if adAccountID != nil {
		account, err := s.Repos.Inventory.GetAdAccount(ctx, *adAccountID)
		if err != nil {
			return nil, err
		}
		if connectionID != nil && account.ConnectionID != *connectionID {
			return nil, invalid("ad_account_id", "does not belong to connection_id")
		}
		if connectionID == nil {
			connectionID = &account.ConnectionID
		}
	}
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(originalName)))
	}
	if kind == domain.MediaImage && contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return nil, invalid("file", "content type is not an image")
	}
	if kind == domain.MediaVideo && contentType != "" && !strings.HasPrefix(contentType, "video/") {
		return nil, invalid("file", "content type is not a video")
	}
	saved, err := s.Storage.Save(ctx, originalName, source)
	if err != nil {
		return nil, err
	}
	record := &domain.Media{
		ConnectionID: connectionID,
		AdAccountID:  adAccountID,
		Kind:         kind,
		Status:       domain.MediaReady,
		OriginalName: filepath.Base(originalName),
		LocalPath:    saved.RelativePath,
		MIMEType:     contentType,
		SHA256:       saved.SHA256,
		SizeBytes:    saved.SizeBytes,
		Metadata:     emptyObject(),
	}
	if err := s.Repos.Media.Create(ctx, record); err != nil {
		// The stored file is intentionally retained: a later reconciliation can
		// recover it by checksum instead of risking data loss.
		return nil, fmt.Errorf("save media record: %w", err)
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: connectionID,
		ActorType:    "internal_api",
		Action:       "media.stored",
		EntityType:   "media",
		EntityID:     record.ID.String(),
		After: domain.MustJSON(map[string]any{
			"kind":       kind,
			"sha256":     saved.SHA256,
			"size_bytes": saved.SizeBytes,
		}),
	})
	return record, nil
}

func (s *Service) uploadMediaForAccount(
	ctx context.Context,
	token string,
	account *domain.AdAccount,
	mediaID uuid.UUID,
) (string, error) {
	item, err := s.Repos.Media.Get(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if item.Status != domain.MediaReady {
		return "", fmt.Errorf("media %s is not ready", mediaID)
	}
	if item.ConnectionID != nil && *item.ConnectionID != account.ConnectionID {
		return "", errors.New("media belongs to another Meta connection")
	}
	if item.AdAccountID != nil && *item.AdAccountID != account.ID {
		return "", errors.New("media belongs to another ad account")
	}

	checkpoint, err := s.Repos.Media.GetAccountUpload(ctx, item.ID, account.ID)
	if err == nil {
		return s.resolveAccountMediaUpload(ctx, token, item, checkpoint)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	videoTitle := ""
	if item.Kind == domain.MediaVideo {
		videoTitle = accountMediaVideoTitle(item.ID, account.ID)
		reconciled, found, reconcileErr := s.Meta.FindUploadedVideoByTitle(
			ctx,
			token,
			adAccountGraphID(account),
			videoTitle,
		)
		if reconcileErr != nil {
			return "", reconcileErr
		}
		if found {
			checkpoint, checkpointErr := s.Repos.Media.CheckpointAccountUpload(
				ctx,
				&domain.MediaAccountUpload{
					MediaID:      item.ID,
					AdAccountID:  account.ID,
					Status:       domain.MediaProcessing,
					MetaVideoID:  reconciled.ID,
					ResponseJSON: domain.MustJSON(reconciled),
				},
			)
			if checkpointErr != nil {
				return "", retryableMediaCheckpointError("checkpoint reconciled Meta video ID", checkpointErr)
			}
			return s.resolveAccountMediaUpload(ctx, token, item, checkpoint)
		}
	}

	path, resolveErr := s.Storage.Resolve(item.LocalPath)
	if resolveErr != nil {
		return "", resolveErr
	}
	switch item.Kind {
	case domain.MediaImage:
		response, err := s.Meta.UploadImageFile(ctx, token, adAccountGraphID(account), path)
		if err != nil {
			return "", err
		}
		for _, image := range response.Images {
			if image.Hash != "" {
				checkpoint, checkpointErr := s.Repos.Media.CheckpointAccountUpload(
					ctx,
					&domain.MediaAccountUpload{
						MediaID:       item.ID,
						AdAccountID:   account.ID,
						Status:        domain.MediaReady,
						MetaImageHash: image.Hash,
						ResponseJSON:  domain.MustJSON(response),
					},
				)
				if checkpointErr != nil {
					return "", retryableMediaCheckpointError("checkpoint Meta image hash", checkpointErr)
				}
				return s.resolveAccountMediaUpload(ctx, token, item, checkpoint)
			}
		}
		return "", &meta.ResponseError{
			Message: "meta: image upload returned a successful response with no usable hash",
		}
	case domain.MediaVideo:
		response, err := s.Meta.UploadVideoFile(ctx, token, adAccountGraphID(account), path, meta.VideoUploadOptions{
			Name:  item.OriginalName,
			Title: videoTitle,
		})
		if err != nil {
			return "", err
		}
		checkpoint, checkpointErr := s.Repos.Media.CheckpointAccountUpload(
			ctx,
			&domain.MediaAccountUpload{
				MediaID:      item.ID,
				AdAccountID:  account.ID,
				Status:       domain.MediaProcessing,
				MetaVideoID:  response.ID,
				ResponseJSON: domain.MustJSON(response),
			},
		)
		if checkpointErr != nil {
			return "", retryableMediaCheckpointError("checkpoint Meta video ID", checkpointErr)
		}
		return s.resolveAccountMediaUpload(ctx, token, item, checkpoint)
	default:
		return "", fmt.Errorf("unsupported media kind %q", item.Kind)
	}
}

func accountMediaVideoTitle(mediaID, adAccountID uuid.UUID) string {
	return "Raze Posting " + mediaID.String() + ":" + adAccountID.String()
}

func (s *Service) resolveAccountMediaUpload(
	ctx context.Context,
	token string,
	item *domain.Media,
	checkpoint *domain.MediaAccountUpload,
) (string, error) {
	if checkpoint == nil {
		return "", errors.New("media account upload checkpoint is nil")
	}
	if checkpoint.Status == domain.MediaFailed {
		message := strings.TrimSpace(checkpoint.LastError)
		if message == "" {
			message = "Meta media processing failed"
		}
		return "", errors.New(message)
	}

	switch item.Kind {
	case domain.MediaImage:
		if checkpoint.MetaImageHash == "" {
			return "", errors.New("Meta image checkpoint has no hash")
		}
		return checkpoint.MetaImageHash, nil
	case domain.MediaVideo:
		if checkpoint.MetaVideoID == "" {
			return "", errors.New("Meta video checkpoint has no ID")
		}
		if checkpoint.Status == domain.MediaReady {
			return checkpoint.MetaVideoID, nil
		}
		return s.waitForCheckpointedVideo(ctx, token, checkpoint)
	default:
		return "", fmt.Errorf("unsupported media kind %q", item.Kind)
	}
}

func (s *Service) waitForCheckpointedVideo(
	ctx context.Context,
	token string,
	checkpoint *domain.MediaAccountUpload,
) (string, error) {
	status, waitErr := s.Meta.WaitForVideoReady(ctx, token, checkpoint.MetaVideoID)
	response := domain.MustJSON(status)
	checkedAt := s.Now()
	if waitErr == nil {
		if err := s.Repos.Media.UpdateAccountUploadStatus(
			ctx,
			checkpoint.ID,
			domain.MediaReady,
			response,
			"",
			checkedAt,
		); err != nil {
			return "", retryableMediaCheckpointError("checkpoint ready Meta video", err)
		}
		return checkpoint.MetaVideoID, nil
	}

	checkpointStatus := domain.MediaProcessing
	var processingErr *meta.VideoProcessingError
	if errors.As(waitErr, &processingErr) {
		checkpointStatus = domain.MediaFailed
	}
	persistCtx, cancelPersist := publishCleanupContext(ctx)
	defer cancelPersist()
	persistErr := s.Repos.Media.UpdateAccountUploadStatus(
		persistCtx,
		checkpoint.ID,
		checkpointStatus,
		response,
		waitErr.Error(),
		checkedAt,
	)
	if persistErr != nil {
		return "", errors.Join(
			waitErr,
			retryableMediaCheckpointError("checkpoint Meta video processing status", persistErr),
		)
	}
	return "", waitErr
}

type mediaCheckpointError struct {
	operation string
	cause     error
}

func retryableMediaCheckpointError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &mediaCheckpointError{operation: operation, cause: err}
}

func (e *mediaCheckpointError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %v", e.operation, e.cause)
}

func (e *mediaCheckpointError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *mediaCheckpointError) Retryable() bool {
	return e != nil
}
