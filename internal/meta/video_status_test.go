package meta

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForVideoReadyPollsWithBoundedExponentialBackoff(t *testing.T) {
	t.Parallel()

	responses := []string{
		`{"id":"video-1","status":{"video_status":"processing","processing_progress":10,"processing_phase":{"status":"in_progress"}}}`,
		`{"id":"video-1","status":{"video_status":"upload_complete","processing_progress":40,"processing_phase":{"status":"in_progress"}}}`,
		`{"id":"video-1","status":{"video_status":"processing","processing_progress":70,"processing_phase":{"status":"in_progress"}}}`,
		`{"id":"video-1","status":{"video_status":"processing","processing_progress":99,"processing_phase":{"status":"in_progress"}}}`,
		`{"id":"video-1","status":{"video_status":"ready","processing_progress":100,"processing_phase":{"status":"complete"}},"future_field":"preserved"}`,
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := int(calls.Add(1))
		if request.URL.Path != "/v25.0/video-1" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("fields") != videoStatusFields {
			t.Errorf("fields = %q", request.URL.Query().Get("fields"))
		}
		index := min(call-1, len(responses)-1)
		_, _ = writer.Write([]byte(responses[index]))
	}))
	defer server.Close()

	var sleeps []time.Duration
	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		4*time.Millisecond,
		func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
	)
	result, err := client.WaitForVideoReady(context.Background(), "token", "video-1")
	if err != nil {
		t.Fatalf("WaitForVideoReady: %v", err)
	}
	if !result.Ready() || result.ID != "video-1" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(string(result.Raw), `"future_field":"preserved"`) {
		t.Fatalf("raw response did not preserve extension: %s", result.Raw)
	}
	if calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5", calls.Load())
	}
	wantSleeps := []time.Duration{
		time.Millisecond,
		2 * time.Millisecond,
		4 * time.Millisecond,
		4 * time.Millisecond,
	}
	if !reflect.DeepEqual(sleeps, wantSleeps) {
		t.Fatalf("sleeps = %v, want %v", sleeps, wantSleeps)
	}
}

func TestWaitForVideoReadyReturnsTerminalProcessingError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
			"id":"video-2",
			"status":{
				"video_status":"error",
				"uploading_phase":{"status":"complete","bytes_transferred":42},
				"processing_phase":{
					"status":"error",
					"errors":[{"code":1363008,"message":"Video creation failed"}]
				}
			}
		}`))
	}))
	defer server.Close()

	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		time.Second,
		func(context.Context, time.Duration) error {
			t.Fatal("terminal processing error unexpectedly slept")
			return nil
		},
	)
	result, err := client.WaitForVideoReady(context.Background(), "token", "video-2")
	if err == nil {
		t.Fatal("WaitForVideoReady unexpectedly succeeded")
	}
	var processingErr *VideoProcessingError
	if !errors.As(err, &processingErr) {
		t.Fatalf("error = %T %v, want *VideoProcessingError", err, err)
	}
	if IsRetryableError(err) || processingErr.Retryable() {
		t.Fatalf("terminal processing error is retryable: %#v", processingErr)
	}
	if processingErr.Phase != "processing" ||
		len(processingErr.Errors) != 1 ||
		processingErr.Errors[0].Code != 1363008 {
		t.Fatalf("processing error = %#v", processingErr)
	}
	if result.Status.ProcessingPhase.Status != "error" ||
		processingErr.LastStatus.Status.ProcessingPhase.Status != "error" {
		t.Fatalf("last status was not returned: result=%#v error=%#v", result, processingErr)
	}
}

func TestWaitForVideoReadyTreatsNarrowCode100StillProcessingAsPending(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{
				"error":{
					"message":"The video is still processing. Please try again later.",
					"type":"GraphMethodException",
					"code":100
				}
			}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"video-code-100","status":{"video_status":"ready"}}`))
	}))
	defer server.Close()

	var sleeps atomic.Int32
	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		time.Second,
		func(context.Context, time.Duration) error {
			sleeps.Add(1)
			return nil
		},
	)
	result, err := client.WaitForVideoReady(
		context.Background(),
		"token",
		"video-code-100",
	)
	if err != nil || !result.Ready() {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if calls.Load() != 2 || sleeps.Load() != 1 {
		t.Fatalf("calls=%d sleeps=%d, want 2/1", calls.Load(), sleeps.Load())
	}
}

