package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestHierarchyForAccountDeepMergesOverride(t *testing.T) {
	base := validHierarchy()
	patch := json.RawMessage(`{
		"campaign":{"daily_budget":2500},
		"ad_set":{"targeting":{"age_min":25}},
		"creative":{"raw":{"degrees_of_freedom_spec":{"creative_features_spec":{"standard_enhancements":{"enroll_status":"OPT_OUT"}}}}}
	}`)

	merged, err := specForAccount(base, patch)
	require.NoError(t, err)
	require.Equal(t, int64(2500), merged.Campaign.DailyBudget)
	require.Equal(t, 25, merged.AdSet.Targeting.AgeMin)
	require.Contains(t, merged.Creative.Raw, "degrees_of_freedom_spec")
	require.Equal(t, int64(1000), base.Campaign.DailyBudget, "base hierarchy must not be mutated")
}

func TestSetHierarchyJSONPointerAddsMediaValue(t *testing.T) {
	hierarchy := validHierarchy()
	require.Empty(t, hierarchy.Creative.ObjectStorySpec.LinkData.ImageHash)

	err := setSpecJSONPointer(
		&hierarchy,
		"/creative/object_story_spec/link_data/image_hash",
		"meta-image-hash",
	)
	require.NoError(t, err)
	require.Equal(t, "meta-image-hash", hierarchy.Creative.ObjectStorySpec.LinkData.ImageHash)
}

func TestSetHierarchyJSONPointerRejectsMissingParent(t *testing.T) {
	hierarchy := validHierarchy()
	err := setSpecJSONPointer(&hierarchy, "/creative/asset_feed_spec/images/0/hash", "hash")
	require.ErrorContains(t, err, "does not exist")
}

func TestSameJSONIgnoresObjectKeyOrder(t *testing.T) {
	require.True(t, sameJSON(
		[]byte(`{"name":"batch","accounts":[1,2],"nested":{"a":1,"b":2}}`),
		[]byte(`{"nested":{"b":2,"a":1},"accounts":[1,2],"name":"batch"}`),
	))
	require.False(t, sameJSON([]byte(`{"budget":100}`), []byte(`{"budget":200}`)))
}

func TestValidateBatchAdAccountRejectsInactiveAccount(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	err := validateBatchAdAccount(&domain.AdAccount{
		ConnectionID: connectionID,
		IsActive:     false,
	}, connectionID)

	require.Error(t, err)
	require.ErrorContains(t, err, "no longer accessible")
}

func TestValidateBatchAdAccountRejectsReadOnlyMetaRole(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	err := validateBatchAdAccount(&domain.AdAccount{
		ConnectionID: connectionID,
		IsActive:     true,
		RawJSON:      domain.MustJSON(map[string]any{"user_tasks": []string{"ANALYZE"}}),
	}, connectionID)

	require.Error(t, err)
	require.ErrorContains(t, err, "ADVERTISE or MANAGE")
}

func TestAdAccountGraphIDFallsBackToMetaNodeID(t *testing.T) {
	t.Parallel()

	require.Equal(t, "123", adAccountGraphID(&domain.AdAccount{
		AccountID:       "123",
		MetaAdAccountID: "act_123",
	}))
	require.Equal(t, "act_456", adAccountGraphID(&domain.AdAccount{
		MetaAdAccountID: "act_456",
	}))
	require.Empty(t, adAccountGraphID(nil))
}

func TestApplyPublishMarkerIsStableAndUTF8Safe(t *testing.T) {
	hierarchy := validHierarchy()
	hierarchy.Campaign.Name = strings.Repeat("К", 300)
	resultID := uuid.MustParse("d8ca5be2-e078-4bd7-a134-187667ccae88")

	applyPublishMarker(&hierarchy, resultID)
	first := hierarchy.Campaign.Name
	applyPublishMarker(&hierarchy, resultID)

	require.Equal(t, first, hierarchy.Campaign.Name)
	require.LessOrEqual(t, len([]rune(first)), publishNameMaxRunes)
	require.True(t, strings.HasSuffix(first, " [RP:"+resultID.String()+"]"))
	require.Contains(t, hierarchy.AdSet.Name, resultID.String())
	require.Contains(t, hierarchy.Creative.Name, resultID.String())
	require.Contains(t, hierarchy.Ad.Name, resultID.String())
}

func TestPublishFailureJobErrorRequestsDurableRetryOnlyWhenSafe(t *testing.T) {
	t.Parallel()

	transient := &meta.GraphError{Code: 2, IsTransient: true, Message: "temporary"}
	err := publishFailureJobError(transient, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, transient)

	serialized := errors.New("typed error was serialized into the stage")
	err = publishFailureJobError(
		serialized,
		&meta.AccountPublishResult{Stages: []meta.PublishStage{{
			Name:  "create_ad_set",
			State: meta.StageFailed,
			Failure: &meta.PublishFailure{
				Message:   "meta HTTP error 503",
				Retryable: true,
			},
		}}},
	)
	require.Error(t, err)
	require.ErrorIs(t, err, serialized)

	err = publishFailureJobError(context.Canceled, nil)
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, publishFailureJobError(
		&meta.GraphError{Code: 100, Message: "invalid targeting"},
		&meta.AccountPublishResult{Stages: []meta.PublishStage{{
			Name:  "create_ad_set",
			State: meta.StageFailed,
			Failure: &meta.PublishFailure{
				Message: "invalid targeting",
			},
		}}},
	))
}

func TestPublishFailureAuditActionDistinguishesRetryFromTerminalFailure(t *testing.T) {
	t.Parallel()

	require.Equal(t, "batch.account.retry_pending", publishFailureAuditAction(errors.New("retry")))
	require.Equal(t, "batch.account.failed", publishFailureAuditAction(nil))
}

func validHierarchy() meta.HierarchySpec {
	return meta.HierarchySpec{
		Campaign: meta.CampaignSpec{
			Name:        "campaign",
			Objective:   meta.ObjectiveOutcomeSales,
			DailyBudget: 1000,
		},
		AdSet: meta.AdSetSpec{
			Name: "ad set",
			Targeting: meta.Targeting{
				GeoLocations: map[string]any{"countries": []string{"AE"}},
			},
		},
		Creative: meta.CreativeSpec{
			Name: "creative",
			ObjectStorySpec: &meta.ObjectStorySpec{
				PageID: "1",
				LinkData: &meta.LinkData{
					Link: "https://example.com",
				},
			},
		},
		Ad: meta.AdSpec{Name: "ad"},
	}
}
