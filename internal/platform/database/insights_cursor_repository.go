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

// servableAccountStatuses are the Meta account states that can still deliver.
//
// 1 ACTIVE, 3 UNSETTLED, 8 PENDING_SETTLEMENT and 9 IN_GRACE_PERIOD can serve
// or are one payment away from serving. 2 DISABLED, 100 PENDING_CLOSURE and
// 101 CLOSED cannot, ever, and polling them returns an empty result forever.
//
// This filter governs incremental polling only. Backfill deliberately ignores
// it: a disabled account's historical spend is real and worth keeping, it
// just is not going to change again.
var servableAccountStatuses = []int{1, 3, 8, 9}

// ScheduledAdAccount is the minimum an insights job needs to be built without
// a second lookup: which account, whose token, and which timezone its dates
// resolve in.
type ScheduledAdAccount struct {
	AdAccountID     uuid.UUID `gorm:"column:id"`
	ConnectionID    uuid.UUID `gorm:"column:connection_id"`
	MetaAdAccountID string    `gorm:"column:meta_ad_account_id"`
	AccountID       string    `gorm:"column:account_id"`
	TimezoneName    string    `gorm:"column:timezone_name"`
	Currency        string    `gorm:"column:currency"`
}

type InsightsCursorRepository struct {
	db *gorm.DB
}

// DueAdAccounts hands out the next slice of ad accounts to poll for a level,
// advancing a persistent round-robin cursor.
//
// A slice rather than "everything" because ad-level insights are expensive
// enough that polling every account every cycle would exhaust the app's
// quota. The cursor is persistent so a restart resumes mid-rotation instead
// of always re-polling the same first accounts, and it is read FOR UPDATE so
// two scheduler replicas hand out disjoint slices rather than the same one.
//
// Throttled accounts are excluded, so an account Meta has blocked does not
// consume a slot in the rotation.
func (r *InsightsCursorRepository) DueAdAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	level string,
	limit int,
	now time.Time,
) ([]ScheduledAdAccount, error) {
	if limit <= 0 {
		return nil, errors.New("database: due ad account limit must be positive")
	}
	var slice []ScheduledAdAccount
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []ScheduledAdAccount
		if err := tx.Raw(`
			SELECT a.id, a.connection_id, a.meta_ad_account_id, a.account_id,
			       a.timezone_name, a.currency
			FROM ad_accounts a
			JOIN meta_connections c ON c.id = a.connection_id
			LEFT JOIN ad_account_sync_state s ON s.ad_account_id = a.id
			WHERE c.id = ?
			  AND c.status = ?
			  AND a.is_active
			  AND a.account_status IN ?
			  AND (s.throttled_until IS NULL OR s.throttled_until < ?)
			ORDER BY a.id
		`, connectionID, domain.MetaConnectionActive, servableAccountStatuses, now).
			Scan(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		cursor := domain.InsightsSyncCursor{
			ConnectionID: connectionID,
			Level:        level,
			UpdatedAt:    now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&cursor).Error; err != nil {
			return err
		}

		var offset int
		if err := tx.Raw(`
			SELECT next_offset FROM insights_sync_cursors
			WHERE connection_id = ? AND level = ?
			FOR UPDATE
		`, connectionID, level).Scan(&offset).Error; err != nil {
			return err
		}
		if offset < 0 || offset >= len(candidates) {
			offset = 0
		}

		end := offset + limit
		if end > len(candidates) {
			end = len(candidates)
		}
		slice = candidates[offset:end]

		next := end
		if next >= len(candidates) {
			next = 0
		}
		return tx.Exec(`
			UPDATE insights_sync_cursors
			SET next_offset = ?, updated_at = ?
			WHERE connection_id = ? AND level = ?
		`, next, now, connectionID, level).Error
	})
	if err != nil {
		return nil, err
	}
	return slice, nil
}

// AllAdAccounts returns every pollable ad account for a connection, for the
// cheap levels that do not need a rotation.
func (r *InsightsCursorRepository) AllAdAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	now time.Time,
) ([]ScheduledAdAccount, error) {
	var accounts []ScheduledAdAccount
	err := r.db.WithContext(ctx).Raw(`
		SELECT a.id, a.connection_id, a.meta_ad_account_id, a.account_id,
		       a.timezone_name, a.currency
		FROM ad_accounts a
		JOIN meta_connections c ON c.id = a.connection_id
		LEFT JOIN ad_account_sync_state s ON s.ad_account_id = a.id
		WHERE c.id = ?
		  AND c.status = ?
		  AND a.is_active
		  AND a.account_status IN ?
		  AND (s.throttled_until IS NULL OR s.throttled_until < ?)
		ORDER BY a.id
	`, connectionID, domain.MetaConnectionActive, servableAccountStatuses, now).Scan(&accounts).Error
	return accounts, err
}

// ResetCursor rewinds a rotation, used when an account set changes enough
// that the stored offset is meaningless.
func (r *InsightsCursorRepository) ResetCursor(
	ctx context.Context,
	connectionID uuid.UUID,
	level string,
	at time.Time,
) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE insights_sync_cursors
		SET next_offset = 0, updated_at = ?
		WHERE connection_id = ? AND level = ?
	`, at, connectionID, level).Error
}

// AllAdAccountsForBackfill returns every ad account of a connection,
// including ones Meta has disabled.
//
// Their spend already happened and is worth storing once; they are excluded
// from incremental polling, not from history.
func (r *InsightsCursorRepository) AllAdAccountsForBackfill(
	ctx context.Context,
	connectionID uuid.UUID,
) ([]ScheduledAdAccount, error) {
	var accounts []ScheduledAdAccount
	err := r.db.WithContext(ctx).Raw(`
		SELECT a.id, a.connection_id, a.meta_ad_account_id, a.account_id,
		       a.timezone_name, a.currency
		FROM ad_accounts a
		WHERE a.connection_id = ? AND a.is_active
		ORDER BY a.id
	`, connectionID).Scan(&accounts).Error
	return accounts, err
}

// ConnectionsWithFastRules returns connections holding at least one active
// rule that asks to be evaluated more often than the standard cadence.
//
// A rule may legitimately ask for a 60-second interval, but the scheduler
// ticks every fifteen minutes and insights are collected on the same cadence,
// so without a fast lane the rule silently runs at fifteen-minute resolution
// against fifteen-minute-old numbers. This is how the lane finds its members.
func (r *InsightsCursorRepository) ConnectionsWithFastRules(
	ctx context.Context,
	maxIntervalSeconds int64,
	now time.Time,
) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT r.connection_id
		FROM automation_rules r
		JOIN meta_connections c ON c.id = r.connection_id
		WHERE r.status = ?
		  AND c.status = ?
		  AND r.evaluation_interval_seconds <= ?
		ORDER BY r.connection_id
	`, domain.RuleActive, domain.MetaConnectionActive, maxIntervalSeconds).Scan(&ids).Error
	return ids, err
}
