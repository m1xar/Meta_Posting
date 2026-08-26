package fieldaudit

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func TestSpecFieldNamesReadsJSONTagsNotGoNames(t *testing.T) {
	// The JSON tag is what reaches Meta; the Go field name is irrelevant.
	names := SpecFieldNames(meta.CampaignSpec{})
	require.Contains(t, names, "objective")
	require.Contains(t, names, "special_ad_categories")
	require.Contains(t, names, "daily_budget")
	require.NotContains(t, names, "Objective")

	// The escape hatch is not a Meta field and must not be counted as one.
	require.NotContains(t, names, "raw")
}

func TestSpecFieldNamesCoversEveryTopLevelSpec(t *testing.T) {
	for _, spec := range []struct {
		name     string
		value    any
		expected string
	}{
		{"campaign", meta.CampaignSpec{}, "objective"},
		{"adset", meta.AdSetSpec{}, "billing_event"},
		{"ad", meta.AdSpec{}, "name"},
		{"creative", meta.CreativeSpec{}, "object_story_spec"},
		{"targeting", meta.Targeting{}, "geo_locations"},
	} {
		names := SpecFieldNames(spec.value)
		require.NotEmpty(t, names, spec.name)
		require.Contains(t, names, spec.expected, spec.name)
	}
}

func TestCompareClassifiesTypedRawAndUnknown(t *testing.T) {
	report := Compare("campaign",
		[]string{"objective", "status", "smart_promotion_type"},
		[]string{"objective", "status", "field_we_invented"},
	)

	require.Equal(t, 3, report.Total)
	require.Equal(t, []string{"objective", "status"}, report.Typed)
	// Reachable through raw, but unvalidated and absent from /v1/capabilities.
	require.Equal(t, []string{"smart_promotion_type"}, report.RawOnly)
	// The more urgent direction: modelled here, not reported by Meta.
	require.Equal(t, []string{"field_we_invented"}, report.Unknown)
	require.NotEmpty(t, report.Warnings)

	require.Equal(t, CoverageTyped, report.ByField["objective"])
	require.Equal(t, CoverageRaw, report.ByField["smart_promotion_type"])
}

func TestCompareIsQuietWhenFullyCovered(t *testing.T) {
	report := Compare("ad", []string{"name", "status"}, []string{"name", "status"})
	require.Empty(t, report.RawOnly)
	require.Empty(t, report.Unknown)
	require.Empty(t, report.Warnings)
}

func TestInvalidFieldNameExtractsTheRejectedField(t *testing.T) {
	// The whole probe loop depends on parsing this correctly.
	for _, testCase := range []struct {
		message  string
		expected string
	}{
		{"(#100) video_p95_watched_actions is not valid for fields param", "video_p95_watched_actions"},
		{"(#100) foo_bar is not a valid field", "foo_bar"},
		{"(#100) Tried accessing nonexisting field (baz) on node", ""},
		{"some unrelated error", ""},
	} {
		err := &meta.GraphError{Code: 100, Message: testCase.message}
		require.Equal(t, testCase.expected, invalidFieldName(err), testCase.message)
	}
}

func TestInvalidFieldNameUnwrapsWrappedErrors(t *testing.T) {
	inner := &meta.GraphError{
		Code:    100,
		Message: "(#100) social_spend is not valid for fields param",
	}
	require.Equal(t, "social_spend", invalidFieldName(wrapped{inner}))
	require.Empty(t, invalidFieldName(nil))
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }
