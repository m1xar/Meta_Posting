package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/config"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	service := &application.Service{
		Config: config.Config{Meta: config.MetaConfig{APIVersion: "v25.0"}},
		Repos:  database.NewRepositories(nil),
	}
	server, err := New(service, Config{
		OpenAPI: []byte("openapi: 3.1.0\n"),
	})
	require.NoError(t, err)
	return server
}

func TestPublicHealthAndRequestID(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", "request-from-test")
	response, err := server.App.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "request-from-test", response.Header.Get("X-Request-ID"))
}

func TestProtectedRouteRequiresSession(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/app/api/capabilities", nil)
	response, err := server.App.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)

	var envelope errorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "session_expired", envelope.Error.Code)
	require.NotEmpty(t, envelope.Error.RequestID)
}

func TestAppPageRedirectsAnonymousToLogin(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/app", nil)
	response, err := server.App.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusSeeOther, response.StatusCode)
	require.Equal(t, "/login", response.Header.Get("Location"))
}

func TestUnauthorizedBodyIsRejectedBeforeFixedLengthBodyIsConsumed(t *testing.T) {
	server := newTestServer(t)
	address := listenForTest(t, server.App)

	connection, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))

	_, err = fmt.Fprintf(connection,
		"POST /app/api/media HTTP/1.1\r\nHost: test\r\nContent-Type: multipart/form-data; boundary=test\r\nContent-Length: %d\r\n\r\nx",
		1<<30,
	)
	require.NoError(t, err)

	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	require.NoError(t, err, "server must reject after the one-byte pre-authentication prefetch")
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.True(t, response.Close)
}

func TestUnauthorizedChunkedBodyIsRejectedBeforeFirstChunk(t *testing.T) {
	server := newTestServer(t)
	address := listenForTest(t, server.App)

	connection, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))

	_, err = fmt.Fprint(connection,
		"POST /app/api/media HTTP/1.1\r\nHost: test\r\nContent-Type: multipart/form-data; boundary=test\r\nTransfer-Encoding: chunked\r\n\r\n",
	)
	require.NoError(t, err)

	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	require.NoError(t, err, "server must reject from headers without waiting for a chunk")
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.True(t, response.Close)
}

func TestAuthorizedOversizedJSONIsRejectedAfterOneBytePrefetch(t *testing.T) {
	server := newTestServer(t)
	address := listenForTest(t, server.App)

	connection, err := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(2*time.Second)))

	_, err = fmt.Fprintf(connection,
		"POST /auth/login HTTP/1.1\r\nHost: test\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n{",
		maxJSONBodyBytes+1,
	)
	require.NoError(t, err)

	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	require.True(t, response.Close)
}

func TestStrictJSONRejectsUnknownFields(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"unexpected":true}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := server.App.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)

	var envelope errorEnvelope
	require.NoError(t, json.NewDecoder(response.Body).Decode(&envelope))
	require.Equal(t, "invalid_request", envelope.Error.Code)
}

func TestAuthPagesArePublic(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/login", "/register"} {
		response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, response.StatusCode, path)
		require.Contains(t, response.Header.Get("Content-Type"), "text/html", path)
		require.NotEmpty(t, body, path)
	}
}

func TestOpenAPIDocumentIsPublic(t *testing.T) {
	server := newTestServer(t)
	response, err := server.App.Test(httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "application/yaml; charset=utf-8", response.Header.Get("Content-Type"))
}

func TestLegalPagesArePublic(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/privacy", "/terms", "/data-deletion"} {
		response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, response.StatusCode, path)
		require.Contains(t, response.Header.Get("Content-Type"), "text/html", path)
		require.Contains(t, string(body), "Raze Posting", path)
	}
}

func TestSwaggerUIIsPublic(t *testing.T) {
	server := newTestServer(t)
	for _, path := range []string{"/docs", "/docs/", "/swagger", "/swagger/"} {
		t.Run(path, func(t *testing.T) {
			response, err := server.App.Test(httptest.NewRequest(http.MethodGet, path, nil))
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, "text/html; charset=utf-8", response.Header.Get("Content-Type"))
			require.Equal(t, "DENY", response.Header.Get("X-Frame-Options"))

			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Contains(t, string(body), `url: "/openapi.yaml"`)
			require.Contains(t, string(body), "swagger-ui-dist@5.32.11")
		})
	}
}

func listenForTest(t *testing.T, app *fiber.App) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true})
	}()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, app.ShutdownWithContext(shutdownContext))
		select {
		case listenErr := <-done:
			require.NoError(t, listenErr)
		case <-time.After(2 * time.Second):
			t.Error("Fiber listener did not stop")
		}
	})
	return listener.Addr().String()
}
