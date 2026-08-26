package rules

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRollingWindowDeltaDerivesOnlyAdditiveWindowMetrics(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	olderMetrics := map[string]float64{
		"spend":                          100,
		"impressions":                    1000,
		"reach":                          800,
		"clicks":                         50,
		"actions.purchase":               2,
		"action_values.purchase":         100,
		"outbound_clicks.outbound_click": 10,
		"ctr":                            5,
	}
	newerMetrics := map[string]float64{
		"spend":                              120,
		"impressions":                        1600,
		"reach":                              900,
		"clicks":                             80,
		"actions.purchase":                   5,
		"action_values.purchase":             250,
		"ctr":                                999,
		"frequency":                          1.77,
		"cpp":                                133.33,
		"outbound_clicks.outbound_click":     22,
		"outbound_clicks_ctr.outbound_click": 42,
		"video_avg_time_watched_actions.video_view": 8,
	}

	delta, err := RollingWindowDelta(
		Snapshot{TakenAt: start, Metrics: olderMetrics},
		Snapshot{TakenAt: start.Add(24 * time.Hour), Metrics: newerMetrics},
	)
	if err != nil {
		t.Fatalf("RollingWindowDelta() error = %v", err)
	}
	if delta.DurationSeconds() != int64((24 * time.Hour).Seconds()) {
		t.Errorf("DurationSeconds() = %d, want %d", delta.DurationSeconds(), int64((24 * time.Hour).Seconds()))
	}

	expected := map[string]float64{
		"spend":                              20,
		"impressions":                        600,
		"clicks":                             30,
		"actions.purchase":                   3,
		"action_values.purchase":             150,
		"outbound_clicks.outbound_click":     12,
		"ctr":                                5,
		"cpa.purchase":                       20.0 / 3.0,
		"cost_per_action_type.purchase":      20.0 / 3.0,
		"roas.purchase":                      7.5,
		"outbound_clicks_ctr.outbound_click": 2,
	}
	for metric, want := range expected {
		if got, ok := delta.Metrics[metric]; !ok || !almostEqual(got, want) {
			t.Errorf("%s = %v (present %v), want %v", metric, got, ok, want)
		}
	}
	for _, metric := range []string{
		"reach",
		"frequency",
		"cpp",
		"video_avg_time_watched_actions.video_view",
	} {
		if _, exists := delta.Metrics[metric]; exists {
			t.Errorf("non-additive metric %q must not be present in a rolling delta", metric)
		}
	}

	if olderMetrics["spend"] != 100 || newerMetrics["ctr"] != 999 || newerMetrics["reach"] != 900 {
		t.Error("RollingWindowDelta() mutated an input map")
	}
}

func TestRollingWindowDeltaRejectsCounterCorrections(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	_, err := RollingWindowDelta(
		Snapshot{TakenAt: now.Add(-time.Hour), Metrics: map[string]float64{
			"spend":            100,
			"actions.purchase": 10,
			"reach":            800,
		}},
		Snapshot{TakenAt: now, Metrics: map[string]float64{
			"spend":            99,
			"actions.purchase": 9,
			"reach":            700,
		}},
	)
	if !errors.Is(err, ErrCounterCorrection) {
		t.Fatalf("RollingWindowDelta() error = %v, want ErrCounterCorrection", err)
	}
	var correction *CounterCorrectionError
	if !errors.As(err, &correction) {
		t.Fatalf("RollingWindowDelta() error type = %T, want *CounterCorrectionError", err)
	}
	if want := []string{"actions.purchase", "spend"}; !reflect.DeepEqual(correction.Metrics, want) {
		t.Fatalf("corrected metrics = %v, want %v", correction.Metrics, want)
	}
}

func TestDeltaMetricsRejectsPreviousOnlyAdditiveCounter(t *testing.T) {
	t.Parallel()

	_, err := DeltaMetrics(
		map[string]float64{"actions.purchase": 1},
		map[string]float64{},
	)
	if !errors.Is(err, ErrCounterCorrection) {
		t.Fatalf("DeltaMetrics() error = %v, want ErrCounterCorrection", err)
	}
	var correction *CounterCorrectionError
	if !errors.As(err, &correction) {
		t.Fatalf("DeltaMetrics() error type = %T, want *CounterCorrectionError", err)
	}
	if want := []string{"actions.purchase"}; !reflect.DeepEqual(correction.Metrics, want) {
		t.Fatalf("corrected metrics = %v, want %v", correction.Metrics, want)
	}
}

