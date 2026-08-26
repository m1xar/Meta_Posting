package httpapi

import (
	"embed"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/application"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
)

//go:embed webui/*.html
var webUI embed.FS

type credentialsRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type createUserBatchRequest struct {
	Batch       application.CreateBatchRequest `json:"batch"`
	Checkpoints []application.GuardCheckpoint  `json:"checkpoints,omitempty"`
	GuardName   string                         `json:"guard_name,omitempty"`
}

type updateGuardRequest = application.UpdateGuardRequest

func (s *Server) registerUser(c fiber.Ctx) error {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.RegisterUser(c.Context(), request.Login, request.Password)
	if err != nil {
		return err
	}
	s.setUserCookies(c, session)
	return jsonOK(c, http.StatusCreated, fiber.Map{"user": session.User})
}

func (s *Server) loginUser(c fiber.Ctx) error {
	var request credentialsRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	session, err := s.service.LoginUser(c.Context(), request.Login, request.Password)
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
		if strings.HasPrefix(c.Path(), "/app") && !strings.HasPrefix(c.Path(), "/app/api") {
			return c.Redirect().To("/login")
		}
		return err
	}
	c.Locals(userSessionLocal, session)
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

// campaignView is one campaign row for the UI: the published object joined
// with its latest lifetime insights, tracker roll-up, guard, and checks.
type campaignView struct {
	Campaign domain.PublishedObject  `json:"campaign"`
	Insights *domain.InsightSnapshot `json:"insights,omitempty"`
	Tracker  *domain.TrackerStat     `json:"tracker,omitempty"`
	Guard    *domain.CampaignGuard   `json:"guard,omitempty"`
	Checks   []domain.GuardCheck     `json:"checks,omitempty"`
}

type campaignTotals struct {
	Campaigns      int     `json:"campaigns"`
	Live           int     `json:"live"`
	Paused         int     `json:"paused"`
	Spend          float64 `json:"spend"`
	Clicks         int64   `json:"clicks"`
	Impressions    int64   `json:"impressions"`
	TrackerClicks  int64   `json:"tracker_clicks"`
	TrackerLeads   float64 `json:"tracker_leads"`
	TrackerSales   float64 `json:"tracker_sales"`
	TrackerRevenue float64 `json:"tracker_revenue"`
}

