package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/meta"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
	"github.com/watchers-factory/raze-ads/internal/storage"
	"gorm.io/gorm"
)

const (
	defaultBodyLimit         = 1 << 30 // one GiB, including multipart overhead
	maxJSONBodyBytes         = 16 << 20
	preAuthBodyPrefetchBytes = 1
	requestIDLocal           = "request_id"
	userSessionLocal         = "user_session"
	userSessionCookie        = "raze_session"
	userCSRFCookie           = "raze_csrf"
)

type Config struct {
	InternalToken []byte
	Environment   string
	OpenAPI       []byte
	Ready         func(context.Context) error
	Logger        *slog.Logger
	BodyLimit     int

	// AllowLegacyInternalToken keeps the shared bearer token accepted as an
	// admin credential. Off by default; it reads every tenant and owns
	// nothing, so it exists only as a migration path to per-user API keys.
	AllowLegacyInternalToken bool
}

type Server struct {
	App           *fiber.App
	service       *application.Service
	token         [sha256.Size]byte
	openAPI       []byte
	ready         func(context.Context) error
	logger        *slog.Logger
	bodyLimit     int
	secureCookies bool

	allowLegacyInternalToken bool
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

func New(service *application.Service, cfg Config) (*Server, error) {
	if service == nil || service.Repos == nil {
		return nil, errors.New("httpapi: application service is required")
	}
	if len(cfg.InternalToken) == 0 {
		return nil, errors.New("httpapi: internal API token is required")
	}
	if cfg.BodyLimit <= 0 {
		cfg.BodyLimit = defaultBodyLimit
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	server := &Server{
		service:       service,
		token:         sha256.Sum256(cfg.InternalToken),
		openAPI:       append([]byte(nil), cfg.OpenAPI...),
		ready:         cfg.Ready,
		logger:        cfg.Logger,
		bodyLimit:     cfg.BodyLimit,
		secureCookies: !strings.EqualFold(cfg.Environment, "development"),

		allowLegacyInternalToken: cfg.AllowLegacyInternalToken,
	}
	server.App = fiber.New(fiber.Config{
		AppName:                      "Raze Posting API",
		BodyLimit:                    cfg.BodyLimit,
		DisablePreParseMultipartForm: true,
		ErrorHandler:                 server.handleError,
		Immutable:                    true,
		StreamRequestBody:            true,
	})
	// StreamRequestBody lets Fiber route and authenticate a request before the
	// whole body is read. fasthttp otherwise prefetches up to BodyLimit before
	// invoking middleware; keep that pre-authentication read to one byte while
	// retaining BodyLimit for the explicit, authenticated parsers below.
	server.App.Server().HeaderReceived = func(*fasthttp.RequestHeader) fasthttp.RequestConfig {
		return fasthttp.RequestConfig{MaxRequestBodySize: preAuthBodyPrefetchBytes}
	}
	server.App.Use(recover.New())
	server.App.Use(server.requestID)
	server.App.Use(server.closeConnectionOnBodyError)
	server.routes()
	return server, nil
}

func (s *Server) routes() {
	s.App.Get("/healthz", s.health)
	s.App.Get("/readyz", s.readiness)
	s.App.Get("/docs", s.swaggerUI)
	s.App.Get("/docs/", s.swaggerUI)
	s.App.Get("/swagger", s.swaggerUI)
	s.App.Get("/swagger/", s.swaggerUI)
	s.App.Get("/openapi.yaml", s.openAPIDocument)
	s.App.Get("/", s.landingPage)
	s.App.Get("/privacy", s.privacyPolicy)
	s.App.Get("/terms", s.termsOfService)
	s.App.Get("/data-deletion", s.dataDeletion)
	s.App.Get("/oauth/facebook/callback", s.oauthCallback)

	// Operator workspace. The shell is public because it holds no data: it
	// asks /v1/me who it is talking to and renders the sign-in gate when the
	// answer is 401.
	s.App.Get("/app", s.workspacePage)
	s.App.Get("/app/", s.workspacePage)
	s.App.Get("/static/*", workspaceAssets)
	s.App.Post("/auth/login", s.loginUser)
	s.App.Post("/auth/register", s.registerUser)
	s.App.Post("/auth/logout", s.requireUser, s.requireCSRF, s.logoutUser)
	s.App.Get("/app/api/overview", s.requireUser, s.appOverview)
	s.App.Get("/app/connect/meta", s.requireUser, s.startUserOAuthRedirect)
	s.App.Post("/app/api/connections/:id/sync", s.requireUser, s.requireCSRF, s.syncUserConnection)
	s.App.Post("/app/api/batches", s.requireUser, s.requireCSRF, s.createUserBatch)
	s.App.Patch("/app/api/guards/:id", s.requireUser, s.requireCSRF, s.updateUserGuard)
	s.App.Post("/app/api/media", s.requireUser, s.requireCSRF, s.createMedia)

	// /v1 is now user-scoped: a session cookie or a per-user API key both
	// produce a Principal, and every list query is restricted to that
	// principal's tenant. Cross-tenant reads live under /v1/admin.
	v1 := s.App.Group("/v1", s.authenticatePrincipal, s.requireCSRFForSessions)
	v1.Post("/oauth/sessions", s.startOAuth)
	v1.Get("/connections", s.listConnections)
	v1.Get("/connections/:id", s.getConnection)
	v1.Delete("/connections/:id", s.revokeConnection)
	v1.Post("/connections/:id/sync", s.syncConnection)
	v1.Get("/ad-accounts", s.listAdAccounts)
	v1.Get("/ad-accounts/:id/campaign-audit", s.auditAdAccount)
	v1.Get("/assets", s.listAssets)
	v1.Get("/assets/:id/posts", s.auditPagePosts)
	v1.Post("/media", s.createMedia)
	v1.Get("/media", s.listMedia)
	v1.Get("/media/:id", s.getMedia)
	v1.Post("/batches", s.createBatch)
	v1.Get("/batches", s.listBatches)
	v1.Get("/batches/:id", s.getBatch)
	v1.Get("/batches/:id/results", s.listBatchResults)
	v1.Get("/published-objects", s.listPublishedObjects)
	v1.Get("/guards", s.listGuards)
	v1.Get("/guards/:id", s.getGuard)
	v1.Patch("/guards/:id", s.updateGuard)
	v1.Post("/guards/:id/enable", s.enableGuard)
	v1.Post("/guards/:id/disable", s.disableGuard)
	v1.Get("/campaigns", s.listCampaignViews)
	v1.Post("/campaigns/:id/pause", s.pauseCampaign)
	v1.Post("/campaigns/:id/resume", s.resumeCampaign)
	v1.Post("/campaigns/:id/guard", s.createCampaignGuard)
	v1.Post("/campaigns/:id/duplicate", s.duplicateCampaign)
	v1.Get("/ad-accounts/:id/stats", s.accountStats)
	v1.Get("/insights", s.listInsights)
	v1.Get("/jobs", s.listJobs)
	v1.Get("/jobs/:id", s.getJob)
	v1.Get("/capabilities", s.capabilities)
	v1.Get("/me", s.currentUser)
	v1.Get("/launch/accounts", s.listLaunchAccounts)
	v1.Get("/launch/templates", s.listLaunchTemplates)
	v1.Get("/launch/templates/:id", s.getLaunchTemplate)
	v1.Post("/sync/refresh", s.syncRefresh)
	v1.Get("/sync/status", s.syncStatus)
	v1.Post("/launch/preview", s.previewLaunch)
	v1.Post("/launch", s.launch)
	v1.Post("/batches/:id/stop", s.stopBatch)
	v1.Get("/ad-entities", s.listAdEntities)
	v1.Get("/insights/daily", s.listDailyInsights)
	v1.Post("/api-keys", s.createAPIKey)
	v1.Get("/api-keys", s.listAPIKeys)
	v1.Delete("/api-keys/:id", s.revokeAPIKey)

	// Cross-tenant. requireAdmin gates the group, and each handler builds an
	// explicit AdminScope rather than inheriting one.
	admin := v1.Group("/admin", s.requireAdmin)
	admin.Get("/users", s.adminListUsers)
	admin.Get("/connections", s.adminListConnections)
	admin.Get("/ad-accounts", s.adminListAdAccounts)
	admin.Get("/insights/daily", s.adminListDailyInsights)
	admin.Get("/rate-limits", s.adminRateLimits)
}

func (s *Server) requestID(c fiber.Ctx) error {
	requestID := strings.TrimSpace(c.Get("X-Request-ID"))
	if len(requestID) > 128 || !isSafeRequestID(requestID) {
		requestID = ""
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	c.Locals(requestIDLocal, requestID)
	c.Set("X-Request-ID", requestID)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "no-store")
	return c.Next()
}

func isSafeRequestID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("-_.:/", character) {
			continue
		}
		return false
	}
	return true
}

