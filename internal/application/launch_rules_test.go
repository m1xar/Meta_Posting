package application

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/rules"
)

func TestSpendCapGuardStopsOnSpendAlone(t *testing.T) {
	guard := LaunchGuard{Kind: GuardSpendCap, Spend: 100}
	require.NoError(t, guard.Validate())

	conditions := guard.Conditions()
	require.Equal(t, rules.LogicAll, conditions.Logic)
	require.Len(t, conditions.Conditions, 1)
	require.Equal(t, "spend", conditions.Conditions[0].Metric)
	require.Equal(t, rules.OperatorGTE, conditions.Conditions[0].Operator)
	require.InDelta(t, 100, conditions.Conditions[0].Threshold, 1e-9)
}

func TestSpendCheckGuardRequiresBothHalves(t *testing.T) {
	// "Give it $100 to prove itself, then stop it unless it produced 5,000
	// impressions." Below the spend threshold the rule must not fire at all,
	// which is why spend is an AND rather than a separate rule.
	guard := LaunchGuard{Kind: GuardSpendCheck, Spend: 100, Metric: "impressions", Minimum: 5000}
	require.NoError(t, guard.Validate())

	conditions := guard.Conditions()
	require.Equal(t, rules.LogicAll, conditions.Logic)
	require.Len(t, conditions.Conditions, 2)

	require.Equal(t, "spend", conditions.Conditions[0].Metric)
	require.Equal(t, rules.OperatorGTE, conditions.Conditions[0].Operator)

	result := conditions.Conditions[1]
	require.Equal(t, "impressions", result.Metric)
	require.Equal(t, rules.OperatorLT, result.Operator)
	require.InDelta(t, 5000, result.Threshold, 1e-9)
	// An object that delivered nothing reports no metric at all rather than a
	// zero, and that is exactly the case worth stopping.
	require.True(t, result.MissingAsZero)
}

func TestSpendCheckAcceptsActionMetrics(t *testing.T) {
	guard := LaunchGuard{Kind: GuardSpendCheck, Spend: 250, Metric: "actions.purchase", Minimum: 3}
	require.NoError(t, guard.Validate())
	require.Equal(t, "actions.purchase", guard.Conditions().Conditions[1].Metric)
}

func TestGuardValidationRejectsIncompleteInput(t *testing.T) {
	require.Error(t, LaunchGuard{Kind: GuardSpendCap}.Validate())
	require.Error(t, LaunchGuard{Kind: GuardSpendCap, Spend: -1}.Validate())
	require.Error(t, LaunchGuard{Kind: GuardSpendCheck, Spend: 100}.Validate())
	require.Error(t, LaunchGuard{Kind: GuardSpendCheck, Spend: 100, Metric: "clicks"}.Validate())
	require.Error(t, LaunchGuard{Kind: "invent", Spend: 10}.Validate())
}

func TestGuardDescribesItselfInPlainWords(t *testing.T) {
	require.Equal(t, "Pause once spend reaches 100.00 USD",
		LaunchGuard{Kind: GuardSpendCap, Spend: 100}.Describe("usd"))
	require.Equal(t, "After 100.00 EUR, pause unless clicks reached 50",
		LaunchGuard{Kind: GuardSpendCheck, Spend: 100, Metric: "clicks", Minimum: 50}.Describe("EUR"))
	require.Contains(t, LaunchGuard{Kind: GuardSpendCap, Spend: 1}.Describe(""), "USD")
}

func TestLaunchRuleDefaults(t *testing.T) {
	request := LaunchRuleRequest{Guard: LaunchGuard{Kind: GuardSpendCap, Spend: 100}}
	created, err := request.ToCreateRule("USD")
	require.NoError(t, err)

	require.Equal(t, domain.InsightCampaign, created.ScopeLevel,
		"stopping the campaign stops everything under it")
	require.Equal(t, domain.RuleActionPause, created.Action)
	require.Equal(t, int64(60), created.EvaluationIntervalSeconds)
	// A launch guard judges the campaign's whole life, not a rolling window:
	// a short window would let a slow burner sit under the cap forever.
	require.Equal(t, int64(30*24*3600), created.LookbackSeconds)
	require.Equal(t, "Pause once spend reaches 100.00 USD", created.Name)
	require.Equal(t, "launcher", created.Metadata["source"])
}

func TestLaunchRuleHonoursExplicitSettings(t *testing.T) {
	request := LaunchRuleRequest{
		Name:                      "Custom",
		Guard:                     LaunchGuard{Kind: GuardSpendCheck, Spend: 50, Metric: "clicks", Minimum: 10},
		ScopeLevel:                domain.InsightAdSet,
		EvaluationIntervalSeconds: 120,
		LookbackSeconds:           3600,
		GracePeriodSeconds:        600,
	}
	created, err := request.ToCreateRule("USD")
	require.NoError(t, err)
	require.Equal(t, "Custom", created.Name)
	require.Equal(t, domain.InsightAdSet, created.ScopeLevel)
	require.Equal(t, int64(120), created.EvaluationIntervalSeconds)
	require.Equal(t, int64(3600), created.LookbackSeconds)
	require.Equal(t, int64(600), created.GracePeriodSeconds)
}

func TestLaunchRuleRejectsBadGuard(t *testing.T) {
	_, err := LaunchRuleRequest{Guard: LaunchGuard{Kind: GuardSpendCheck, Spend: 10}}.ToCreateRule("USD")
	require.Error(t, err)
}
