package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// currentUser answers "who am I" for the workspace.
//
// The session cookie is HttpOnly, so the browser cannot read its own identity
// from it; without this the client would have to infer whether it is signed
// in from whether some unrelated call happened to 401.
func (s *Server) currentUser(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if !principal.HasTenant() {
		// The shared internal token authenticates but owns nothing, so there
		// is no user to describe.
		return jsonOK(c, http.StatusOK, fiber.Map{
			"user": nil,
			"kind": principal.Kind,
			"role": principal.Role,
		})
	}
	var user domain.User
	if err := s.service.Repos.DB().WithContext(c.Context()).
		First(&user, "id = ?", principal.UserID).Error; err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"user": user, "kind": principal.Kind})
}

// --- per-user API keys ---

type createAPIKeyRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) createAPIKey(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if !principal.HasTenant() {
		return application.ErrForbidden
	}
	var request createAPIKeyRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	issued, err := s.service.CreateAPIKey(c.Context(), principal.UserID, request.Name, request.ExpiresAt)
	if err != nil {
		return err
	}
	// The plaintext token appears here and nowhere else, ever.
	return jsonOK(c, http.StatusCreated, fiber.Map{
		"api_key": issued.Key,
		"token":   issued.Token,
	})
}

func (s *Server) listAPIKeys(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	keys, err := s.service.ListAPIKeys(c.Context(), principal.UserID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"items": keys})
}

func (s *Server) revokeAPIKey(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Params("id")))
	if err != nil {
		return invalidField("id", "must be a UUID")
	}
	if err := s.service.RevokeAPIKey(c.Context(), id, principal.UserID); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

// --- tenant-scoped reads ---

func (s *Server) listAdEntities(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	return s.respondAdEntities(c, scope)
}

func (s *Server) respondAdEntities(c fiber.Ctx, scope database.Scope) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	filter := database.AdEntityFilter{
		Scope:           scope,
		CampaignMetaID:  strings.TrimSpace(c.Query("campaign_id")),
		AdSetMetaID:     strings.TrimSpace(c.Query("adset_id")),
		MetaObjectID:    strings.TrimSpace(c.Query("meta_object_id")),
		EffectiveStatus: strings.TrimSpace(c.Query("effective_status")),
		IncludeGone:     strings.EqualFold(c.Query("include_gone"), "true"),
		OwnedOnly:       strings.EqualFold(c.Query("owned_only"), "true"),
		Page:            domain.PageRequest{Limit: limit, Offset: offset},
	}
	if raw := strings.TrimSpace(c.Query("ad_account_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return invalidField("ad_account_id", "must be a UUID")
		}
		filter.AdAccountID = &id
	}
	if raw := strings.TrimSpace(c.Query("level")); raw != "" {
		level := domain.AdEntityLevel(raw)
		if !level.Valid() {
			return invalidField("level", "must be campaign, adset, or ad")
		}
		filter.Level = &level
	}
	page, err := s.service.Repos.AdEntities.List(c.Context(), filter)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, page)
}

func (s *Server) listDailyInsights(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	return s.respondDailyInsights(c, scope)
}

