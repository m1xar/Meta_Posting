package database

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"gorm.io/gorm"
)

type RuleFilter struct {
	ConnectionID *uuid.UUID
	AdAccountID  *uuid.UUID
	BatchID      *uuid.UUID
	Statuses     []domain.RuleStatus
	Page         domain.PageRequest
}

type RuleEvaluationFilter struct {
	RuleID            uuid.UUID
	PublishedObjectID *uuid.UUID
	Statuses          []domain.RuleEvaluationStatus
	Page              domain.PageRequest
}

type RuleRepository struct {
	db *gorm.DB
}

func (r *RuleRepository) Create(ctx context.Context, rule *domain.AutomationRule) error {
	return r.db.WithContext(ctx).Create(rule).Error
}

func (r *RuleRepository) Get(ctx context.Context, id uuid.UUID) (*domain.AutomationRule, error) {
	var rule domain.AutomationRule
	if err := r.db.WithContext(ctx).First(&rule, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// UpdateConfiguration changes mutable rule fields without overwriting
// scheduling/history timestamps maintained by the evaluator.
func (r *RuleRepository) UpdateConfiguration(ctx context.Context, rule *domain.AutomationRule) error {
	result := r.db.WithContext(ctx).Model(&domain.AutomationRule{}).Where("id = ?", rule.ID).Updates(map[string]any{
		"ad_account_id":               rule.AdAccountID,
		"batch_id":                    rule.BatchID,
		"name":                        rule.Name,
		"status":                      rule.Status,
		"scope_level":                 rule.ScopeLevel,
		"action":                      rule.Action,
		"conditions":                  rule.Conditions,
		"lookback_seconds":            rule.LookbackSeconds,
		"evaluation_interval_seconds": rule.EvaluationIntervalSeconds,
		"grace_period_seconds":        rule.GracePeriodSeconds,
		"minimum_spend":               rule.MinimumSpend,
		"minimum_impressions":         rule.MinimumImpressions,
		"timezone":                    rule.Timezone,
		"next_evaluation_at":          rule.NextEvaluationAt,
		"metadata":                    rule.Metadata,
		"updated_at":                  time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *RuleRepository) SetStatus(ctx context.Context, id uuid.UUID, status domain.RuleStatus, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&domain.AutomationRule{}).Where("id = ?", id).Updates(map[string]any{
		"status":     status,
		"updated_at": now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *RuleRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result := r.db.WithContext(ctx).Delete(&domain.AutomationRule{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *RuleRepository) List(ctx context.Context, filter RuleFilter) (domain.Page[domain.AutomationRule], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.AutomationRule{})
	if filter.ConnectionID != nil {
		query = query.Where("connection_id = ?", *filter.ConnectionID)
	}
	if filter.AdAccountID != nil {
		query = query.Where("ad_account_id = ?", *filter.AdAccountID)
	}
	if filter.BatchID != nil {
		query = query.Where("batch_id = ?", *filter.BatchID)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.AutomationRule]{}, err
	}
	var items []domain.AutomationRule
	if err := applyPage(query.Order("created_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.AutomationRule]{}, err
	}
	return domain.Page[domain.AutomationRule]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}

func (r *RuleRepository) ListDue(
	ctx context.Context,
	connectionID *uuid.UUID,
	now time.Time,
	limit int,
) ([]domain.AutomationRule, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > domain.MaxPageLimit {
		limit = domain.MaxPageLimit
	}
	query := r.db.WithContext(ctx).
		Where("status = ? AND next_evaluation_at <= ?", domain.RuleActive, now)
	if connectionID != nil {
		query = query.Where("connection_id = ?", *connectionID)
	}
	var rules []domain.AutomationRule
	if err := query.
		Order("next_evaluation_at ASC, id ASC").
		Limit(limit).
		Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

func (r *RuleRepository) SaveEvaluation(
	ctx context.Context,
	evaluation *domain.RuleEvaluation,
	nextEvaluationAt time.Time,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(evaluation).Error; err != nil {
			return err
		}
		values := map[string]any{
			"last_evaluated_at":  evaluation.EvaluatedAt,
			"next_evaluation_at": nextEvaluationAt,
			"updated_at":         evaluation.EvaluatedAt,
		}
		if evaluation.Status == domain.RuleEvaluationActionSucceeded {
			values["last_triggered_at"] = evaluation.EvaluatedAt
		}
		result := tx.Model(&domain.AutomationRule{}).Where("id = ?", evaluation.RuleID).Updates(values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *RuleRepository) GetEvaluation(ctx context.Context, id uuid.UUID) (*domain.RuleEvaluation, error) {
	var evaluation domain.RuleEvaluation
	if err := r.db.WithContext(ctx).First(&evaluation, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &evaluation, nil
}

func (r *RuleRepository) ListEvaluations(
	ctx context.Context,
	filter RuleEvaluationFilter,
) (domain.Page[domain.RuleEvaluation], error) {
	page := filter.Page.Normalized()
	query := r.db.WithContext(ctx).Model(&domain.RuleEvaluation{}).Where("rule_id = ?", filter.RuleID)
	if filter.PublishedObjectID != nil {
		query = query.Where("published_object_id = ?", *filter.PublishedObjectID)
	}
	if len(filter.Statuses) > 0 {
		query = query.Where("status IN ?", filter.Statuses)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return domain.Page[domain.RuleEvaluation]{}, err
	}
	var items []domain.RuleEvaluation
	if err := applyPage(query.Order("evaluated_at DESC, id DESC"), page.Limit, page.Offset).Find(&items).Error; err != nil {
		return domain.Page[domain.RuleEvaluation]{}, err
	}
	return domain.Page[domain.RuleEvaluation]{Items: items, Total: total, Limit: page.Limit, Offset: page.Offset}, nil
}
