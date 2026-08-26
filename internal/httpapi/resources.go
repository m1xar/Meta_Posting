package httpapi

import (
	"errors"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/valyala/fasthttp"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

func (s *Server) health(c fiber.Ctx) error {
	return jsonOK(c, http.StatusOK, fiber.Map{
		"status": "ok",
		"time":   time.Now().UTC(),
	})
}

func (s *Server) readiness(c fiber.Ctx) error {
	if s.ready != nil {
		if err := s.ready(c.Context()); err != nil {
			s.logger.Warn("readiness check failed", "request_id", getRequestID(c), "error", err)
			return fiber.ErrServiceUnavailable
		}
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"status": "ready"})
}

func (s *Server) openAPIDocument(c fiber.Ctx) error {
	if len(s.openAPI) == 0 {
		return fiber.ErrNotFound
	}
	c.Set("Content-Type", "application/yaml; charset=utf-8")
	c.Set("Cache-Control", "public, max-age=300")
	return c.Send(s.openAPI)
}

func (s *Server) oauthCallback(c fiber.Ctx) error {
	if metaError := strings.TrimSpace(c.Query("error")); metaError != "" {
		description := strings.TrimSpace(c.Query("error_description"))
		if description == "" {
			description = "Meta authorization was not completed"
		}
		return c.Status(http.StatusBadRequest).JSON(errorEnvelope{Error: errorBody{
			Code:      "oauth_denied",
			Message:   description,
			RequestID: getRequestID(c),
		}})
	}
	completion, err := s.service.CompleteOAuth(c.Context(), strings.TrimSpace(c.Query("state")), strings.TrimSpace(c.Query("code")))
	if err != nil {
		return err
	}
	if strings.Contains(c.Get("Accept"), "text/html") {
		return c.Redirect().To("/app?meta=connected")
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"status":     "connected",
		"connection": completion,
	})
}

// startOAuthRedirect starts the browser-facing Meta connection flow.
// The API endpoint remains available for programmatic clients; this route is
// intended for an operator who opens the product in a normal browser.
func (s *Server) startOAuthRedirect(c fiber.Ctx) error {
	userID, err := oauthTargetUser(c, uuid.Nil)
	if err != nil {
		return err
	}
	result, err := s.service.StartOAuthForUser(c.Context(), userID)
	if err != nil {
		return err
	}
	c.Set("Cache-Control", "no-store")
	return c.Redirect().To(result.AuthorizationURL)
}

func (s *Server) startOAuth(c fiber.Ctx) error {
	// An admin may start a connection on behalf of a named user; anyone else
	// connects for themselves.
	var body struct {
		UserID string `json:"user_id,omitempty"`
	}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	requested := uuid.Nil
	if trimmed := strings.TrimSpace(body.UserID); trimmed != "" {
		parsed, parseErr := uuid.Parse(trimmed)
		if parseErr != nil {
			return invalidField("user_id", "must be a UUID")
		}
		requested = parsed
	}
	userID, err := oauthTargetUser(c, requested)
	if err != nil {
		return err
	}
	result, err := s.service.StartOAuthForUser(c.Context(), userID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, result)
}

// oauthTargetUser decides which tenant a new Meta connection will belong to.
//
// Previously this was hardcoded to the disabled legacy tenant, so every
// connection created through /v1 was owned by an account nobody could log
// into. A connection must have a real owner, which means the shared internal
// token - which has no tenant identity - can no longer start one without
// naming the user explicitly.
func oauthTargetUser(c fiber.Ctx, requested uuid.UUID) (uuid.UUID, error) {
	principal, err := currentPrincipal(c)
	if err != nil {
		return uuid.Nil, err
	}
	if requested != uuid.Nil {
		if !principal.IsAdmin() {
			return uuid.Nil, application.ErrForbidden
		}
		return requested, nil
	}
	if !principal.HasTenant() {
		return uuid.Nil, invalidField("user_id",
			"is required when authenticating with the shared internal token")
	}
	return principal.UserID, nil
}

