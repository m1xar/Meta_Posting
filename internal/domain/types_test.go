package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJSONRoundTrip(t *testing.T) {
	value := MustJSON(map[string]any{"name": "Raze", "count": 3})

	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"Raze","count":3}`, string(raw))

	var decoded map[string]any
	require.NoError(t, value.Decode(&decoded))
	require.Equal(t, "Raze", decoded["name"])
}

func TestPageRequestNormalized(t *testing.T) {
	require.Equal(t, PageRequest{Limit: DefaultPageLimit}, (PageRequest{Limit: -1, Offset: -4}).Normalized())
	require.Equal(t, PageRequest{Limit: MaxPageLimit, Offset: 9}, (PageRequest{Limit: MaxPageLimit + 1, Offset: 9}).Normalized())
}

func TestRetryDelay(t *testing.T) {
	require.Equal(t, time.Second, RetryDelay(1, time.Second, 10*time.Second))
	require.Equal(t, 4*time.Second, RetryDelay(3, time.Second, 10*time.Second))
	require.Equal(t, 10*time.Second, RetryDelay(10, time.Second, 10*time.Second))
}
