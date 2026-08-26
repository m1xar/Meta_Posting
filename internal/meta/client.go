package meta

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAPIVersion             = "v25.0"
	defaultGraphBaseURL           = "https://graph.facebook.com"
	defaultOAuthBaseURL           = "https://www.facebook.com"
	defaultMaxResponseSize        = int64(64 << 20)
	defaultVideoProcessingTimeout = 30 * time.Minute
	defaultVideoPollBaseDelay     = 2 * time.Second
	defaultVideoPollMaxDelay      = 30 * time.Second
)

// ClientConfig configures the Meta Graph API client. BaseURL and OAuthBaseURL
// are primarily useful for tests; production callers normally leave them
// empty.
type ClientConfig struct {
	AppID            string
	AppSecret        string
	APIVersion       string
	BaseURL          string
	OAuthBaseURL     string
	HTTPClient       *http.Client
	UploadHTTPClient *http.Client
	MaxRetries       int
	BaseRetryDelay   time.Duration
	MaxRetryDelay    time.Duration
	MaxResponseSize  int64
	UserAgent        string
	Sleep            func(context.Context, time.Duration) error

	// VideoProcessingTimeout bounds WaitForVideoReady even when its caller
	// supplies a context without a deadline. Poll delays grow exponentially
	// from VideoPollBaseDelay up to VideoPollMaxDelay.
	VideoProcessingTimeout time.Duration
	VideoPollBaseDelay     time.Duration
	VideoPollMaxDelay      time.Duration
}

// Client is safe for concurrent use.
type Client struct {
	appID            string
	appSecret        string
	apiVersion       string
	baseURL          *url.URL
	oauthBaseURL     *url.URL
	httpClient       *http.Client
	uploadHTTPClient *http.Client
	maxRetries       int
	baseRetryDelay   time.Duration
	maxRetryDelay    time.Duration
	maxResponseSize  int64
	userAgent        string
	sleep            func(context.Context, time.Duration) error

	videoProcessingTimeout time.Duration
	videoPollBaseDelay     time.Duration
	videoPollMaxDelay      time.Duration
}

// ResponseMeta contains Meta's request identifiers and rate-usage snapshots.
// The usage values are intentionally left as raw JSON because Meta may add
// fields without an API version change.
type ResponseMeta struct {
	StatusCode     int
	RequestID      string
	TraceID        string
	AppUsage       map[string]json.RawMessage
	AdAccountUsage map[string]json.RawMessage
	BusinessUsage  map[string]json.RawMessage
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, errors.New("meta: app ID is required")
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, errors.New("meta: app secret is required")
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = DefaultAPIVersion
	}
	if !strings.HasPrefix(cfg.APIVersion, "v") {
		return nil, fmt.Errorf("meta: API version %q must start with v", cfg.APIVersion)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultGraphBaseURL
	}
	if cfg.OAuthBaseURL == "" {
		cfg.OAuthBaseURL = defaultOAuthBaseURL
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("meta: parse Graph base URL: %w", err)
	}
	oauthBaseURL, err := url.Parse(cfg.OAuthBaseURL)
	if err != nil {
		return nil, fmt.Errorf("meta: parse OAuth base URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, errors.New("meta: Graph base URL must be absolute")
	}
	if oauthBaseURL.Scheme == "" || oauthBaseURL.Host == "" {
		return nil, errors.New("meta: OAuth base URL must be absolute")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.UploadHTTPClient == nil {
		// Reuse an explicitly supplied client by default so custom transports
		// (for example, test or proxy transports) continue to apply. Production
		// callers provide a dedicated long-timeout upload client.
		cfg.UploadHTTPClient = cfg.HTTPClient
	}
	if cfg.MaxRetries < 0 {
		return nil, errors.New("meta: max retries cannot be negative")
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 4
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = 500 * time.Millisecond
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 30 * time.Second
	}
	if cfg.MaxResponseSize <= 0 {
		cfg.MaxResponseSize = defaultMaxResponseSize
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "raze-posting/1.0"
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if cfg.VideoProcessingTimeout <= 0 {
		cfg.VideoProcessingTimeout = defaultVideoProcessingTimeout
	}
	if cfg.VideoPollBaseDelay <= 0 {
		cfg.VideoPollBaseDelay = defaultVideoPollBaseDelay
	}
	if cfg.VideoPollMaxDelay <= 0 {
		cfg.VideoPollMaxDelay = defaultVideoPollMaxDelay
	}
	if cfg.VideoPollMaxDelay < cfg.VideoPollBaseDelay {
		return nil, errors.New("meta: video poll max delay cannot be less than base delay")
	}

	return &Client{
		appID:            cfg.AppID,
		appSecret:        cfg.AppSecret,
		apiVersion:       strings.Trim(cfg.APIVersion, "/"),
		baseURL:          baseURL,
		oauthBaseURL:     oauthBaseURL,
		httpClient:       cfg.HTTPClient,
		uploadHTTPClient: cfg.UploadHTTPClient,
		maxRetries:       cfg.MaxRetries,
		baseRetryDelay:   cfg.BaseRetryDelay,
		maxRetryDelay:    cfg.MaxRetryDelay,
		maxResponseSize:  cfg.MaxResponseSize,
		userAgent:        cfg.UserAgent,
		sleep:            cfg.Sleep,

		videoProcessingTimeout: cfg.VideoProcessingTimeout,
		videoPollBaseDelay:     cfg.VideoPollBaseDelay,
		videoPollMaxDelay:      cfg.VideoPollMaxDelay,
	}, nil
}

func (c *Client) AppID() string      { return c.appID }
func (c *Client) APIVersion() string { return c.apiVersion }

// AppSecretProof returns the HMAC-SHA256 proof required for server-side Graph
// calls made with a user access token.
func (c *Client) AppSecretProof(accessToken string) string {
	mac := hmac.New(sha256.New, []byte(c.appSecret))
	_, _ = mac.Write([]byte(accessToken))
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *Client) Get(ctx context.Context, graphPath, accessToken string, query url.Values, out any) error {
	_, err := c.GetWithMeta(ctx, graphPath, accessToken, query, out)
	return err
}

func (c *Client) GetWithMeta(ctx context.Context, graphPath, accessToken string, query url.Values, out any) (ResponseMeta, error) {
	return c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		target, err := c.graphURL(graphPath, accessToken, query)
		if err != nil {
			return nil, err
		}
		return http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	}, out)
}

