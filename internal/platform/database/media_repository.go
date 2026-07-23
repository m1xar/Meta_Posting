package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MediaFilter struct {
	ConnectionID *uuid.UUID
	AdAccountID  *uuid.UUID
	Kind         *domain.MediaKind
	Status       *domain.MediaStatus
	Page         domain.PageRequest
}

type MediaRepository struct {
	db *gorm.DB
}

func (r *MediaRepository) Create(ctx context.Context, media *domain.Media) error {
	return r.db.WithContext(ctx).Create(media).Error
}

func (r *MediaRepository) Get(ctx context.Context, id uuid.UUID) (*domain.Media, error) {
	var media domain.Media
	if err := r.db.WithContext(ctx).First(&media, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &media, nil
}

func (r *MediaRepository) FindBySHA256(ctx context.Context, checksum string) ([]domain.Media, error) {
	var media []domain.Media
	if err := r.db.WithContext(ctx).Where("sha256 = ?", checksum).Order("created_at DESC").Find(&media).Error; err != nil {
		return nil, err
	}
	return media, nil
}

func (r *MediaRepository) List(ctx context.Context, filter MediaFilter) (domain.Page[domain.Media], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.Media{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *filter.AdAccountID)
	}
	if filter.Kind != nil {
		query = query.Where("kind = ?", *filter.Kind)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.Media]{}, err
	}
	var items []domain.Media
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.Media]{}, err
	}
	return domain.Page[domain.Media]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *MediaRepository) GetAccountUpload(
	ctx context.Context,
	mediaID uuid.UUID,
	adAccountID uuid.UUID,
) (*domain.MediaAccountUpload, error) {
	var upload domain.MediaAccountUpload
	if err := r.db.WithContext(ctx).
		First(&upload, "media_id = ? AND ad_account_id = ?", mediaID, adAccountID).
		Error; err != nil {
		return nil, err
	}
	return &upload, nil
}

// CheckpointAccountUpload inserts the first durable Meta identifier for an
// account-scoped upload. A concurrent caller never replaces an existing
// identifier; it receives the winning checkpoint and must reuse it.
func (r *MediaRepository) CheckpointAccountUpload(
	ctx context.Context,
	upload *domain.MediaAccountUpload,
) (*domain.MediaAccountUpload, error) {
	if upload == nil || upload.MediaID == uuid.Nil || upload.AdAccountID == uuid.Nil {
		return nil, errors.New("media account upload requires media_id and ad_account_id")
	}
	if (upload.MetaImageHash == "") == (upload.MetaVideoID == "") {
		return nil, errors.New("media account upload requires exactly one Meta identifier")
	}
	if upload.Status != domain.MediaProcessing && upload.Status != domain.MediaReady {
		return nil, errors.New("new media account upload must be processing or ready")
	}
	if len(upload.ResponseJSON) == 0 {
		upload.ResponseJSON = domain.EmptyJSONObject
	}

	result := r.db.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "media_id"}, {Name: "ad_account_id"}},
			DoNothing: true,
		},
		clause.Returning{},
	).Create(upload)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected > 0 {
		return upload, nil
	}
	return r.GetAccountUpload(ctx, upload.MediaID, upload.AdAccountID)
}

func (r *MediaRepository) UpdateAccountUploadStatus(
	ctx context.Context,
	id uuid.UUID,
	status domain.MediaStatus,
	response domain.JSON,
	lastError string,
	checkedAt time.Time,
) error {
	switch status {
	case domain.MediaProcessing, domain.MediaReady, domain.MediaFailed:
	default:
		return errors.New("invalid media account upload status")
	}
	if len(response) == 0 {
		response = domain.EmptyJSONObject
	}
	result := r.db.WithContext(ctx).Model(&domain.MediaAccountUpload{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":          status,
			"response_json":   response,
			"last_error":      lastError,
			"last_checked_at": checkedAt,
			"updated_at":      checkedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MediaRepository) MarkProcessing(ctx context.Context, id uuid.UUID) error {
	return r.updateStatus(ctx, id, domain.MediaProcessing, map[string]any{"last_error": ""})
}

func (r *MediaRepository) MarkReady(
	ctx context.Context,
	id uuid.UUID,
	metaImageHash string,
	metaVideoID string,
	metadata domain.JSON,
) error {
	return r.updateStatus(ctx, id, domain.MediaReady, map[string]any{
		"meta_image_hash": metaImageHash,
		"meta_video_id":   metaVideoID,
		"metadata":        metadata,
		"last_error":      "",
	})
}

func (r *MediaRepository) MarkFailed(ctx context.Context, id uuid.UUID, message string) error {
	return r.updateStatus(ctx, id, domain.MediaFailed, map[string]any{"last_error": message})
}

func (r *MediaRepository) updateStatus(ctx context.Context, id uuid.UUID, status domain.MediaStatus, values map[string]any) error {
	values["status"] = status
	values["updated_at"] = time.Now().UTC()
	result := r.db.WithContext(ctx).Model(&domain.Media{}).Where("id = ?", id).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MediaRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&domain.Media{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
