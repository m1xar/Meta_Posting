package application

import (
	"testing"
	"time"
)

func TestRuleBaselineTolerance(t *testing.T) {
	t.Parallel()

	if got := ruleBaselineTolerance(0); got != defaultRuleBaselineTolerance {
		t.Fatalf("ruleBaselineTolerance(0) = %s, want %s", got, defaultRuleBaselineTolerance)
	}
	if got := ruleBaselineTolerance(7 * time.Minute); got != 7*time.Minute {
		t.Fatalf("ruleBaselineTolerance(7m) = %s, want 7m", got)
	}
}

func TestRuleInsightFreshnessLimit(t *testing.T) {
	t.Parallel()

	if got := ruleInsightFreshnessLimit(0); got != 2*defaultRuleBaselineTolerance {
		t.Fatalf("ruleInsightFreshnessLimit(0) = %s, want %s", got, 2*defaultRuleBaselineTolerance)
	}
	if got := ruleInsightFreshnessLimit(7 * time.Minute); got != 14*time.Minute {
		t.Fatalf("ruleInsightFreshnessLimit(7m) = %s, want 14m", got)
	}
	if got := ruleInsightFreshnessLimit(maxTimeDuration); got != maxTimeDuration {
		t.Fatalf("ruleInsightFreshnessLimit(max) = %s, want %s", got, maxTimeDuration)
	}
}

func TestInsightSnapshotFresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	freshnessLimit := 30 * time.Minute
	tests := []struct {
		name      string
		windowEnd time.Time
		want      bool
	}{
		{name: "current", windowEnd: now, want: true},
		{name: "inside", windowEnd: now.Add(-29 * time.Minute), want: true},
		{name: "boundary", windowEnd: now.Add(-freshnessLimit), want: true},
		{name: "stale", windowEnd: now.Add(-freshnessLimit - time.Second), want: false},
		{name: "concurrent newer snapshot", windowEnd: now.Add(time.Second), want: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := insightSnapshotFresh(now, test.windowEnd, freshnessLimit); got != test.want {
				t.Fatalf("insightSnapshotFresh() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBaselineWithinTolerance(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	tolerance := 15 * time.Minute
	tests := []struct {
		name     string
		baseline time.Time
		want     bool
	}{
		{name: "exact", baseline: target, want: true},
		{name: "inside", baseline: target.Add(-14 * time.Minute), want: true},
		{name: "boundary", baseline: target.Add(-tolerance), want: true},
		{name: "too old", baseline: target.Add(-tolerance - time.Second), want: false},
		{name: "after target", baseline: target.Add(time.Second), want: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := baselineWithinTolerance(target, test.baseline, tolerance); got != test.want {
				t.Fatalf("baselineWithinTolerance() = %v, want %v", got, test.want)
			}
		})
	}
}
