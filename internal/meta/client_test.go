package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientAddsTokenProofAndDecodesUsage(t *testing.T) {
	t.Parallel()
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/act_123/campaigns" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("access_token") != "user-token" {
			t.Errorf("access_token = %q", request.URL.Query().Get("access_token"))
		}
		expectedProof := proof("secret", "user-token")
		if request.URL.Query().Get("appsecret_proof") != expectedProof {
			t.Errorf("appsecret_proof = %q, want %q", request.URL.Query().Get("appsecret_proof"), expectedProof)
		}
		if request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(request.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		writer.Header().Set("X-FB-Request-ID", "req-1")
		writer.Header().Set("X-App-Usage", `{"call_count":7}`)
		_, _ = writer.Write([]byte(`{"id":"campaign-1"}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1, nil)
	var response struct {
		ID string `json:"id"`
	}
	meta, err := client.PostJSONWithMeta(
		context.Background(),
		"/act_123/campaigns",
		"user-token",
		nil,
		map[string]any{"name": "Campaign"},
		&response,
	)
	if err != nil {
		t.Fatalf("PostJSONWithMeta: %v", err)
	}
	if response.ID != "campaign-1" {
		t.Errorf("ID = %q", response.ID)
	}
	if gotBody["name"] != "Campaign" {
		t.Errorf("body = %#v", gotBody)
	}
	if meta.RequestID != "req-1" || string(meta.AppUsage["call_count"]) != "7" {
		t.Errorf("metadata = %#v", meta)
	}
}

func TestClientRetriesTransientGraphError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var sleeps atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writer.Header().Set("Retry-After", "1")
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":{"message":"temporary","type":"OAuthException","code":2,"is_transient":true}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"me"}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 2, func(_ context.Context, delay time.Duration) error {
		sleeps.Add(1)
		if delay != time.Second {
			t.Errorf("retry delay = %s", delay)
		}
		return nil
	})
	var user User
	if err := client.Get(context.Background(), "/me", "token", nil, &user); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if calls.Load() != 2 || sleeps.Load() != 1 {
		t.Fatalf("calls=%d sleeps=%d", calls.Load(), sleeps.Load())
	}
}

func TestClientReturnsTypedNonRetryableGraphError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-FB-Request-ID", "request-id")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":{"message":"bad targeting","type":"OAuthException","code":100,"error_subcode":18157520,"fbtrace_id":"trace"}}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1, func(context.Context, time.Duration) error {
		t.Fatal("non-retryable error slept")
		return nil
	})
	err := client.Get(context.Background(), "/me", "token", nil, &User{})
	var graphErr *GraphError
	if !errors.As(err, &graphErr) {
		t.Fatalf("error = %T %v", err, err)
	}
	if graphErr.Code != 100 || graphErr.ErrorSubcode != 18157520 || graphErr.RequestID != "request-id" {
		t.Errorf("GraphError = %#v", graphErr)
	}
	if graphErr.Retryable() {
		t.Error("GraphError unexpectedly retryable")
	}
}

func TestClientUsesDedicatedHTTPClientForMultipartUploads(t *testing.T) {
	t.Parallel()

	var normalCalls atomic.Int32
	var uploadCalls atomic.Int32
	normalClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		normalCalls.Add(1)
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("normal content type = %q", got)
		}
		return jsonResponse(http.StatusOK, `{"id":"normal"}`), nil
	})}
	uploadClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		uploadCalls.Add(1)
		if got := request.Header.Get("Content-Type"); !strings.HasPrefix(got, "multipart/form-data; boundary=") {
			t.Errorf("upload content type = %q", got)
		}
		_, _ = io.Copy(io.Discard, request.Body)
		_ = request.Body.Close()
		return jsonResponse(http.StatusOK, `{"id":"upload"}`), nil
	})}

	client, err := NewClient(ClientConfig{
		AppID:            "app",
		AppSecret:        "secret",
		BaseURL:          "https://graph.example",
		OAuthBaseURL:     "https://oauth.example",
		HTTPClient:       normalClient,
		UploadHTTPClient: uploadClient,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	var normalResponse struct {
		ID string `json:"id"`
	}
	if err := client.PostJSONNoRetry(
		context.Background(),
		"/act_123/campaigns",
		"token",
		nil,
		map[string]string{"name": "Campaign"},
		&normalResponse,
	); err != nil {
		t.Fatalf("PostJSONNoRetry: %v", err)
	}

	var uploadResponse struct {
		ID string `json:"id"`
	}
	if err := client.PostMultipartNoRetry(
		context.Background(),
		"/act_123/advideos",
		"token",
		nil,
		map[string]string{"name": "Video"},
		[]MultipartFile{{
			FieldName: "source",
			FileName:  "video.mp4",
			Open: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("video")), nil
			},
		}},
		&uploadResponse,
	); err != nil {
		t.Fatalf("PostMultipartNoRetry: %v", err)
	}

	if normalResponse.ID != "normal" || uploadResponse.ID != "upload" {
		t.Fatalf("responses: normal=%q upload=%q", normalResponse.ID, uploadResponse.ID)
	}
	if normalCalls.Load() != 1 || uploadCalls.Load() != 1 {
		t.Fatalf("calls: normal=%d upload=%d", normalCalls.Load(), uploadCalls.Load())
	}
}

func TestSanitizeTransportErrorRemovesGraphSecrets(t *testing.T) {
	t.Parallel()
	requestURL, err := url.Parse("https://graph.facebook.com/v25.0/me?access_token=user-token-secret&appsecret_proof=proof-secret&code=oauth-code-secret")
	if err != nil {
		t.Fatal(err)
	}
	original := &url.Error{
		Op:  "Get",
		URL: requestURL.String(),
		Err: errors.New("request for user-token-secret failed with oauth-code-secret"),
	}
	sanitized := sanitizeTransportError(original, requestURL).Error()
	for _, secret := range []string{"user-token-secret", "proof-secret", "oauth-code-secret"} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("sanitized error leaked %q: %s", secret, sanitized)
		}
	}
	if !strings.Contains(sanitized, "%3Credacted%3E") && !strings.Contains(sanitized, "<redacted>") {
		t.Fatalf("sanitized error has no redaction marker: %s", sanitized)
	}
}

func TestPostJSONNoRetryReturnsRetryableSanitizedTransportError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	client, err := NewClient(ClientConfig{
		AppID:        "app",
		AppSecret:    "secret",
		BaseURL:      "https://graph.example",
		OAuthBaseURL: "https://oauth.example",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("temporary dial failure mentioning user-token-secret")
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.PostJSONNoRetry(
		context.Background(),
		"/act_123/campaigns",
		"user-token-secret",
		nil,
		map[string]string{"name": "Campaign"},
		nil,
	)
	if err == nil {
		t.Fatal("PostJSONNoRetry unexpectedly succeeded")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %T %v, want *TransportError", err, err)
	}
	if !IsRetryableError(err) || !transportErr.Retryable() {
		t.Fatalf("transport error is not retryable: %#v", transportErr)
	}
	if strings.Contains(err.Error(), "user-token-secret") {
		t.Fatalf("transport error leaked access token: %s", err)
	}
}

func TestPostJSONNoRetryReturnsDurableRetryableResponseReadErrorWithoutReplay(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	client, err := NewClient(ClientConfig{
		AppID:        "app",
		AppSecret:    "secret",
		BaseURL:      "https://graph.example",
		OAuthBaseURL: "https://oauth.example",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     make(http.Header),
				Body: &errorReadCloser{
					err: errors.New("response read failed near user-token-secret"),
				},
			}, nil
		})},
		MaxRetries: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.PostJSONNoRetry(
		context.Background(),
		"/act_123/campaigns",
		"user-token-secret",
		nil,
		map[string]string{"name": "Campaign"},
		&CreateResult{},
	)
	if calls.Load() != 1 {
		t.Fatalf("non-idempotent request was replayed: calls = %d", calls.Load())
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || !IsRetryableError(err) {
		t.Fatalf("error = %T %v, want retryable *ResponseError", err, err)
	}
	if strings.Contains(err.Error(), "user-token-secret") {
		t.Fatalf("response error leaked access token: %s", err)
	}
}

func TestPostJSONNoRetryReturnsDurableRetryableDecodeErrorWithoutReplay(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"id":`))
	}))
	defer server.Close()

	err := testClient(t, server.URL, 4, nil).PostJSONNoRetry(
		context.Background(),
		"/act_123/campaigns",
		"token",
		nil,
		map[string]string{"name": "Campaign"},
		&CreateResult{},
	)
	if calls.Load() != 1 {
		t.Fatalf("non-idempotent request was replayed: calls = %d", calls.Load())
	}
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || !IsRetryableError(err) {
		t.Fatalf("error = %T %v, want retryable *ResponseError", err, err)
	}
}

