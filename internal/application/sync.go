package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
)

func (s *Service) EnqueueConnectionSync(ctx context.Context, connectionID uuid.UUID, dedupeSuffix string) (*domain.Job, error) {
	if connectionID == uuid.Nil {
		return nil, invalid("connection_id", "is required")
	}
	if _, err := s.Repos.MetaConnections.Get(ctx, connectionID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dedupeSuffix) == "" {
		dedupeSuffix = uuid.NewString()
	}
	payload, err := jsonValue(SyncJobPayload{ConnectionID: connectionID})
	if err != nil {
		return nil, err
	}
	dedupeKey := connectionID.String() + ":" + dedupeSuffix
	job, _, err := s.Repos.Jobs.Enqueue(ctx, &domain.Job{
		ConnectionID: &connectionID,
		Type:         JobSyncConnection,
		Status:       domain.JobPending,
		Priority:     100,
		Payload:      payload,
		DedupeKey:    &dedupeKey,
		MaxAttempts:  s.Config.Worker.MaxAttempts,
		AvailableAt:  s.Now(),
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Service) SyncConnection(ctx context.Context, connectionID uuid.UUID) (summary SyncSummary, err error) {
	connection, token, err := s.accessToken(ctx, connectionID)
	if err != nil {
		return summary, err
	}
	result, err := s.Meta.Discover(ctx, token, 8)
	if err != nil {
		expired, statusErr := s.markConnectionExpiredForMetaError(ctx, connectionID, err)
		if expired {
			return summary, errors.Join(err, statusErr)
		}
		_ = s.Repos.MetaConnections.SetStatus(ctx, connectionID, domain.MetaConnectionActive, err.Error())
		return summary, err
	}
	now := s.Now()
	summary.ConnectionID = connectionID
	summary.Failures = result.Failures
	summary.SyncedAt = now

	if result.User.Name != "" && result.User.Name != connection.DisplayName {
		_ = s.Repos.MetaConnections.UpdateProfile(ctx, connectionID, result.User.Name, connection.Email, connection.Metadata)
	}

	businessIDs := make(map[string]uuid.UUID, len(result.Businesses))
	for _, source := range result.Businesses {
		record := &domain.Business{
			ConnectionID:       connectionID,
			MetaBusinessID:     source.ID,
			Name:               source.Name,
			VerificationStatus: source.VerificationStatus,
			Vertical:           source.Vertical,
			LastSyncedAt:       now,
			RawJSON:            domain.MustJSON(source),
		}
		if source.PrimaryPage != nil {
			record.PrimaryPageID = source.PrimaryPage.ID
		}
		if err := s.Repos.Inventory.UpsertBusiness(ctx, record); err != nil {
			return summary, fmt.Errorf("upsert business %s: %w", source.ID, err)
		}
		businessIDs[source.ID] = record.ID
		summary.Businesses++
	}

	adAccountIDs := make(map[string]uuid.UUID, len(result.AdAccounts))
	seenAdAccountMetaIDs := make([]string, 0, len(result.AdAccounts))
	for _, source := range result.AdAccounts {
		record := &domain.AdAccount{
			ConnectionID:      connectionID,
			MetaAdAccountID:   meta.AdAccountNodeID(firstNonEmpty(source.AccountID, source.ID)),
			AccountID:         strings.TrimPrefix(firstNonEmpty(source.AccountID, source.ID), "act_"),
			Name:              source.Name,
			Currency:          source.Currency,
			TimezoneName:      source.TimezoneName,
			TimezoneOffsetUTC: source.TimezoneOffsetHoursUTC,
			AccountStatus:     source.AccountStatus,
			DisableReason:     source.DisableReason,
			AmountSpent:       parseMinorAmount(source.AmountSpent),
			Balance:           parseMinorAmount(source.Balance),
			SpendCap:          parseMinorAmount(source.SpendCap),
			Capabilities:      domain.MustJSON(source.Capabilities),
			IsActive:          true,
			RawJSON:           domain.MustJSON(source),
			LastSyncedAt:      now,
		}
		if source.Business != nil {
			record.BusinessName = source.Business.Name
			if localID, ok := businessIDs[source.Business.ID]; ok {
				record.BusinessID = &localID
			}
		}
		if err := s.Repos.Inventory.UpsertAdAccount(ctx, record); err != nil {
			return summary, fmt.Errorf("upsert ad account %s: %w", source.AccountID, err)
		}
		adAccountIDs[record.AccountID] = record.ID
		adAccountIDs[record.MetaAdAccountID] = record.ID
		seenAdAccountMetaIDs = append(seenAdAccountMetaIDs, record.MetaAdAccountID)
		summary.AdAccounts++
	}

	for _, source := range result.Pages {
		// Page access tokens are intentionally never persisted; the encrypted
		// user token remains the single credential source.
		source.AccessToken = ""
		if err := s.upsertAsset(ctx, domain.Asset{
			ConnectionID: connectionID,
			AssetType:    domain.AssetPage,
			MetaAssetID:  source.ID,
			Name:         source.Name,
			Status:       strings.Join(source.Tasks, ","),
			Normalized: domain.MustJSON(map[string]any{
				"category": source.Category,
				"tasks":    source.Tasks,
			}),
			RawJSON:      domain.MustJSON(source),
			LastSyncedAt: now,
			IsActive:     true,
		}); err != nil {
			return summary, err
		}
		summary.Assets++
		if source.InstagramBusinessAccount != nil {
			instagram := *source.InstagramBusinessAccount
			if err := s.upsertAsset(ctx, domain.Asset{
				ConnectionID: connectionID,
				AssetType:    domain.AssetInstagramAccount,
				MetaAssetID:  instagram.ID,
				ParentMetaID: source.ID,
				Name:         firstNonEmpty(instagram.Username, instagram.Name),
				Normalized:   domain.MustJSON(instagram),
				RawJSON:      domain.MustJSON(instagram),
				LastSyncedAt: now,
				IsActive:     true,
			}); err != nil {
				return summary, err
			}
			summary.Assets++
		}
	}

	for accountKey, assets := range result.Assets {
		localAccountID, ok := adAccountIDs[strings.TrimPrefix(accountKey, "act_")]
		if !ok {
			localAccountID = adAccountIDs[meta.AdAccountNodeID(accountKey)]
		}
		var localBusinessID *uuid.UUID
		if assets.AdAccount.Business != nil {
			if id, exists := businessIDs[assets.AdAccount.Business.ID]; exists {
				localBusinessID = &id
			}
		}
		for _, source := range assets.InstagramAccounts {
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, domain.AssetInstagramAccount, source.ID, firstNonEmpty(source.Username, source.Name), source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
		for _, source := range assets.Pixels {
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, domain.AssetPixel, source.ID, source.Name, source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
		for _, source := range assets.Datasets {
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, domain.AssetDataset, firstNonEmpty(source.ID, source.DatasetID), source.Name, source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
		for _, source := range assets.CustomConversions {
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, domain.AssetCustomConversion, source.ID, source.Name, source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
		for _, source := range assets.CustomAudiences {
			assetType := domain.AssetCustomAudience
			if strings.EqualFold(source.Subtype, "LOOKALIKE") {
				assetType = domain.AssetLookalikeAudience
			}
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, assetType, source.ID, source.Name, source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
		for _, source := range assets.Applications {
			if err := s.upsertAsset(ctx, assetRecord(connectionID, localBusinessID, localAccountID, domain.AssetMetaApp, source.ID, source.Name, source, now)); err != nil {
				return summary, err
			}
			summary.Assets++
		}
	}

	reconciliation := databaseInventoryReconciliation(
		connectionID,
		now,
		result,
		adAccountIDs,
		seenAdAccountMetaIDs,
	)
	if err := s.Repos.Inventory.Reconcile(ctx, reconciliation); err != nil {
		return summary, fmt.Errorf("reconcile Meta inventory: %w", err)
	}

	if err := s.Repos.MetaConnections.MarkSynced(ctx, connectionID, now); err != nil {
		return summary, err
	}
	after, _ := jsonValue(summary)
	severity := domain.AuditInfo
	if len(summary.Failures) > 0 {
		severity = domain.AuditWarning
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &connectionID,
		ActorType:    "worker",
		Action:       "meta.connection.synced",
		EntityType:   "meta_connection",
		EntityID:     connectionID.String(),
		Severity:     severity,
		After:        after,
	})
	return summary, nil
}

func databaseInventoryReconciliation(
	connectionID uuid.UUID,
	now time.Time,
	result meta.DiscoveryResult,
	adAccountIDs map[string]uuid.UUID,
	seenAdAccountMetaIDs []string,
) database.InventoryReconciliation {
	input := database.InventoryReconciliation{
		ConnectionID:         connectionID,
		SeenAdAccountMetaIDs: uniqueStrings(seenAdAccountMetaIDs),
		PagesComplete:        !discoveryScopeFailed(result.Failures, "pages", ""),
		ReconciledAt:         now,
	}
	if input.PagesComplete {
		for _, page := range result.Pages {
			if strings.TrimSpace(page.ID) != "" {
				input.SeenPageMetaIDs = append(input.SeenPageMetaIDs, page.ID)
			}
			if page.InstagramBusinessAccount != nil && strings.TrimSpace(page.InstagramBusinessAccount.ID) != "" {
				input.SeenPageInstagramIDs = append(input.SeenPageInstagramIDs, page.InstagramBusinessAccount.ID)
			}
		}
		input.SeenPageMetaIDs = uniqueStrings(input.SeenPageMetaIDs)
		input.SeenPageInstagramIDs = uniqueStrings(input.SeenPageInstagramIDs)
	}

	for accountKey, assets := range result.Assets {
		localAccountID := localAdAccountID(adAccountIDs, accountKey)
		if localAccountID == uuid.Nil {
			continue
		}
		addScope := func(scopeName string, assetType domain.AssetType, ids []string) {
			if discoveryScopeFailed(result.Failures, scopeName, accountKey) {
				return
			}
			input.AccountAssetScopes = append(input.AccountAssetScopes, database.AssetAccessScope{
				AdAccountID: localAccountID,
				AssetType:   assetType,
				SeenMetaIDs: uniqueStrings(ids),
			})
		}

		instagramIDs := make([]string, 0, len(assets.InstagramAccounts))
		for _, asset := range assets.InstagramAccounts {
			instagramIDs = appendNonEmpty(instagramIDs, asset.ID)
		}
		addScope("instagram_accounts", domain.AssetInstagramAccount, instagramIDs)

		pixelIDs := make([]string, 0, len(assets.Pixels))
		for _, asset := range assets.Pixels {
			pixelIDs = appendNonEmpty(pixelIDs, asset.ID)
		}
		addScope("pixels", domain.AssetPixel, pixelIDs)

		conversionIDs := make([]string, 0, len(assets.CustomConversions))
		for _, asset := range assets.CustomConversions {
			conversionIDs = appendNonEmpty(conversionIDs, asset.ID)
		}
		addScope("custom_conversions", domain.AssetCustomConversion, conversionIDs)

		customAudienceIDs := make([]string, 0, len(assets.CustomAudiences))
		lookalikeAudienceIDs := make([]string, 0, len(assets.CustomAudiences))
		for _, asset := range assets.CustomAudiences {
			if strings.EqualFold(asset.Subtype, "LOOKALIKE") {
				lookalikeAudienceIDs = appendNonEmpty(lookalikeAudienceIDs, asset.ID)
			} else {
				customAudienceIDs = appendNonEmpty(customAudienceIDs, asset.ID)
			}
		}
		addScope("custom_audiences", domain.AssetCustomAudience, customAudienceIDs)
		addScope("custom_audiences", domain.AssetLookalikeAudience, lookalikeAudienceIDs)

		applicationIDs := make([]string, 0, len(assets.Applications))
		for _, asset := range assets.Applications {
			applicationIDs = appendNonEmpty(applicationIDs, asset.ID)
		}
		addScope("advertisable_applications", domain.AssetMetaApp, applicationIDs)

		if assets.AdAccount.Business != nil {
			if _, complete := result.BusinessDatasets[assets.AdAccount.Business.ID]; complete {
				datasetIDs := make([]string, 0, len(assets.Datasets))
				for _, asset := range assets.Datasets {
					datasetIDs = appendNonEmpty(datasetIDs, firstNonEmpty(asset.ID, asset.DatasetID))
				}
				input.AccountAssetScopes = append(input.AccountAssetScopes, database.AssetAccessScope{
					AdAccountID: localAccountID,
					AssetType:   domain.AssetDataset,
					SeenMetaIDs: uniqueStrings(datasetIDs),
				})
			}
		}
	}
	return input
}

func localAdAccountID(adAccountIDs map[string]uuid.UUID, accountID string) uuid.UUID {
	accountID = strings.TrimSpace(accountID)
	if id := adAccountIDs[accountID]; id != uuid.Nil {
		return id
	}
	if id := adAccountIDs[strings.TrimPrefix(accountID, "act_")]; id != uuid.Nil {
		return id
	}
	return adAccountIDs[meta.AdAccountNodeID(accountID)]
}

func discoveryScopeFailed(failures []meta.DiscoveryFailure, scope, accountID string) bool {
	normalizedAccountID := strings.TrimPrefix(strings.TrimSpace(accountID), "act_")
	for _, failure := range failures {
		if failure.Scope != scope {
			continue
		}
		if normalizedAccountID == "" ||
			strings.TrimPrefix(strings.TrimSpace(failure.AccountID), "act_") == normalizedAccountID {
			return true
		}
	}
	return false
}

func appendNonEmpty(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(values, value)
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func (s *Service) upsertAsset(ctx context.Context, asset domain.Asset) error {
	if asset.MetaAssetID == "" {
		return nil
	}
	if asset.Normalized == nil {
		asset.Normalized = emptyObject()
	}
	if asset.RawJSON == nil {
		asset.RawJSON = emptyObject()
	}
	return s.Repos.Inventory.UpsertAsset(ctx, &asset)
}

func assetRecord(
	connectionID uuid.UUID,
	businessID *uuid.UUID,
	adAccountID uuid.UUID,
	assetType domain.AssetType,
	metaID, name string,
	source any,
	now time.Time,
) domain.Asset {
	var accountID *uuid.UUID
	if adAccountID != uuid.Nil {
		accountID = &adAccountID
	}
	raw := domain.MustJSON(source)
	return domain.Asset{
		ConnectionID: connectionID,
		BusinessID:   businessID,
		AdAccountID:  accountID,
		AssetType:    assetType,
		MetaAssetID:  metaID,
		Name:         name,
		IsActive:     true,
		Normalized:   raw,
		RawJSON:      raw,
		LastSyncedAt: now,
	}
}

func parseMinorAmount(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
