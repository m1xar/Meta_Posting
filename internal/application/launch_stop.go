package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
	"github.com/watchers-factory/raze-ads/internal/meta"
	"github.com/watchers-factory/raze-ads/internal/platform/database"
)

// StopBatchResult reports what was paused.
type StopBatchResult struct {
	BatchID uuid.UUID `json:"batch_id"`
	Paused  int       `json:"paused"`
	Skipped int       `json:"skipped"`
	Failed  int       `json:"failed"`
}

// StopBatch pauses everything a batch published.
//
// The launcher could start spend but nothing could stop it: disabling a guard
// only stops the guard, and the only remaining route was Ads Manager. A
// system that can start spending has to be able to stop it from the same
// place.
//
// Campaigns are paused first. Pausing a campaign stops every ad set and ad
// beneath it immediately, so the spend stops at the first call rather than
// after walking the whole tree - which matters when the reason for stopping
// is that money is going somewhere unintended.
func (s *Service) StopBatch(ctx context.Context, batchID uuid.UUID) (StopBatchResult, error) {
	batch, err := s.Repos.Batches.Get(ctx, batchID)
	if err != nil {
		return StopBatchResult{}, err
	}
	page, err := s.Repos.Batches.ListPublishedObjects(ctx, database.PublishedObjectFilter{
		BatchID: &batchID,
		Page:    domain.PageRequest{Limit: domain.MaxPageLimit},
	})
	if err != nil {
		return StopBatchResult{}, err
	}
	objects := page.Items
	_, token, err := s.accessToken(ctx, batch.ConnectionID)
	if err != nil {
		return StopBatchResult{}, err
	}

	result := StopBatchResult{BatchID: batchID}
	now := s.Now()
	var failures []error

	// Campaign, then ad set, then ad. A creative has no status of its own.
	for _, level := range []domain.PublishedObjectType{
		domain.PublishedCampaign, domain.PublishedAdSet, domain.PublishedAd,
	} {
		for index := range objects {
			object := &objects[index]
			if object.ObjectType != level {
				continue
			}
			if object.EffectiveStatus == string(meta.StatusPaused) {
				result.Skipped++
				continue
			}
			if err := s.Meta.SetEntityStatus(ctx, token, object.MetaObjectID, meta.StatusPaused); err != nil {
				failures = append(failures, fmt.Errorf("pause %s %s: %w", level, object.MetaObjectID, err))
				result.Failed++
				continue
			}
			if err := s.Repos.Batches.UpdatePublishedStatus(
				ctx, object.ID, string(meta.StatusPaused),
				domain.MustJSON(map[string]string{"source": "stop_batch"}), now,
			); err != nil {
				failures = append(failures, err)
			}
			result.Paused++
		}
	}

	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &batch.ConnectionID,
		ActorType:    "user",
		Action:       "launch.stopped",
		EntityType:   "batch",
		EntityID:     batchID.String(),
		Severity:     domain.AuditWarning,
		Metadata:     domain.MustJSON(result),
	})
	return result, errors.Join(failures...)
}
