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
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
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
