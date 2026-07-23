package meta

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFindUploadedVideoByTitleMatchesExactlyAcrossPages(t *testing.T) {
	t.Parallel()

	const title = "Raze Posting media-id:account-id"
	var calls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch call := calls.Add(1); call {
		case 1:
			if request.URL.Path != "/v25.0/act_123/advideos" {
				t.Errorf("path = %q", request.URL.Path)
			}
			if request.URL.Query().Get("fields") != "id,title,status" ||
				request.URL.Query().Get("limit") != "500" ||
				request.URL.Query().Get("title") != title {
				t.Errorf("query = %v", request.URL.Query())
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"data": []map[string]any{{
					"id":    "near-match",
					"title": title + " copy",
				}},
				"paging": map[string]string{
					"next": server.URL + "/v25.0/act_123/advideos?after=cursor",
				},
			})
		case 2:
			if request.URL.Query().Get("after") != "cursor" {
				t.Errorf("after = %q", request.URL.Query().Get("after"))
			}
			_, _ = writer.Write([]byte(`{
				"data":[{
					"id":"video-id",
					"title":"Raze Posting media-id:account-id",
					"status":{
						"video_status":"processing",
						"processing_phase":{"status":"in_progress"}
					}
				}]
			}`))
		default:
			t.Fatalf("unexpected call %d", call)
		}
	}))
	defer server.Close()

	video, found, err := testClient(t, server.URL, 1, nil).FindUploadedVideoByTitle(
		context.Background(),
		"token",
		"123",
		title,
	)
	if err != nil {
		t.Fatalf("FindUploadedVideoByTitle: %v", err)
	}
	if !found || video.ID != "video-id" || video.Title != title ||
		video.Status.ProcessingPhase.Status != "in_progress" {
		t.Fatalf("video=%#v found=%v", video, found)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

func TestFindUploadedVideoByTitleRefusesAmbiguousMatch(t *testing.T) {
	t.Parallel()

	const title = "Raze Posting duplicate"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "video-1", "title": title},
				{"id": "video-2", "title": title},
			},
		})
	}))
	defer server.Close()

	_, found, err := testClient(t, server.URL, 1, nil).FindUploadedVideoByTitle(
		context.Background(),
		"token",
		"123",
		title,
	)
	if err == nil || found || !strings.Contains(err.Error(), "ambiguous retry") {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if IsRetryableError(err) {
		t.Fatalf("ambiguous reconciliation error must be terminal: %v", err)
	}
}

func TestFindUploadedVideoByTitleEmptyMatchedIDIsRetryableResponseError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"data":[{"title":"unique-title"}]}`))
	}))
	defer server.Close()

	_, found, err := testClient(t, server.URL, 1, nil).FindUploadedVideoByTitle(
		context.Background(),
		"token",
		"123",
		"unique-title",
	)
	var responseErr *ResponseError
	if found || !errors.As(err, &responseErr) || !IsRetryableError(err) {
		t.Fatalf("found=%v error=%T %v, want retryable *ResponseError", found, err, err)
	}
}

func TestUploadVideoSuccessfulEmptyIDIsRetryableResponseError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	_, err := testClient(t, server.URL, 4, nil).UploadVideo(
		context.Background(),
		"token",
		"123",
		"creative.mp4",
		"video/mp4",
		func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("video")), nil
		},
		VideoUploadOptions{Title: "unique-title"},
	)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || !IsRetryableError(err) {
		t.Fatalf("error = %T %v, want retryable *ResponseError", err, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("non-idempotent upload calls = %d, want 1", calls.Load())
	}
}

func TestUploadImageSuccessfulEmptyImagesIsRetryableResponseError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		_, _ = writer.Write([]byte(`{"images":{}}`))
	}))
	defer server.Close()

	_, err := testClient(t, server.URL, 4, nil).UploadImage(
		context.Background(),
		"token",
		"123",
		"creative.jpg",
		"image/jpeg",
		func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("image")), nil
		},
	)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || !IsRetryableError(err) {
		t.Fatalf("error = %T %v, want retryable *ResponseError", err, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("non-idempotent upload calls = %d, want 1", calls.Load())
	}
}
