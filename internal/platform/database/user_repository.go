package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

type AuthenticatedSession struct {
	Session domain.UserSession
	User    domain.User
}

type UserRepository struct {
	db *gorm.DB
}

func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *UserRepository) Get(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) FindByLogin(ctx context.Context, login string) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).First(&user, "lower(login) = lower(?)", login).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) MarkLogin(ctx context.Context, id uuid.UUID, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.User{}).Where("id = ?", id).Updates(map[string]any{
		"last_login_at": at,
		"updated_at":    at,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) CreateSession(ctx context.Context, session *domain.UserSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *UserRepository) FindSession(ctx context.Context, tokenHash []byte, now time.Time) (*AuthenticatedSession, error) {
	var session domain.UserSession
	if err := r.db.WithContext(ctx).
		Where("token_hash = ? AND expires_at > ?", tokenHash, now).
		First(&session).Error; err != nil {
		return nil, err
	}
	user, err := r.Get(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	return &AuthenticatedSession{Session: session, User: *user}, nil
}

func (r *UserRepository) TouchSession(ctx context.Context, id uuid.UUID, at time.Time) error {
	return r.db.WithContext(ctx).Model(&domain.UserSession{}).Where("id = ?", id).Updates(map[string]any{
		"last_seen_at": at,
		"updated_at":   at,
	}).Error
}

func (r *UserRepository) DeleteSession(ctx context.Context, tokenHash []byte) error {
	return r.db.WithContext(ctx).Delete(&domain.UserSession{}, "token_hash = ?", tokenHash).Error
}

func (r *UserRepository) DeleteExpiredSessions(ctx context.Context, now time.Time) error {
	return r.db.WithContext(ctx).Delete(&domain.UserSession{}, "expires_at <= ?", now).Error
}

func (r *UserRepository) OwnsConnection(ctx context.Context, userID, connectionID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.MetaConnection{}).
		Where("id = ? AND user_id = ?", connectionID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) OwnsAdAccount(ctx context.Context, userID, adAccountID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Table("ad_accounts AS a").
		Joins("JOIN meta_connections AS c ON c.id = a.connection_id").
		Where("a.id = ? AND c.user_id = ?", adAccountID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) OwnsBatch(ctx context.Context, userID, batchID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Table("batches AS b").
		Joins("JOIN meta_connections AS c ON c.id = b.connection_id").
		Where("b.id = ? AND c.user_id = ?", batchID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) OwnsGuard(ctx context.Context, userID, guardID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Table("campaign_guards AS g").
		Joins("JOIN meta_connections AS c ON c.id = g.connection_id").
		Where("g.id = ? AND c.user_id = ?", guardID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) OwnsPublishedObject(ctx context.Context, userID, objectID uuid.UUID) error {
	var count int64
	err := r.db.WithContext(ctx).Table("published_objects AS p").
		Joins("JOIN meta_connections AS c ON c.id = p.connection_id").
		Where("p.id = ? AND c.user_id = ?", objectID, userID).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *UserRepository) ListConnections(ctx context.Context, userID uuid.UUID) ([]domain.MetaConnection, error) {
	var items []domain.MetaConnection
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Find(&items).Error
	return items, err
}

func (r *UserRepository) ListAdAccounts(ctx context.Context, userID uuid.UUID, limit int) ([]domain.AdAccount, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var items []domain.AdAccount
	err := r.db.WithContext(ctx).Table("ad_accounts AS a").
		Select("a.*").
		Joins("JOIN meta_connections AS c ON c.id = a.connection_id").
		Where("c.user_id = ?", userID).
		Order("a.is_active DESC, a.account_status ASC, a.name ASC, a.id ASC").
		Limit(limit).Scan(&items).Error
	return items, err
}

func (r *UserRepository) ListAssets(ctx context.Context, userID uuid.UUID, limit int) ([]domain.Asset, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	var items []domain.Asset
	err := r.db.WithContext(ctx).Table("assets AS a").
		Select("a.*").
		Joins("JOIN meta_connections AS c ON c.id = a.connection_id").
		Where("c.user_id = ? AND a.is_active = true", userID).
		Order("a.asset_type ASC, a.name ASC, a.id ASC").
		Limit(limit).Scan(&items).Error
	return items, err
}

func (r *UserRepository) ListBatches(ctx context.Context, userID uuid.UUID, limit int) ([]domain.Batch, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var items []domain.Batch
	err := r.db.WithContext(ctx).Table("batches AS b").Select("b.*").
		Joins("JOIN meta_connections AS c ON c.id = b.connection_id").
		Where("c.user_id = ?", userID).
		Order("b.created_at DESC, b.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

func (r *UserRepository) ListGuards(ctx context.Context, userID uuid.UUID, limit int) ([]domain.CampaignGuard, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var items []domain.CampaignGuard
	err := r.db.WithContext(ctx).Table("campaign_guards AS g").Select("g.*").
		Joins("JOIN meta_connections AS c ON c.id = g.connection_id").
		Where("c.user_id = ?", userID).
		Order("g.created_at DESC, g.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}

// ListCampaigns returns the user's published campaigns, newest first.
func (r *UserRepository) ListCampaigns(ctx context.Context, userID uuid.UUID, limit int) ([]domain.PublishedObject, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var items []domain.PublishedObject
	err := r.db.WithContext(ctx).Table("published_objects AS p").Select("p.*").
		Joins("JOIN meta_connections AS c ON c.id = p.connection_id").
		Where("c.user_id = ? AND p.object_type = ?", userID, domain.PublishedCampaign).
		Order("p.created_at DESC, p.id DESC").Limit(limit).Scan(&items).Error
	return items, err
}
