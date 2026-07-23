package application

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
)

func TestDatabaseInventoryReconciliationIncludesOnlyCompleteScopes(t *testing.T) {
	t.Parallel()

	connectionID := uuid.New()
	accountID := uuid.New()
	now := time.Now().UTC()
	result := meta.DiscoveryResult{
		Pages: []meta.Page{{
			ID: "page-current",
			InstagramBusinessAccount: &meta.InstagramAccount{
				ID: "ig-page-current",
			},
		}},
		Assets: map[string]meta.AccountAssets{
			"1": {
				AdAccount: meta.AdAccount{
					ID:        "act_1",
					AccountID: "1",
					Business:  &meta.ObjectRef{ID: "business-1"},
				},
				InstagramAccounts: []meta.InstagramAccount{{ID: "ig-account-current"}},
				Pixels:            []meta.Pixel{{ID: "pixel-partial"}},
				Datasets:          []meta.Dataset{{ID: "dataset-current"}},
				CustomConversions: []meta.CustomConversion{{ID: "conversion-current"}},
				CustomAudiences: []meta.CustomAudience{
					{ID: "audience-current"},
					{ID: "lookalike-current", Subtype: "LOOKALIKE"},
				},
				Applications: []meta.AdvertisableApplication{{ID: "app-partial"}},
			},
		},
		BusinessDatasets: map[string][]meta.Dataset{
			"business-1": {{ID: "dataset-current"}},
		},
		Failures: []meta.DiscoveryFailure{
			{Scope: "pages", Message: "partial"},
			{Scope: "pixels", AccountID: "act_1", Message: "partial"},
			{Scope: "advertisable_applications", AccountID: "1", Message: "partial"},
		},
	}

	input := databaseInventoryReconciliation(
		connectionID,
		now,
		result,
		map[string]uuid.UUID{"1": accountID, "act_1": accountID},
		[]string{"act_1"},
	)

	require.False(t, input.PagesComplete)
	require.Empty(t, input.SeenPageMetaIDs)
	require.Empty(t, input.SeenPageInstagramIDs)
	require.Equal(t, []string{"act_1"}, input.SeenAdAccountMetaIDs)

	scopes := make(map[domain.AssetType][]string, len(input.AccountAssetScopes))
	for _, scope := range input.AccountAssetScopes {
		require.Equal(t, accountID, scope.AdAccountID)
		scopes[scope.AssetType] = scope.SeenMetaIDs
	}
	require.NotContains(t, scopes, domain.AssetPixel)
	require.NotContains(t, scopes, domain.AssetMetaApp)
	require.Equal(t, []string{"ig-account-current"}, scopes[domain.AssetInstagramAccount])
	require.Equal(t, []string{"dataset-current"}, scopes[domain.AssetDataset])
	require.Equal(t, []string{"conversion-current"}, scopes[domain.AssetCustomConversion])
	require.Equal(t, []string{"audience-current"}, scopes[domain.AssetCustomAudience])
	require.Equal(t, []string{"lookalike-current"}, scopes[domain.AssetLookalikeAudience])
}

func TestDatabaseInventoryReconciliationRequiresCompletedDatasetEdge(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	result := meta.DiscoveryResult{
		Assets: map[string]meta.AccountAssets{
			"1": {
				AdAccount: meta.AdAccount{
					AccountID: "1",
					Business:  &meta.ObjectRef{ID: "business-1"},
				},
			},
		},
		BusinessDatasets: map[string][]meta.Dataset{},
	}
	input := databaseInventoryReconciliation(
		uuid.New(),
		time.Now().UTC(),
		result,
		map[string]uuid.UUID{"1": accountID},
		[]string{"act_1"},
	)
	for _, scope := range input.AccountAssetScopes {
		require.NotEqual(t, domain.AssetDataset, scope.AssetType)
	}
}
