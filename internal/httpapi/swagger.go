package httpapi

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
)

const swaggerUIPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="referrer" content="no-referrer">
  <title>Raze Posting API — Swagger</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.32.11/swagger-ui.css">
  <style>
    html { box-sizing: border-box; overflow-y: scroll; }
    *, *::before, *::after { box-sizing: inherit; }
    body { margin: 0; background: #f6f7f8; }
    .swagger-ui .topbar { background: #12191f; }
    .swagger-ui .topbar .download-url-wrapper { display: none; }
    .swagger-ui .info .title small { vertical-align: middle; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.32.11/swagger-ui-bundle.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.32.11/swagger-ui-standalone-preset.js"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "/openapi.yaml",
        dom_id: "#swagger-ui",
        deepLinking: true,
        displayRequestDuration: true,
        filter: true,
        persistAuthorization: true,
        presets: [
          SwaggerUIBundle.presets.apis,
          SwaggerUIStandalonePreset
        ],
        layout: "StandaloneLayout"
      });
    };
  </script>
</body>
</html>
`

func (s *Server) swaggerUI(c fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Content-Security-Policy",
		"default-src 'none'; "+
			"connect-src 'self'; "+
			"img-src 'self' data: https://validator.swagger.io; "+
			"script-src 'unsafe-inline' https://cdn.jsdelivr.net; "+
			"style-src 'unsafe-inline' https://cdn.jsdelivr.net; "+
			"font-src https://cdn.jsdelivr.net; "+
			"base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
	)
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Frame-Options", "DENY")
	return c.Status(http.StatusOK).SendString(swaggerUIPage)
}
