package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// effectiveStatusesFor returns the effective_status filter for one edge, or
// nil to omit the filter entirely.
//
// Omitting it is the default, and deliberate. The values are not
// interchangeable across edges - review states exist only on ads - and Meta
// rejects the whole request with 100/1815001 when an edge receives one it
// does not define, so a single wrong value takes out an entire inventory
// sweep rather than degrading it. The accepted enum also drifts between API
// versions, which makes a hardcoded list a recurring outage waiting for the
// next upgrade.
//
// Without the filter Meta returns the account's live objects, which is what
// an inventory sweep wants. Nothing is lost by dropping it: an object that
// stops being returned is soft-deleted via disappeared_at rather than
// removed, and its insight rows survive because insights are queried at the
// account level, not per entity.
func effectiveStatusesFor(level domain.AdEntityLevel) []string {
	return nil
}

// SyncAdEntities refreshes the campaign, ad set and ad inventory of one ad
// account, including objects created outside this service.
func (s *Service) SyncAdEntities(ctx context.Context, adAccountID uuid.UUID) error {
	account, err := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
	if err != nil {
		return err
	}
	if _, err := s.Repos.AdAccountSync.Ensure(ctx, account.ID, account.ConnectionID, nil); err != nil {
		return err
	}
	_, token, err := s.accessToken(ctx, account.ConnectionID)
	if err != nil {
		return err
	}

	audit, err := s.Meta.AuditAdAccount(ctx, token, account.AccountID, meta.AdAccountAuditStatuses{
		Campaigns: effectiveStatusesFor(domain.AdEntityCampaign),
		AdSets:    effectiveStatusesFor(domain.AdEntityAdSet),
		Ads:       effectiveStatusesFor(domain.AdEntityAd),
		// The sweep starts at 200 rather than the maximum: the ad edge carries
		// a full creative sub-selection, and CollectPagesAdaptive halves this
		// further for accounts that still return too much.
	}, 200)
	if err != nil {
		if expired, markErr := s.markConnectionExpiredForMetaError(ctx, account.ConnectionID, err); expired {
			return errors.Join(err, markErr)
		}
		return errors.Join(err, s.Repos.AdAccountSync.RecordFailure(
			ctx, adAccountID, truncateError(err), s.Now(),
		))
	}

	now := s.Now()
	groups := []struct {
		level   domain.AdEntityLevel
		records []map[string]any
	}{
		{domain.AdEntityCampaign, audit.Campaigns},
		{domain.AdEntityAdSet, audit.AdSets},
		{domain.AdEntityAd, audit.Ads},
	}

	var failures []error
	for _, group := range groups {
		entities := make([]domain.AdEntity, 0, len(group.records))
		seen := make([]string, 0, len(group.records))
		for _, record := range group.records {
			entity, ok := adEntityFromGraph(record, group.level, account, now)
			if !ok {
				continue
			}
			entities = append(entities, entity)
			seen = append(seen, entity.MetaObjectID)
		}
		if err := s.Repos.AdEntities.UpsertMany(ctx, entities); err != nil {
			failures = append(failures, fmt.Errorf("store %s entities: %w", group.level, err))
			continue
		}
		// Only prune when the sweep returned something. An empty result is
		// ambiguous - it can mean a genuinely empty account or a partial
		// upstream response - and treating it as "everything is gone" would
		// mass-disappear a live account's inventory.
		if len(seen) == 0 {
			continue
		}
		if _, err := s.Repos.AdEntities.MarkDisappeared(ctx, adAccountID, group.level, seen, now); err != nil {
			failures = append(failures, fmt.Errorf("prune %s entities: %w", group.level, err))
		}
	}
	if len(failures) > 0 {
		joined := errors.Join(failures...)
		return errors.Join(joined, s.Repos.AdAccountSync.RecordFailure(
			ctx, adAccountID, truncateError(joined), now,
		))
	}
	return s.Repos.AdAccountSync.MarkEntitiesSynced(ctx, adAccountID, now)
}

// AccountInsightsRequest describes one ingestion pass. Dates are resolved by
// the caller and frozen, so a retried job re-fetches exactly the same range
// and the upsert stays deterministic.
type AccountInsightsRequest struct {
	AdAccountID uuid.UUID
	Level       domain.InsightLevel
	Since       string
	Until       string
	// Reason is recorded for observability: incremental, lookback, backfill
	// or gap_repair.
	Reason string
	// AdvanceWatermark is false for backfill and gap repair, which work on
	// ranges behind the live edge and must not move it.
	AdvanceWatermark bool
}

