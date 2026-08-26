package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// RunAdEntitiesJob refreshes one ad account's inventory.
func (s *Service) RunAdEntitiesJob(ctx context.Context, payload AdEntitiesJobPayload) error {
	if payload.AdAccountID == uuid.Nil {
		return invalid("ad_account_id", "is required")
	}
	return s.SyncAdEntities(ctx, payload.AdAccountID)
}

// RunAccountInsightsJob ingests one frozen range for one account and level.
func (s *Service) RunAccountInsightsJob(ctx context.Context, payload AccountInsightsJobPayload) error {
	if payload.AdAccountID == uuid.Nil {
		return invalid("ad_account_id", "is required")
	}
	if !validInsightLevel(payload.Level) {
		return invalid("level", fmt.Sprintf("%q is not a supported insights level", payload.Level))
	}
	return s.CollectAccountInsights(ctx, AccountInsightsRequest{
		AdAccountID: payload.AdAccountID,
		Level:       payload.Level,
		Since:       payload.Since,
		Until:       payload.Until,
		Reason:      firstNonEmpty(payload.Reason, "incremental"),
		// Only the live incremental pass owns the watermark. Lookback,
		// backfill and gap repair all work on ranges behind the live edge and
		// must not move it forward or backward.
		AdvanceWatermark: payload.Reason == "" || payload.Reason == "incremental",
	})
}

// RunBackfillInsightsJob fetches one chunk of history and, if more remains,
// enqueues the next chunk.
//
// Chaining rather than looping keeps each unit of work small enough to retry
// cleanly and lets live polling interleave, since backfill runs at a lower
// priority.
func (s *Service) RunBackfillInsightsJob(ctx context.Context, payload BackfillInsightsJobPayload) error {
	if payload.AdAccountID == uuid.Nil {
		return invalid("ad_account_id", "is required")
	}
	if !validInsightLevel(payload.Level) {
		return invalid("level", fmt.Sprintf("%q is not a supported insights level", payload.Level))
	}
	more, err := s.BackfillAccountInsights(ctx, payload.AdAccountID, payload.Level)
	if err != nil {
		return err
	}
	if !more {
		return nil
	}
	_, err = s.EnqueueBackfillInsights(ctx, payload.AdAccountID, payload.Level)
	return err
}

// RunRepairInsightGapsJob re-fetches days that were never covered.
func (s *Service) RunRepairInsightGapsJob(ctx context.Context, payload RepairInsightGapsJobPayload) error {
	if payload.AdAccountID == uuid.Nil {
		return invalid("ad_account_id", "is required")
	}
	if !validInsightLevel(payload.Level) {
		return invalid("level", fmt.Sprintf("%q is not a supported insights level", payload.Level))
	}
	since, err := meta.ParseAccountDate(payload.Since)
	if err != nil {
		return err
	}
	until, err := meta.ParseAccountDate(payload.Until)
	if err != nil {
		return err
	}
	repaired, err := s.RepairInsightGaps(ctx, payload.AdAccountID, payload.Level, since, until)
	if err != nil {
		return err
	}
	if repaired > 0 {
		s.audit(ctx, domain.AuditEvent{
			ActorType:  "worker",
			Action:     "insights.gaps_repaired",
			EntityType: "ad_account",
			EntityID:   payload.AdAccountID.String(),
			Severity:   domain.AuditInfo,
			Metadata: domain.MustJSON(map[string]any{
				"level":  payload.Level,
				"ranges": repaired,
				"since":  payload.Since,
				"until":  payload.Until,
			}),
		})
	}
	return nil
}

