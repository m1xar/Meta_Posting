package meta

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// recordingGovernor swaps the real sleep for a recorder, so tests assert the
// decision rather than waiting for it.
func newTestGovernor(t *testing.T) (*MemoryGovernor, *[]time.Duration) {
	t.Helper()
	var slept []time.Duration
	governor := NewMemoryGovernor(DefaultThrottlePolicy())
	governor.sleep = func(_ context.Context, duration time.Duration) error {
		slept = append(slept, duration)
		return nil
	}
	return governor, &slept
}

func TestThrottlePolicyLeavesNormalTrafficAlone(t *testing.T) {
	policy := DefaultThrottlePolicy()
	require.Zero(t, policy.Delay(0))
	require.Zero(t, policy.Delay(0.5))
	require.Zero(t, policy.Delay(0.74))
}

func TestThrottlePolicyRampsThenSaturates(t *testing.T) {
	policy := DefaultThrottlePolicy()
	low := policy.Delay(0.80)
	high := policy.Delay(0.88)
	require.Greater(t, low, time.Duration(0))
	require.Greater(t, high, low)
	require.Equal(t, policy.MaxDelay, policy.Delay(0.90))
	require.Equal(t, policy.MaxDelay, policy.Delay(1.0))
}

func TestGovernorDoesNotDelayBeforeAnyReading(t *testing.T) {
	governor, slept := newTestGovernor(t)
	require.NoError(t, governor.Wait(context.Background(), "app", time.Now()))
	require.Empty(t, *slept)
}

func TestGovernorDelaysOnlyOncePressureIsHigh(t *testing.T) {
	governor, slept := newTestGovernor(t)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	governor.Observe("app", usageHeaders(t, `{"call_count":30}`, "", ""), now)
	require.NoError(t, governor.Wait(context.Background(), "app", now))
	require.Empty(t, *slept, "normal traffic must pay nothing")

	governor.Observe("app", usageHeaders(t, `{"call_count":85}`, "", ""), now)
	require.NoError(t, governor.Wait(context.Background(), "app", now))
	require.Len(t, *slept, 1)
	require.Greater(t, (*slept)[0], time.Duration(0))
}

func TestGovernorTreatsAnActiveBlockAsAHardStop(t *testing.T) {
	governor, slept := newTestGovernor(t)
	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)

	governor.Observe("account:act_1", usageHeaders(t, "", "",
		`{"178":[{"type":"ads_insights","call_count":100,"estimated_time_to_regain_access":15}]}`), now)

	until, blocked := governor.BlockedUntil("account:act_1")
	require.True(t, blocked)
	require.Equal(t, now.Add(15*time.Minute), until)

	// The in-request wait is capped: a 15-minute block is the scheduler's
	// problem to route around, not something to hold a worker slot for.
	require.NoError(t, governor.Wait(context.Background(), "account:act_1", now))
	require.Len(t, *slept, 1)
	require.Equal(t, DefaultThrottlePolicy().MaxDelay, (*slept)[0])

	// Once it expires, pressure alone governs again.
	after := now.Add(20 * time.Minute)
	governor.Observe("account:act_1", usageHeaders(t, `{"call_count":5}`, "", ""), after)
	require.NoError(t, governor.Wait(context.Background(), "account:act_1", after))
	require.Len(t, *slept, 1, "no further sleep once the block has passed")
}

func TestGovernorIsolatesAccountsFromEachOther(t *testing.T) {
	governor, slept := newTestGovernor(t)
	now := time.Now().UTC()

	governor.Observe("account:act_1", usageHeaders(t, `{"call_count":95}`, "", ""), now)
	governor.Observe("account:act_2", usageHeaders(t, `{"call_count":5}`, "", ""), now)

	// One saturated account must not slow every other account.
	require.NoError(t, governor.Wait(context.Background(), "account:act_2", now))
	require.Empty(t, *slept)

	require.NoError(t, governor.Wait(context.Background(), "account:act_1", now))
	require.Len(t, *slept, 1)
}

func TestGovernorWaitRespectsContextCancellation(t *testing.T) {
	governor := NewMemoryGovernor(DefaultThrottlePolicy())
	now := time.Now().UTC()
	governor.Observe("app", usageHeaders(t, `{"call_count":99}`, "", ""), now)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, governor.Wait(ctx, "app", now))
}

func TestGovernorIgnoresResponsesWithoutUsageHeaders(t *testing.T) {
	governor, slept := newTestGovernor(t)
	now := time.Now().UTC()
	governor.Observe("app", ResponseMeta{StatusCode: 200}, now)
	_, ok := governor.Pressure("app")
	require.False(t, ok)
	require.NoError(t, governor.Wait(context.Background(), "app", now))
	require.Empty(t, *slept)
}

func TestGovernorKeyBucketsByQuotaOwner(t *testing.T) {
	// Ad-account calls are isolated per account; everything else shares the
	// app budget.
	require.Equal(t, "account:act_123", GovernorKey("/v25.0/act_123/insights"))
	require.Equal(t, "account:act_123", GovernorKey("act_123/campaigns"))
	require.Equal(t, "app", GovernorKey("/v25.0/me/adaccounts"))
	require.Equal(t, "app", GovernorKey("/debug_token"))
	require.Equal(t, "app", GovernorKey(""))
	// A paging URL arrives absolute.
	require.Equal(t, "account:act_99",
		GovernorKey("https://graph.facebook.com/v25.0/act_99/insights?after=x"))
}
