package httpapi

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/watchers-factory/raze-ads/web"
)

// workspaceAssets serves the JS and CSS.
//
// Assets live under /static/ rather than /app/ so they cannot collide with
// the /app/api/* and /app/connect/* routes that predate this workspace.
func workspaceAssets(c fiber.Ctx) error {
	name := strings.TrimPrefix(c.Path(), "/static/")
	if name == "" || strings.Contains(name, "..") {
		return fiber.ErrNotFound
	}
	data, err := fs.ReadFile(web.Files, name)
	if err != nil {
		return fiber.ErrNotFound
	}
	switch {
	case strings.HasSuffix(name, ".js"):
		c.Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		c.Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".svg"):
		c.Set("Content-Type", "image/svg+xml")
	default:
		c.Set("Content-Type", "application/octet-stream")
	}
	// Hashed filenames are not in use, so revalidation keeps a deploy from
	// serving a stale module against a new API.
	c.Set("Cache-Control", "no-cache")
	return c.Status(http.StatusOK).Send(data)
}

// workspacePage serves the shell for every workspace route.
//
// It is deliberately unauthenticated: the page is an empty container that
// asks /v1/me who it is talking to, and every byte of data behind it is
// gated by the API. Gating the shell itself would only add a redirect.
func (s *Server) workspacePage(c fiber.Ctx) error {
	data, err := fs.ReadFile(web.Files, "index.html")
	if err != nil {
		return err
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Cache-Control", "no-cache")
	return c.Status(http.StatusOK).Send(data)
}
