package database

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrOAuthSessionUnavailable = errors.New("OAuth session is missing, expired, or already consumed")

type MetaConnectionFilter struct {
	Scope  Scope
	UserID *uuid.UUID
	Status *domain.MetaConnectionStatus
	Search string
	Page   domain.PageRequest
}

type TokenUpdate struct {
	Ciphertext          []byte
	Nonce               []byte
	KeyVersion          int16
	ExpiresAt           *time.Time
	DataAccessExpiresAt *time.Time
	GrantedScopes       domain.JSON
	DeclinedScopes      domain.JSON
}

type MetaConnectionRepository struct {
	db *gorm.DB
}

func (r *MetaConnectionRepository) Create(ctx context.Context, connection *domain.MetaConnection) error {
	return r.db.WithContext(ctx).Create(connection).Error
}

// Upsert reconnects an existing Meta user atomically. The OAuth service should
// encrypt the token with meta_user_id as associated data before calling this
// method; the stable unique key keeps AAD valid across reconnects.
func (r *MetaConnectionRepository) Upsert(ctx context.Context, connection *domain.MetaConnection) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "meta_user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "email", "status", "access_token_ciphertext",
			"access_token_nonce", "token_key_version", "token_expires_at",
			"data_access_expires_at", "granted_scopes", "declined_scopes",
			"last_validated_at", "last_error", "metadata", "updated_at",
		}),
	}, clause.Returning{}).Create(connection).Error
}

