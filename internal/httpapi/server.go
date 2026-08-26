package httpapi

import (
	"bytes"
	"context"
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
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"github.com/watchers-factory/raze-posting/internal/storage"
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
	Environment string
	OpenAPI     []byte
	Ready       func(context.Context) error
	Logger      *slog.Logger
	BodyLimit   int
}

type Server struct {
	App           *fiber.App
	service       *application.Service
	openAPI       []byte
	ready         func(context.Context) error
	logger        *slog.Logger
	bodyLimit     int
	secureCookies bool
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
	if cfg.BodyLimit <= 0 {
		cfg.BodyLimit = defaultBodyLimit
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	server := &Server{
		service:       service,
		openAPI:       append([]byte(nil), cfg.OpenAPI...),
		ready:         cfg.Ready,
		logger:        cfg.Logger,
		bodyLimit:     cfg.BodyLimit,
		secureCookies: !strings.EqualFold(cfg.Environment, "development"),
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
	s.App.Get("/static/app.css", s.staticAsset("webui/app.css", "text/css; charset=utf-8"))
	s.App.Get("/static/app.js", s.staticAsset("webui/app.js", "text/javascript; charset=utf-8"))
	s.App.Get("/login", s.loginPage)
	s.App.Get("/register", s.registerPage)
	s.App.Post("/auth/login", s.loginUser)
	s.App.Post("/auth/register", s.registerUser)
	s.App.Post("/auth/logout", s.requireUser, s.requireCSRF, s.logoutUser)

	s.App.Get("/app", s.requireUser, s.dashboardPage)
	s.App.Get("/app/launch", s.requireUser, s.launcherPage)
	s.App.Get("/app/campaigns", s.requireUser, s.campaignsPage)
	s.App.Get("/app/accounts/:id", s.requireUser, s.accountPage)
	s.App.Get("/app/connect/meta", s.requireUser, s.startUserOAuthRedirect)

	api := s.App.Group("/app/api", s.requireUser)
	api.Get("/overview", s.appOverview)
	api.Get("/launcher", s.launcherData)
	api.Get("/campaigns", s.listUserCampaigns)
	api.Get("/accounts/:id/stats", s.accountStats)
	api.Get("/batches/:id", s.getUserBatch)
	api.Get("/capabilities", s.userCapabilities)
	api.Post("/connections/:id/sync", s.requireCSRF, s.syncUserConnection)
	api.Delete("/connections/:id", s.requireCSRF, s.revokeUserConnection)
	api.Post("/batches", s.requireCSRF, s.createUserBatch)
	api.Patch("/guards/:id", s.requireCSRF, s.updateUserGuard)
	api.Post("/campaigns/:id/guard", s.requireCSRF, s.createCampaignGuard)
	api.Post("/campaigns/:id/pause", s.requireCSRF, s.pauseUserCampaign)
	api.Post("/campaigns/:id/resume", s.requireCSRF, s.resumeUserCampaign)
	api.Post("/media", s.requireCSRF, s.createMedia)
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
		status, code, message = http.StatusUnauthorized, "unauthorized", "sign in to continue"
	case errors.Is(err, application.ErrInvalidCredentials):
		status, code, message = http.StatusUnauthorized, "invalid_credentials", "invalid login or password"
	case errors.Is(err, application.ErrSessionExpired):
		status, code, message = http.StatusUnauthorized, "session_expired", "sign in to continue"
	case errors.Is(err, database.ErrOAuthSessionUnavailable):
		status, code, message = http.StatusBadRequest, "oauth_session_unavailable", "OAuth session is missing, expired, or already used"
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code, message = http.StatusNotFound, "not_found", "resource not found"
	case errors.Is(err, gorm.ErrDuplicatedKey), errors.Is(err, application.ErrConflict):
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
