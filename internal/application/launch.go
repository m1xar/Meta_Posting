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
	// Checkpoints is the spend ladder guarding every campaign this batch
	// publishes. Empty means the batch launches unguarded, which the UI
	// treats as an explicit choice rather than a default.
	Checkpoints []GuardCheckpoint `json:"checkpoints,omitempty"`
	GuardName   string            `json:"guard_name,omitempty"`
	// GuardIntervalSeconds overrides the default evaluation cadence.
	GuardIntervalSeconds int64 `json:"guard_interval_seconds,omitempty"`
}

// LaunchResult reports what was created.
type LaunchResult struct {
	Batch *domain.Batch         `json:"batch"`
	Guard *domain.CampaignGuard `json:"guard,omitempty"`
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

	// The ladder is validated before anything is created. A rejected guard
	// after a successful publish would leave live campaigns with no stop
	// condition, which is the one outcome this endpoint exists to prevent.
	if len(request.Checkpoints) > 0 {
		if _, err := normalizeCheckpoints(request.Checkpoints); err != nil {
			return LaunchResult{}, err
		}
		// Tracker checkpoints only work when the click reaches Keitaro. The
		// launcher auto-injects the campaign-id macro so it is always present,
		// but whether the destination link actually routes through the tracker
		// is the operator's call, made against the explicit warning shown
		// before launch rather than guessed at here.
	}

	batch, err := s.CreateBatch(ctx, request.CreateBatchRequest)
	if err != nil {
		return LaunchResult{}, err
	}

	var guard *domain.CampaignGuard
	if len(request.Checkpoints) > 0 {
		name := request.GuardName
		if name == "" {
			name = "Guard " + batch.Name
		}
		guard, err = s.CreateGuard(ctx, CreateGuardRequest{
			ConnectionID:              request.ConnectionID,
			BatchID:                   &batch.ID,
			Name:                      name,
			Checkpoints:               request.Checkpoints,
			EvaluationIntervalSeconds: request.GuardIntervalSeconds,
		})
		if err != nil {
			// The batch is already queued and cannot be unmade. A caller
			// seeing this must decide whether to stop the batch or attach
			// the guard by hand.
			s.audit(ctx, domain.AuditEvent{
				ConnectionID: &request.ConnectionID,
				ActorType:    "user",
				Action:       "launch.guard_failed",
				EntityType:   "batch",
				EntityID:     batch.ID.String(),
				Severity:     domain.AuditWarning,
				Metadata:     domain.MustJSON(map[string]any{"error": err.Error()}),
			})
			return LaunchResult{Batch: batch}, fmt.Errorf(
				"batch %s was queued but its guard failed to attach: %w", batch.ID, err,
			)
		}
	}

	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &request.ConnectionID,
		ActorType:    "user",
		Action:       "launch.created",
		EntityType:   "batch",
		EntityID:     batch.ID.String(),
		Severity:     domain.AuditInfo,
		Metadata: domain.MustJSON(map[string]any{
			"ad_accounts": len(request.AdAccountIDs),
			"guarded":     guard != nil,
		}),
	})
	return LaunchResult{Batch: batch, Guard: guard}, nil
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