func (s *Server) listConnections(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	return s.respondConnections(c, scope)
}

func (s *Server) respondConnections(c fiber.Ctx, scope database.Scope) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	filter := database.MetaConnectionFilter{
		Scope:  scope,
		Search: strings.TrimSpace(c.Query("search")),
		Page:   domain.PageRequest{Limit: limit, Offset: offset},
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		status := domain.MetaConnectionStatus(raw)
		switch status {
		case domain.MetaConnectionActive, domain.MetaConnectionExpired, domain.MetaConnectionRevoked,
			domain.MetaConnectionDisconnected, domain.MetaConnectionError:
			filter.Status = &status
		default:
			return invalidField("status", "is not a supported connection status")
		}
	}
	page, err := s.service.Repos.MetaConnections.List(c.Context(), filter)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) getConnection(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	item, err := s.service.Repos.MetaConnections.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func (s *Server) revokeConnection(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.RevokeConnection(c.Context(), id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) syncConnection(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	var body struct{}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	job, err := s.service.EnqueueConnectionSync(c.Context(), id, getRequestID(c))
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusAccepted, job)
}

func (s *Server) listAdAccounts(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	return s.respondAdAccounts(c, scope)
}

func (s *Server) respondAdAccounts(c fiber.Ctx, scope database.Scope) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	activeOnly := true
	if raw := strings.TrimSpace(c.Query("active_only")); raw != "" {
		switch raw {
		case "true":
			activeOnly = true
		case "false":
			activeOnly = false
		default:
			return invalidField("active_only", "must be true or false")
		}
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	businessID, err := optionalID(c, "business_id")
	if err != nil {
		return err
	}
	accountStatus, err := parseIntQuery(c, "account_status")
	if err != nil {
		return err
	}
	page, err := s.service.Repos.Inventory.ListAdAccounts(c.Context(), database.AdAccountFilter{
		Scope:         scope,
		ConnectionID:  connectionID,
		BusinessID:    businessID,
		AccountStatus: accountStatus,
		Currency:      strings.TrimSpace(c.Query("currency")),
		ActiveOnly:    activeOnly,
		Search:        strings.TrimSpace(c.Query("search")),
		Page:          domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) auditAdAccount(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	limit, _, err := pageRequest(c)
	if err != nil {
		return err
	}
	statuses := splitQuery(c, "effective_status")
	if len(statuses) == 0 {
		statuses = []string{"ACTIVE"}
	}
	result, err := s.service.AuditAdAccount(c.Context(), id, statuses, limit)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, result)
}

func (s *Server) listAssets(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	if connectionID == nil {
		return invalidField("connection_id", "is required")
	}
	businessID, err := optionalID(c, "business_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	types := make([]domain.AssetType, 0)
	for _, raw := range splitQuery(c, "types") {
		value := domain.AssetType(raw)
		switch value {
		case domain.AssetPage, domain.AssetInstagramAccount, domain.AssetPixel, domain.AssetDataset,
			domain.AssetCustomConversion, domain.AssetCustomAudience, domain.AssetLookalikeAudience,
			domain.AssetMetaApp, domain.AssetPost:
			types = append(types, value)
		default:
			return invalidField("types", "contains an unsupported asset type")
		}
	}
	if len(types) == 0 {
		types = []domain.AssetType{
			domain.AssetPage, domain.AssetInstagramAccount, domain.AssetPixel, domain.AssetDataset,
			domain.AssetCustomConversion, domain.AssetCustomAudience, domain.AssetLookalikeAudience,
			domain.AssetMetaApp, domain.AssetPost,
		}
	}
	activeOnly := true
	if raw := strings.TrimSpace(c.Query("active_only")); raw != "" {
		switch raw {
		case "true":
			activeOnly = true
		case "false":
			activeOnly = false
		default:
			return invalidField("active_only", "must be true or false")
		}
	}
	page, err := s.service.Repos.Inventory.ListAssets(c.Context(), database.AssetFilter{
		ConnectionID: *connectionID,
		BusinessID:   businessID,
		AdAccountID:  adAccountID,
		Types:        types,
		ActiveOnly:   activeOnly,
		Search:       strings.TrimSpace(c.Query("search")),
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) auditPagePosts(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	limit, _, err := pageRequest(c)
	if err != nil {
		return err
	}
	posts, err := s.service.AuditPagePosts(c.Context(), id, limit)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"items": posts, "total": len(posts)})
}

func (s *Server) createMedia(c fiber.Ctx) error {
	contentType, _, err := mimeMediaType(c.Get("Content-Type"))
	if err != nil || contentType != "multipart/form-data" {
		return fiber.NewError(http.StatusUnsupportedMediaType, "Content-Type must be multipart/form-data")
	}
	if encoding := strings.TrimSpace(c.Get("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return fiber.NewError(http.StatusUnsupportedMediaType, "compressed multipart request bodies are not supported")
	}
	if contentLength := c.Request().Header.ContentLength(); contentLength > s.bodyLimit {
		return fiber.ErrRequestEntityTooLarge
	}
	form, err := c.MultipartForm()
	if err != nil {
		if errors.Is(err, fasthttp.ErrBodyTooLarge) {
			return fiber.ErrRequestEntityTooLarge
		}
		return &application.ValidationError{Message: "invalid multipart body"}
	}
	connectionID, err := optionalMultipartID(form, "connection_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalMultipartID(form, "ad_account_id")
	if err != nil {
		return err
	}
	if err := userOwnsMediaContext(c, connectionID, adAccountID, s.service); err != nil {
		return err
	}
	kind := domain.MediaKind(strings.TrimSpace(firstMultipartValue(form, "kind")))
	switch kind {
	case domain.MediaImage, domain.MediaVideo:
	default:
		return invalidField("kind", "must be image or video")
	}
	files := form.File["file"]
	if len(files) == 0 || files[0] == nil {
		return invalidField("file", "is required")
	}
	header := files[0]
	source, err := header.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	item, err := s.service.SaveMedia(c.Context(), connectionID, adAccountID, kind, header.Filename, header.Header.Get("Content-Type"), source)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, item)
}

func (s *Server) listMedia(c fiber.Ctx) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	connectionID, err := optionalID(c, "connection_id")
	if err != nil {
		return err
	}
	adAccountID, err := optionalID(c, "ad_account_id")
	if err != nil {
		return err
	}
	var kind *domain.MediaKind
	if raw := strings.TrimSpace(c.Query("kind")); raw != "" {
		value := domain.MediaKind(raw)
		if value != domain.MediaImage && value != domain.MediaVideo {
			return invalidField("kind", "must be image or video")
		}
		kind = &value
	}
	var status *domain.MediaStatus
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		value := domain.MediaStatus(raw)
		switch value {
		case domain.MediaPending, domain.MediaProcessing, domain.MediaReady, domain.MediaFailed:
			status = &value
		default:
			return invalidField("status", "is not a supported media status")
		}
	}
	page, err := s.service.Repos.Media.List(c.Context(), database.MediaFilter{
		ConnectionID: connectionID,
		AdAccountID:  adAccountID,
		Kind:         kind,
		Status:       status,
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) getMedia(c fiber.Ctx) error {
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	item, err := s.service.Repos.Media.Get(c.Context(), id)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, item)
}

func optionalMultipartID(form *multipart.Form, name string) (*uuid.UUID, error) {
	raw := strings.TrimSpace(firstMultipartValue(form, name))
	if raw == "" {
		return nil, nil
	}
	id, err := parseID(raw, name)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func firstMultipartValue(form *multipart.Form, name string) string {
	if form == nil {
		return ""
	}
	values := form.Value[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func mimeMediaType(value string) (string, map[string]string, error) {
	// Kept behind a helper so handlers do not accidentally accept a prefix
	// match such as application/json-malformed.
	return mime.ParseMediaType(value)
}
