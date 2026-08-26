package httpapi

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/application"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// launchAccount is an ad account as the launcher needs to see it: can we
// publish here, and if not, why not.
type launchAccount struct {
	ID              uuid.UUID              `json:"id"`
	ConnectionID    uuid.UUID              `json:"connection_id"`
	MetaAdAccountID string                 `json:"meta_ad_account_id"`
	AccountID       string                 `json:"account_id"`
	Name            string                 `json:"name"`
	Currency        string                 `json:"currency"`
	TimezoneName    string                 `json:"timezone_name"`
	AccountStatus   int                    `json:"account_status"`
	AmountSpent     int64                  `json:"amount_spent_minor"`
	SpendCap        int64                  `json:"spend_cap_minor"`
	RemainingCap    int64                  `json:"remaining_cap_minor"`
	FundingSource   string                 `json:"funding_source,omitempty"`
	IsPrepay        bool                   `json:"is_prepay_account"`
	Readiness       domain.LaunchReadiness `json:"readiness"`
}

// listLaunchAccounts returns every ad account with its readiness verdict.
//
// Blocked accounts are included rather than filtered out. "Why is my account
// not in the list" is a worse thing to leave someone with than a row saying
// the payment method was removed.
func (s *Server) listLaunchAccounts(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	page, err := s.service.Repos.Inventory.ListAdAccounts(c.Context(), database.AdAccountFilter{
		Scope:      scope,
		ActiveOnly: true,
		Page:       domain.PageRequest{Limit: domain.MaxPageLimit},
	})
	if err != nil {
		return err
	}

	readyOnly := strings.EqualFold(c.Query("ready_only"), "true")
	items := make([]launchAccount, 0, len(page.Items))
	ready := 0
	for index := range page.Items {
		account := &page.Items[index]
		readiness := domain.LaunchReadinessFor(account)
		if readiness.Ready {
			ready++
		}
		if readyOnly && !readiness.Ready {
			continue
		}
		items = append(items, launchAccount{
			ID:              account.ID,
			ConnectionID:    account.ConnectionID,
			MetaAdAccountID: account.MetaAdAccountID,
			AccountID:       account.AccountID,
			Name:            account.Name,
			Currency:        account.Currency,
			TimezoneName:    account.TimezoneName,
			AccountStatus:   account.AccountStatus,
			AmountSpent:     account.AmountSpent,
			SpendCap:        account.SpendCap,
			RemainingCap:    account.RemainingSpendCap(),
			FundingSource:   account.FundingSource,
			IsPrepay:        account.IsPrepayAccount,
			Readiness:       readiness,
		})
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"items": items,
		"total": page.Total,
		"ready": ready,
	})
}

// launchTemplate is an existing ad set offered as a starting point.
type launchTemplate struct {
	ID             uuid.UUID   `json:"id"`
	AdAccountID    uuid.UUID   `json:"ad_account_id"`
	AdAccountName  string      `json:"ad_account_name,omitempty"`
	MetaObjectID   string      `json:"meta_object_id"`
	Name           string      `json:"name"`
	CampaignMetaID string      `json:"campaign_meta_id,omitempty"`
	Status         string      `json:"effective_status"`
	Objective      string      `json:"objective,omitempty"`
	Optimization   string      `json:"optimization_goal,omitempty"`
	BillingEvent   string      `json:"billing_event,omitempty"`
	DailyBudget    int64       `json:"daily_budget_minor"`
	LifetimeBudget int64       `json:"lifetime_budget_minor"`
	Raw            domain.JSON `json:"raw,omitempty"`
	// Creative is the whole creative of one ad inside this ad set. The copy
	// lives in different places depending on how the ad was built - a plain
	// ad keeps it in object_story_spec, a flexible one in asset_feed_spec -
	// so the whole object is returned and the caller reads whichever is
	// present. Copying an ad set is rarely only about targeting: the page,
	// the Instagram identity and the body are what someone wants carried
	// over, and a page ID typed from memory is how a launch ends up
	// published by the wrong brand.
	Creative domain.JSON `json:"creative,omitempty"`
}

// listLaunchTemplates offers existing ad sets as launch starting points.
//
// The inventory sweep already stores each ad set's full targeting, promoted
// object and attribution spec, so a proven ad set can seed a new launch
// without asking anyone to retype a targeting tree.
func (s *Server) listLaunchTemplates(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	limit, offset, err := pageRequest(c)
	if err != nil {
		return err
	}
	level := domain.AdEntityAdSet
	filter := database.AdEntityFilter{
		Scope:        scope,
		Level:        &level,
		MetaObjectID: strings.TrimSpace(c.Query("meta_object_id")),
		Search:       strings.TrimSpace(c.Query("search")),
		Light:        true,
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	}
	if raw := strings.TrimSpace(c.Query("ad_account_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return invalidField("ad_account_id", "must be a UUID")
		}
		filter.AdAccountID = &id
	}
	// Restrict templates to the launch-selected accounts, so a source ad set
	// is only offered from an account the buyer is actually publishing into.
	for _, raw := range splitQuery(c, "ad_account_ids") {
		id, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			return invalidField("ad_account_ids", "must be UUIDs")
		}
		filter.AdAccountIDs = append(filter.AdAccountIDs, id)
	}
	page, err := s.service.Repos.AdEntities.List(c.Context(), filter)
	if err != nil {
		return err
	}

	// The list stays light on purpose: identity and headline fields only.
	// The full targeting tree and creative arrive from the detail endpoint
	// once one template is actually picked.
	items := make([]launchTemplate, 0, len(page.Items))
	for index := range page.Items {
		entity := &page.Items[index]
		items = append(items, launchTemplate{
			ID:             entity.ID,
			AdAccountID:    entity.AdAccountID,
			MetaObjectID:   entity.MetaObjectID,
			Name:           entity.Name,
			CampaignMetaID: entity.CampaignMetaID,
			Status:         entity.EffectiveStatus,
			Objective:      entity.Objective,
			Optimization:   entity.OptimizationGoal,
			BillingEvent:   entity.BillingEvent,
			DailyBudget:    entity.DailyBudget,
			LifetimeBudget: entity.LifetimeBudget,
		})
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"items": items, "total": page.Total, "limit": limit, "offset": offset,
	})
}

