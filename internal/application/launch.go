package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// LaunchRequest publishes a campaign hierarchy across ad accounts and attaches
// its stop conditions in the same call.
//
// The two belong together. Creating a batch and then remembering to add a
// spend guard leaves a window where the campaign is live and unguarded, and
// that window is exactly when a misconfigured launch does its damage.
type LaunchRequest struct {
	CreateBatchRequest
	// Form is the field-by-field alternative to supplying Hierarchy directly.
	// When present it is composed into Hierarchy, so the API accepts either
	// and the UI never has to assemble Meta's nested shape by hand.
	Form *LaunchForm `json:"form,omitempty"`
	// SharedRules apply to every account in the batch.
	SharedRules []LaunchRuleRequest `json:"shared_rules,omitempty"`
	// AccountRules are keyed by ad account ID and apply to that account only.
	// They are additional to SharedRules, not a replacement.
	AccountRules map[string][]LaunchRuleRequest `json:"account_rules,omitempty"`
	// MirrorToMeta also registers each guard in Meta's own rules library, so
	// it keeps working while this service is down. Meta evaluates on its own
	// far slower schedule, so this is a backstop rather than a substitute.
	MirrorToMeta bool `json:"mirror_to_meta,omitempty"`
}

// LaunchResult reports what was created.
type LaunchResult struct {
	Batch *domain.Batch            `json:"batch"`
	Rules []*domain.AutomationRule `json:"rules"`
	// Mirrors lists the guards also registered inside Meta. Fewer mirrors
	// than rules is normal: Meta's rule vocabulary is narrower, and a guard
	// it cannot express is left unmirrored rather than approximated.
	Mirrors []*domain.RuleMirror `json:"mirrors,omitempty"`
}

// Launch creates the batch and its guards.
// PreviewLaunch composes the form and reports what would be sent, without
// contacting Meta at all.
//
// Separate from the dry run on purpose: this catches the mistakes that are
// ours - a missing page, both budgets set, a lifetime budget with no end date
// - instantly and for free, before spending a Graph call to learn the same
// thing more slowly.
func (s *Service) PreviewLaunch(request LaunchRequest) (meta.HierarchySpec, error) {
	return request.resolveHierarchy()
}

// resolveHierarchy returns the hierarchy to publish, composing the form when
// one was supplied.
func (r *LaunchRequest) resolveHierarchy() (meta.HierarchySpec, error) {
	if r.Form == nil {
		return r.Hierarchy, nil
	}
	return r.Form.Compose()
}

func (s *Service) Launch(ctx context.Context, request LaunchRequest) (LaunchResult, error) {
	composed, err := request.resolveHierarchy()
	if err != nil {
		return LaunchResult{}, err
	}
	request.Hierarchy = composed

	// Guards are validated before anything is created. A rejected guard after
	// a successful publish would leave live campaigns with no stop condition,
	// which is the one outcome this endpoint exists to prevent.
	if err := s.validateLaunchRules(request); err != nil {
		return LaunchResult{}, err
	}

	currency := s.launchCurrency(ctx, request.AdAccountIDs)

	batch, err := s.CreateBatch(ctx, request.CreateBatchRequest)
	if err != nil {
		return LaunchResult{}, err
	}

	created := make([]*domain.AutomationRule, 0, len(request.SharedRules))
	var failures []error

	for _, ruleRequest := range request.SharedRules {
		rule, ruleErr := s.createLaunchRule(ctx, request.ConnectionID, batch.ID, nil, ruleRequest, currency)
		if ruleErr != nil {
			failures = append(failures, ruleErr)
			continue
		}
		created = append(created, rule)
	}

	for accountID, ruleRequests := range request.AccountRules {
		parsed, parseErr := uuid.Parse(accountID)
		if parseErr != nil {
			failures = append(failures, invalid("account_rules", fmt.Sprintf("%q is not a UUID", accountID)))
			continue
		}
		for _, ruleRequest := range ruleRequests {
			rule, ruleErr := s.createLaunchRule(ctx, request.ConnectionID, batch.ID, &parsed, ruleRequest, currency)
			if ruleErr != nil {
				failures = append(failures, ruleErr)
				continue
			}
			created = append(created, rule)
		}
	}

	if len(failures) > 0 {
		// The batch is already queued and cannot be unmade, so the guards
		// that did attach are reported alongside the failures rather than
		// discarded. A caller seeing this must decide whether to stop the
		// batch or add the missing guard by hand.
		s.audit(ctx, domain.AuditEvent{
			ConnectionID: &request.ConnectionID,
			ActorType:    "user",
			Action:       "launch.guards_incomplete",
			EntityType:   "batch",
			EntityID:     batch.ID.String(),
			Severity:     domain.AuditWarning,
			Metadata: domain.MustJSON(map[string]any{
				"attached": len(created),
				"failed":   len(failures),
			}),
		})
		return LaunchResult{Batch: batch, Rules: created}, fmt.Errorf(
			"batch %s was queued but %d guard(s) failed to attach: %w",
			batch.ID, len(failures), failures[0],
		)
	}

	mirrors := s.mirrorLaunchRules(ctx, request, created)

	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &request.ConnectionID,
		ActorType:    "user",
		Action:       "launch.created",
		EntityType:   "batch",
		EntityID:     batch.ID.String(),
		Severity:     domain.AuditInfo,
		Metadata: domain.MustJSON(map[string]any{
			"ad_accounts": len(request.AdAccountIDs),
			"guards":      len(created),
			"mirrors":     len(mirrors),
		}),
	})
	return LaunchResult{Batch: batch, Rules: created, Mirrors: mirrors}, nil
}

