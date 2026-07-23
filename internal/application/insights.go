package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/rules"
)

const (
	// Filtering keeps each synchronous Graph query bounded while still
	// collapsing the previous one-request-per-object fan-out.
	insightObjectChunkSize         = 50
	publishedStatusRefreshInterval = time.Hour
)

func (s *Service) CollectInsights(ctx context.Context, connectionID uuid.UUID) error {
	_, token, err := s.accessToken(ctx, connectionID)
	if err != nil {
		return err
	}
	var objects []domain.PublishedObject
	if err := s.Repos.DB().WithContext(ctx).
		Where("connection_id = ? AND object_type IN ?", connectionID, []domain.PublishedObjectType{
			domain.PublishedCampaign,
			domain.PublishedAdSet,
			domain.PublishedAd,
		}).
		Order("created_at ASC").
		Find(&objects).Error; err != nil {
		return err
	}
	accounts, err := s.loadInsightAccounts(ctx, connectionID, objects)
	if err != nil {
		return err
	}
	now := s.Now()
	var failures []error
	for index := range objects {
		object := &objects[index]
		if !publishedStatusRefreshDue(object.LastSyncedAt, now) {
			continue
		}
		statusResult, statusErr := s.Meta.GetEntityStatus(ctx, token, object.MetaObjectID)
		if statusErr != nil {
			failures = append(failures, fmt.Errorf("fetch %s status: %w", object.MetaObjectID, statusErr))
			expired, markErr := s.markConnectionExpiredForMetaError(ctx, connectionID, statusErr)
			if markErr != nil {
				failures = append(failures, markErr)
			}
			if expired {
				return s.finishInsightCollection(ctx, connectionID, failures)
			}
			if err := s.Repos.Batches.MarkPublishedStatusChecked(ctx, object.ID, now); err != nil {
				failures = append(failures, fmt.Errorf("store %s failed status check: %w", object.MetaObjectID, err))
			} else {
				object.LastSyncedAt = &now
			}
			continue
		}
		effectiveStatus := firstNonEmpty(
			statusResult.EffectiveStatus,
			statusResult.ConfiguredStatus,
			statusResult.Status,
			object.EffectiveStatus,
		)
		if err := s.Repos.Batches.UpdatePublishedStatus(
			ctx,
			object.ID,
			effectiveStatus,
			domain.MustJSON(statusResult),
			now,
		); err != nil {
			failures = append(failures, fmt.Errorf("store %s status: %w", object.MetaObjectID, err))
			continue
		}
		object.LastSyncedAt = &now
		object.EffectiveStatus = effectiveStatus
	}

	batches, batchErrors := buildInsightCollectionBatches(objects, accounts, insightObjectChunkSize)
	failures = append(failures, batchErrors...)
	for _, batch := range batches {
		rows, fetchErr := s.Meta.FetchAccountInsights(ctx, token, batch.accountNodeID, meta.InsightQuery{
			Level: batch.level,
			TimeRange: &meta.InsightTimeRange{
				Since: batch.earliestCreatedAt().UTC().Format("2006-01-02"),
				Until: now.UTC().Format("2006-01-02"),
			},
			TimeIncrement: "all_days",
			Filtering: []meta.InsightFilter{{
				Field:    batch.filterField,
				Operator: "IN",
				Value:    batch.metaObjectIDs(),
			}},
			Limit: 500,
		})
		if fetchErr != nil {
			failures = append(failures, fmt.Errorf(
				"fetch %s insights for account %s (%d objects): %w",
				batch.level,
				batch.accountNodeID,
				len(batch.objects),
				fetchErr,
			))
			expired, markErr := s.markConnectionExpiredForMetaError(ctx, connectionID, fetchErr)
			if markErr != nil {
				failures = append(failures, markErr)
			}
			if expired {
				return s.finishInsightCollection(ctx, connectionID, failures)
			}
			continue
		}
		rowsByObjectID, rowErrors := groupInsightRows(batch.level, rows)
		failures = append(failures, rowErrors...)

		snapshots := make([]domain.InsightSnapshot, 0, len(batch.objects))
		for _, object := range batch.objects {
			snapshot, convertErr := insightSnapshot(
				connectionID,
				batch.account,
				object,
				rowsByObjectID[object.MetaObjectID],
				now,
			)
			if convertErr != nil {
				failures = append(failures, fmt.Errorf("normalize %s insights: %w", object.MetaObjectID, convertErr))
				continue
			}
			snapshots = append(snapshots, snapshot)
		}
		if err := s.Repos.Insights.UpsertMany(ctx, snapshots); err != nil {
			failures = append(failures, fmt.Errorf(
				"store %s insights for account %s (%d snapshots): %w",
				batch.level,
				batch.accountNodeID,
				len(snapshots),
				err,
			))
		}
	}
	return s.finishInsightCollection(ctx, connectionID, failures)
}

