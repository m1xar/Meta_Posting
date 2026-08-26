package application

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeCheckpointsSortsAndValidates(t *testing.T) {
	normalized, err := normalizeCheckpoints([]GuardCheckpoint{
		{Spend: 20, MinTrackerSales: 1},
		{Spend: 5, MinClicks: 10},
		{Spend: 10, MinTrackerLeads: 3},
	})
	require.NoError(t, err)
	require.Equal(t, []float64{5, 10, 20}, []float64{normalized[0].Spend, normalized[1].Spend, normalized[2].Spend})

	_, err = normalizeCheckpoints(nil)
	require.Error(t, err)

	_, err = normalizeCheckpoints([]GuardCheckpoint{{Spend: 0, MinClicks: 1}})
	require.Error(t, err, "zero spend is rejected")

	_, err = normalizeCheckpoints([]GuardCheckpoint{{Spend: 5, MinClicks: 1}, {Spend: 5, MinClicks: 2}})
	require.Error(t, err, "duplicate spend is rejected")

	_, err = normalizeCheckpoints([]GuardCheckpoint{{Spend: 5}})
	require.Error(t, err, "a checkpoint without thresholds is rejected")

	_, err = normalizeCheckpoints([]GuardCheckpoint{{Spend: 5, MinClicks: -1}})
	require.Error(t, err, "negative thresholds are rejected")
}

func TestCheckpointSatisfied(t *testing.T) {
	checkpoint := GuardCheckpoint{
		Spend:           10,
		MinClicks:       50,
		MinTrackerLeads: 3,
		MinTrackerSales: 1,
	}
	passing := GuardObservation{Spend: 12, Clicks: 60, TrackerLeads: 3, TrackerSales: 1}
	require.True(t, checkpointSatisfied(checkpoint, passing))

	noSales := passing
	noSales.TrackerSales = 0
	require.False(t, checkpointSatisfied(checkpoint, noSales))

	fewClicks := passing
	fewClicks.Clicks = 49
	require.False(t, checkpointSatisfied(checkpoint, fewClicks))

	// Thresholds that are zero are not enforced.
	require.True(t, checkpointSatisfied(GuardCheckpoint{Spend: 5, MinClicks: 1}, GuardObservation{Clicks: 1}))
}
