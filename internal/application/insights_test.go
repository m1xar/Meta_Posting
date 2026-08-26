package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestInsightSnapshotFlattensActionMetrics(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	raw := map[string]json.RawMessage{
		"date_start":  json.RawMessage(`"2026-07-22"`),
		"date_stop":   json.RawMessage(`"2026-07-23"`),
		"spend":       json.RawMessage(`"42.50"`),
		"impressions": json.RawMessage(`"1000"`),
		"clicks":      json.RawMessage(`"20"`),
		"actions": json.RawMessage(`[
			{"action_type":"complete_registration","value":"4"},
			{"action_type":"landing_page_view","value":"3"},
			{"action_type":"offsite_conversion.fb_pixel_purchase","value":"2"}
		]`),
		"action_values": json.RawMessage(`[
			{"action_type":"offsite_conversion.fb_pixel_purchase","value":"120"}
		]`),
	}
	rowBytes, err := json.Marshal(raw)
	require.NoError(t, err)
	var row meta.InsightRow
	require.NoError(t, json.Unmarshal(rowBytes, &row))

	account := &domain.AdAccount{Model: domain.Model{ID: uuid.New()}, TimezoneName: "Asia/Dubai"}
	object := &domain.PublishedObject{
		Model:        domain.Model{ID: uuid.New(), CreatedAt: now.Add(-48 * time.Hour)},
		ObjectType:   domain.PublishedAd,
		MetaObjectID: "123",
	}
	snapshot, err := insightSnapshot(uuid.New(), account, object, []meta.InsightRow{row}, now)
	require.NoError(t, err)
	require.Equal(t, int64(1000), snapshot.Impressions)
	require.Equal(t, int64(20), snapshot.Clicks)
	require.Equal(t, float64(4), snapshot.Registrations)
	require.Equal(t, float64(2), snapshot.Purchases)
	require.Equal(t, float64(120), snapshot.PurchaseValue)
	require.Equal(t, float64(3), snapshot.LandingPageViews)
	require.Equal(t, float64(9), snapshot.Actions)

	var metrics map[string]float64
	require.NoError(t, snapshot.Metrics.Decode(&metrics))
	require.Equal(t, 42.5, metrics["cpm"], "derived CPM remains available")
}

func TestBuildInsightCollectionBatchesGroupsByAccountAndLevelAndChunks(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	firstAccountID := uuid.New()
	secondAccountID := uuid.New()
	accounts := map[uuid.UUID]*domain.AdAccount{
		firstAccountID: {
			Model:           domain.Model{ID: firstAccountID},
			MetaAdAccountID: "act_111",
		},
		secondAccountID: {
			Model:     domain.Model{ID: secondAccountID},
			AccountID: "222",
		},
	}
	objects := []domain.PublishedObject{
		publishedInsightObject(firstAccountID, domain.PublishedCampaign, "campaign-1", now.Add(-24*time.Hour)),
		publishedInsightObject(firstAccountID, domain.PublishedCampaign, "campaign-2", now.Add(-72*time.Hour)),
		publishedInsightObject(firstAccountID, domain.PublishedCampaign, "campaign-3", now.Add(-48*time.Hour)),
		publishedInsightObject(firstAccountID, domain.PublishedAd, "ad-1", now.Add(-12*time.Hour)),
		publishedInsightObject(secondAccountID, domain.PublishedAdSet, "adset-1", now.Add(-36*time.Hour)),
	}

	batches, failures := buildInsightCollectionBatches(objects, accounts, 2)

	require.Empty(t, failures)
	require.Len(t, batches, 4)
	require.Equal(t, meta.InsightLevelCampaign, batches[0].level)
	require.Equal(t, "campaign.id", batches[0].filterField)
	require.Equal(t, []string{"campaign-1", "campaign-2"}, batches[0].metaObjectIDs())
	require.Equal(t, now.Add(-72*time.Hour), batches[0].earliestCreatedAt())
	require.Equal(t, []string{"campaign-3"}, batches[1].metaObjectIDs())
	require.Equal(t, meta.InsightLevelAd, batches[2].level)
	require.Equal(t, "act_111", batches[2].accountNodeID)
	require.Equal(t, meta.InsightLevelAdSet, batches[3].level)
	require.Equal(t, "act_222", batches[3].accountNodeID)
}

func TestGroupInsightRowsAndStatusRefreshCadence(t *testing.T) {
	rows, failures := groupInsightRows(meta.InsightLevelAd, []meta.InsightRow{
		{AdID: "ad-1", DateStart: "2026-07-22"},
		{AdID: "ad-1", DateStart: "2026-07-23"},
		{CampaignID: "campaign-without-ad-id"},
	})
	require.Len(t, failures, 1)
	require.Len(t, rows["ad-1"], 2)

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-59 * time.Minute)
	boundary := now.Add(-publishedStatusRefreshInterval)
	future := now.Add(time.Minute)
	require.True(t, publishedStatusRefreshDue(nil, now))
	require.False(t, publishedStatusRefreshDue(&recent, now))
	require.True(t, publishedStatusRefreshDue(&boundary, now))
	require.False(t, publishedStatusRefreshDue(&future, now))
}

func TestInsightSnapshotStoresZeroSnapshotWhenMetaReturnsNoRows(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	createdAt := now.Add(-6 * time.Hour)
	account := &domain.AdAccount{
		Model:        domain.Model{ID: uuid.New()},
		TimezoneName: "Asia/Dubai",
	}
	object := publishedInsightObject(account.ID, domain.PublishedCampaign, "campaign-1", createdAt)

	snapshot, err := insightSnapshot(uuid.New(), account, &object, nil, now)

	require.NoError(t, err)
	require.Equal(t, object.ID, *snapshot.PublishedObjectID)
	require.Equal(t, "campaign-1", snapshot.MetaObjectID)
	require.Equal(t, domain.InsightCampaign, snapshot.Level)
	require.Equal(t, createdAt, snapshot.WindowStart)
	require.Equal(t, now, snapshot.WindowEnd)
	require.Zero(t, snapshot.Spend)
	require.Zero(t, snapshot.Impressions)
	require.JSONEq(t, `{"rows":[]}`, string(snapshot.RawJSON))
}

func TestInsightSnapshotKeepsWindowPositiveWhenObjectClockIsAhead(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	account := &domain.AdAccount{Model: domain.Model{ID: uuid.New()}}
	object := publishedInsightObject(
		account.ID,
		domain.PublishedCampaign,
		"campaign-future",
		now.Add(time.Minute),
	)

	snapshot, err := insightSnapshot(uuid.New(), account, &object, nil, now)

	require.NoError(t, err)
	require.True(t, snapshot.WindowStart.Before(snapshot.WindowEnd))
	require.Equal(t, time.Microsecond, snapshot.WindowEnd.Sub(snapshot.WindowStart))
}

func publishedInsightObject(
	accountID uuid.UUID,
	objectType domain.PublishedObjectType,
	metaObjectID string,
	createdAt time.Time,
) domain.PublishedObject {
	return domain.PublishedObject{
		Model:        domain.Model{ID: uuid.New(), CreatedAt: createdAt},
		AdAccountID:  accountID,
		ObjectType:   objectType,
		MetaObjectID: metaObjectID,
	}
}
