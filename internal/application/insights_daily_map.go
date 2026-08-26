package application

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
	"github.com/watchers-factory/raze-ads/internal/rules"
)

// dailyRowContext carries the account-level facts that a single insight row
// does not repeat but a stored row needs.
type dailyRowContext struct {
	ConnectionID       uuid.UUID
	AdAccountID        uuid.UUID
	MetaAccountID      string
	AccountTimezone    string
	Currency           string
	AttributionSetting string
	Level              domain.InsightLevel
	FetchedAt          time.Time
}

// objectIDForLevel picks the identifier a row is keyed by. Meta returns every
// ancestor ID on each row, so the level decides which one identifies it.
func objectIDForLevel(row meta.InsightRow, level domain.InsightLevel) string {
	switch level {
	case domain.InsightAccount:
		return row.AccountID
	case domain.InsightCampaign:
		return row.CampaignID
	case domain.InsightAdSet:
		return row.AdSetID
	case domain.InsightAd:
		return row.AdID
	default:
		return ""
	}
}

func objectNameForLevel(row meta.InsightRow, level domain.InsightLevel) string {
	switch level {
	case domain.InsightAccount:
		return row.AccountName
	case domain.InsightCampaign:
		return row.CampaignName
	case domain.InsightAdSet:
		return row.AdSetName
	case domain.InsightAd:
		return row.AdName
	default:
		return ""
	}
}

// dailyInsightFromRow converts one Graph insight row into a storable daily
// record. It returns ok=false for a row that carries no identifier at the
// requested level, which Meta occasionally emits for deleted objects.
func dailyInsightFromRow(
	row meta.InsightRow,
	context dailyRowContext,
) (domain.AdInsightDaily, bool, error) {
	objectID := objectIDForLevel(row, context.Level)
	if objectID == "" {
		return domain.AdInsightDaily{}, false, nil
	}
	// date_start and date_stop are equal under time_increment=1; date_start is
	// the day this row describes.
	date, err := meta.ParseAccountDate(row.DateStart)
	if err != nil {
		return domain.AdInsightDaily{}, false, err
	}

	currency := context.Currency
	if fromRow := rawString(row.Raw, "account_currency"); fromRow != "" {
		currency = fromRow
	}

	record := domain.AdInsightDaily{
		ConnectionID:       context.ConnectionID,
		AdAccountID:        context.AdAccountID,
		Level:              context.Level,
		MetaObjectID:       objectID,
		MetaAccountID:      firstNonEmptyString(row.AccountID, context.MetaAccountID),
		ObjectName:         objectNameForLevel(row, context.Level),
		Date:               date,
		AccountTimezone:    context.AccountTimezone,
		Currency:           currency,
		AttributionSetting: context.AttributionSetting,
		FetchedAt:          context.FetchedAt,

		Spend:                  parseFloat(row.Spend),
		Impressions:            parseInt64(row.Impressions),
		Reach:                  parseInt64(row.Reach),
		Frequency:              parseFloat(row.Frequency),
		Clicks:                 parseInt64(row.Clicks),
		UniqueClicks:           parseInt64(row.UniqueClicks),
		InlineLinkClicks:       parseInt64(row.InlineLinkClicks),
		UniqueInlineLinkClicks: parseInt64(row.UniqueInlineLinkClicks),
		CTR:                    parseFloat(row.CTR),
		UniqueCTR:              parseFloat(row.UniqueCTR),
		CPC:                    parseFloat(row.CPC),
		CPM:                    parseFloat(row.CPM),
		CPP:                    parseFloat(row.CPP),
		CostPerUniqueClick:     parseFloat(row.CostPerUniqueClick),
		CostPerInlineLinkClick: parseFloat(row.CostPerInlineLinkClick),
		QualityRanking:         row.QualityRanking,
		EngagementRateRanking:  row.EngagementRateRanking,
		ConversionRateRanking:  row.ConversionRateRanking,
	}

	// Parent identifiers, so a campaign rollup can be built without joining
	// back through ad_entities.
	if context.Level == domain.InsightAdSet || context.Level == domain.InsightAd {
		record.CampaignMetaID = row.CampaignID
	}
	if context.Level == domain.InsightAd {
		record.AdSetMetaID = row.AdSetID
	}

	record.Actions = actionMap(row.Actions)
	record.ActionValues = actionMap(row.ActionValues)
	record.CostPerAction = actionMap(row.CostPerActionType)
	record.Conversions = actionMap(row.Conversions)
	record.ROAS = actionMap(concatActions(
		row.PurchaseROAS, row.WebsitePurchaseROAS, row.MobileAppPurchaseROAS,
	))
	record.Video = actionMap(concatActions(
		row.VideoPlayActions, row.VideoThruPlayWatchedActions, row.VideoAvgTimeWatchedActions,
	))

	encodedRaw, err := json.Marshal(row.Raw)
	if err != nil {
		return domain.AdInsightDaily{}, false, err
	}
	record.RawJSON = domain.JSON(encodedRaw)

	// The flat metric map is the shape internal/rules evaluates against, so
	// automation rules read these rows without a translation layer.
	flattened, err := rules.FlattenInsightsJSON(encodedRaw)
	if err != nil {
		return domain.AdInsightDaily{}, false, err
	}
	record.Metrics = domain.MustJSON(flattened)

	return record, true, nil
}

// actionMap keys Meta's action arrays by action_type, preserving every
// attribution-window value alongside the headline one. Storing the windows
// means a later question about 1d_view versus 7d_click does not require
// re-fetching history.
func actionMap(metrics []meta.ActionMetric) domain.JSON {
	if len(metrics) == 0 {
		return domain.EmptyJSONObject
	}
	out := make(map[string]map[string]float64, len(metrics))
	for _, metric := range metrics {
		if metric.ActionType == "" {
			continue
		}
		windows := make(map[string]float64, len(metric.Raw))
		for key, raw := range metric.Raw {
			if key == "action_type" {
				continue
			}
			var text string
			if err := json.Unmarshal(raw, &text); err == nil {
				if parsed, err := strconv.ParseFloat(text, 64); err == nil {
					windows[key] = parsed
				}
				continue
			}
			var number float64
			if err := json.Unmarshal(raw, &number); err == nil {
				windows[key] = number
			}
		}
		if len(windows) == 0 {
			continue
		}
		// Merge rather than overwrite: purchase_roas and
		// website_purchase_roas can both report action_type "purchase".
		if existing, ok := out[metric.ActionType]; ok {
			for key, value := range windows {
				if _, present := existing[key]; !present {
					existing[key] = value
				}
			}
			continue
		}
		out[metric.ActionType] = windows
	}
	if len(out) == 0 {
		return domain.EmptyJSONObject
	}
	return domain.MustJSON(out)
}

func concatActions(groups ...[]meta.ActionMetric) []meta.ActionMetric {
	var combined []meta.ActionMetric
	for _, group := range groups {
		combined = append(combined, group...)
	}
	return combined
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func rawString(raw map[string]json.RawMessage, key string) string {
	encoded, ok := raw[key]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(encoded, &text); err != nil {
		return ""
	}
	return text
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
