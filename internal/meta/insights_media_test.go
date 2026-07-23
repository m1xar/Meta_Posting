package meta

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchInsightsBuildsQueryAndPreservesRawMetrics(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/act_123/insights" {
			t.Errorf("path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("level") != "ad" || query.Get("time_increment") != "1" {
			t.Errorf("query = %v", query)
		}
		if query.Get("time_range") != `{"since":"2026-01-01","until":"2026-01-02"}` {
			t.Errorf("time_range = %q", query.Get("time_range"))
		}
		fields := "," + query.Get("fields") + ","
		if strings.Contains(fields, ",landing_page_views,") {
			t.Errorf("default fields request unsupported top-level landing_page_views: %q", query.Get("fields"))
		}
		if !strings.Contains(fields, ",actions,") {
			t.Errorf("default fields must request actions for landing_page_view: %q", query.Get("fields"))
		}
		var filtering []InsightFilter
		if err := json.Unmarshal([]byte(query.Get("filtering")), &filtering); err != nil {
			t.Errorf("decode filtering: %v", err)
		} else if len(filtering) != 1 ||
			filtering[0].Field != "ad.id" ||
			filtering[0].Operator != "IN" {
			t.Errorf("filtering = %#v", filtering)
		}
		_, _ = writer.Write([]byte(`{"data":[{
			"ad_id":"ad",
			"spend":"12.34",
			"actions":[{"action_type":"purchase","value":"2","7d_click":"1","new_window":"9"}],
			"new_metric":[{"value":"123"}]
		}]}`))
	}))
	defer server.Close()

	rows, err := testClient(t, server.URL, 1, nil).FetchAccountInsights(
		context.Background(),
		"token",
		"123",
		InsightQuery{
			Level:         InsightLevelAd,
			TimeRange:     &InsightTimeRange{Since: "2026-01-01", Until: "2026-01-02"},
			TimeIncrement: 1,
			Filtering: []InsightFilter{{
				Field:    "ad.id",
				Operator: "IN",
				Value:    []string{"ad"},
			}},
		},
	)
	if err != nil {
		t.Fatalf("FetchAccountInsights: %v", err)
	}
	if len(rows) != 1 || rows[0].Actions[0].SevenDayClick != "1" {
		t.Fatalf("rows = %#v", rows)
	}
	if string(rows[0].Actions[0].Raw["new_window"]) != `"9"` ||
		string(rows[0].Raw["new_metric"]) != `[{"value":"123"}]` {
		t.Errorf("raw metrics not preserved: %#v", rows[0])
	}
}

func TestUploadImageFileUsesMultipart(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	imagePath := filepath.Join(tempDir, "creative.png")
	if err := os.WriteFile(imagePath, []byte("fake-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/act_123/adimages" {
			t.Errorf("path = %q", request.URL.Path)
		}
		mediaType, params, err := mimeParse(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("content type: %s %v", mediaType, err)
		}
		reader := multipart.NewReader(request.Body, params["boundary"])
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		if part.FormName() != "filename" || part.FileName() != "creative.png" {
			t.Errorf("part = %q %q", part.FormName(), part.FileName())
		}
		data, _ := io.ReadAll(part)
		if string(data) != "fake-image" {
			t.Errorf("upload = %q", data)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"images": map[string]any{
				"creative.png": map[string]any{"hash": "image-hash", "url": "https://image"},
			},
		})
	}))
	defer server.Close()

	response, err := testClient(t, server.URL, 1, nil).UploadImageFile(
		context.Background(),
		"token",
		"123",
		imagePath,
	)
	if err != nil {
		t.Fatalf("UploadImageFile: %v", err)
	}
	if response.Images["creative.png"].Hash != "image-hash" {
		t.Errorf("response = %#v", response)
	}
}

func mimeParse(value string) (string, map[string]string, error) {
	// Kept in a helper so the main assertion remains easy to scan.
	return parseMediaType(value)
}

var parseMediaType = func(value string) (string, map[string]string, error) {
	parts := strings.Split(value, ";")
	mediaType := strings.TrimSpace(parts[0])
	params := make(map[string]string)
	for _, part := range parts[1:] {
		keyValue := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(keyValue) == 2 {
			params[keyValue[0]] = strings.Trim(keyValue[1], `"`)
		}
	}
	return mediaType, params, nil
}
