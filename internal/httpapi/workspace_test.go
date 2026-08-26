package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceShellIsServedAndPublic(t *testing.T) {
	server := newTestServer(t)

	// The shell holds no data - it asks /v1/me who it is talking to - so
	// gating it would only add a redirect for an anonymous visitor.
	for _, path := range []string{"/app", "/app/"} {
		response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, path)
		require.Contains(t, response.Header.Get("Content-Type"), "text/html")

		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `id="root"`)
		require.Contains(t, string(body), "/static/app/main.js")
	}
}

func TestWorkspaceAssetsServeWithCorrectTypes(t *testing.T) {
	server := newTestServer(t)

	for path, contentType := range map[string]string{
		"/static/app/main.js":            "text/javascript",
		"/static/app/api.js":             "text/javascript",
		"/static/app/views/campaigns.js": "text/javascript",
		"/static/styles/base.css":        "text/css",
		"/static/styles/app.css":         "text/css",
		"/static/favicon.svg":            "image/svg+xml",
	} {
		response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, path)
		require.Contains(t, response.Header.Get("Content-Type"), contentType, path)

		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.NotEmpty(t, body, path)
	}
}

func TestWorkspaceAssetsRejectTraversal(t *testing.T) {
	server := newTestServer(t)

	for _, path := range []string{
		"/static/../go.mod",
		"/static/app/../../go.mod",
		"/static/does-not-exist.js",
	} {
		response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, response.StatusCode, path)
	}
}

// The workspace routes were added alongside /app/api/* and /app/connect/meta.
// Serving assets from /app/ would have shadowed them, so they live under
// /static/ and this pins that they still resolve to their own handlers.
func TestWorkspaceDoesNotShadowExistingAppRoutes(t *testing.T) {
	server := newTestServer(t)

	response, err := server.App.Test(httptest.NewRequest(http.MethodGet, "/app/api/overview", nil))
	require.NoError(t, err)
	require.NotEqual(t, http.StatusOK, response.StatusCode,
		"/app/api/overview must still require a session, not fall through to the shell")

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(body), `id="root"`),
		"/app/api/overview must not be answered with the workspace shell")
}

func TestSessionMutationsOnV1RequireCSRF(t *testing.T) {
	server := newTestServer(t)

	// A bearer caller is unaffected: the credential is not ambient, so there
	// is nothing for a third-party page to ride on.
	request := httptest.NewRequest(http.MethodPost, "/v1/api-keys", strings.NewReader(`{"name":"ci"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-internal-token")
	response, err := server.App.Test(request)
	require.NoError(t, err)
	require.NotEqual(t, http.StatusUnauthorized, response.StatusCode,
		"bearer callers must not be asked for a CSRF token")
}
