package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func launchableAccount() *AdAccount {
	return &AdAccount{
		AccountStatus: 1,
		DisableReason: 0,
		FundingSource: "1234567890",
		UserTasks:     MustJSON([]string{"DRAFT", "ANALYZE", "ADVERTISE"}),
	}
}

func TestLaunchReadinessAcceptsAServiceableAccount(t *testing.T) {
	readiness := LaunchReadinessFor(launchableAccount())
	require.True(t, readiness.Ready)
	require.Empty(t, readiness.Blockers)
}

func TestBalanceIsNotUsedAsAFundingSignal(t *testing.T) {
	// The busiest accounts on this profile report balance 0 while having
	// spent thousands: Meta's balance is the amount due, not funds available.
	// Treating it as "no money" would hide exactly the accounts in use.
	account := launchableAccount()
	account.Balance = 0
	account.AmountSpent = 331610
	require.True(t, LaunchReadinessFor(account).Ready)
}

func TestLaunchReadinessNamesEveryBlocker(t *testing.T) {
	for name, mutate := range map[string]struct {
		apply   func(*AdAccount)
		blocker LaunchBlocker
	}{
		"disabled status":  {func(a *AdAccount) { a.AccountStatus = 2 }, BlockerNotActive},
		"unsettled status": {func(a *AdAccount) { a.AccountStatus = 3 }, BlockerNotActive},
		"disable reason":   {func(a *AdAccount) { a.DisableReason = 1 }, BlockerDisabled},
		"no card":          {func(a *AdAccount) { a.FundingSource = "" }, BlockerNoFunding},
		"draft only": {
			func(a *AdAccount) { a.UserTasks = MustJSON([]string{"DRAFT", "ANALYZE"}) },
			BlockerNoPermission,
		},
		"cap reached": {
			func(a *AdAccount) { a.SpendCap = 10000; a.AmountSpent = 10000 },
			BlockerSpendCapMet,
		},
	} {
		account := launchableAccount()
		mutate.apply(account)
		readiness := LaunchReadinessFor(account)
		require.False(t, readiness.Ready, name)
		require.Contains(t, readiness.Blockers, mutate.blocker, name)
	}
}

func TestLaunchReadinessReportsEveryBlockerAtOnce(t *testing.T) {
	// Fixing one problem should not reveal the next one only on retry.
	account := &AdAccount{AccountStatus: 2, DisableReason: 1}
	readiness := LaunchReadinessFor(account)
	require.False(t, readiness.Ready)
	require.Len(t, readiness.Blockers, 4)
}

func TestCanAdvertiseRequiresTheRightTask(t *testing.T) {
	// DRAFT permits building but not publishing, which is exactly the case
	// that would otherwise fail at the last step of a batch.
	require.False(t, (&AdAccount{UserTasks: MustJSON([]string{"DRAFT", "ANALYZE"})}).CanAdvertise())
	require.True(t, (&AdAccount{UserTasks: MustJSON([]string{"ADVERTISE"})}).CanAdvertise())
	require.True(t, (&AdAccount{UserTasks: MustJSON([]string{"MANAGE"})}).CanAdvertise())
	require.False(t, (&AdAccount{}).CanAdvertise())
	require.False(t, (&AdAccount{UserTasks: JSON("not json")}).CanAdvertise())
}

func TestRemainingSpendCap(t *testing.T) {
	require.Equal(t, int64(-1), (&AdAccount{}).RemainingSpendCap())
	require.Equal(t, int64(4000), (&AdAccount{SpendCap: 10000, AmountSpent: 6000}).RemainingSpendCap())
	// Overspend clamps rather than reporting a negative allowance.
	require.Equal(t, int64(0), (&AdAccount{SpendCap: 10000, AmountSpent: 12000}).RemainingSpendCap())
}

func TestNilAccountIsNotLaunchable(t *testing.T) {
	readiness := LaunchReadinessFor(nil)
	require.False(t, readiness.Ready)
	require.Equal(t, []LaunchBlocker{BlockerNotDiscovered}, readiness.Blockers)
}
