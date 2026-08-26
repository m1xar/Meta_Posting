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
	Raw            domain.JSON `json:"raw"`
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
		Page:         domain.PageRequest{Limit: limit, Offset: offset},
	}
	if raw := strings.TrimSpace(c.Query("ad_account_id")); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return invalidField("ad_account_id", "must be a UUID")
		}
		filter.AdAccountID = &id
	}
	page, err := s.service.Repos.AdEntities.List(c.Context(), filter)
	if err != nil {
		return err
	}

	// One representative creative per ad set, fetched in a single query
	// rather than per row.
	creatives := map[string]domain.JSON{}
	if len(page.Items) > 0 {
		adSetIDs := make([]string, 0, len(page.Items))
		for index := range page.Items {
			adSetIDs = append(adSetIDs, page.Items[index].MetaObjectID)
		}
		var rows []struct {
			AdSetMetaID string
			Story       []byte
		}
		if err := s.service.Repos.DB().WithContext(c.Context()).Raw(`
			SELECT DISTINCT ON (adset_meta_id)
			       adset_meta_id AS ad_set_meta_id,
			       raw_json->'creative' AS story
			FROM ad_entities
			WHERE level = 'ad'
			  AND adset_meta_id IN ?
			  AND raw_json->'creative' IS NOT NULL
			ORDER BY adset_meta_id, last_seen_at DESC
		`, adSetIDs).Scan(&rows).Error; err == nil {
			for _, row := range rows {
				creatives[row.AdSetMetaID] = domain.JSON(row.Story)
			}
		}
	}

	items := make([]launchTemplate, 0, len(page.Items))
	for index := range page.Items {
		entity := &page.Items[index]
		items = append(items, launchTemplate{
			Creative:       creatives[entity.MetaObjectID],
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
	result, err := s.service.Launch(c.Context(), request)
	if err != nil {
		// A partially guarded batch still reports what was created, so the
		// caller can act rather than guess.
		if result.Batch != nil {
			return jsonOK(c, http.StatusAccepted, fiber.Map{
				"batch": result.Batch, "rules": result.Rules, "warning": err.Error(),
			})
		}
		return err
	}
	return jsonOK(c, http.StatusCreated, fiber.Map{
		"batch": result.Batch, "rules": result.Rules,
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
	guards := make([]string, 0)
	for _, rule := range request.SharedRules {
		guards = append(guards, rule.Guard.Describe(""))
	}
	for _, rules := range request.AccountRules {
		for _, rule := range rules {
			guards = append(guards, rule.Guard.Describe(""))
		}
	}
	return jsonOK(c, http.StatusOK, fiber.Map{
		"hierarchy":   hierarchy,
		"guards":      guards,
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