func TestWaitForVideoReadyPersistentCode100ReturnsTypedPendingError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{
			"error":{
				"message":"Video is still processing",
				"type":"GraphMethodException",
				"code":100
			}
		}`))
	}))
	defer server.Close()

	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		time.Second,
		func(context.Context, time.Duration) error {
			return context.DeadlineExceeded
		},
	)
	result, err := client.WaitForVideoReady(
		context.Background(),
		"token",
		"video-code-100-pending",
	)
	var pendingErr *VideoProcessingPendingError
	if !errors.As(err, &pendingErr) || !IsRetryableError(err) {
		t.Fatalf("error = %T %v, want retryable *VideoProcessingPendingError", err, err)
	}
	if result.ID != "video-code-100-pending" ||
		pendingErr.LastStatus.ID != "video-code-100-pending" {
		t.Fatalf("last status IDs: result=%#v error=%#v", result, pendingErr)
	}
}

func TestWaitForVideoReadyLeavesOtherCode100Terminal(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{
			"error":{
				"message":"Invalid fields parameter",
				"type":"OAuthException",
				"code":100
			}
		}`))
	}))
	defer server.Close()

	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		time.Second,
		func(context.Context, time.Duration) error {
			t.Fatal("terminal code 100 unexpectedly slept")
			return nil
		},
	)
	_, err := client.WaitForVideoReady(context.Background(), "token", "video-bad-request")
	var graphErr *GraphError
	if !errors.As(err, &graphErr) || graphErr.Code != 100 {
		t.Fatalf("error = %T %v, want code-100 *GraphError", err, err)
	}
	if IsRetryableError(err) {
		t.Fatalf("unrelated code 100 became retryable: %v", err)
	}
}

func TestWaitForVideoReadyTimeoutIsTypedDurableRetryable(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = writer.Write([]byte(`{
			"id":"video-3",
			"status":{
				"video_status":"processing",
				"processing_progress":25,
				"processing_phase":{"status":"in_progress"}
			}
		}`))
	}))
	defer server.Close()

	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Second,
		time.Second,
		func(context.Context, time.Duration) error {
			return context.DeadlineExceeded
		},
	)
	result, err := client.WaitForVideoReady(context.Background(), "token", "video-3")
	if err == nil {
		t.Fatal("WaitForVideoReady unexpectedly succeeded")
	}
	var pendingErr *VideoProcessingPendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("error = %T %v, want *VideoProcessingPendingError", err, err)
	}
	if !IsRetryableError(err) || !pendingErr.Retryable() {
		t.Fatalf("pending error is not durable-retryable: %#v", pendingErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending error does not unwrap deadline: %v", err)
	}
	if result.Status.ProcessingProgress != 25 ||
		pendingErr.LastStatus.Status.ProcessingProgress != 25 {
		t.Fatalf("last processing status was not preserved: result=%#v error=%#v", result, pendingErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestWaitForVideoReadyHonorsCallerCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"id":"video-4","status":{"video_status":"processing"}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := videoStatusTestClient(
		t,
		server.URL,
		time.Minute,
		time.Millisecond,
		time.Second,
		func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		},
	)
	_, err := client.WaitForVideoReady(ctx, "token", "video-4")
	var pendingErr *VideoProcessingPendingError
	if !errors.As(err, &pendingErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %T %v, want pending cancellation", err, err)
	}
	if !IsRetryableError(err) {
		t.Fatalf("canceled pending video is not durable-retryable: %v", err)
	}
}

func TestNewClientRejectsInvertedVideoPollDelays(t *testing.T) {
	t.Parallel()

	_, err := NewClient(ClientConfig{
		AppID:              "app",
		AppSecret:          "secret",
		BaseURL:            "https://graph.example",
		OAuthBaseURL:       "https://oauth.example",
		VideoPollBaseDelay: 2 * time.Second,
		VideoPollMaxDelay:  time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "video poll max delay") {
		t.Fatalf("NewClient error = %v", err)
	}
}

func videoStatusTestClient(
	t *testing.T,
	serverURL string,
	timeout time.Duration,
	baseDelay time.Duration,
	maxDelay time.Duration,
	sleep func(context.Context, time.Duration) error,
) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		AppID:                  "app",
		AppSecret:              "secret",
		APIVersion:             DefaultAPIVersion,
		BaseURL:                serverURL,
		OAuthBaseURL:           serverURL,
		HTTPClient:             &http.Client{Timeout: 3 * time.Second},
		MaxRetries:             1,
		BaseRetryDelay:         time.Millisecond,
		MaxRetryDelay:          time.Second,
		Sleep:                  sleep,
		VideoProcessingTimeout: timeout,
		VideoPollBaseDelay:     baseDelay,
		VideoPollMaxDelay:      maxDelay,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}