func (s *Service) finishInsightCollection(
	ctx context.Context,
	connectionID uuid.UUID,
	failures []error,
) error {
	if len(failures) > 0 {
		s.audit(ctx, domain.AuditEvent{
			ConnectionID: &connectionID,
			ActorType:    "worker",
			Action:       "insights.collection.partial_failure",
			EntityType:   "meta_connection",
			EntityID:     connectionID.String(),
			Severity:     domain.AuditWarning,
			Metadata:     domain.MustJSON(map[string]any{"failures": errorStrings(failures)}),
		})
	}
	return errors.Join(failures...)
}

type insightCollectionBatch struct {
	account       *domain.AdAccount
	accountNodeID string
	level         meta.InsightLevel
	filterField   string
	objects       []*domain.PublishedObject
}

func (s *Service) loadInsightAccounts(
	ctx context.Context,
	connectionID uuid.UUID,
	objects []domain.PublishedObject,
) (map[uuid.UUID]*domain.AdAccount, error) {
	accountIDs := make([]uuid.UUID, 0)
	seen := make(map[uuid.UUID]struct{})
	for index := range objects {
		accountID := objects[index].AdAccountID
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		accountIDs = append(accountIDs, accountID)
	}
	if len(accountIDs) == 0 {
		return map[uuid.UUID]*domain.AdAccount{}, nil
	}

	var records []domain.AdAccount
	if err := s.Repos.DB().WithContext(ctx).
		Where("connection_id = ? AND id IN ?", connectionID, accountIDs).
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("load Insights ad accounts: %w", err)
	}
	accounts := make(map[uuid.UUID]*domain.AdAccount, len(records))
	for index := range records {
		record := &records[index]
		accounts[record.ID] = record
	}
	return accounts, nil
}

func buildInsightCollectionBatches(
	objects []domain.PublishedObject,
	accounts map[uuid.UUID]*domain.AdAccount,
	chunkSize int,
) ([]insightCollectionBatch, []error) {
	if chunkSize <= 0 {
		chunkSize = insightObjectChunkSize
	}
	type groupKey struct {
		accountID uuid.UUID
		level     meta.InsightLevel
	}
	type group struct {
		account       *domain.AdAccount
		accountNodeID string
		level         meta.InsightLevel
		filterField   string
		objects       []*domain.PublishedObject
	}

	var failures []error
	groups := make([]group, 0)
	groupIndexes := make(map[groupKey]int)
	for index := range objects {
		object := &objects[index]
		account := accounts[object.AdAccountID]
		if account == nil {
			failures = append(failures, fmt.Errorf(
				"load account for %s: ad account %s was not found for this connection",
				object.MetaObjectID,
				object.AdAccountID,
			))
			continue
		}
		rawAccountID := firstNonEmpty(account.MetaAdAccountID, account.AccountID)
		if strings.TrimSpace(rawAccountID) == "" {
			failures = append(failures, fmt.Errorf(
				"load account for %s: ad account %s has an empty Meta account ID",
				object.MetaObjectID,
				object.AdAccountID,
			))
			continue
		}
		accountNodeID := meta.AdAccountNodeID(rawAccountID)
		if strings.TrimSpace(object.MetaObjectID) == "" {
			failures = append(failures, fmt.Errorf("published object %s has an empty Meta object ID", object.ID))
			continue
		}
		level, err := metaLevelForObject(object.ObjectType)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		filterField, err := insightFilterField(level)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		key := groupKey{accountID: object.AdAccountID, level: level}
		groupIndex, exists := groupIndexes[key]
		if !exists {
			groupIndex = len(groups)
			groupIndexes[key] = groupIndex
			groups = append(groups, group{
				account:       account,
				accountNodeID: accountNodeID,
				level:         level,
				filterField:   filterField,
			})
		}
		groups[groupIndex].objects = append(groups[groupIndex].objects, object)
	}

	batches := make([]insightCollectionBatch, 0, len(groups))
	for _, grouped := range groups {
		for start := 0; start < len(grouped.objects); start += chunkSize {
			stop := min(start+chunkSize, len(grouped.objects))
			batches = append(batches, insightCollectionBatch{
				account:       grouped.account,
				accountNodeID: grouped.accountNodeID,
				level:         grouped.level,
				filterField:   grouped.filterField,
				objects:       grouped.objects[start:stop],
			})
		}
	}
	return batches, failures
}

