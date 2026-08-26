package meta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestClientForServer points a client at an httptest server.
func newTestClientForServer(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		AppID:      "app",
		AppSecret:  "secret",
		APIVersion: DefaultAPIVersion,
		BaseURL:    server.URL,
		MaxRetries: 0,
	})
	require.NoError(t, err)
	return client
}

func TestIsOversizedRequestRecognisesMetasWording(t *testing.T) {
	// Meta returns this as the generic transient code 1, so an ordinary
	// retry repeats the same oversized request and fails identically until
	// the job dies. Seventeen inventory sweeps died this way.
	oversized := &GraphError{
		Code:    1,
		Message: "Please reduce the amount of data you're asking for, then retry your request",
	}
	require.True(t, IsOversizedRequest(oversized))
	require.True(t, IsOversizedRequest(fmt.Errorf("sweep failed: %w", oversized)))

	// Other transient code-1 failures must keep their normal retry, which
	// does help them.
	require.False(t, IsOversizedRequest(&GraphError{Code: 1, Message: "An unknown error occurred"}))
	require.False(t, IsOversizedRequest(&GraphError{Code: 4, Message: "reduce the amount of data"}))
	require.False(t, IsOversizedRequest(errors.New("not a graph error")))
	require.False(t, IsOversizedRequest(nil))
}

func TestCollectPagesAdaptiveHalvesUntilMetaAccepts(t *testing.T) {
	var limits []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		limits = append(limits, limit)
		w.Header().Set("Content-Type", "application/json")
		// Accounts differ by orders of magnitude, so a fixed page size is
		// wrong for someone; this one only answers at 50 or below.
		if limit > 50 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":1,"message":"Please reduce the amount of data you're asking for, then retry your request"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"1"},{"id":"2"}]}`))
	}))
	defer server.Close()

	client := newTestClientForServer(t, server)
	query := url.Values{"limit": {"200"}}
	rows, err := CollectPagesAdaptive[map[string]any](
		context.Background(), client, "/act_1/ads", "token", query)

	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, []int{200, 100, 50}, limits, "each refusal should halve the page")
	// The caller's own values must not be mutated behind its back.
	require.Equal(t, "200", query.Get("limit"))
}

func TestCollectPagesAdaptiveStopsAtTheFloor(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":1,"message":"Please reduce the amount of data you're asking for"}}`))
	}))
	defer server.Close()

	client := newTestClientForServer(t, server)
	_, err := CollectPagesAdaptive[map[string]any](
		context.Background(), client, "/act_1/ads", "token", url.Values{"limit": {"100"}})

	// Below the floor the page size is not the problem, so the error is
	// surfaced rather than retried forever.
	require.Error(t, err)
	require.Greater(t, attempts, 1)
	require.Less(t, attempts, 12)
}

func TestCollectPagesAdaptivePassesThroughOtherErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":100,"message":"Invalid parameter"}}`))
	}))
	defer server.Close()

	client := newTestClientForServer(t, server)
	_, err := CollectPagesAdaptive[map[string]any](
		context.Background(), client, "/act_1/ads", "token", url.Values{"limit": {"100"}})
	require.Error(t, err)
	require.Equal(t, 1, attempts, "a non-size error must not trigger the halving loop")
}
