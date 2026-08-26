package application

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

func launcherRule(kind GuardKind, spend float64, metric string, threshold float64) *domain.AutomationRule {
	conditions := map[string]any{
		"logic": "all",
		"conditions": []map[string]any{
			{"metric": "spend", "operator": "gte", "threshold": spend},
		},
	}
	if kind == GuardSpendCheck {
		conditions["conditions"] = append(conditions["conditions"].([]map[string]any),
			map[string]any{"metric": metric, "operator": "lt", "threshold": threshold})
	}
	return &domain.AutomationRule{
		Model:      domain.Model{ID: uuid.New()},
		Name:       "guard",
		ScopeLevel: domain.InsightCampaign,
		Conditions: domain.MustJSON(conditions),
		Metadata: domain.MustJSON(map[string]any{
			"source": "launcher", "guard_kind": string(kind), "guard_spend": spend,
		}),
	}
}

func TestSpendCapMirrorsCleanly(t *testing.T) {
	service := &Service{}
	spec, err := service.adRuleSpecFor(launcherRule(GuardSpendCap, 100, "", 0))
	require.NoError(t, err)

	require.Equal(t, "PAUSE", spec.ExecutionSpec.ExecutionType)
	require.Equal(t, meta.AdRuleSchedule, spec.EvaluationSpec.EvaluationType)

	fields := map[string]any{}
	for _, filter := range spec.EvaluationSpec.Filters {
		fields[filter.Field] = filter.Value
	}
	require.Equal(t, "CAMPAIGN", fields["entity_type"])
	// Meta's rule engine takes spend in minor units.
	require.Equal(t, int64(10000), fields["spent"])
}

func TestSpendCheckMirrorsWhenMetaKnowsTheMetric(t *testing.T) {
	service := &Service{}
	spec, err := service.adRuleSpecFor(
		launcherRule(GuardSpendCheck, 100, "impressions", 5000))
	require.NoError(t, err)
	require.Len(t, spec.EvaluationSpec.Filters, 3)

	fields := map[string]any{}
	for _, filter := range spec.EvaluationSpec.Filters {
		fields[filter.Field] = filter.Value
	}
	require.Equal(t, int64(10000), fields["spent"])
	require.InDelta(t, 5000, fields["impressions"], 1e-9)
}

func TestUnmirrorableGuardIsRefusedRatherThanApproximated(t *testing.T) {
	service := &Service{}

	// Meta's rule vocabulary has no equivalent for a pixel action, and a
	// backstop that pauses on the wrong signal is worse than no backstop.
	_, err := service.adRuleSpecFor(
		launcherRule(GuardSpendCheck, 100, "actions.complete_registration", 1))
	require.ErrorContains(t, err, "does not expose")

	// A rule this service did not create has no guard to mirror.
	handWritten := &domain.AutomationRule{
		Model:      domain.Model{ID: uuid.New()},
		ScopeLevel: domain.InsightCampaign,
		Conditions: domain.MustJSON(map[string]any{"logic": "all"}),
		Metadata:   domain.EmptyJSONObject,
	}
	_, err = service.adRuleSpecFor(handWritten)
	require.ErrorContains(t, err, "no mirrorable guard")
}

func TestMirrorRejectsLevelsMetaCannotTarget(t *testing.T) {
	service := &Service{}
	rule := launcherRule(GuardSpendCap, 100, "", 0)
	rule.ScopeLevel = domain.InsightAccount
	_, err := service.adRuleSpecFor(rule)
	require.ErrorContains(t, err, "entity type")
}

func TestAdRuleEntityTypeMapping(t *testing.T) {
	for level, expected := range map[string]string{
		"campaign": "CAMPAIGN", "adset": "ADSET", "ad": "AD",
	} {
		entity, err := meta.AdRuleEntityType(level)
		require.NoError(t, err, level)
		require.Equal(t, expected, entity)
	}
	_, err := meta.AdRuleEntityType("account")
	require.Error(t, err)
}

func TestRuleNamesAreTruncatedForMeta(t *testing.T) {
	long := ""
	for i := 0; i < 120; i++ {
		long += "x"
	}
	spec := meta.SpendCapAdRule(long, "CAMPAIGN", 100)
	require.LessOrEqual(t, len(spec.Name), 60)
}
