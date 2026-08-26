package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

type registerRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginRequest takes one identifier, which may be an email or a username.
type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type createUserBatchRequest struct {
	Batch       application.CreateBatchRequest `json:"batch"`
	Checkpoints []application.GuardCheckpoint  `json:"checkpoints,omitempty"`
	GuardName   string                         `json:"guard_name,omitempty"`
}

func (s *Server) registerUser(c fiber.Ctx) error {
	var request registerRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.RegisterUser(c.Context(), application.RegisterRequest{
		Email:    request.Email,
		Username: request.Username,
		Password: request.Password,
	})
	if err != nil {
		return err
	}
	s.setUserCookies(c, session)
	return jsonOK(c, http.StatusCreated, fiber.Map{"user": session.User})
}

func (s *Server) loginUser(c fiber.Ctx) error {
	var request loginRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.LoginUser(c.Context(), request.Identifier, request.Password)
	if err != nil {
		return err
	}
	s.setUserCookies(c, session)
	return jsonOK(c, http.StatusOK, fiber.Map{"user": session.User})
}

func (s *Server) logoutUser(c fiber.Ctx) error {
	if err := s.service.LogoutUser(c.Context(), c.Cookies(userSessionCookie)); err != nil {
		return err
	}
	s.clearUserCookies(c)
	return c.SendStatus(http.StatusNoContent)
}

func (s *Server) requireUser(c fiber.Ctx) error {
	session, err := s.service.AuthenticateUserSession(c.Context(), c.Cookies(userSessionCookie))
	if err != nil {
		return err
	}
	c.Locals(userSessionLocal, session)
	// Populate a Principal too, so handlers shared with /v1 - notably the
	// media ownership check - see the same shape whichever route reached them.
	c.Locals(principalLocal, Principal{
		UserID:    session.User.ID,
		Role:      session.User.Role,
		Kind:      PrincipalSession,
		SessionID: session.SessionID,
	})
	return c.Next()
}

func (s *Server) requireCSRF(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(c.Get("X-CSRF-Token"))
	if token == "" || token != c.Cookies(userCSRFCookie) {
		return application.ErrUnauthorized
	}
	if err := s.service.ValidateCSRF(c.Context(), session.SessionID, token); err != nil {
		return err
	}
	return c.Next()
}

func currentUserSession(c fiber.Ctx) (application.AuthSession, error) {
	value := c.Locals(userSessionLocal)
	session, ok := value.(application.AuthSession)
	if !ok || session.User.ID == uuid.Nil {
		return application.AuthSession{}, application.ErrSessionExpired
	}
	return session, nil
}

func (s *Server) setUserCookies(c fiber.Ctx, session application.AuthSession) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	c.Cookie(&fiber.Cookie{
		Name: userSessionCookie, Value: session.Token, Path: "/", MaxAge: maxAge,
		Expires: session.ExpiresAt, HTTPOnly: true, Secure: s.secureCookies, SameSite: "Lax",
	})
	c.Cookie(&fiber.Cookie{
		Name: userCSRFCookie, Value: session.CSRFToken, Path: "/", MaxAge: maxAge,
		Expires: session.ExpiresAt, HTTPOnly: false, Secure: s.secureCookies, SameSite: "Lax",
	})
}

func (s *Server) clearUserCookies(c fiber.Ctx) {
	past := time.Unix(1, 0)
	for _, name := range []string{userSessionCookie, userCSRFCookie} {
		c.Cookie(&fiber.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Expires: past, HTTPOnly: name == userSessionCookie, Secure: s.secureCookies, SameSite: "Lax"})
	}
}

func (s *Server) startUserOAuthRedirect(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	result, err := s.service.StartOAuthForUser(c.Context(), session.User.ID)
	if err != nil {
		return err
	}
	return c.Redirect().To(result.AuthorizationURL)
}

func (s *Server) appOverview(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	connections, err := s.service.Repos.Users.ListConnections(c.Context(), session.User.ID)
	if err != nil {
		return err
	}
	accounts, err := s.service.Repos.Users.ListAdAccounts(c.Context(), session.User.ID, 500)
	if err != nil {
		return err
	}
	assets, err := s.service.Repos.Users.ListAssets(c.Context(), session.User.ID, 1000)
	if err != nil {
		return err
	}
	batches, err := s.service.Repos.Users.ListBatches(c.Context(), session.User.ID, 50)
	if err != nil {
		return err
	}
	guards, err := s.service.Repos.Users.ListGuards(c.Context(), session.User.ID, 100)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"user": session.User, "connections": connections, "ad_accounts": accounts, "assets": assets,
		"batches": batches, "guards": guards,
	})
}

func (s *Server) syncUserConnection(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	job, err := s.service.EnqueueConnectionSync(c.Context(), id, "web:"+getRequestID(c))
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusAccepted, job)
}

func (s *Server) createUserBatch(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	var request createUserBatchRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, request.Batch.ConnectionID); err != nil {
		return err
	}
	for _, accountID := range request.Batch.AdAccountIDs {
		if err := s.service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, accountID); err != nil {
			return err
		}
	}
	request.Batch.CreatedBy = "user:" + session.User.ID.String()
	batch, err := s.service.CreateBatch(c.Context(), request.Batch)
	if err != nil {
		return err
	}
	var guard *domain.CampaignGuard
	if len(request.Checkpoints) > 0 {
		name := request.GuardName
		if name == "" {
			name = "Guard " + batch.Name
		}
		guard, err = s.service.CreateGuard(c.Context(), application.CreateGuardRequest{
			ConnectionID: request.Batch.ConnectionID,
			BatchID:      &batch.ID,
			Name:         name,
			Checkpoints:  request.Checkpoints,
		})
		if err != nil {
			return err
		}
	}
	return jsonOK(c, http.StatusAccepted, fiber.Map{"batch": batch, "guard": guard})
}

func (s *Server) updateUserGuard(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsGuard(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	var request application.UpdateGuardRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	guard, err := s.service.UpdateGuard(c.Context(), id, request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, guard)
}

// userOwnsMediaContext verifies the caller may attach media to the named
// connection or ad account.
//
// It previously returned nil - allow - when no session was present, on the
// assumption that meant the internal bearer API. Once /v1 became user-scoped
// that assumption turned into an authorization bypass: any authenticated user
// could name another tenant's connection_id. A missing principal is now an
// error, and only the tenantless internal token is waved through.
func userOwnsMediaContext(c fiber.Ctx, connectionID, adAccountID *uuid.UUID, service *application.Service) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if !principal.HasTenant() {
		if principal.Kind == PrincipalInternal {
			return nil
		}
		return application.ErrUnauthorized
	}
	if connectionID == nil && adAccountID == nil {
		return invalidField("connection_id", "is required for user uploads")
	}
	if connectionID != nil {
		if err := service.Repos.Users.OwnsConnection(c.Context(), principal.UserID, *connectionID); err != nil {
			return err
		}
	}
	if adAccountID != nil {
		if err := service.Repos.Users.OwnsAdAccount(c.Context(), principal.UserID, *adAccountID); err != nil {
			return err
		}
	}
	return nil
}
