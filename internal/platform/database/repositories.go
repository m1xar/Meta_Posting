package database

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var ErrLeaseLost = errors.New("job lease is no longer owned by this worker")

type Repositories struct {
	db *gorm.DB

	Users           *UserRepository
	APIKeys         *APIKeyRepository
	MetaConnections *MetaConnectionRepository
	OAuthSessions   *OAuthSessionRepository
	Inventory       *InventoryRepository
	Media           *MediaRepository
	Batches         *BatchRepository
	Jobs            *JobRepository
	Insights        *InsightRepository
	AdInsights      *AdInsightRepository
	AdEntities      *AdEntityRepository
	AdAccountSync   *AdAccountSyncStateRepository
	InsightsCursors *InsightsCursorRepository
	Rules           *RuleRepository
	Audit           *AuditRepository
}

func NewRepositories(db *gorm.DB) *Repositories {
	repositories := &Repositories{db: db}
	repositories.bind()
	return repositories
}

func (r *Repositories) bind() {
	r.Users = &UserRepository{db: r.db}
	r.APIKeys = &APIKeyRepository{db: r.db}
	r.MetaConnections = &MetaConnectionRepository{db: r.db}
	r.OAuthSessions = &OAuthSessionRepository{db: r.db}
	r.Inventory = &InventoryRepository{db: r.db}
	r.Media = &MediaRepository{db: r.db}
	r.Batches = &BatchRepository{db: r.db}
	r.Jobs = &JobRepository{db: r.db}
	r.Insights = &InsightRepository{db: r.db}
	r.AdInsights = &AdInsightRepository{db: r.db}
	r.AdEntities = &AdEntityRepository{db: r.db}
	r.AdAccountSync = &AdAccountSyncStateRepository{db: r.db}
	r.InsightsCursors = &InsightsCursorRepository{db: r.db}
	r.Rules = &RuleRepository{db: r.db}
	r.Audit = &AuditRepository{db: r.db}
}

func (r *Repositories) DB() *gorm.DB {
	if r == nil {
		return nil
	}
	return r.db
}

func (r *Repositories) WithContext(ctx context.Context) *Repositories {
	if r == nil || r.db == nil {
		return NewRepositories(nil)
	}
	return NewRepositories(r.db.WithContext(ctx))
}

func (r *Repositories) WithDB(db *gorm.DB) *Repositories {
	return NewRepositories(db)
}

// Transaction runs fn with every repository bound to the same transaction.
// Returning an error rolls the transaction back; panics retain GORM's normal
// rollback behavior.
func (r *Repositories) Transaction(ctx context.Context, fn func(*Repositories) error) error {
	if r == nil || r.db == nil {
		return errors.New("repositories are not initialized")
	}
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(NewRepositories(tx))
	})
	if err != nil {
		return fmt.Errorf("database transaction: %w", err)
	}
	return nil
}

func applyPage(query *gorm.DB, limit, offset int) *gorm.DB {
	return query.Limit(limit).Offset(offset)
}