// Delete revokes or removes a Graph object. It is used for account-level
// disconnects, which are idempotent from the user's perspective.
func (c *Client) Delete(ctx context.Context, graphPath, accessToken string, query url.Values, out any) error {
	_, err := c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		target, err := c.graphURL(graphPath, accessToken, query)
		if err != nil {
			return nil, err
		}
		return http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	}, out)
	return err
}

func (c *Client) PostJSON(ctx context.Context, graphPath, accessToken string, query url.Values, payload any, out any) error {
	_, err := c.PostJSONWithMeta(ctx, graphPath, accessToken, query, payload, out)
	return err
}

func (c *Client) PostJSONWithMeta(ctx context.Context, graphPath, accessToken string, query url.Values, payload any, out any) (ResponseMeta, error) {
	return c.postJSONWithRetries(ctx, graphPath, accessToken, query, payload, out, c.maxRetries)
}

// PostJSONNoRetry is for non-idempotent Graph mutations such as hierarchy
// creation. Retrying after a transport ambiguity could create a duplicate even
// though the first response was lost.
func (c *Client) PostJSONNoRetry(
	ctx context.Context,
	graphPath, accessToken string,
	query url.Values,
	payload any,
	out any,
) error {
	_, err := c.postJSONWithRetries(ctx, graphPath, accessToken, query, payload, out, 0)
	return err
}

func (c *Client) postJSONWithRetries(
	ctx context.Context,
	graphPath, accessToken string,
	query url.Values,
	payload any,
	out any,
	maxRetries int,
) (ResponseMeta, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return ResponseMeta{}, fmt.Errorf("meta: marshal request: %w", err)
	}
	return c.doWithRetries(ctx, maxRetries, func(ctx context.Context) (*http.Request, error) {
		target, err := c.graphURL(graphPath, accessToken, query)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, out)
}

// MultipartFile describes a replayable multipart upload. Open is invoked once
// for each retry, so callers should return a newly opened reader every time.
type MultipartFile struct {
	FieldName   string
	FileName    string
	ContentType string
	Open        func() (io.ReadCloser, error)
}

func (c *Client) PostMultipart(
	ctx context.Context,
	graphPath string,
	accessToken string,
	query url.Values,
	fields map[string]string,
	files []MultipartFile,
	out any,
) error {
	_, err := c.PostMultipartWithMeta(ctx, graphPath, accessToken, query, fields, files, out)
	return err
}

func (c *Client) PostMultipartWithMeta(
	ctx context.Context,
	graphPath string,
	accessToken string,
	query url.Values,
	fields map[string]string,
	files []MultipartFile,
	out any,
) (ResponseMeta, error) {
	return c.postMultipartWithRetries(ctx, graphPath, accessToken, query, fields, files, out, c.maxRetries)
}

// PostMultipartNoRetry avoids replaying non-idempotent video and asset
// uploads when the upstream outcome is ambiguous.
func (c *Client) PostMultipartNoRetry(
	ctx context.Context,
	graphPath string,
	accessToken string,
	query url.Values,
	fields map[string]string,
	files []MultipartFile,
	out any,
) error {
	_, err := c.postMultipartWithRetries(ctx, graphPath, accessToken, query, fields, files, out, 0)
	return err
}