func (s *Server) userCampaignViews(c fiber.Ctx, userID uuid.UUID) ([]campaignView, campaignTotals, error) {
	totals := campaignTotals{}
	campaigns, err := s.service.Repos.Users.ListCampaigns(c.Context(), userID, 500)
	if err != nil {
		return nil, totals, err
	}
	objectIDs := make([]uuid.UUID, 0, len(campaigns))
	for _, campaign := range campaigns {
		objectIDs = append(objectIDs, campaign.ID)
	}
	snapshots, err := s.service.Repos.Insights.LatestForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	trackerStats, err := s.service.Repos.Tracker.ListForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	checks, err := s.service.Repos.Guards.ListChecksForObjects(c.Context(), objectIDs)
	if err != nil {
		return nil, totals, err
	}
	guards, err := s.service.Repos.Users.ListGuards(c.Context(), userID, 200)
	if err != nil {
		return nil, totals, err
	}

	snapshotByObject := make(map[uuid.UUID]*domain.InsightSnapshot, len(snapshots))
	for index := range snapshots {
		if snapshots[index].PublishedObjectID != nil {
			snapshotByObject[*snapshots[index].PublishedObjectID] = &snapshots[index]
		}
	}
	trackerByObject := make(map[uuid.UUID]*domain.TrackerStat, len(trackerStats))
	for index := range trackerStats {
		if trackerStats[index].PublishedObjectID != nil {
			trackerByObject[*trackerStats[index].PublishedObjectID] = &trackerStats[index]
		}
	}
	checksByObject := make(map[uuid.UUID][]domain.GuardCheck)
	for _, check := range checks {
		checksByObject[check.PublishedObjectID] = append(checksByObject[check.PublishedObjectID], check)
	}
	guardByBatch := make(map[uuid.UUID]*domain.CampaignGuard)
	guardByObject := make(map[uuid.UUID]*domain.CampaignGuard)
	for index := range guards {
		guard := &guards[index]
		if guard.PublishedObjectID != nil {
			guardByObject[*guard.PublishedObjectID] = guard
		} else if guard.BatchID != nil {
			if _, exists := guardByBatch[*guard.BatchID]; !exists {
				guardByBatch[*guard.BatchID] = guard
			}
		}
	}

	views := make([]campaignView, 0, len(campaigns))
	for index := range campaigns {
		campaign := campaigns[index]
		view := campaignView{Campaign: campaign}
		view.Insights = snapshotByObject[campaign.ID]
		view.Tracker = trackerByObject[campaign.ID]
		view.Checks = checksByObject[campaign.ID]
		if guard, ok := guardByObject[campaign.ID]; ok {
			view.Guard = guard
		} else if guard, ok := guardByBatch[campaign.BatchID]; ok {
			view.Guard = guard
		}
		views = append(views, view)

		totals.Campaigns++
		switch campaign.EffectiveStatus {
		case "ACTIVE", "IN_PROCESS", "WITH_ISSUES":
			totals.Live++
		case "PAUSED", "CAMPAIGN_PAUSED":
			totals.Paused++
		}
		if view.Insights != nil {
			totals.Spend += view.Insights.Spend
			totals.Clicks += view.Insights.Clicks
			totals.Impressions += view.Insights.Impressions
		}
		if view.Tracker != nil {
			totals.TrackerClicks += view.Tracker.Clicks
			totals.TrackerLeads += view.Tracker.Leads
			totals.TrackerSales += view.Tracker.Sales
			totals.TrackerRevenue += view.Tracker.Revenue
		}
	}
	return views, totals, nil
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
	batches, err := s.service.Repos.Users.ListBatches(c.Context(), session.User.ID, 50)
	if err != nil {
		return err
	}
	views, totals, err := s.userCampaignViews(c, session.User.ID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"user":        session.User,
		"connections": connections,
		"ad_accounts": accounts,
		"batches":     batches,
		"campaigns":   views,
		"totals":      totals,
	})
}

func (s *Server) launcherData(c fiber.Ctx) error {
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
	return jsonOK(c, http.StatusOK, fiber.Map{
		"user":         session.User,
		"connections":  connections,
		"ad_accounts":  accounts,
		"assets":       assets,
		"batches":      batches,
		"capabilities": s.service.Capabilities(),
	})
}

func (s *Server) listUserCampaigns(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	views, totals, err := s.userCampaignViews(c, session.User.ID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"user": session.User, "campaigns": views, "totals": totals})
}