// RunWindowedReachJob stores deduplicated reach for an explicit window.
//
// This exists because reach cannot be recovered from daily rows: it counts
// distinct people per query window, so summing days overcounts. Meta must do
// the deduplication, which means one query per window.
func (s *Service) RunWindowedReachJob(ctx context.Context, payload WindowedReachJobPayload) error {
	if payload.AdAccountID == uuid.Nil {
		return invalid("ad_account_id", "is required")
	}
	if !validInsightLevel(payload.Level) {
		return invalid("level", fmt.Sprintf("%q is not a supported insights level", payload.Level))
	}
	account, err := s.Repos.Inventory.GetAdAccount(ctx, payload.AdAccountID)
	if err != nil {
		return err
	}
	state, err := s.Repos.AdAccountSync.Ensure(ctx, account.ID, account.ConnectionID, nil)
	if err != nil {
		return err
	}
	_, token, err := s.accessToken(ctx, account.ConnectionID)
	if err != nil {
		return err
	}
	attribution := attributionModeFor(state.AttributionSetting)

	rows, _, err := s.Meta.FetchWindowedInsights(ctx, token, meta.WindowedInsightRequest{
		AccountID:   account.AccountID,
		Level:       meta.InsightLevel(payload.Level),
		Since:       payload.Since,
		Until:       payload.Until,
		Attribution: attribution,
	})
	if err != nil {
		if expired, markErr := s.markConnectionExpiredForMetaError(ctx, account.ConnectionID, err); expired {
			return errors.Join(err, markErr)
		}
		return err
	}

	since, err := meta.ParseAccountDate(payload.Since)
	if err != nil {
		return err
	}
	until, err := meta.ParseAccountDate(payload.Until)
	if err != nil {
		return err
	}
	now := s.Now()

	records := make([]domain.AdInsightWindowed, 0, len(rows))
	for _, row := range rows {
		objectID := objectIDForLevel(row, payload.Level)
		if objectID == "" {
			continue
		}
		records = append(records, domain.AdInsightWindowed{
			ConnectionID:       account.ConnectionID,
			AdAccountID:        account.ID,
			Level:              payload.Level,
			MetaObjectID:       objectID,
			Since:              since,
			Until:              until,
			AccountTimezone:    account.TimezoneName,
			AttributionSetting: attribution.Setting(),
			Reach:              parseInt64(row.Reach),
			Frequency:          parseFloat(row.Frequency),
			Impressions:        parseInt64(row.Impressions),
			Spend:              parseFloat(row.Spend),
			RawJSON:            domain.EmptyJSONObject,
			FetchedAt:          now,
		})
	}
	return s.Repos.AdInsights.UpsertWindowed(ctx, records)
}

// RunRetentionSweepJob trims stored history.
//
// insight_snapshots keys its uniqueness on wall-clock window bounds, so every
// collection run inserts rather than upserts and the table grows without
// bound. InsightRepository.DeleteBefore existed for this and had no caller
// until now.
func (s *Service) RunRetentionSweepJob(ctx context.Context, payload RetentionSweepJobPayload) error {
	limit := payload.Limit
	if limit <= 0 {
		limit = 5000
	}
	before := payload.Before
	if before.IsZero() {
		before = s.Now().Add(-s.Config.Worker.InsightRetention)
	}

	snapshots, err := s.Repos.Insights.DeleteBefore(ctx, before, limit)
	if err != nil {
		return err
	}
	daily, err := s.Repos.AdInsights.DeleteDailyBefore(ctx, before, limit)
	if err != nil {
		return err
	}
	if snapshots+daily > 0 {
		s.audit(ctx, domain.AuditEvent{
			ActorType:  "worker",
			Action:     "insights.retention_swept",
			EntityType: "insights",
			Severity:   domain.AuditInfo,
			Metadata: domain.MustJSON(map[string]any{
				"before":            before,
				"snapshots_deleted": snapshots,
				"daily_deleted":     daily,
			}),
		})
	}
	return nil
}

// --- enqueue helpers ---

func (s *Service) enqueueAdAccountJob(
	ctx context.Context,
	connectionID uuid.UUID,
	jobType string,
	dedupeKey string,
	priority int,
	payload domain.JSON,
	availableAt time.Time,
) (*domain.Job, error) {
	connection := connectionID
	job, _, err := s.Repos.Jobs.Enqueue(ctx, &domain.Job{
		ConnectionID: &connection,
		Type:         jobType,
		Status:       domain.JobPending,
		Priority:     priority,
		Payload:      payload,
		DedupeKey:    &dedupeKey,
		MaxAttempts:  s.Config.Worker.MaxAttempts,
		AvailableAt:  availableAt.UTC(),
	})
	return job, err
}

