package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
)

type APIKeyRepository struct {
	db *gorm.DB
}

func (r *APIKeyRepository) Create(ctx context.Context, key *domain.APIKey) error {
	return r.db.WithContext(ctx).Create(key).Error
}

// FindActiveByHash resolves a presented key to its owner, rejecting revoked
// and expired keys in the query rather than after it.
func (r *APIKeyRepository) FindActiveByHash(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) (*domain.APIKey, *domain.User, error) {
	var key domain.APIKey
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL", tokenHash).
		Where("expires_at IS NULL OR expires_at > ?", now).
		First(&key).Error
	if err != nil {
		return nil, nil, err
	}
	var user domain.User
	if err := r.db.WithContext(ctx).First(&user, "id = ?", key.UserID).Error; err != nil {
		return nil, nil, err
	}
	return &key, &user, nil
}

// TouchUsed records last use. Called at most once every few minutes by the
// caller, so authenticating a request does not always cost a write.
func (r *APIKeyRepository) TouchUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.APIKey{}).
		Where("id = ?", id).
		Updates(map[string]any{"last_used_at": at, "updated_at": at}).Error
}

func (r *APIKeyRepository) Revoke(ctx context.Context, id, userID uuid.UUID, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.APIKey{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Updates(map[string]any{"revoked_at": at, "updated_at": at})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *APIKeyRepository) ListForUser(ctx context.Context, userID uuid.UUID) ([]domain.APIKey, error) {
	var keys []domain.APIKey
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Order("created_at DESC").
		Find(&keys).Error
	return keys, err
}