// CollectAccountInsights fetches daily rows for one ad account and level,
// stores them, and records coverage for every day in the range.
func (s *Service) CollectAccountInsights(ctx context.Context, request AccountInsightsRequest) error {
	account, err := s.Repos.Inventory.GetAdAccount(ctx, request.AdAccountID)
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
	rows, _, err := s.Meta.FetchDailyInsights(ctx, token, meta.DailyInsightRequest{
		AccountID:   account.AccountID,
		Level:       meta.InsightLevel(request.Level),
		Since:       request.Since,
		Until:       request.Until,
		Attribution: attribution,
	})
	if err != nil {
		if expired, markErr := s.markConnectionExpiredForMetaError(ctx, account.ConnectionID, err); expired {
			return errors.Join(err, markErr)
		}
		return errors.Join(err, s.Repos.AdAccountSync.RecordFailure(
			ctx, request.AdAccountID, truncateError(err), s.Now(),
		))
	}

	now := s.Now()
	rowContext := dailyRowContext{
		ConnectionID:       account.ConnectionID,
		AdAccountID:        account.ID,
		MetaAccountID:      account.MetaAdAccountID,
		AccountTimezone:    account.TimezoneName,
		Currency:           account.Currency,
		AttributionSetting: attribution.Setting(),
		Level:              request.Level,
		FetchedAt:          now,
	}

	records := make([]domain.AdInsightDaily, 0, len(rows))
	counts := map[time.Time]int{}
	for _, row := range rows {
		record, ok, mapErr := dailyInsightFromRow(row, rowContext)
		if mapErr != nil {
			return mapErr
		}
		if !ok {
			continue
		}
		records = append(records, record)
		counts[record.Date]++
	}

	if err := s.Repos.AdInsights.UpsertDaily(ctx, records); err != nil {
		return err
	}

	// Every day in the requested range gets a coverage row, including days
	// that produced nothing. A zero count is the answer "checked, nothing
	// ran", which is what stops gap repair re-fetching quiet days forever.
	since, err := meta.ParseAccountDate(request.Since)
	if err != nil {
		return err
	}
	until, err := meta.ParseAccountDate(request.Until)
	if err != nil {
		return err
	}
	for _, day := range meta.EachDay(since, until) {
		if _, ok := counts[day]; !ok {
			counts[day] = 0
		}
	}
	if err := s.Repos.AdInsights.MarkCoverage(ctx, account.ID, request.Level, counts, now); err != nil {
		return err
	}

	if request.AdvanceWatermark {
		return s.Repos.AdAccountSync.AdvanceSyncedThrough(ctx, account.ID, request.Level, until, now)
	}
	return nil
}

// backfillChunkDays bounds a backfill chunk by expected row count rather than
// day count. time_increment=1 multiplies rows by days, so a 30-day ad-level
// chunk on a large account is tens of thousands of rows and many pages.
func backfillChunkDays(level domain.InsightLevel) int {
	switch level {
	case domain.InsightAccount, domain.InsightCampaign:
		return 30
	case domain.InsightAdSet:
		return 14
	default:
		return 7
	}
}

// BackfillChunk computes the next range to backfill for a level, walking
// backwards from the oldest day already stored towards the target date.
// ok is false once the target has been reached.
func (s *Service) BackfillChunk(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
) (since, until time.Time, ok bool, err error) {
	state, err := s.Repos.AdAccountSync.Get(ctx, adAccountID)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if state.BackfillTargetDate == nil {
		return time.Time{}, time.Time{}, false, nil
	}
	target := state.BackfillTargetDate.UTC()

	oldest, _, err := s.Repos.AdInsights.CoverageBounds(ctx, adAccountID, level)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if oldest == nil {
		// Nothing fetched yet; the incremental pass owns the live edge, so
		// backfill starts just behind it.
		account, accountErr := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
		if accountErr != nil {
			return time.Time{}, time.Time{}, false, accountErr
		}
		today := meta.AccountDay(s.Now(), account.TimezoneName)
		until = today.AddDate(0, 0, -1)
	} else {
		until = oldest.UTC().AddDate(0, 0, -1)
	}

	if until.Before(target) {
		return time.Time{}, time.Time{}, false, nil
	}
	since = until.AddDate(0, 0, -(backfillChunkDays(level) - 1))
	if since.Before(target) {
		since = target
	}
	return since, until, true, nil
}

// BackfillAccountInsights fetches one chunk of history and records how far
// back the account now reaches. Each chunk is a separate unit of work so a
// failure retries only that chunk.
func (s *Service) BackfillAccountInsights(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
) (bool, error) {
	since, until, ok, err := s.BackfillChunk(ctx, adAccountID, level)
	if err != nil || !ok {
		return false, err
	}
	err = s.CollectAccountInsights(ctx, AccountInsightsRequest{
		AdAccountID: adAccountID,
		Level:       level,
		Since:       since.Format(meta.DateLayout),
		Until:       until.Format(meta.DateLayout),
		Reason:      "backfill",
	})
	if err != nil {
		return false, err
	}
	return true, s.Repos.AdAccountSync.SetBackfilledThrough(ctx, adAccountID, since, s.Now())
}

// maxGapRepairDays bounds one repair pass so a long outage cannot produce a
// single unbounded request.
const maxGapRepairDays = 30