func (c *Client) postMultipartWithRetries(
	ctx context.Context,
	graphPath string,
	accessToken string,
	query url.Values,
	fields map[string]string,
	files []MultipartFile,
	out any,
	maxRetries int,
) (ResponseMeta, error) {
	for _, file := range files {
		if file.FieldName == "" || file.FileName == "" || file.Open == nil {
			return ResponseMeta{}, errors.New("meta: multipart file requires field name, file name, and opener")
		}
	}

	return c.doWithClientRetries(ctx, c.uploadHTTPClient, maxRetries, func(ctx context.Context) (*http.Request, error) {
		target, err := c.graphURL(graphPath, accessToken, query)
		if err != nil {
			return nil, err
		}
		reader, contentType := streamMultipart(ctx, fields, files)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reader)
		if err != nil {
			_ = reader.Close()
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		return req, nil
	}, out)
}

func (c *Client) do(ctx context.Context, build func(context.Context) (*http.Request, error), out any) (ResponseMeta, error) {
	return c.doWithRetries(ctx, c.maxRetries, build, out)
}

func (c *Client) doWithRetries(
	ctx context.Context,
	maxRetries int,
	build func(context.Context) (*http.Request, error),
	out any,
) (ResponseMeta, error) {
	return c.doWithClientRetries(ctx, c.httpClient, maxRetries, build, out)
}

func (c *Client) doWithClientRetries(
	ctx context.Context,
	httpClient *http.Client,
	maxRetries int,
	build func(context.Context) (*http.Request, error),
	out any,
) (ResponseMeta, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return ResponseMeta{}, err
		}
		req, err := build(ctx)
		if err != nil {
			return ResponseMeta{}, fmt.Errorf("meta: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := httpClient.Do(req)
		if err != nil {
			inlineRetryable := retryableTransportError(err)
			durableRetryable := inlineRetryable ||
				errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded)
			lastErr = &TransportError{
				Message:  fmt.Sprintf("meta: execute request: %v", sanitizeTransportError(err, req.URL)),
				CanRetry: durableRetryable,
			}
			if attempt == maxRetries || !inlineRetryable {
				return ResponseMeta{}, lastErr
			}
			if err := c.sleep(ctx, c.retryDelay(attempt, 0)); err != nil {
				return ResponseMeta{}, err
			}
			continue
		}

		meta := responseMetadata(resp)
		body, readErr := readResponseBody(resp.Body, c.maxResponseSize)
		_ = resp.Body.Close()
		if readErr != nil {
			return meta, &ResponseError{
				Message: redactKnownSecrets(readErr.Error(), req.URL),
			}
		}

		graphErr := decodeGraphError(body, resp.StatusCode, resp.Header)
		if graphErr != nil {
			lastErr = graphErr
			if attempt < maxRetries && graphErr.Retryable() {
				if err := c.sleep(ctx, c.retryDelay(attempt, graphErr.RetryAfter)); err != nil {
					return meta, err
				}
				continue
			}
			return meta, graphErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			httpErr := &HTTPError{
				StatusCode: resp.StatusCode,
				Status:     resp.Status,
				Body:       truncateString(redactKnownSecrets(string(body), req.URL), 4096),
				RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			}
			lastErr = httpErr
			if attempt < maxRetries && httpErr.Retryable() {
				if err := c.sleep(ctx, c.retryDelay(attempt, httpErr.RetryAfter)); err != nil {
					return meta, err
				}
				continue
			}
			return meta, httpErr
		}

		if out == nil || len(bytes.TrimSpace(body)) == 0 {
			return meta, nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return meta, &ResponseError{
				Message: redactKnownSecrets(
					fmt.Sprintf("meta: decode response (status %d): %v", resp.StatusCode, err),
					req.URL,
				),
			}
		}
		return meta, nil
	}
	return ResponseMeta{}, lastErr
}

func sanitizeTransportError(err error, requestURL *url.URL) error {
	if err == nil {
		return nil
	}
	message := redactKnownSecrets(err.Error(), requestURL)
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return errors.New(message)
	}
	copy := *urlErr
	copy.URL = redactedURLString(copy.URL)
	copy.Err = errors.New(redactKnownSecrets(urlErr.Err.Error(), requestURL))
	return &copy
}

func redactedURLString(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<redacted-url>"
	}
	redactSensitiveQuery(parsed.Query(), parsed)
	return parsed.String()
}

func redactKnownSecrets(text string, requestURL *url.URL) string {
	if requestURL == nil {
		return text
	}
	for _, key := range sensitiveQueryKeys {
		for _, secret := range requestURL.Query()[key] {
			if len(secret) >= 6 {
				text = strings.ReplaceAll(text, secret, "<redacted>")
				text = strings.ReplaceAll(text, url.QueryEscape(secret), "%3Credacted%3E")
			}
		}
	}
	return text
}

