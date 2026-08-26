package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
)

// MirrorRuleToMeta registers a copy of a rule in Meta's own automated-rules
// library, so the guard survives this service being unavailable.
//
// Mirroring is best-effort by design. A failure here must never fail the
// launch: the primary guard - this service's own minute-level evaluation - is
// already in place, and a missing backstop is a smaller problem than a
// campaign that did not launch. Failures are recorded rather than raised.
func (s *Service) MirrorRuleToMeta(
	ctx context.Context,
	rule *domain.AutomationRule,
	adAccountID uuid.UUID,
) (*domain.RuleMirror, error) {
	spec, err := s.adRuleSpecFor(rule)
	if err != nil {
		// A guard Meta's vocabulary cannot express stays with this service
		// alone. Approximating it would produce a backstop that pauses the
		// wrong thing, which is worse than having none.
		return nil, err
	}
	account, err := s.Repos.Inventory.GetAdAccount(ctx, adAccountID)
	if err != nil {
		return nil, err
	}
	_, token, err := s.accessToken(ctx, account.ConnectionID)
	if err != nil {
		return nil, err
	}

	created, err := s.Meta.CreateAdRule(ctx, token, account.AccountID, spec)
	mirror := &domain.RuleMirror{
		RuleID:      rule.ID,
		AdAccountID: adAccountID,
		Status:      "active",
	}
	if err != nil {
		mirror.Status = "failed"
		mirror.LastError = truncateError(err)
		if storeErr := s.Repos.Rules.UpsertMirror(ctx, mirror); storeErr != nil {
			return nil, errors.Join(err, storeErr)
		}
		return mirror, err
	}
	mirror.MetaRuleID = created.ID
	if err := s.Repos.Rules.UpsertMirror(ctx, mirror); err != nil {
		return nil, err
	}
	return mirror, nil
}

// RemoveRuleMirrors deletes the Meta-side copies of a rule.
//
// Called when a rule is disabled: a backstop that outlives the intent behind
// it would keep pausing campaigns nobody is guarding any more.
func (s *Service) RemoveRuleMirrors(ctx context.Context, ruleID uuid.UUID) error {
	mirrors, err := s.Repos.Rules.ActiveMirrors(ctx, ruleID)
	if err != nil {
		return err
	}
	var failures []error
	for index := range mirrors {
		mirror := &mirrors[index]
		account, accountErr := s.Repos.Inventory.GetAdAccount(ctx, mirror.AdAccountID)
		if accountErr != nil {
			failures = append(failures, accountErr)
			continue
		}
		_, token, tokenErr := s.accessToken(ctx, account.ConnectionID)
		if tokenErr != nil {
			failures = append(failures, tokenErr)
			continue
		}
		if deleteErr := s.Meta.DeleteAdRule(ctx, token, mirror.MetaRuleID); deleteErr != nil {
			failures = append(failures, deleteErr)
			continue
		}
		if markErr := s.Repos.Rules.MarkMirrorRemoved(ctx, mirror.ID, s.Now()); markErr != nil {
			failures = append(failures, markErr)
		}
	}
	return errors.Join(failures...)
}

// adRuleSpecFor translates one of this service's rules into Meta's vocabulary,
// or reports that it cannot be translated.
func (s *Service) adRuleSpecFor(rule *domain.AutomationRule) (meta.AdRuleSpec, error) {
	entityType, err := meta.AdRuleEntityType(string(rule.ScopeLevel))
	if err != nil {
		return meta.AdRuleSpec{}, err
	}
	var metadata struct {
		GuardKind  string  `json:"guard_kind"`
		GuardSpend float64 `json:"guard_spend"`
	}
	if len(rule.Metadata) > 0 {
		_ = rule.Metadata.Decode(&metadata)
	}
	if metadata.GuardSpend <= 0 {
		return meta.AdRuleSpec{}, fmt.Errorf(
			"rule %s was not created by the launcher and has no mirrorable guard", rule.ID)
	}

	switch GuardKind(metadata.GuardKind) {
	case GuardSpendCap:
		return meta.SpendCapAdRule(rule.Name, entityType, metadata.GuardSpend), nil
	case GuardSpendCheck:
		field, minimum, ok := mirrorableCheck(rule)
		if !ok {
			return meta.AdRuleSpec{}, fmt.Errorf(
				"rule %s checks a metric Meta's rule vocabulary does not expose", rule.ID)
		}
		return meta.SpendCheckAdRule(rule.Name, entityType, metadata.GuardSpend, field, minimum), nil
	default:
		return meta.AdRuleSpec{}, fmt.Errorf("rule %s has no mirrorable guard kind", rule.ID)
	}
}

// metaRuleFields maps the metrics this service guards on to the field names
// Meta's rule engine understands. Deliberately short: anything absent stays
// unmirrored rather than being approximated by a near-enough field.
var metaRuleFields = map[string]string{
	"impressions":        "impressions",
	"clicks":             "clicks",
	"inline_link_clicks": "link_click",
}

func mirrorableCheck(rule *domain.AutomationRule) (string, float64, bool) {
	var conditions struct {
		Conditions []struct {
			Metric    string  `json:"metric"`
			Threshold float64 `json:"threshold"`
		} `json:"conditions"`
	}
	if rule.Conditions.Decode(&conditions) != nil {
		return "", 0, false
	}
	for _, condition := range conditions.Conditions {
		if condition.Metric == "spend" {
			continue
		}
		field, ok := metaRuleFields[condition.Metric]
		if !ok {
			return "", 0, false
		}
		return field, condition.Threshold, true
	}
	return "", 0, false
}