func TestCollectPagesRefreshesCredentials(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	var calls atomic.Int32
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		if request.URL.Query().Get("access_token") != "fresh" {
			t.Errorf("call %d access token = %q", call, request.URL.Query().Get("access_token"))
		}
		if request.URL.Query().Get("appsecret_proof") != proof("secret", "fresh") {
			t.Errorf("call %d proof not refreshed", call)
		}
		if call == 1 {
			next := server.URL + "/v25.0/items?after=cursor&access_token=stale&appsecret_proof=stale"
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"data":   []map[string]string{{"id": "1"}},
				"paging": map[string]string{"next": next},
			})
			return
		}
		if request.URL.Query().Get("after") != "cursor" {
			t.Errorf("after = %q", request.URL.Query().Get("after"))
		}
		_, _ = writer.Write([]byte(`{"data":[{"id":"2"}]}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1, nil)
	items, err := CollectPages[ObjectRef](
		context.Background(),
		client,
		"/items",
		"fresh",
		url.Values{"limit": {"1"}},
	)
	if err != nil {
		t.Fatalf("CollectPages: %v", err)
	}
	if len(items) != 2 || items[0].ID != "1" || items[1].ID != "2" {
		t.Errorf("items = %#v", items)
	}
}

func TestAuthorizationURLAndTokenExchange(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/oauth/access_token" {
			t.Errorf("exchange path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("client_id") != "app" || query.Get("client_secret") != "secret" ||
			query.Get("code") != "code" || query.Get("redirect_uri") != "https://callback.example" {
			t.Errorf("exchange query = %v", query)
		}
		_, _ = writer.Write([]byte(`{"access_token":"long-token","token_type":"bearer","expires_in":3600}`))
	}))
	defer server.Close()
	client := testClient(t, server.URL, 1, nil)

	authURL, err := client.AuthorizationURL(OAuthOptions{
		RedirectURI: "https://callback.example",
		State:       "state",
		Scopes:      []string{"ads_read", "ads_management"},
		ConfigID:    "config",
	})
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	parsed, _ := url.Parse(authURL)
	if parsed.Path != "/v25.0/dialog/oauth" || parsed.Query().Get("config_id") != "config" ||
		parsed.Query().Get("scope") != "" ||
		parsed.Query().Get("response_type") != "code" ||
		parsed.Query().Get("override_default_response_type") != "true" {
		t.Errorf("authorization URL = %s", authURL)
	}
	token, err := client.ExchangeCode(context.Background(), "code", "https://callback.example")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.AccessToken != "long-token" || token.ExpiresIn != 3600 {
		t.Errorf("token = %#v", token)
	}
}

func TestLongLivedAndDebugToken(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch calls.Add(1) {
		case 1:
			query := request.URL.Query()
			if request.URL.Path != "/v25.0/oauth/access_token" ||
				query.Get("grant_type") != "fb_exchange_token" ||
				query.Get("fb_exchange_token") != "short" {
				t.Errorf("long-lived exchange request = %s?%s", request.URL.Path, request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"access_token":"long","expires_in":5184000}`))
		case 2:
			query := request.URL.Query()
			appToken := "app|secret"
			if request.URL.Path != "/v25.0/debug_token" ||
				query.Get("input_token") != "long" ||
				query.Get("access_token") != appToken ||
				query.Get("appsecret_proof") != proof("secret", appToken) {
				t.Errorf("debug request = %s?%s", request.URL.Path, request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"data":{"app_id":"app","type":"USER","user_id":"user","is_valid":true,"scopes":["ads_read"]}}`))
		}
	}))
	defer server.Close()
	client := testClient(t, server.URL, 1, nil)

	token, err := client.ExchangeLongLivedToken(context.Background(), "short")
	if err != nil {
		t.Fatalf("ExchangeLongLivedToken: %v", err)
	}
	if token.AccessToken != "long" {
		t.Errorf("token = %#v", token)
	}
	debug, err := client.DebugToken(context.Background(), token.AccessToken)
	if err != nil {
		t.Fatalf("DebugToken: %v", err)
	}
	if !debug.IsValid || debug.UserID != "user" || len(debug.Scopes) != 1 {
		t.Errorf("debug = %#v", debug)
	}
}

func testClient(
	t *testing.T,
	serverURL string,
	maxRetries int,
	sleep func(context.Context, time.Duration) error,
) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		AppID:          "app",
		AppSecret:      "secret",
		APIVersion:     DefaultAPIVersion,
		BaseURL:        serverURL,
		OAuthBaseURL:   serverURL,
		HTTPClient:     &http.Client{Timeout: 3 * time.Second},
		MaxRetries:     maxRetries,
		BaseRetryDelay: time.Millisecond,
		MaxRetryDelay:  time.Second,
		Sleep:          sleep,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func proof(secret, token string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func decodeJSONBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request body %q: %v", strings.TrimSpace(string(body)), err)
	}
	return payload
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type errorReadCloser struct {
	err error
}

func (r *errorReadCloser) Read([]byte) (int, error) {
	return 0, r.err
}

func (r *errorReadCloser) Close() error {
	return nil
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
