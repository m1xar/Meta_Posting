package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"gorm.io/gorm"
)

type AuditFilter struct {
	ConnectionID *uuid.UUID
	EntityType   string
	EntityID     string
	ActorID      string
	Action       string
	Severities   []domain.AuditSeverity
	From         *time.Time
	To           *time.Time
	Page         domain.PageRequest
}

type AuditRepository struct {
	db *gorm.DB
}

func (r *AuditRepository) Append(ctx context.Context, event *domain.AuditEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *AuditRepository) AppendMany(ctx context.Context, events []domain.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(&events, 500).Error
}

func (r *AuditRepository) Get(ctx context.Context, id uuid.UUID) (*domain.AuditEvent, error) {
	var event domain.AuditEvent
	if err := r.db.WithContext(ctx).First(&event, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *AuditRepository) List(ctx context.Context, filter AuditFilter) (domain.Page[domain.AuditEvent], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.AuditEvent{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.EntityType != "" {
		query = query.Where("entity_type = ?", filter.EntityType)
	}
	if filter.EntityID != "" {
		query = query.Where("entity_id = ?", filter.EntityID)
	}
	if filter.ActorID != "" {
		query = query.Where("actor_id = ?", filter.ActorID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if len(filter.Severities) > 0 {
		query = query.Where("severity IN ?", filter.Severities)
	}
	if filter.From != nil {
		query = query.Where("created_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("created_at <= ?", *filter.To)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	var items []domain.AuditEvent
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.AuditEvent]{}, err
	}
	return domain.Page[domain.AuditEvent]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}