func (batch insightCollectionBatch) metaObjectIDs() []string {
	ids := make([]string, 0, len(batch.objects))
	for _, object := range batch.objects {
		ids = append(ids, object.MetaObjectID)
	}
	return ids
}

func (batch insightCollectionBatch) earliestCreatedAt() time.Time {
	if len(batch.objects) == 0 {
		return time.Time{}
	}
	earliest := batch.objects[0].CreatedAt
	for _, object := range batch.objects[1:] {
		if object.CreatedAt.Before(earliest) {
			earliest = object.CreatedAt
		}
	}
	return earliest
}

func groupInsightRows(
	level meta.InsightLevel,
	rows []meta.InsightRow,
) (map[string][]meta.InsightRow, []error) {
	grouped := make(map[string][]meta.InsightRow)
	var failures []error
	for index := range rows {
		objectID := insightRowObjectID(level, rows[index])
		if objectID == "" {
			failures = append(failures, fmt.Errorf(
				"Meta %s Insights row %d is missing its object ID",
				level,
				index,
			))
			continue
		}
		grouped[objectID] = append(grouped[objectID], rows[index])
	}
	return grouped, failures
}

func insightRowObjectID(level meta.InsightLevel, row meta.InsightRow) string {
	switch level {
	case meta.InsightLevelCampaign:
		return row.CampaignID
	case meta.InsightLevelAdSet:
		return row.AdSetID
	case meta.InsightLevelAd:
		return row.AdID
	default:
		return ""
	}
}

func insightFilterField(level meta.InsightLevel) (string, error) {
	switch level {
	case meta.InsightLevelCampaign:
		return "campaign.id", nil
	case meta.InsightLevelAdSet:
		return "adset.id", nil
	case meta.InsightLevelAd:
		return "ad.id", nil
	default:
		return "", fmt.Errorf("Insights level %q cannot be filtered by published object ID", level)
	}
}

func publishedStatusRefreshDue(lastSyncedAt *time.Time, now time.Time) bool {
	if lastSyncedAt == nil {
		return true
	}
	return !lastSyncedAt.After(now) && now.Sub(*lastSyncedAt) >= publishedStatusRefreshInterval
}