func (s *Server) respondDailyInsights(c fiber.Ctx, scope database.Scope) error {
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	filter := database.AdInsightDailyFilter{
		Scope:          scope,
		MetaObjectID:   strings.TrimSpace(c.Query("meta_object_id")),
		CampaignMetaID: strings.TrimSpace(c.Query("campaign_id")),
		AdSetMetaID:    strings.TrimSpace(c.Query("adset_id")),
		Page:           domain.PageRequest{Limit: limit, Offset: offset},
	}
	if raw := strings.TrimSpace(c.Query("ad_account_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return invalidField("ad_account_id", "must be a UUID")
		}
		filter.AdAccountID = &id
	}
	if raw := strings.TrimSpace(c.Query("level")); raw != "" {
		level := domain.InsightLevel(raw)
		switch level {
		case domain.InsightAccount, domain.InsightCampaign, domain.InsightAdSet, domain.InsightAd:
			filter.Level = &level
		default:
			return invalidField("level", "must be account, campaign, adset, or ad")
		}
	}
	since, err := parseDateQuery(c, "since")
	if err != nil {
		return err
	}
	until, err := parseDateQuery(c, "until")
	if err != nil {
		return err
	}
	if since != nil && until != nil && since.After(*until) {
		return invalidField("since", "must not be after until")
	}
	filter.Since, filter.Until = since, until

	page, err := s.service.Repos.AdInsights.ListDaily(c.Context(), filter)
	if err != nil {
		return err
	}
	// Rollups deliberately omit reach and frequency for multi-day windows:
	// they are deduplicated per query window and cannot be summed.
	rollup := application.SumDaily(page.Items)
	return jsonOK(c, http.StatusOK, fiber.Map{
		"items":  page.Items,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
		"rollup": rollup,
	})
}

func parseDateQuery(c fiber.Ctx, key string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, raw, time.UTC)
	if err != nil {
		return nil, invalidField(key, "must be a YYYY-MM-DD date")
	}
	return &parsed, nil
}

// --- admin, cross-tenant ---

// auditCrossTenantRead records that an admin looked beyond their own tenant.
// Cross-tenant access is legitimate but must never be invisible.
func (s *Server) auditCrossTenantRead(c fiber.Ctx, resource string) {
	principal, err := currentPrincipal(c)
	if err != nil {
		return
	}
	actorID := principal.UserID.String()
	if !principal.HasTenant() {
		actorID = string(principal.Kind)
	}
	s.service.Audit(c.Context(), domain.AuditEvent{
		ActorType:  string(principal.Kind),
		ActorID:    actorID,
		Action:     "admin.cross_tenant_read",
		EntityType: resource,
		Severity:   domain.AuditInfo,
		Metadata: domain.MustJSON(map[string]string{
			"path":  c.Path(),
			"query": c.Request().URI().QueryArgs().String(),
		}),
	})
}

func (s *Server) adminListUsers(c fiber.Ctx) error {
	if _, err := s.adminScope(c); err != nil {
		return err
	}
	s.auditCrossTenantRead(c, "users")
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	var users []domain.User
	var total int64
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.User{})
	if err := query.Count(&total).Error; err != nil {
		return err
	}
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&users).Error; err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"items": users, "total": total, "limit": limit, "offset": offset,
	})
}

func (s *Server) adminListConnections(c fiber.Ctx) error {
	scope, err := s.adminScope(c)
	if err != nil {
		return err
	}
	s.auditCrossTenantRead(c, "meta_connections")
	return s.respondConnections(c, scope)
}

func (s *Server) adminListAdAccounts(c fiber.Ctx) error {
	scope, err := s.adminScope(c)
	if err != nil {
		return err
	}
	s.auditCrossTenantRead(c, "ad_accounts")
	return s.respondAdAccounts(c, scope)
}

func (s *Server) adminListDailyInsights(c fiber.Ctx) error {
	scope, err := s.adminScope(c)
	if err != nil {
		return err
	}
	s.auditCrossTenantRead(c, "ad_insights_daily")
	return s.respondDailyInsights(c, scope)
}

// adminRateLimits surfaces the last observed Meta usage per ad account, so an
// operator can see throttling before it becomes a stalled backfill.
func (s *Server) adminRateLimits(c fiber.Ctx) error {
	if _, err := s.adminScope(c); err != nil {
		return err
	}
	var states []domain.AdAccountSyncState
	err := s.service.Repos.DB().WithContext(c.Context()).
		Model(&domain.AdAccountSyncState{}).
		Where("throttled_until IS NOT NULL OR last_usage <> '{}'::jsonb").
		Order("throttled_until DESC NULLS LAST, updated_at DESC").
		Limit(500).
		Find(&states).Error
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"items": states})
}