func (r *MetaConnectionRepository) Get(ctx context.Context, id uuid.UUID) (*domain.MetaConnection, error) {
	var connection domain.MetaConnection
	if err := r.db.WithContext(ctx).First(&connection, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &connection, nil
}

func (r *MetaConnectionRepository) GetForUser(ctx context.Context, userID, id uuid.UUID) (*domain.MetaConnection, error) {
	var connection domain.MetaConnection
	if err := r.db.WithContext(ctx).First(&connection, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, err
	}
	return &connection, nil
}

func (r *MetaConnectionRepository) FindByMetaUserID(ctx context.Context, metaUserID string) (*domain.MetaConnection, error) {
	var connection domain.MetaConnection
	if err := r.db.WithContext(ctx).First(&connection, "meta_user_id = ?", metaUserID).Error; err != nil {
		return nil, err
	}
	return &connection, nil
}

func (r *MetaConnectionRepository) FindByUserAndMetaUserID(ctx context.Context, userID uuid.UUID, metaUserID string) (*domain.MetaConnection, error) {
	var connection domain.MetaConnection
	if err := r.db.WithContext(ctx).First(&connection, "user_id = ? AND meta_user_id = ?", userID, metaUserID).Error; err != nil {
		return nil, err
	}
	return &connection, nil
}

func (r *MetaConnectionRepository) List(ctx context.Context, filter MetaConnectionFilter) (domain.Page[domain.MetaConnection], error) {
	if !filter.Scope.Valid() {
		return domain.Page[domain.MetaConnection]{}, ErrScopeRequired
	}
	page := filter.Page.Normalized()
	query := filter.Scope.ApplyUserColumn(
		r.db.WithContext(ctx).Model(&domain.MetaConnection{}),
		"meta_connections",
	)
	if filter.UserID != nil {
		query = query.Where("user_id = ?", *filter.UserID)
	}
	if filter.Status != nil {
		query = query.Where("status = ?", *filter.Status)
	}
	if filter.Search != "" {
		pattern := "%" + filter.Search + "%"
		query = query.Where("display_name ILIKE ? OR email ILIKE ? OR meta_user_id ILIKE ?", pattern, pattern, pattern)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.MetaConnection]{}, err
	}
	var items []domain.MetaConnection
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.MetaConnection]{}, err
	}
	return domain.Page[domain.MetaConnection]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *MetaConnectionRepository) UpdateToken(ctx context.Context, id uuid.UUID, update TokenUpdate) error {
	values := map[string]any{
		"access_token_ciphertext": update.Ciphertext,
		"access_token_nonce":      update.Nonce,
		"token_key_version":       update.KeyVersion,
		"token_expires_at":        update.ExpiresAt,
		"data_access_expires_at":  update.DataAccessExpiresAt,
		"granted_scopes":          update.GrantedScopes,
		"declined_scopes":         update.DeclinedScopes,
		"status":                  domain.MetaConnectionActive,
		"last_error":              "",
		"updated_at":              time.Now().UTC(),
	}
	result := r.db.WithContext(ctx).Model(&domain.MetaConnection{}).Where("id = ?", id).Updates(values)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MetaConnectionRepository) UpdateProfile(
	ctx context.Context,
	id uuid.UUID,
	displayName string,
	email string,
	metadata domain.JSON,
) error {
	result := r.db.WithContext(ctx).Model(&domain.MetaConnection{}).Where("id = ?", id).Updates(map[string]any{
		"display_name": displayName,
		"email":        email,
		"metadata":     metadata,
		"updated_at":   time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MetaConnectionRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.MetaConnectionStatus, lastError string) error {
	result := r.db.WithContext(ctx).Model(&domain.MetaConnection{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"last_error": lastError,
		"updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MetaConnectionRepository) MarkSynced(ctx context.Context, id uuid.UUID, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.MetaConnection{}).Where("id = ?", id).Updates(map[string]any{
		"last_synced_at": at,
		"last_error":     "",
		"status":         domain.MetaConnectionActive,
		"updated_at":     time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *MetaConnectionRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&domain.MetaConnection{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

type OAuthSessionRepository struct {
	db *gorm.DB
}

func (r *OAuthSessionRepository) Create(ctx context.Context, session *domain.OAuthSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *OAuthSessionRepository) Get(ctx context.Context, id uuid.UUID) (*domain.OAuthSession, error) {
	var session domain.OAuthSession
	if err := r.db.WithContext(ctx).First(&session, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// Consume performs the replay-protection transition in one UPDATE and returns
// the session only to the first caller presenting a valid state hash.
func (r *OAuthSessionRepository) Consume(ctx context.Context, stateHash []byte, now time.Time) (*domain.OAuthSession, error) {
	var session domain.OAuthSession
	result := r.db.WithContext(ctx).
		Model(&session).
		Clauses(clause.Returning{}).
		Where("state_hash = ? AND status = ? AND expires_at > ?", stateHash, domain.OAuthSessionPending, now).
		Updates(map[string]any{
			"status":      domain.OAuthSessionConsumed,
			"consumed_at": now,
			"updated_at":  now,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrOAuthSessionUnavailable
	}
	return &session, nil
}

func (r *OAuthSessionRepository) Complete(ctx context.Context, id, connectionID uuid.UUID, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.OAuthSession{}).
		Where("id = ? AND status = ?", id, domain.OAuthSessionConsumed).
		Updates(map[string]any{
			"status":                  domain.OAuthSessionCompleted,
			"completed_connection_id": connectionID,
			"completed_at":            now,
			"updated_at":              now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOAuthSessionUnavailable
	}
	return nil
}

func (r *OAuthSessionRepository) Fail(ctx context.Context, id uuid.UUID, message string, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.OAuthSession{}).
		Where("id = ? AND status IN ?", id, []domain.OAuthSessionStatus{domain.OAuthSessionPending, domain.OAuthSessionConsumed}).
		Updates(map[string]any{
			"status":     domain.OAuthSessionFailed,
			"error":      message,
			"updated_at": now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrOAuthSessionUnavailable
	}
	return nil
}

func (r *OAuthSessionRepository) ExpirePending(ctx context.Context, now time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Model(&domain.OAuthSession{}).
		Where("status = ? AND expires_at <= ?", domain.OAuthSessionPending, now).
		Updates(map[string]any{"status": domain.OAuthSessionExpired, "updated_at": now})
	return result.RowsAffected, result.Error
}