// mirrorLaunchRules registers each guard inside Meta as a backstop.
//
// Best-effort on purpose: the primary guard is already active, and a missing
// backstop is a far smaller problem than a launch that failed because Meta's
// rules library was briefly unavailable. Failures are audited, not raised.
func (s *Service) mirrorLaunchRules(
	ctx context.Context,
	request LaunchRequest,
	rules []*domain.AutomationRule,
) []*domain.RuleMirror {
	if !request.MirrorToMeta || len(rules) == 0 {
		return nil
	}
	var mirrors []*domain.RuleMirror
	for _, rule := range rules {
		targets := request.AdAccountIDs
		// A per-account guard mirrors only onto its own account.
		if rule.AdAccountID != nil {
			targets = []uuid.UUID{*rule.AdAccountID}
		}
		for _, accountID := range targets {
			mirror, err := s.MirrorRuleToMeta(ctx, rule, accountID)
			if err != nil {
				s.audit(ctx, domain.AuditEvent{
					ConnectionID: &request.ConnectionID,
					ActorType:    "user",
					Action:       "launch.mirror_failed",
					EntityType:   "automation_rule",
					EntityID:     rule.ID.String(),
					Severity:     domain.AuditWarning,
					Metadata: domain.MustJSON(map[string]string{
						"ad_account_id": accountID.String(),
						"error":         truncateError(err),
					}),
				})
				continue
			}
			mirrors = append(mirrors, mirror)
		}
	}
	return mirrors
}

func (s *Service) validateLaunchRules(request LaunchRequest) error {
	for _, rule := range request.SharedRules {
		if _, err := rule.ToCreateRule("USD"); err != nil {
			return err
		}
	}
	for accountID, rules := range request.AccountRules {
		if _, err := uuid.Parse(accountID); err != nil {
			return invalid("account_rules", fmt.Sprintf("%q is not a UUID", accountID))
		}
		for _, rule := range rules {
			if _, err := rule.ToCreateRule("USD"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) createLaunchRule(
	ctx context.Context,
	connectionID, batchID uuid.UUID,
	adAccountID *uuid.UUID,
	request LaunchRuleRequest,
	currency string,
) (*domain.AutomationRule, error) {
	create, err := request.ToCreateRule(currency)
	if err != nil {
		return nil, err
	}
	create.ConnectionID = connectionID
	create.BatchID = &batchID
	create.AdAccountID = adAccountID
	create.Status = domain.RuleActive
	return s.CreateRule(ctx, create)
}

// launchCurrency reports the currency shared by the targeted accounts, used
// only to phrase a guard's description. Mixed currencies fall back to empty
// so the description does not claim a precision it does not have.
func (s *Service) launchCurrency(ctx context.Context, adAccountIDs []uuid.UUID) string {
	currency := ""
	for _, id := range adAccountIDs {
		account, err := s.Repos.Inventory.GetAdAccount(ctx, id)
		if err != nil {
			continue
		}
		if currency == "" {
			currency = account.Currency
			continue
		}
		if currency != account.Currency {
			return ""
		}
	}
	return currency
}