var sensitiveQueryKeys = []string{
	"access_token",
	"appsecret_proof",
	"client_secret",
	"code",
	"fb_exchange_token",
	"input_token",
}

func redactSensitiveQuery(values url.Values, target *url.URL) {
	for _, key := range sensitiveQueryKeys {
		if values.Has(key) {
			values.Set(key, "<redacted>")
		}
	}
	target.RawQuery = values.Encode()
}

func (c *Client) graphURL(graphPath, accessToken string, query url.Values) (string, error) {
	var target *url.URL
	parsed, err := url.Parse(graphPath)
	if err != nil {
		return "", fmt.Errorf("meta: parse Graph path: %w", err)
	}
	if parsed.IsAbs() {
		if !sameHost(parsed, c.baseURL) {
			return "", fmt.Errorf("meta: refusing paging URL for unexpected host %q", parsed.Host)
		}
		target = parsed
	} else {
		target = cloneURL(c.baseURL)
		target.Path = path.Join(strings.TrimSuffix(target.Path, "/"), c.apiVersion, strings.TrimPrefix(parsed.Path, "/"))
		target.RawQuery = parsed.RawQuery
	}

	values := target.Query()
	for key, entries := range query {
		values.Del(key)
		for _, value := range entries {
			values.Add(key, value)
		}
	}
	// Never trust the token or proof embedded in a paging URL. Refresh both
	// from the caller's current credential on every request.
	values.Del("access_token")
	values.Del("appsecret_proof")
	if accessToken != "" {
		values.Set("access_token", accessToken)
		values.Set("appsecret_proof", c.AppSecretProof(accessToken))
	}
	target.RawQuery = values.Encode()
	return target.String(), nil
}

func (c *Client) retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		// Honor Meta's explicit backoff. The request context remains the
		// caller-controlled upper bound.
		return retryAfter
	}
	exponential := float64(c.baseRetryDelay) * math.Pow(2, float64(attempt))
	if exponential > float64(c.maxRetryDelay) {
		exponential = float64(c.maxRetryDelay)
	}
	// Full jitter avoids synchronized retries when hundreds of ad accounts are
	// published at once.
	return time.Duration(rand.Float64() * exponential)
}

func streamMultipart(ctx context.Context, fields map[string]string, files []MultipartFile) (io.ReadCloser, string) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()

	go func() {
		var writeErr error
		defer func() {
			if closeErr := multipartWriter.Close(); writeErr == nil {
				writeErr = closeErr
			}
			_ = writer.CloseWithError(writeErr)
		}()
		for key, value := range fields {
			if writeErr = multipartWriter.WriteField(key, value); writeErr != nil {
				return
			}
		}
		for _, file := range files {
			if err := ctx.Err(); err != nil {
				writeErr = err
				return
			}
			source, err := file.Open()
			if err != nil {
				writeErr = err
				return
			}
			var part io.Writer
			if file.ContentType == "" {
				part, err = multipartWriter.CreateFormFile(file.FieldName, file.FileName)
			} else {
				part, err = createMultipartPart(multipartWriter, file)
			}
			if err == nil {
				_, err = io.Copy(part, source)
			}
			closeErr := source.Close()
			if err != nil {
				writeErr = err
				return
			}
			if closeErr != nil {
				writeErr = closeErr
				return
			}
		}
	}()
	return reader, contentType
}

func createMultipartPart(writer *multipart.Writer, file MultipartFile) (io.Writer, error) {
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{fmt.Sprintf(
		`form-data; name=%q; filename=%q`,
		escapeQuotes(file.FieldName),
		escapeQuotes(file.FileName),
	)}
	header["Content-Type"] = []string{file.ContentType}
	return writer.CreatePart(header)
}

func escapeQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}

func readResponseBody(body io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, fmt.Errorf("meta: read response: %w", err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("meta: response exceeds %d bytes", max)
	}
	return data, nil
}

func responseMetadata(resp *http.Response) ResponseMeta {
	meta := ResponseMeta{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("X-FB-Request-ID"),
		TraceID:    resp.Header.Get("X-FB-Trace-ID"),
	}
	decodeUsageHeader(resp.Header.Get("X-App-Usage"), &meta.AppUsage)
	decodeUsageHeader(resp.Header.Get("X-Ad-Account-Usage"), &meta.AdAccountUsage)
	decodeUsageHeader(resp.Header.Get("X-Business-Use-Case-Usage"), &meta.BusinessUsage)
	return meta
}

func decodeUsageHeader(raw string, target *map[string]json.RawMessage) {
	if raw == "" {
		return
	}
	_ = json.Unmarshal([]byte(raw), target)
}

func retryableTransportError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return 0
}

func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func cloneURL(source *url.URL) *url.URL {
	clone := *source
	return &clone
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