// launch publishes a batch together with its stop conditions.
func (s *Server) launch(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	var request application.LaunchRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	// Ownership of the connection is what authorises publishing into its ad
	// accounts; CreateBatch validates the accounts themselves.
	if principal.HasTenant() {
		if err := s.service.Repos.Users.OwnsConnection(
			c.Context(), principal.UserID, request.ConnectionID,
		); err != nil {
			return err
		}
	}
	s.logger.Info("launch requested",
		"request_id", getRequestID(c),
		"user_id", principal.UserID,
		"connection_id", request.ConnectionID,
		"ad_accounts", len(request.AdAccountIDs),
		"checkpoints", len(request.Checkpoints),
	)
	result, err := s.service.Launch(c.Context(), request)
	if err != nil {
		s.logger.Warn("launch failed", "request_id", getRequestID(c), "error", err)
	}
	if err != nil {
		// A partially guarded batch still reports what was created, so the
		// caller can act rather than guess.
		if result.Batch != nil {
			return jsonOK(c, http.StatusAccepted, fiber.Map{
				"batch": result.Batch, "guard": result.Guard, "warning": err.Error(),
			})
		}
		return err
	}
	return jsonOK(c, http.StatusCreated, fiber.Map{
		"batch": result.Batch, "guard": result.Guard,
	})
}

// previewLaunch composes the form and returns the hierarchy that would be
// published, without contacting Meta.
//
// It answers "what am I about to send" for free. The dry run answers "will
// Meta accept it", which costs a Graph call, so the cheap check comes first.
func (s *Server) previewLaunch(c fiber.Ctx) error {
	var request application.LaunchRequest
	if err := decodeJSON(c, &request); err != nil {
		return err
	}
	hierarchy, err := s.service.PreviewLaunch(request)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"hierarchy":   hierarchy,
		"checkpoints": request.Checkpoints,
		"ad_accounts": len(request.AdAccountIDs),
	})
}

// stopBatch pauses everything a batch published.
func (s *Server) stopBatch(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(strings.TrimSpace(c.Params("id")))
	if err != nil {
		return invalidField("id", "must be a UUID")
	}
	if principal.HasTenant() {
		if err := s.service.Repos.Users.OwnsBatch(c.Context(), principal.UserID, id); err != nil {
			return err
		}
	}
	result, stopErr := s.service.StopBatch(c.Context(), id)
	if stopErr != nil {
		// Report what was paused even when some of it failed: knowing that
		// the campaign stopped but one ad did not is more useful than an
		// error alone, because the campaign is what holds the spend.
		return jsonOK(c, http.StatusAccepted, fiber.Map{
			"result": result, "warning": stopErr.Error(),
		})
	}
	return jsonOK(c, http.StatusOK, fiber.Map{"result": result})
}

// getLaunchTemplate returns one ad set with its full raw targeting tree and a
// representative creative - the heavy halves the list omits.
func (s *Server) getLaunchTemplate(c fiber.Ctx) error {
	scope, err := scopeFor(c)
	if err != nil {
		return err
	}
	id, err := parseID(c.Params("id"), "id")
	if err != nil {
		return err
	}
	var entity domain.AdEntity
	query := s.service.Repos.DB().WithContext(c.Context()).Model(&domain.AdEntity{}).
		Where("ad_entities.id = ? AND ad_entities.level = ?", id, domain.AdEntityAdSet)
	if err := scope.Apply(query, "ad_entities").First(&entity).Error; err != nil {
		return err
	}
	var creative struct{ Story []byte }
	_ = s.service.Repos.DB().WithContext(c.Context()).Raw(`
		SELECT raw_json->'creative' AS story
		FROM ad_entities
		WHERE level = 'ad'
		  AND adset_meta_id = ?
		  AND raw_json->'creative' IS NOT NULL
		ORDER BY last_seen_at DESC
		LIMIT 1
	`, entity.MetaObjectID).Scan(&creative).Error
	return jsonOK(c, http.StatusOK, launchTemplate{
		ID:             entity.ID,
		AdAccountID:    entity.AdAccountID,
		MetaObjectID:   entity.MetaObjectID,
		Name:           entity.Name,
		CampaignMetaID: entity.CampaignMetaID,
		Status:         entity.EffectiveStatus,
		Objective:      entity.Objective,
		Optimization:   entity.OptimizationGoal,
		BillingEvent:   entity.BillingEvent,
		DailyBudget:    entity.DailyBudget,
		LifetimeBudget: entity.LifetimeBudget,
		Raw:            entity.RawJSON,
		Creative:       domain.JSON(creative.Story),
	})
}

// syncRefresh queues an immediate data refresh for the caller's tenant.
func (s *Server) syncRefresh(c fiber.Ctx) error {
	principal, err := currentPrincipal(c)
	if err != nil {
		return err
	}
	if !principal.HasTenant() {
		return application.ErrForbidden
	}
	var body struct{}
	if err := decodeOptionalJSON(c, &body); err != nil {
		return err
	}
	summary, err := s.service.RefreshUserData(c.Context(), principal.UserID)
	if err != nil {
		return err
	}
	return jsonOK(c, http.StatusAccepted, summary)
}