func insightSnapshot(
	connectionID uuid.UUID,
	account *domain.AdAccount,
	object *domain.PublishedObject,
	rows []meta.InsightRow,
	now time.Time,
) (domain.InsightSnapshot, error) {
	metrics := make(map[string]float64)
	rawRows := make([]map[string]json.RawMessage, 0, len(rows))
	var dateStart, dateStop time.Time
	for _, row := range rows {
		rawRows = append(rawRows, row.Raw)
		encoded, err := json.Marshal(row.Raw)
		if err != nil {
			return domain.InsightSnapshot{}, err
		}
		flattened, err := rules.FlattenInsightsJSON(encoded)
		if err != nil {
			return domain.InsightSnapshot{}, err
		}
		for metric, value := range flattened {
			metrics[metric] += value
		}
		if parsed := parseMetaDate(row.DateStart); !parsed.IsZero() && (dateStart.IsZero() || parsed.Before(dateStart)) {
			dateStart = parsed
		}
		if parsed := parseMetaDate(row.DateStop); !parsed.IsZero() && parsed.After(dateStop) {
			dateStop = parsed
		}
	}
	metrics = rules.WithDerivedMetrics(metrics)
	if dateStart.IsZero() {
		dateStart = truncateDate(object.CreatedAt)
	}
	if dateStop.IsZero() {
		dateStop = truncateDate(now)
	}
	level, err := domainLevelForObject(object.ObjectType)
	if err != nil {
		return domain.InsightSnapshot{}, err
	}
	windowStart := object.CreatedAt.UTC()
	if !windowStart.Before(now) {
		// PostgreSQL stores timestamptz at microsecond precision and enforces a
		// strictly positive snapshot window.
		windowStart = now.Add(-time.Microsecond)
	}
	return domain.InsightSnapshot{
		ConnectionID:       connectionID,
		AdAccountID:        account.ID,
		PublishedObjectID:  &object.ID,
		MetaObjectID:       object.MetaObjectID,
		Level:              level,
		DateStart:          dateStart,
		DateStop:           dateStop,
		WindowStart:        windowStart,
		WindowEnd:          now,
		AccountTimezone:    account.TimezoneName,
		AttributionSetting: "account_default",
		QueryHash:          LifetimeInsightQueryHash,
		Impressions:        int64(metric(metrics, "impressions")),
		Reach:              int64(metric(metrics, "reach")),
		Clicks:             int64(metric(metrics, "clicks")),
		LinkClicks:         int64(firstMetric(metrics, "inline_link_clicks", "link_clicks")),
		LandingPageViews: firstMetric(metrics,
			"actions.landing_page_view",
			"actions.offsite_conversion.fb_pixel_landing_page_view",
		),
		Actions:     prefixSum(metrics, "actions."),
		Conversions: prefixSum(metrics, "conversions."),
		Leads: firstMetric(metrics,
			"actions.lead",
			"actions.offsite_conversion.fb_pixel_lead",
		),
		Registrations: firstMetric(metrics,
			"actions.complete_registration",
			"actions.offsite_conversion.fb_pixel_complete_registration",
			"actions.omni_complete_registration",
		),
		Purchases: firstMetric(metrics,
			"actions.purchase",
			"actions.offsite_conversion.fb_pixel_purchase",
			"actions.omni_purchase",
		),
		Spend:         metric(metrics, "spend"),
		Frequency:     metric(metrics, "frequency"),
		CTR:           metric(metrics, "ctr"),
		CPC:           metric(metrics, "cpc"),
		CPM:           metric(metrics, "cpm"),
		CostPerAction: safeRatio(metric(metrics, "spend"), prefixSum(metrics, "actions.")),
		PurchaseValue: firstMetric(metrics,
			"action_values.purchase",
			"action_values.offsite_conversion.fb_pixel_purchase",
			"action_values.omni_purchase",
		),
		ROAS: firstMetric(metrics,
			"purchase_roas.omni_purchase",
			"website_purchase_roas.offsite_conversion.fb_pixel_purchase",
			"roas.purchase",
		),
		Breakdowns: emptyObject(),
		Metrics:    domain.MustJSON(metrics),
		RawJSON:    domain.MustJSON(map[string]any{"rows": rawRows}),
		FetchedAt:  now,
	}, nil
}

func metaLevelForObject(objectType domain.PublishedObjectType) (meta.InsightLevel, error) {
	switch objectType {
	case domain.PublishedCampaign:
		return meta.InsightLevelCampaign, nil
	case domain.PublishedAdSet:
		return meta.InsightLevelAdSet, nil
	case domain.PublishedAd:
		return meta.InsightLevelAd, nil
	default:
		return "", fmt.Errorf("object type %q has no insights level", objectType)
	}
}

func domainLevelForObject(objectType domain.PublishedObjectType) (domain.InsightLevel, error) {
	switch objectType {
	case domain.PublishedCampaign:
		return domain.InsightCampaign, nil
	case domain.PublishedAdSet:
		return domain.InsightAdSet, nil
	case domain.PublishedAd:
		return domain.InsightAd, nil
	default:
		return "", fmt.Errorf("object type %q has no insights level", objectType)
	}
}

func parseMetaDate(value string) time.Time {
	parsed, _ := time.Parse("2006-01-02", value)
	return parsed.UTC()
}

func truncateDate(value time.Time) time.Time {
	parsed, _ := time.Parse("2006-01-02", value.UTC().Format("2006-01-02"))
	return parsed.UTC()
}

func metric(metrics map[string]float64, key string) float64 {
	value := metrics[key]
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func firstMetric(metrics map[string]float64, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := metrics[key]; ok {
			return value
		}
	}
	return 0
}

func prefixSum(metrics map[string]float64, prefix string) float64 {
	var total float64
	for key, value := range metrics {
		if strings.HasPrefix(key, prefix) && !hasAttributionSuffix(key) {
			total += value
		}
	}
	return total
}

func hasAttributionSuffix(metricName string) bool {
	for _, suffix := range []string{
		".1d_click",
		".1d_view",
		".7d_click",
		".7d_view",
		".28d_click",
		".28d_view",
		".inline",
	} {
		if strings.HasSuffix(metricName, suffix) {
			return true
		}
	}
	return false
}

func safeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func errorStrings(errors []error) []string {
	result := make([]string, 0, len(errors))
	for _, err := range errors {
		result = append(result, err.Error())
	}
	return result
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}