func TestDeltaMetricsAppliesPreviousOnlyPolicyAndEpsilon(t *testing.T) {
	t.Parallel()

	_, err := DeltaMetrics(
		map[string]float64{
			"actions.purchase":         1,
			"conversions.registration": 2,
			"spend":                    3,
			"reach":                    800,
			"cpa.purchase":             50,
		},
		map[string]float64{},
	)
	var correction *CounterCorrectionError
	if !errors.As(err, &correction) {
		t.Fatalf("DeltaMetrics() error = %v, want *CounterCorrectionError", err)
	}
	if want := []string{"actions.purchase", "conversions.registration", "spend"}; !reflect.DeepEqual(correction.Metrics, want) {
		t.Fatalf("corrected metrics = %v, want additive previous-only metrics %v", correction.Metrics, want)
	}

	delta, err := DeltaMetrics(
		map[string]float64{
			"actions.at_epsilon":    counterCorrectionEpsilon,
			"actions.below_epsilon": counterCorrectionEpsilon / 2,
			"actions.negative":      -2,
		},
		map[string]float64{},
	)
	if err != nil {
		t.Fatalf("DeltaMetrics() epsilon error = %v", err)
	}
	if got := delta["actions.at_epsilon"]; got != 0 {
		t.Errorf("actions.at_epsilon delta = %v, want 0", got)
	}
	if got := delta["actions.below_epsilon"]; got != 0 {
		t.Errorf("actions.below_epsilon delta = %v, want 0", got)
	}
	if got := delta["actions.negative"]; got != 2 {
		t.Errorf("actions.negative delta = %v, want 2 (same behavior as explicit current zero)", got)
	}

	_, err = DeltaMetrics(
		map[string]float64{"actions.above_epsilon": counterCorrectionEpsilon + 1e-12},
		map[string]float64{},
	)
	if !errors.Is(err, ErrCounterCorrection) {
		t.Fatalf("DeltaMetrics() above-epsilon error = %v, want ErrCounterCorrection", err)
	}
}

func TestRollingWindowDeltaDoesNotHideLargeValueCorrection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	_, err := RollingWindowDelta(
		Snapshot{TakenAt: now.Add(-time.Hour), Metrics: map[string]float64{"spend": 1_000_000_000}},
		Snapshot{TakenAt: now, Metrics: map[string]float64{"spend": 999_999_999.5}},
	)
	if !errors.Is(err, ErrCounterCorrection) {
		t.Fatalf("RollingWindowDelta() error = %v, want correction for a 0.5 decrease", err)
	}
}

func TestRollingWindowDeltaIgnoresNonAdditiveReachCorrection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	delta, err := RollingWindowDelta(
		Snapshot{TakenAt: now.Add(-time.Hour), Metrics: map[string]float64{"spend": 100, "reach": 800}},
		Snapshot{TakenAt: now, Metrics: map[string]float64{"spend": 120, "reach": 700, "frequency": 2, "cpp": 10}},
	)
	if err != nil {
		t.Fatalf("RollingWindowDelta() error = %v", err)
	}
	if delta.Metrics["spend"] != 20 {
		t.Fatalf("spend delta = %v, want 20", delta.Metrics["spend"])
	}
	for _, metric := range []string{"reach", "frequency", "cpp"} {
		if _, exists := delta.Metrics[metric]; exists {
			t.Errorf("non-additive metric %q must not be present in a rolling delta", metric)
		}
	}
}

func TestRollingWindowDeltaRejectsReversedTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Now()
	_, err := RollingWindowDelta(
		Snapshot{TakenAt: now, Metrics: map[string]float64{}},
		Snapshot{TakenAt: now.Add(-time.Minute), Metrics: map[string]float64{}},
	)
	if err == nil {
		t.Fatal("RollingWindowDelta() error = nil, want reversed timestamp error")
	}
}

func almostEqual(left, right float64) bool {
	delta := left - right
	if delta < 0 {
		delta = -delta
	}
	return delta < 1e-9
}