func (s *Server) authenticate(c fiber.Ctx) error {
	header := strings.TrimSpace(c.Get("Authorization"))
	scheme, credential, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return application.ErrUnauthorized
	}
	credential = strings.TrimSpace(credential)
	provided := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(provided[:], s.token[:]) != 1 {
		return application.ErrUnauthorized
	}
	return c.Next()
}

func (s *Server) closeConnectionOnBodyError(c fiber.Ctx) error {
	err := c.Next()
	if err != nil && requestHasBody(c.Request()) {
		// A streaming parser may deliberately stop at the first error. Closing
		// prevents unread body bytes from being interpreted as another request
		// on the same HTTP/1.1 connection.
		c.Response().Header.SetConnectionClose()
	}
	return err
}

func requestHasBody(request *fasthttp.Request) bool {
	contentLength := request.Header.ContentLength()
	return contentLength > 0 || contentLength == -1
}

func (s *Server) handleError(c fiber.Ctx, err error) error {
	status, code, message := http.StatusInternalServerError, "internal_error", "internal server error"
	details := map[string]any(nil)

	var validation *application.ValidationError
	var fiberError *fiber.Error
	var graphError *meta.GraphError
	var metaHTTPError *meta.HTTPError
	switch {
	case errors.As(err, &validation):
		status, code, message = http.StatusBadRequest, "invalid_request", validation.Error()
		details = map[string]any{"field": validation.Field}
	case errors.Is(err, application.ErrUnauthorized):
		status, code, message = http.StatusUnauthorized, "unauthorized", "valid bearer credentials are required"
		c.Set("WWW-Authenticate", `Bearer realm="raze-posting"`)
	case errors.Is(err, application.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "invalid credentials"
	case errors.Is(err, application.ErrAccountDisabled):
		status, code, message = http.StatusForbidden, "account_disabled", "this account is disabled"
	case errors.Is(err, application.ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", "this action requires elevated privileges"
	case errors.Is(err, application.ErrSessionExpired):
		status, code, message = http.StatusUnauthorized, "session_expired", "sign in to continue"
	case errors.Is(err, database.ErrOAuthSessionUnavailable):
		status, code, message = http.StatusBadRequest, "oauth_session_unavailable", "OAuth session is missing, expired, or already used"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "not_found", "resource not found"
	case errors.Is(err, application.ErrConflict):
		// The application's own conflicts carry a caller-safe message; the
		// bare ORM error does not and must stay generic. Strip the sentinel
		// prefix so the caller sees the message, not the wrapping.
		status, code, message = http.StatusConflict, "conflict",
			strings.TrimPrefix(err.Error(), application.ErrConflict.Error()+": ")
	case errors.Is(err, gorm.ErrDuplicatedKey):
		status, code, message = http.StatusConflict, "conflict", "resource conflicts with an existing record"
	case errors.Is(err, storage.ErrFileTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "file_too_large", "uploaded file exceeds the 1 GiB limit"
	case errors.Is(err, context.DeadlineExceeded):
		status, code, message = http.StatusGatewayTimeout, "upstream_timeout", "an upstream request timed out"
	case errors.As(err, &graphError):
		status, code, message = http.StatusBadGateway, "meta_api_error", "Meta rejected the request"
		if graphError.ErrorUserMsg != "" {
			message = graphError.ErrorUserMsg
		}
		details = map[string]any{
			"meta_code":      graphError.Code,
			"meta_subcode":   graphError.ErrorSubcode,
			"meta_transient": graphError.IsTransient,
		}
	case errors.As(err, &metaHTTPError):
		status, code, message = http.StatusBadGateway, "meta_upstream_error", "Meta returned an invalid upstream response"
	case errors.As(err, &fiberError):
		status, code = fiberError.Code, strings.ToLower(strings.ReplaceAll(http.StatusText(fiberError.Code), " ", "_"))
		message = http.StatusText(fiberError.Code)
		if message == "" {
			message = "request failed"
		}
	}

	requestID := getRequestID(c)
	if status >= 500 {
		s.logger.Error("HTTP request failed",
			"request_id", requestID,
			"method", c.Method(),
			"path", c.Path(),
			"error", err,
		)
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	return c.Status(status).JSON(errorEnvelope{Error: errorBody{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Details:   details,
	}})
}

func getRequestID(c fiber.Ctx) string {
	if value, ok := c.Locals(requestIDLocal).(string); ok {
		return value
	}
	return ""
}

func decodeJSON(c fiber.Ctx, target any) error {
	return decodeJSONBody(c, target, true)
}

func decodeOptionalJSON(c fiber.Ctx, target any) error {
	return decodeJSONBody(c, target, false)
}

func decodeJSONBody(c fiber.Ctx, target any, required bool) error {
	contentLength := c.Request().Header.ContentLength()
	if !required && !requestHasBody(c.Request()) {
		return nil
	}
	contentType, _, err := mime.ParseMediaType(c.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return fiber.NewError(http.StatusUnsupportedMediaType, "Content-Type must be application/json")
	}
	if encoding := strings.TrimSpace(c.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return fiber.NewError(http.StatusUnsupportedMediaType, "compressed JSON request bodies are not supported")
	}
	if contentLength > maxJSONBodyBytes {
		return fiber.ErrRequestEntityTooLarge
	}

	var reader io.Reader
	if stream := c.RequestCtx().RequestBodyStream(); stream != nil {
		reader = stream
	} else {
		reader = bytes.NewReader(c.Request().Body())
	}
	limited := &io.LimitedReader{R: reader, N: maxJSONBodyBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if limited.N <= 0 {
			return fiber.ErrRequestEntityTooLarge
		}
		if errors.Is(err, io.EOF) && !required {
			return nil
		}
		if errors.Is(err, io.EOF) {
			return &application.ValidationError{Message: "request body is required"}
		}
		return &application.ValidationError{Message: "invalid JSON body: " + err.Error()}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if limited.N <= 0 {
			return fiber.ErrRequestEntityTooLarge
		}
		if err == nil {
			return &application.ValidationError{Message: "request body must contain one JSON value"}
		}
		return &application.ValidationError{Message: "invalid trailing JSON: " + err.Error()}
	}
	if limited.N <= 0 {
		return fiber.ErrRequestEntityTooLarge
	}
	return nil
}

func parseID(value, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, &application.ValidationError{Field: field, Message: "must be a UUID"}
	}
	return id, nil
}

func optionalID(c fiber.Ctx, name string) (*uuid.UUID, error) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil, nil
	}
	id, err := parseID(value, name)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func pageRequest(c fiber.Ctx) (limit, offset int, err error) {
	limit = 50
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			return 0, 0, &application.ValidationError{Field: "limit", Message: "must be between 1 and 500"}
		}
	}
	if raw := strings.TrimSpace(c.Query("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, &application.ValidationError{Field: "offset", Message: "must be a non-negative integer"}
		}
	}
	return limit, offset, nil
}

func splitQuery(c fiber.Ctx, name string) []string {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil
	}
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parseTimeQuery(c fiber.Ctx, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, &application.ValidationError{Field: name, Message: "must be RFC3339"}
	}
	value = value.UTC()
	return &value, nil
}

func parseIntQuery(c fiber.Ctx, name string) (*int, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, &application.ValidationError{Field: name, Message: "must be an integer"}
	}
	return &value, nil
}

func jsonOK(c fiber.Ctx, status int, value any) error {
	c.Set("Cache-Control", "no-store")
	return c.Status(status).JSON(value)
}

func noContent(c fiber.Ctx) error {
	return c.SendStatus(http.StatusNoContent)
}

func invalidField(field, message string) error {
	return &application.ValidationError{Field: field, Message: message}
}

func wrapInvalid(field string, err error) error {
	return invalidField(field, fmt.Sprintf("%v", err))
}