// EnqueueBackfillInsights queues the next history chunk at a low priority, so
// backfill never starves live polling.
func (s *Service) EnqueueBackfillInsights(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
) (*domain.Job, error) {
	account, err := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	// The dedupe key includes a coarse time bucket so a chained chunk is not
	// mistaken for the one that scheduled it, while a duplicate enqueue in the
	// same minute still collapses.
	dedupeKey := fmt.Sprintf("%s:%s:backfill:%d", adAccountID, level, now.Unix()/60)
	return s.enqueueAdAccountJob(
		ctx,
		account.ConnectionID,
		JobBackfillInsights,
		dedupeKey,
		backfillJobPriority,
		domain.MustJSON(BackfillInsightsJobPayload{AdAccountID: adAccountID, Level: level}),
		now,
	)
}

// seedReleaseSpacing spreads the initial burst. At 400ms a 250-account
// profile finishes releasing in under two minutes, which is far quicker than
// the work itself takes.
const seedReleaseSpacing = 400 * time.Millisecond

const (
	// Live polling outranks history. The existing recurring jobs use 20 and
	// 10; backfill sits below both so a long backfill cannot delay today's
	// spend numbers.
	accountInsightsJobPriority = 18
	entitySyncJobPriority      = 12
	gapRepairJobPriority       = 8
	windowedReachJobPriority   = 6
	backfillJobPriority        = 5
	retentionJobPriority       = 1
)

func validInsightLevel(level domain.InsightLevel) bool {
	switch level {
	case domain.InsightAccount, domain.InsightCampaign, domain.InsightAdSet, domain.InsightAd:
		return true
	default:
		return false
	}
}

// seedAccountTracking prepares every ad account of a connection for
// account-wide tracking: a sync-state row with a backfill target, an
// immediate inventory sync so entity names exist before the first insight
// rows land, and a backfill chain.
//
// Called after discovery, so a newly connected account starts producing
// history rather than only accumulating it from today forward.
func (s *Service) seedAccountTracking(
	ctx context.Context,
	connectionID uuid.UUID,
	now time.Time,
) error {
	// Backfill covers every discovered account, including ones Meta has
	// disabled: their spend already happened and is worth storing once.
	accounts, err := s.Repos.InsightsCursors.AllAdAccountsForBackfill(ctx, connectionID)
	if err != nil {
		return err
	}
	backfillDays := s.Config.Worker.InsightsBackfillDays
	var failures []error

	// Connecting a profile with hundreds of ad accounts would otherwise
	// release every seeded job at once - one real account produced 717 of
	// them - and the worker pool would spend the next hour hammering Meta at
	// full tilt. Spreading the release keeps the initial sync polite without
	// making it meaningfully slower overall.
	release := func(index int) time.Time {
		return now.Add(time.Duration(index) * seedReleaseSpacing)
	}

	for index, account := range accounts {
		var target *time.Time
		if backfillDays > 0 {
			day := meta.AccountDay(now, account.TimezoneName).AddDate(0, 0, -backfillDays)
			target = &day
		}
		state, err := s.Repos.AdAccountSync.Ensure(ctx, account.AdAccountID, connectionID, target)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// Only seed work for an account that has never been synced. A
		// reconnect must not restart a backfill that already completed.
		if state.EntitiesSyncedAt != nil {
			continue
		}
		if _, err := s.enqueueAdAccountJob(
			ctx,
			connectionID,
			JobSyncAdEntities,
			fmt.Sprintf("%s:entities:seed", account.AdAccountID),
			entitySyncJobPriority,
			domain.MustJSON(AdEntitiesJobPayload{AdAccountID: account.AdAccountID}),
			release(index),
		); err != nil {
			failures = append(failures, err)
		}
		if backfillDays <= 0 {
			continue
		}
		for _, level := range []domain.InsightLevel{
			domain.InsightAccount, domain.InsightCampaign,
		} {
			if _, err := s.enqueueAdAccountJob(
				ctx,
				connectionID,
				JobBackfillInsights,
				fmt.Sprintf("%s:%s:backfill:seed", account.AdAccountID, level),
				backfillJobPriority,
				domain.MustJSON(BackfillInsightsJobPayload{
					AdAccountID: account.AdAccountID,
					Level:       level,
				}),
				release(index),
			); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
