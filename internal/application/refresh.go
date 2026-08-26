package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

// RefreshSummary reports what a manual refresh queued.
type RefreshSummary struct {
	Connections int `json:"connections"`
	Accounts    int `json:"accounts"`
	Jobs        int `json:"jobs"`
}

// RefreshUserData queues an immediate re-sync of everything behind the
// dashboard for one tenant: campaign/ad set/ad inventory and the last two
// days of account and campaign insights for every account on an active
// connection, plus one Keitaro report pass.
//
// Dedupe keys are bucketed to ten minutes, so mashing the button queues each
// piece of work at most once per bucket instead of stacking duplicates.
func (s *Service) RefreshUserData(ctx context.Context, userID uuid.UUID) (RefreshSummary, error) {
	summary := RefreshSummary{}
	var connections []domain.MetaConnection
	if err := s.Repos.DB().WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, domain.MetaConnectionActive).
		Find(&connections).Error; err != nil {
		return summary, err
	}
	summary.Connections = len(connections)
	if len(connections) == 0 {
		return summary, nil
	}
	connectionIDs := make([]uuid.UUID, 0, len(connections))
	for _, connection := range connections {
		connectionIDs = append(connectionIDs, connection.ID)
	}
	var accounts []domain.AdAccount
	if err := s.Repos.DB().WithContext(ctx).
		Select("id, connection_id").
		Where("connection_id IN ? AND is_active = true", connectionIDs).
		Find(&accounts).Error; err != nil {
		return summary, err
	}
	summary.Accounts = len(accounts)

	now := s.Now()
	bucket := fmt.Sprintf("%d", now.UTC().Truncate(10*time.Minute).Unix())
	since := now.UTC().AddDate(0, 0, -2).Format("2006-01-02")
	until := now.UTC().Format("2006-01-02")

	var failures []error
	enqueue := func(connectionID uuid.UUID, jobType, dedupe string, payload any) {
		key := dedupe + ":" + bucket
		id := connectionID
		_, created, err := s.Repos.Jobs.Enqueue(ctx, &domain.Job{
			ConnectionID: &id,
			Type:         jobType,
			Status:       domain.JobPending,
			Priority:     25, // Someone is waiting at the screen.
			Payload:      domain.MustJSON(payload),
			DedupeKey:    &key,
			MaxAttempts:  3,
			AvailableAt:  now.UTC(),
		})
		if err != nil {
			failures = append(failures, err)
			return
		}
		if created {
			summary.Jobs++
		}
	}

	for _, account := range accounts {
		enqueue(account.ConnectionID, JobSyncAdEntities,
			"manual:entities:"+account.ID.String(),
			AdEntitiesJobPayload{AdAccountID: account.ID})
		for _, level := range []domain.InsightLevel{domain.InsightAccount, domain.InsightCampaign} {
			enqueue(account.ConnectionID, JobCollectAccountInsights,
				"manual:insights:"+string(level)+":"+account.ID.String(),
				AccountInsightsJobPayload{
					AdAccountID: account.ID,
					Level:       level,
					Since:       since,
					Until:       until,
					Reason:      "manual_refresh",
				})
		}
	}

	// The tracker job is executed by the worker, which owns the Keitaro
	// client; it no-ops harmlessly when the tracker is not configured.
	{
		key := "manual:tracker:" + bucket
		_, created, err := s.Repos.Jobs.Enqueue(ctx, &domain.Job{
			Type:        JobSyncTracker,
			Status:      domain.JobPending,
			Priority:    25,
			Payload:     domain.MustJSON(SyncTrackerJobPayload{}),
			DedupeKey:   &key,
			MaxAttempts: 3,
			AvailableAt: now.UTC(),
		})
		if err != nil {
			failures = append(failures, err)
		} else if created {
			summary.Jobs++
		}
	}
	return summary, errors.Join(failures...)
}