// RepairInsightGaps re-fetches days that were never covered. Contiguous
// missing days are merged into ranges so a week-long outage costs one request
// per level, not seven.
func (s *Service) RepairInsightGaps(
	ctx context.Context,
	adAccountID uuid.UUID,
	level domain.InsightLevel,
	since, until time.Time,
) (int, error) {
	missing, err := s.Repos.AdInsights.MissingDates(ctx, adAccountID, level, since, until)
	if err != nil {
		return 0, err
	}
	ranges := contiguousRanges(missing, maxGapRepairDays)
	for _, window := range ranges {
		err := s.CollectAccountInsights(ctx, AccountInsightsRequest{
			AdAccountID: adAccountID,
			Level:       level,
			Since:       window[0].Format(meta.DateLayout),
			Until:       window[1].Format(meta.DateLayout),
			Reason:      "gap_repair",
		})
		if err != nil {
			return 0, err
		}
	}
	return len(ranges), nil
}

// contiguousRanges groups sorted dates into [start, end] pairs, splitting any
// run longer than maxDays.
func contiguousRanges(days []time.Time, maxDays int) [][2]time.Time {
	if len(days) == 0 {
		return nil
	}
	if maxDays <= 0 {
		maxDays = 1
	}
	var ranges [][2]time.Time
	start := days[0].UTC()
	previous := start
	length := 1

	for _, day := range days[1:] {
		current := day.UTC()
		if current.Equal(previous.AddDate(0, 0, 1)) && length < maxDays {
			previous = current
			length++
			continue
		}
		ranges = append(ranges, [2]time.Time{start, previous})
		start, previous, length = current, current, 1
	}
	return append(ranges, [2]time.Time{start, previous})
}

func attributionModeFor(setting string) meta.AttributionMode {
	setting = strings.TrimSpace(setting)
	if setting == "" || setting == "unified" {
		return meta.AttributionMode{Unified: true}
	}
	if setting == "account_default" {
		return meta.AttributionMode{}
	}
	windows := make([]string, 0, 4)
	for _, window := range strings.Split(setting, ",") {
		if trimmed := strings.TrimSpace(window); trimmed != "" {
			windows = append(windows, trimmed)
		}
	}
	if len(windows) == 0 {
		return meta.AttributionMode{Unified: true}
	}
	return meta.AttributionMode{Windows: windows}
}

// truncateError keeps a stored error message short enough to be readable.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 1000 {
		return message[:1000]
	}
	return message
}

// adEntityFromGraph promotes the fields worth querying into columns and keeps
// the whole Graph object in raw, so a field this service does not model yet is
// still recoverable without re-fetching.
func adEntityFromGraph(
	record map[string]any,
	level domain.AdEntityLevel,
	account *domain.AdAccount,
	now time.Time,
) (domain.AdEntity, bool) {
	id := graphString(record, "id")
	if id == "" {
		return domain.AdEntity{}, false
	}
	entity := domain.AdEntity{
		ConnectionID:     account.ConnectionID,
		AdAccountID:      account.ID,
		Level:            level,
		MetaObjectID:     id,
		CampaignMetaID:   graphString(record, "campaign_id"),
		AdSetMetaID:      graphString(record, "adset_id"),
		Name:             graphString(record, "name"),
		Status:           graphString(record, "status"),
		ConfiguredStatus: graphString(record, "configured_status"),
		EffectiveStatus:  graphString(record, "effective_status"),
		Objective:        graphString(record, "objective"),
		BuyingType:       graphString(record, "buying_type"),
		OptimizationGoal: graphString(record, "optimization_goal"),
		BillingEvent:     graphString(record, "billing_event"),
		DestinationType:  graphString(record, "destination_type"),
		BidStrategy:      graphString(record, "bid_strategy"),
		DailyBudget:      graphInt64(record, "daily_budget"),
		LifetimeBudget:   graphInt64(record, "lifetime_budget"),
		BudgetRemaining:  graphInt64(record, "budget_remaining"),
		BidAmount:        graphInt64(record, "bid_amount"),
		SpendCap:         graphInt64(record, "spend_cap"),
		StartTime:        graphTime(record, "start_time"),
		StopTime:         graphTime(record, "stop_time", "end_time"),
		MetaCreatedTime:  graphTime(record, "created_time"),
		MetaUpdatedTime:  graphTime(record, "updated_time"),
		RawJSON:          domain.MustJSON(record),
		FirstSeenAt:      now,
		LastSeenAt:       now,
	}

	switch level {
	case domain.AdEntityAdSet:
		entity.ParentMetaObjectID = entity.CampaignMetaID
	case domain.AdEntityAd:
		entity.ParentMetaObjectID = entity.AdSetMetaID
	}
	return entity, true
}

func graphString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}

// graphInt64 reads a Meta money/count field. Budgets arrive as strings of
// minor units; a JSON number is accepted too.
func graphInt64(record map[string]any, key string) int64 {
	value, ok := record[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	case float64:
		return int64(typed)
	case int64:
		return typed
	default:
		return 0
	}
}

func graphTime(record map[string]any, keys ...string) *time.Time {
	for _, key := range keys {
		text := graphString(record, key)
		if text == "" {
			continue
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				utc := parsed.UTC()
				return &utc
			}
		}
	}
	return nil
}
