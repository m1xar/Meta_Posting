package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

const principalLocal = "principal"

// PrincipalKind records how a caller authenticated.
type PrincipalKind string

const (
	// PrincipalSession is a browser session cookie.
	PrincipalSession PrincipalKind = "session"
	// PrincipalAPIKey is a per-user bearer key.
	PrincipalAPIKey PrincipalKind = "api_key"
	// PrincipalInternal is the legacy shared INTERNAL_API_TOKEN. It has admin
	// reach but no tenant identity, which is exactly why it is being retired:
	// it can read every tenant and cannot create anything owned by one.
	PrincipalInternal PrincipalKind = "internal"
)

// Principal is the authenticated caller, normalized across auth schemes so
// handlers do not branch on how someone signed in.
type Principal struct {
	UserID    uuid.UUID
	Role      domain.UserRole
	Kind      PrincipalKind
	SessionID uuid.UUID
}

func (p Principal) IsAdmin() bool { return p.Role == domain.RoleAdmin }

// HasTenant reports whether the principal owns resources. The legacy internal
// token does not, so it cannot be the owner of anything it creates.
func (p Principal) HasTenant() bool { return p.UserID != uuid.Nil }

// Scope is the tenant restriction this principal gets by default.
//
// Admin status alone does not widen it: an admin browsing their own dashboard
// sees their own data. Widening is an explicit, audited act - see adminScope.
func (p Principal) Scope() database.Scope {
	if !p.HasTenant() && p.IsAdmin() {
		return database.AdminScope()
	}
	return database.UserScope(p.UserID)
}

func currentPrincipal(c fiber.Ctx) (Principal, error) {
	principal, ok := c.Locals(principalLocal).(Principal)
	if !ok {
		return Principal{}, application.ErrUnauthorized
	}
	return principal, nil
}

// scopeFor returns the tenant scope for the current request.
func scopeFor(c fiber.Ctx) (database.Scope, error) {
	principal, err := currentPrincipal(c)
	if err != nil {
		return database.Scope{}, err
	}
	scope := principal.Scope()
	if !scope.Valid() {
		return database.Scope{}, application.ErrUnauthorized
	}
	return scope, nil
}

// adminScope widens a query across tenants. It is only reachable from an
// /v1/admin route and requires the admin role, so a cross-tenant read is
// always a deliberate act on a deliberate endpoint.
func (s *Server) adminScope(c fiber.Ctx) (database.Scope, error) {
	principal, err := currentPrincipal(c)
	if err != nil {
		return database.Scope{}, err
	}
	if !principal.IsAdmin() {
		return database.Scope{}, application.ErrForbidden
	}
	return database.AdminScope(), nil
}

// authenticatePrincipal accepts either a session cookie or a bearer
// credential and normalizes both into a Principal.
//
// The cookie is tried first because a browser sends both a cookie and, on
// some proxies, an inherited Authorization header; the session is the more
// specific signal.
func (s *Server) authenticatePrincipal(c fiber.Ctx) error {
	if token := strings.TrimSpace(c.Cookies(userSessionCookie)); token != "" {
		session, err := s.service.AuthenticateUserSession(c.Context(), token)
		if err == nil {
			c.Locals(userSessionLocal, session)
			c.Locals(principalLocal, Principal{
				UserID:    session.User.ID,
				Role:      session.User.Role,
				Kind:      PrincipalSession,
				SessionID: session.SessionID,
			})
			return c.Next()
		}
		// Fall through: an expired cookie must not stop a valid bearer key.
	}

	header := strings.TrimSpace(c.Get("Authorization"))
	scheme, credential, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return application.ErrUnauthorized
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return application.ErrUnauthorized
	}

	// The legacy shared token is a constant-time compare against a fixed
	// value, so checking it first costs nothing and spares a database round
	// trip. When the flag is off - the production default - this is skipped
	// entirely and only per-user keys are accepted.
	if s.allowLegacyInternalToken {
		provided := sha256.Sum256([]byte(credential))
		if subtle.ConstantTimeCompare(provided[:], s.token[:]) == 1 {
			c.Locals(principalLocal, Principal{
				Role: domain.RoleAdmin,
				Kind: PrincipalInternal,
			})
			return c.Next()
		}
	}

	user, err := s.service.AuthenticateAPIKey(c.Context(), credential)
	if err == nil {
		c.Locals(principalLocal, Principal{
			UserID: user.ID,
			Role:   user.Role,
			Kind:   PrincipalAPIKey,
		})
		return c.Next()
	}
	return application.ErrUnauthorized
}

// requireAdmin gates the cross-tenant routes.
func (s *Server) requireAdmin(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if !principal.IsAdmin() {
		return application.ErrForbidden
	}
	return c.Next()
}

// requireCSRFForSessions enforces double-submit CSRF on state-changing
// requests, but only for callers authenticated by a session cookie.
//
// A cookie is an ambient credential: the browser attaches it to any request
// the page can provoke, so a cookie-authenticated mutation needs a second
// factor the attacker's page cannot read. A bearer key is not ambient - it
// has to be attached deliberately - so requiring CSRF there would break every
// script for no gain.
//
// SameSite=Lax already blocks cross-site form POSTs, so this is defence in
// depth rather than the only barrier. It exists because /v1 became
// cookie-aware when the cabinet moved onto it, and the mutating routes there
// had no CSRF check at all.
func (s *Server) requireCSRFForSessions(c fiber.Ctx) error {
	switch c.Method() {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return c.Next()
	}
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if principal.Kind != PrincipalSession {
		return c.Next()
	}
	token := strings.TrimSpace(c.Get("X-CSRF-Token"))
	if token == "" || token != c.Cookies(userCSRFCookie) {
		return application.ErrUnauthorized
	}
	if err := s.service.ValidateCSRF(c.Context(), principal.SessionID, token); err != nil {
		return err
	}
	return c.Next()
}