func (s *Server) accountStats(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	account, err := s.service.Repos.Inventory.GetAdAccount(c.Context(), id)
	if err != nil {
		return err
	}
	views, _, err := s.userCampaignViews(c, session.User.ID)
	if err != nil {
		return err
	}
	accountViews := make([]campaignView, 0)
	totals := campaignTotals{}
	for _, view := range views {
		if view.Campaign.AdAccountID != id {
			continue
		}
		accountViews = append(accountViews, view)
		totals.Campaigns++
		switch view.Campaign.EffectiveStatus {
		case "ACTIVE", "IN_PROCESS", "WITH_ISSUES":
			totals.Live++
		case "PAUSED", "CAMPAIGN_PAUSED":
			totals.Paused++
		}
		if view.Insights != nil {
			totals.Spend += view.Insights.Spend
			totals.Clicks += view.Insights.Clicks
			totals.Impressions += view.Insights.Impressions
		}
		if view.Tracker != nil {
			totals.TrackerClicks += view.Tracker.Clicks
			totals.TrackerLeads += view.Tracker.Leads
			totals.TrackerSales += view.Tracker.Sales
			totals.TrackerRevenue += view.Tracker.Revenue
		}
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"user":      session.User,
		"account":   account,
		"campaigns": accountViews,
		"totals":    totals,
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

func (s *Server) revokeUserConnection(c fiber.Ctx) error {
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
	if err := s.service.RevokeConnection(c.Context(), id); err != nil {
		return err
	}
	return noContent(c)
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

func (s *Server) getUserBatch(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsBatch(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	batch, err := s.service.Repos.Batches.Get(c.Context(), id)
	if err != nil {
		return err
	}
	results, err := s.service.Repos.Batches.ListAccountResults(c.Context(), batchResultsFilter(id))
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"batch": batch, "results": results})
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
	var request updateGuardRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	guard, err := s.service.UpdateGuard(c.Context(), id, request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, guard)
}

func (s *Server) createCampaignGuard(c fiber.Ctx) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsPublishedObject(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	var campaign domain.PublishedObject
	if err := s.service.Repos.DB().WithContext(c.Context()).
		Where("id = ? AND object_type = ?", id, domain.PublishedCampaign).
		First(&campaign).Error; err != nil {
		return err
	}
	var request updateGuardRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	name := request.Name
	if name == "" {
		name = "Guard " + campaign.Name
	}
	guard, err := s.service.CreateGuard(c.Context(), application.CreateGuardRequest{
		ConnectionID:      campaign.ConnectionID,
		PublishedObjectID: &campaign.ID,
		Name:              name,
		Checkpoints:       request.Checkpoints,
	})
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusCreated, guard)
}

func (s *Server) pauseUserCampaign(c fiber.Ctx) error  { return s.setUserCampaignStatus(c, true) }
func (s *Server) resumeUserCampaign(c fiber.Ctx) error { return s.setUserCampaignStatus(c, false) }

func (s *Server) setUserCampaignStatus(c fiber.Ctx, pause bool) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	if err := s.service.Repos.Users.OwnsPublishedObject(c.Context(), session.User.ID, id); err != nil {
		return err
	}
	campaign, err := s.service.SetCampaignStatus(c.Context(), id, pause)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, campaign)
}

func (s *Server) userCapabilities(c fiber.Ctx) error {
	return jsonOK(c, http.StatusOK, s.service.Capabilities())
}

func (s *Server) loginPage(c fiber.Ctx) error    { return s.sendWebPage(c, "webui/auth.html") }
func (s *Server) registerPage(c fiber.Ctx) error { return s.sendWebPage(c, "webui/auth.html") }

func (s *Server) dashboardPage(c fiber.Ctx) error { return s.sendWebPage(c, "webui/dashboard.html") }
func (s *Server) launcherPage(c fiber.Ctx) error  { return s.sendWebPage(c, "webui/launch.html") }
func (s *Server) campaignsPage(c fiber.Ctx) error { return s.sendWebPage(c, "webui/campaigns.html") }
func (s *Server) accountPage(c fiber.Ctx) error   { return s.sendWebPage(c, "webui/account.html") }

func (s *Server) sendWebPage(c fiber.Ctx, name string) error {
	content, err := webUI.ReadFile(name)
	if err != nil {
		return fiber.ErrNotFound
	}
	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.Status(http.StatusOK).Send(content)
}

func userOwnsMediaContext(c fiber.Ctx, connectionID, adAccountID *uuid.UUID, service *application.Service) error {
	session, err := currentUserSession(c)
	if err != nil {
		return err
	}
	if connectionID == nil && adAccountID == nil {
		return invalidField("connection_id", "is required for user uploads")
	}
	if connectionID != nil {
		if err := service.Repos.Users.OwnsConnection(c.Context(), session.User.ID, *connectionID); err != nil {
			return err
		}
	}
	if adAccountID != nil {
		if err := service.Repos.Users.OwnsAdAccount(c.Context(), session.User.ID, *adAccountID); err != nil {
			return err
		}
	}
	return nil
}

func batchResultsFilter(batchID uuid.UUID) database.BatchAccountResultFilter {
	return database.BatchAccountResultFilter{
		BatchID: batchID,
		Page:    domain.PageRequest{Limit: 500},
	}
}
