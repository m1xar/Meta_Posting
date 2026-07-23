package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/watchers-factory/raze-posting/internal/domain"
	"github.com/watchers-factory/raze-posting/internal/meta"
	"github.com/watchers-factory/raze-posting/internal/platform/database"
	"gorm.io/gorm"
)

func (s *Service) CreateBatch(ctx context.Context, request CreateBatchRequest) (*domain.Batch, error) {
	if request.ConnectionID == uuid.Nil {
		return nil, invalid("connection_id", "is required")
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, invalid("idempotency_key", "is required")
	}
	if len(request.AdAccountIDs) == 0 {
		return nil, invalid("ad_account_ids", "at least one account is required")
	}
	if len(request.AdAccountIDs) > 1000 {
		return nil, invalid("ad_account_ids", "cannot contain more than 1000 accounts")
	}
	if request.Tree != nil && hierarchyProvided(request.Hierarchy) {
		return nil, invalid("tree", "cannot be combined with hierarchy")
	}
	specification, err := jsonValue(request)
	if err != nil {
		return nil, err
	}
	if existing, err := s.Repos.Batches.FindByIdempotencyKey(ctx, request.ConnectionID, request.IdempotencyKey); err == nil {
		if !sameJSON(existing.Specification, specification) {
			return nil, fmt.Errorf("%w: idempotency key was already used with a different batch request", ErrConflict)
		}
		return existing, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if _, err := s.Repos.MetaConnections.Get(ctx, request.ConnectionID); err != nil {
		return nil, err
	}

	selected := make(map[uuid.UUID]struct{}, len(request.AdAccountIDs))
	results := make([]domain.BatchAccountResult, 0, len(request.AdAccountIDs))
	for _, accountID := range request.AdAccountIDs {
		if accountID == uuid.Nil {
			return nil, invalid("ad_account_ids", "contains an empty UUID")
		}
		if _, duplicate := selected[accountID]; duplicate {
			return nil, invalid("ad_account_ids", "contains duplicates")
		}
		selected[accountID] = struct{}{}
		account, err := s.Repos.Inventory.GetAdAccount(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("load ad account %s: %w", accountID, err)
		}
		if err := validateBatchAdAccount(account, request.ConnectionID); err != nil {
			return nil, err
		}
		plan := AccountPublishPlan{
			MediaBindings: request.MediaBindings,
			ValidateOnly:  request.ValidateOnly,
			LeavePaused:   request.LeavePaused,
		}
		if request.Tree != nil {
			tree, treeErr := campaignTreeForAccount(*request.Tree, request.AccountOverrides[accountID.String()])
			if treeErr != nil {
				return nil, invalid("account_overrides."+accountID.String(), treeErr.Error())
			}
			if treeErr := tree.Validate(); treeErr != nil {
				return nil, invalid("tree", fmt.Sprintf("account %s: %v", accountID, treeErr))
			}
			plan.Tree = &tree
		} else {
			hierarchy, hierarchyErr := hierarchyForAccount(request.Hierarchy, request.AccountOverrides[accountID.String()])
			if hierarchyErr != nil {
				return nil, invalid("account_overrides."+accountID.String(), hierarchyErr.Error())
			}
			if hierarchyErr := hierarchy.Validate(); hierarchyErr != nil {
				return nil, invalid("hierarchy", fmt.Sprintf("account %s: %v", accountID, hierarchyErr))
			}
			plan.Hierarchy = hierarchy
		}
		results = append(results, domain.BatchAccountResult{
			AdAccountID:  accountID,
			Status:       domain.BatchAccountPending,
			RequestJSON:  domain.MustJSON(plan),
			ResponseJSON: emptyObject(),
		})
	}
	for rawID := range request.AccountOverrides {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, invalid("account_overrides", "keys must be ad-account UUIDs")
		}
		if _, ok := selected[id]; !ok {
			return nil, invalid("account_overrides", "contains an account not selected in ad_account_ids")
		}
	}
	for index, binding := range request.MediaBindings {
		if binding.MediaID == uuid.Nil {
			return nil, invalid(fmt.Sprintf("media_bindings[%d].media_id", index), "is required")
		}
		validTarget := strings.HasPrefix(binding.Target, "/creative/")
		if request.Tree != nil {
			validTarget = strings.HasPrefix(binding.Target, "/ad_sets/")
		}
		if !validTarget {
			return nil, invalid(
				fmt.Sprintf("media_bindings[%d].target", index),
				"must point below /creative for hierarchy or /ad_sets for tree",
			)
		}
		if _, err := s.Repos.Media.Get(ctx, binding.MediaID); err != nil {
			return nil, fmt.Errorf("load media binding %s: %w", binding.MediaID, err)
		}
	}

	batch := &domain.Batch{
		ConnectionID:   request.ConnectionID,
		Name:           strings.TrimSpace(request.Name),
		Status:         domain.BatchQueued,
		IdempotencyKey: request.IdempotencyKey,
		Specification:  specification,
		CreatedBy:      request.CreatedBy,
	}
	if err := s.Repos.Transaction(ctx, func(repositories *database.Repositories) error {
		if err := repositories.Batches.Create(ctx, batch, results); err != nil {
			return err
		}
		now := s.Now()
		for index := range results {
			payload := domain.MustJSON(PublishJobPayload{ResultID: results[index].ID})
			dedupeKey := results[index].ID.String()
			if _, _, err := repositories.Jobs.Enqueue(ctx, &domain.Job{
				ConnectionID: &request.ConnectionID,
				Type:         JobPublishAccount,
				Status:       domain.JobPending,
				Priority:     50,
				Payload:      payload,
				DedupeKey:    &dedupeKey,
				MaxAttempts:  s.Config.Worker.MaxAttempts,
				AvailableAt:  now,
			}); err != nil {
				return fmt.Errorf("enqueue account %s: %w", results[index].AdAccountID, err)
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			existing, lookupErr := s.Repos.Batches.FindByIdempotencyKey(ctx, request.ConnectionID, request.IdempotencyKey)
			if lookupErr == nil {
				if !sameJSON(existing.Specification, specification) {
					return nil, fmt.Errorf("%w: idempotency key was already used with a different batch request", ErrConflict)
				}
				return existing, nil
			}
		}
		return nil, err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &request.ConnectionID,
		ActorType:    "internal_api",
		ActorID:      request.CreatedBy,
		Action:       "batch.created",
		EntityType:   "batch",
		EntityID:     batch.ID.String(),
		After: domain.MustJSON(map[string]any{
			"name":           batch.Name,
			"total_accounts": len(results),
			"validate_only":  request.ValidateOnly,
			"leave_paused":   request.LeavePaused,
		}),
	})
	return batch, nil
}

func hierarchyProvided(hierarchy meta.HierarchySpec) bool {
	return hierarchy.Campaign.Name != "" ||
		hierarchy.Campaign.Objective != "" ||
		hierarchy.AdSet.Name != "" ||
		hierarchy.Creative.Name != "" ||
		hierarchy.Ad.Name != ""
}

func validateBatchAdAccount(account *domain.AdAccount, connectionID uuid.UUID) error {
	if account.ConnectionID != connectionID {
		return invalid("ad_account_ids", "contains an account from another connection")
	}
	if !account.IsActive {
		return invalid("ad_account_ids", "contains an account no longer accessible through this connection")
	}
	var permissions struct {
		UserTasks []string `json:"user_tasks"`
	}
	if account.RawJSON.Decode(&permissions) == nil && len(permissions.UserTasks) > 0 {
		canAdvertise := false
		for _, task := range permissions.UserTasks {
			if task == "ADVERTISE" || task == "MANAGE" {
				canAdvertise = true
				break
			}
		}
		if !canAdvertise {
			return invalid(
				"ad_account_ids",
				"contains an account without Meta ADVERTISE or MANAGE permission",
			)
		}
	}
	return nil
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if decodeJSONUseNumber(left, &leftValue) != nil || decodeJSONUseNumber(right, &rightValue) != nil {
		return bytes.Equal(bytes.TrimSpace(left), bytes.TrimSpace(right))
	}
	leftCanonical, leftErr := json.Marshal(leftValue)
	rightCanonical, rightErr := json.Marshal(rightValue)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func (s *Service) PublishAccountResult(ctx context.Context, resultID uuid.UUID) (returnErr error) {
	accountResult, err := s.Repos.Batches.GetAccountResult(ctx, resultID)
	if err != nil {
		return err
	}
	batch, err := s.Repos.Batches.Get(ctx, accountResult.BatchID)
	if err != nil {
		return err
	}
	defer func() {
		if !isMetaAccessTokenError(returnErr) {
			return
		}
		persistCtx, cancelPersist := publishCleanupContext(ctx)
		defer cancelPersist()
		_, statusErr := s.markConnectionExpiredForMetaError(
			persistCtx,
			batch.ConnectionID,
			returnErr,
		)
		returnErr = errors.Join(returnErr, statusErr)
	}()
	account, err := s.Repos.Inventory.GetAdAccount(ctx, accountResult.AdAccountID)
	if err != nil {
		return err
	}
	if account.ConnectionID != batch.ConnectionID {
		return errors.New("batch result account does not belong to the batch connection")
	}
	if err := s.Repos.Batches.MarkAccountRunning(ctx, resultID, s.Now()); err != nil {
		return err
	}
	var plan AccountPublishPlan
	if err := accountResult.RequestJSON.Decode(&plan); err != nil {
		return s.finishPublishFailure(ctx, batch.ConnectionID, accountResult, nil, fmt.Errorf("decode publish plan: %w", err))
	}
	if plan.Tree != nil {
		return s.publishTreeAccountResult(ctx, batch, accountResult, account, plan)
	}
	applyPublishMarker(&plan.Hierarchy, accountResult.ID)
	checkpoints, err := s.Repos.Batches.ListResultPublishedObjects(ctx, accountResult.ID)
	if err != nil {
		return err
	}
	checkpointByType := make(map[domain.PublishedObjectType]*domain.PublishedObject, len(checkpoints))
	for index := range checkpoints {
		checkpoint := &checkpoints[index]
		checkpointByType[checkpoint.ObjectType] = checkpoint
	}
	_, token, err := s.accessToken(ctx, batch.ConnectionID)
	if err != nil {
		return s.finishPublishFailure(ctx, batch.ConnectionID, accountResult, nil, err)
	}
	// A prior attempt may have received a Meta object ID but lost the local
	// checkpoint during a database outage. Exact, uniquely tagged names let us
	// reconcile that upstream result before issuing another create.
	if accountResult.Attempts > 0 && !plan.ValidateOnly {
		if err := s.reconcilePublishRetry(
			ctx,
			token,
			batch,
			accountResult,
			account,
			plan,
			checkpointByType,
		); err != nil {
			return err
		}
	}
	if checkpointByType[domain.PublishedCreative] == nil {
		uploadedMedia := make(map[uuid.UUID]string, len(plan.MediaBindings))
		for _, binding := range plan.MediaBindings {
			replacement, uploaded := uploadedMedia[binding.MediaID]
			if !uploaded {
				var uploadErr error
				replacement, uploadErr = s.uploadMediaForAccount(ctx, token, account, binding.MediaID)
				if uploadErr != nil {
					return s.finishPublishFailure(ctx, batch.ConnectionID, accountResult, nil, fmt.Errorf("upload media %s: %w", binding.MediaID, uploadErr))
				}
				uploadedMedia[binding.MediaID] = replacement
			}
			if err := setHierarchyJSONPointer(&plan.Hierarchy, binding.Target, replacement); err != nil {
				return s.finishPublishFailure(ctx, batch.ConnectionID, accountResult, nil, fmt.Errorf("apply media binding %s: %w", binding.Target, err))
			}
		}
	}
	resume := resumeFromCheckpoints(checkpointByType)
	resolvedRequestJSON := domain.MustJSON(plan)
	onStage := func(stage meta.PublishStage) error {
		if objectType, ok := createdObjectType(stage.Name); ok {
			checkpoint := checkpointForStage(
				batch,
				accountResult,
				plan,
				objectType,
				stage.EntityID,
				resolvedRequestJSON,
				domain.MustJSON(stage),
			)
			switch objectType {
			case domain.PublishedAdSet:
				if campaign := checkpointByType[domain.PublishedCampaign]; campaign != nil {
					checkpoint.ParentMetaObjectID = campaign.MetaObjectID
				}
			case domain.PublishedAd:
				if adSet := checkpointByType[domain.PublishedAdSet]; adSet != nil {
					checkpoint.ParentMetaObjectID = adSet.MetaObjectID
				}
			}
			if err := s.Repos.Batches.CheckpointPublishedObject(ctx, &checkpoint); err != nil {
				return err
			}
			checkpointByType[objectType] = &checkpoint
			return nil
		}
		if objectType, ok := activatedObjectType(stage.Name); ok {
			checkpoint := checkpointByType[objectType]
			if checkpoint == nil {
				return fmt.Errorf("activation checkpoint for %s is missing", objectType)
			}
			if err := s.Repos.Batches.UpdatePublishedStatus(
				ctx,
				checkpoint.ID,
				string(meta.StatusActive),
				domain.MustJSON(stage),
				s.Now(),
			); err != nil {
				return err
			}
			checkpoint.EffectiveStatus = string(meta.StatusActive)
		}
		return nil
	}
	result := s.Publisher.PublishAccount(ctx, meta.AccountPublishRequest{
		AccountID:    adAccountGraphID(account),
		AccessToken:  token,
		Hierarchy:    plan.Hierarchy,
		ValidateOnly: plan.ValidateOnly,
		LeavePaused:  plan.LeavePaused,
		Resume:       resume,
		OnStage:      onStage,
	})
	responseJSON := domain.MustJSON(result)
	published := publishedObjects(batch, accountResult, plan, result, responseJSON)
	if !result.Success {
		failure := lastPublishFailure(result)
		persistCtx, cancelPersist := publishCleanupContext(ctx)
		defer cancelPersist()

		if result.CampaignID != "" && !plan.LeavePaused {
			pauseStage, pauseErr := s.safetyPauseCampaign(token, result.CampaignID)
			result.Stages = append(result.Stages, pauseStage)
			responseJSON = domain.MustJSON(result)
			published = publishedObjects(batch, accountResult, plan, result, responseJSON)
			finishErr := s.finishPublishFailure(persistCtx, batch.ConnectionID, accountResult, &result, failure)
			if pauseErr != nil {
				// Retrying is safe: the unique remote names are reconciled and
				// the retry preflight pauses the campaign before any create.
				return errors.Join(pauseErr, finishErr)
			}
			return finishErr
		}
		return s.finishPublishFailure(persistCtx, batch.ConnectionID, accountResult, &result, failure)
	}
	if err := s.Repos.Batches.FinishAccountResult(ctx, resultID, database.AccountResultCompletion{
		Status:       domain.BatchAccountSucceeded,
		ResponseJSON: responseJSON,
	}, published, s.Now()); err != nil {
		// Never leave a remotely ACTIVE campaign spending while its successful
		// result is not durable. The next retry will reconcile the same IDs and
		// activate again only after local persistence is healthy.
		if !plan.LeavePaused && result.CampaignID != "" {
			_, pauseErr := s.safetyPauseCampaign(token, result.CampaignID)
			return errors.Join(err, pauseErr)
		}
		return err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &batch.ConnectionID,
		ActorType:    "worker",
		Action:       "batch.account.published",
		EntityType:   "batch_account_result",
		EntityID:     resultID.String(),
		After:        responseJSON,
	})
	return nil
}

const publishNameMaxRunes = 240

func applyPublishMarker(hierarchy *meta.HierarchySpec, resultID uuid.UUID) {
	if hierarchy == nil || resultID == uuid.Nil {
		return
	}
	marker := " [RP:" + resultID.String() + "]"
	hierarchy.Campaign.Name = taggedPublishName(hierarchy.Campaign.Name, marker)
	hierarchy.AdSet.Name = taggedPublishName(hierarchy.AdSet.Name, marker)
	hierarchy.Creative.Name = taggedPublishName(hierarchy.Creative.Name, marker)
	hierarchy.Ad.Name = taggedPublishName(hierarchy.Ad.Name, marker)
}

func taggedPublishName(name, marker string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, marker) {
		return name
	}
	maxBaseRunes := publishNameMaxRunes - len([]rune(marker))
	if maxBaseRunes < 1 {
		return marker
	}
	runes := []rune(name)
	if len(runes) > maxBaseRunes {
		runes = runes[:maxBaseRunes]
	}
	return strings.TrimSpace(string(runes)) + marker
}

func publishCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(context.Background(), 30*time.Second)
}

func (s *Service) safetyPauseCampaign(
	accessToken string,
	campaignID string,
) (meta.PublishStage, error) {
	started := s.Now()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.Meta.PauseEntity(cleanupCtx, accessToken, campaignID)
	stage := meta.PublishStage{
		Name:       "safety_pause_campaign",
		State:      meta.StageActivated,
		EntityID:   campaignID,
		StartedAt:  started,
		FinishedAt: s.Now(),
		Note:       "campaign forced to PAUSED after an incomplete or non-durable publish",
	}
	if err != nil {
		stage.State = meta.StageFailed
		stage.Failure = &meta.PublishFailure{
			Message:   err.Error(),
			Retryable: true,
		}
	}
	return stage, err
}

func (s *Service) reconcilePublishRetry(
	ctx context.Context,
	accessToken string,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	account *domain.AdAccount,
	plan AccountPublishPlan,
	checkpoints map[domain.PublishedObjectType]*domain.PublishedObject,
) error {
	accountID := adAccountGraphID(account)
	requestJSON := domain.MustJSON(plan)

	campaign := checkpoints[domain.PublishedCampaign]
	if campaign == nil {
		remote, found, err := s.Meta.FindPublishedObjectByName(
			ctx,
			accessToken,
			accountID,
			meta.PublishedObjectCampaign,
			plan.Hierarchy.Campaign.Name,
			"",
		)
		if err != nil {
			return err
		}
		if !found {
			// Creation always happens in hierarchy order, so no child from a
			// prior attempt can exist without the uniquely tagged campaign.
			return nil
		}
		pauseStage, err := s.safetyPauseCampaign(accessToken, remote.ID)
		if err != nil {
			return fmt.Errorf("safety-pause reconciled campaign %s: %w", remote.ID, err)
		}
		checkpoint := checkpointForStage(
			batch,
			accountResult,
			plan,
			domain.PublishedCampaign,
			remote.ID,
			requestJSON,
			domain.MustJSON(map[string]any{
				"reconciled": true,
				"remote":     remote,
				"safety":     pauseStage,
			}),
		)
		checkpoint.EffectiveStatus = string(meta.StatusPaused)
		checkpoint.LastSyncedAt = timePointer(s.Now())
		if err := s.Repos.Batches.CheckpointPublishedObject(ctx, &checkpoint); err != nil {
			return fmt.Errorf("checkpoint reconciled campaign %s: %w", remote.ID, err)
		}
		checkpoints[domain.PublishedCampaign] = &checkpoint
		campaign = &checkpoint
	} else {
		pauseStage, err := s.safetyPauseCampaign(accessToken, campaign.MetaObjectID)
		if err != nil {
			return fmt.Errorf("safety-pause checkpointed campaign %s: %w", campaign.MetaObjectID, err)
		}
		if err := s.Repos.Batches.UpdatePublishedStatus(
			ctx,
			campaign.ID,
			string(meta.StatusPaused),
			domain.MustJSON(pauseStage),
			s.Now(),
		); err != nil {
			return fmt.Errorf("record retry safety pause for campaign %s: %w", campaign.MetaObjectID, err)
		}
		campaign.EffectiveStatus = string(meta.StatusPaused)
	}

	adSet, err := s.reconcileRemoteObject(
		ctx,
		accessToken,
		accountID,
		batch,
		accountResult,
		plan,
		checkpoints,
		domain.PublishedAdSet,
		meta.PublishedObjectAdSet,
		plan.Hierarchy.AdSet.Name,
		campaign.MetaObjectID,
		requestJSON,
	)
	if err != nil {
		return err
	}
	if adSet == nil {
		return nil
	}
	creative, err := s.reconcileRemoteObject(
		ctx,
		accessToken,
		accountID,
		batch,
		accountResult,
		plan,
		checkpoints,
		domain.PublishedCreative,
		meta.PublishedObjectCreative,
		plan.Hierarchy.Creative.Name,
		"",
		requestJSON,
	)
	if err != nil {
		return err
	}
	if creative == nil {
		return nil
	}
	_, err = s.reconcileRemoteObject(
		ctx,
		accessToken,
		accountID,
		batch,
		accountResult,
		plan,
		checkpoints,
		domain.PublishedAd,
		meta.PublishedObjectAd,
		plan.Hierarchy.Ad.Name,
		adSet.MetaObjectID,
		requestJSON,
	)
	return err
}

func adAccountGraphID(account *domain.AdAccount) string {
	if account == nil {
		return ""
	}
	return firstNonEmpty(account.AccountID, account.MetaAdAccountID)
}

func (s *Service) reconcileRemoteObject(
	ctx context.Context,
	accessToken string,
	accountID string,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	checkpoints map[domain.PublishedObjectType]*domain.PublishedObject,
	objectType domain.PublishedObjectType,
	remoteKind meta.PublishedObjectKind,
	name string,
	parentID string,
	requestJSON domain.JSON,
) (*domain.PublishedObject, error) {
	if checkpoint := checkpoints[objectType]; checkpoint != nil {
		return checkpoint, nil
	}
	remote, found, err := s.Meta.FindPublishedObjectByName(
		ctx,
		accessToken,
		accountID,
		remoteKind,
		name,
		parentID,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	checkpoint := checkpointForStage(
		batch,
		accountResult,
		plan,
		objectType,
		remote.ID,
		requestJSON,
		domain.MustJSON(map[string]any{"reconciled": true, "remote": remote}),
	)
	checkpoint.ParentMetaObjectID = parentID
	checkpoint.EffectiveStatus = reconciledEffectiveStatus(remote)
	checkpoint.LastSyncedAt = timePointer(s.Now())
	if err := s.Repos.Batches.CheckpointPublishedObject(ctx, &checkpoint); err != nil {
		return nil, fmt.Errorf("checkpoint reconciled %s %s: %w", objectType, remote.ID, err)
	}
	checkpoints[objectType] = &checkpoint
	return &checkpoint, nil
}

func reconciledEffectiveStatus(remote meta.RemotePublishedObject) string {
	if status := strings.TrimSpace(remote.EffectiveStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(remote.Status); status != "" {
		return status
	}
	return "UNKNOWN"
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func resumeFromCheckpoints(
	checkpoints map[domain.PublishedObjectType]*domain.PublishedObject,
) meta.PublishResume {
	resume := meta.PublishResume{}
	if object := checkpoints[domain.PublishedCampaign]; object != nil {
		resume.CampaignID = object.MetaObjectID
	}
	if object := checkpoints[domain.PublishedAdSet]; object != nil {
		resume.AdSetID = object.MetaObjectID
	}
	if object := checkpoints[domain.PublishedCreative]; object != nil {
		resume.CreativeID = object.MetaObjectID
	}
	if object := checkpoints[domain.PublishedAd]; object != nil {
		resume.AdID = object.MetaObjectID
	}
	return resume
}

func createdObjectType(stageName string) (domain.PublishedObjectType, bool) {
	switch stageName {
	case "create_campaign":
		return domain.PublishedCampaign, true
	case "create_ad_set":
		return domain.PublishedAdSet, true
	case "create_creative":
		return domain.PublishedCreative, true
	case "create_ad":
		return domain.PublishedAd, true
	default:
		return "", false
	}
}

func activatedObjectType(stageName string) (domain.PublishedObjectType, bool) {
	switch stageName {
	case "activate_campaign":
		return domain.PublishedCampaign, true
	case "activate_ad_set":
		return domain.PublishedAdSet, true
	case "activate_ad":
		return domain.PublishedAd, true
	default:
		return "", false
	}
}

func checkpointForStage(
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	objectType domain.PublishedObjectType,
	metaObjectID string,
	requestJSON, responseJSON domain.JSON,
) domain.PublishedObject {
	name, parent := "", ""
	switch objectType {
	case domain.PublishedCampaign:
		name = plan.Hierarchy.Campaign.Name
	case domain.PublishedAdSet:
		name = plan.Hierarchy.AdSet.Name
	case domain.PublishedCreative:
		name = plan.Hierarchy.Creative.Name
	case domain.PublishedAd:
		name = plan.Hierarchy.Ad.Name
	}
	desired := string(meta.StatusActive)
	if plan.LeavePaused {
		desired = string(meta.StatusPaused)
	}
	return domain.PublishedObject{
		BatchID:              batch.ID,
		BatchAccountResultID: accountResult.ID,
		ConnectionID:         batch.ConnectionID,
		AdAccountID:          accountResult.AdAccountID,
		ObjectType:           objectType,
		MetaObjectID:         metaObjectID,
		ParentMetaObjectID:   parent,
		Name:                 name,
		DesiredStatus:        desired,
		EffectiveStatus:      string(meta.StatusPaused),
		IdempotencyKey:       batch.IdempotencyKey + ":" + string(objectType),
		RequestJSON:          requestJSON,
		ResponseJSON:         responseJSON,
	}
}

func (s *Service) finishPublishFailure(
	ctx context.Context,
	connectionID uuid.UUID,
	accountResult *domain.BatchAccountResult,
	result *meta.AccountPublishResult,
	cause error,
) error {
	if cause == nil {
		cause = errors.New("Meta publish failed")
	}
	_, expirationErr := s.markConnectionExpiredForMetaError(ctx, connectionID, cause)
	response := emptyObject()
	var published []domain.PublishedObject
	graphErr := metaAccessTokenError(cause)
	if graphErr == nil {
		_ = errors.As(cause, &graphErr)
	}
	if result != nil {
		response = domain.MustJSON(result)
		batch, batchErr := s.Repos.Batches.Get(ctx, accountResult.BatchID)
		if batchErr == nil {
			var plan AccountPublishPlan
			if accountResult.RequestJSON.Decode(&plan) == nil {
				applyPublishMarker(&plan.Hierarchy, accountResult.ID)
				published = publishedObjects(batch, accountResult, plan, *result, response)
			}
		}
		for _, stage := range result.Stages {
			if stage.Failure != nil && stage.Failure.Graph != nil {
				graphErr = stage.Failure.Graph
			}
		}
	}
	code, subcode := "", ""
	if graphErr != nil {
		code = strconv.Itoa(graphErr.Code)
		subcode = strconv.Itoa(graphErr.ErrorSubcode)
	}
	retryErr := publishFailureJobError(cause, result)
	var finishErr error
	if retryErr != nil {
		finishErr = s.Repos.Batches.RecordAccountRetry(
			ctx,
			accountResult.ID,
			database.AccountResultRetry{
				ResponseJSON: response,
				ErrorCode:    code,
				ErrorSubcode: subcode,
				ErrorMessage: cause.Error(),
			},
			published,
			s.Now(),
		)
	} else {
		finishErr = s.Repos.Batches.FinishAccountResult(ctx, accountResult.ID, database.AccountResultCompletion{
			Status:       domain.BatchAccountFailed,
			ResponseJSON: response,
			ErrorCode:    code,
			ErrorSubcode: subcode,
			ErrorMessage: cause.Error(),
		}, published, s.Now())
	}
	if finishErr != nil {
		return errors.Join(cause, expirationErr, finishErr)
	}
	if expirationErr != nil {
		return errors.Join(cause, expirationErr)
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &connectionID,
		ActorType:    "worker",
		Action:       publishFailureAuditAction(retryErr),
		EntityType:   "batch_account_result",
		EntityID:     accountResult.ID.String(),
		Severity:     domain.AuditWarning,
		After:        response,
		Metadata: domain.MustJSON(map[string]any{
			"error":     cause.Error(),
			"retryable": retryErr != nil,
		}),
	})
	if retryErr != nil {
		// The failure and every known remote ID are already durable. Returning
		// the error releases the queue job for backoff; the next attempt
		// reconciles uniquely tagged Meta objects before creating anything.
		return retryErr
	}
	// A per-account Meta rejection is a terminal business result, not a queue
	// infrastructure failure. Returning nil preserves partial-success behavior.
	return nil
}

func publishFailureAuditAction(retryErr error) string {
	if retryErr != nil {
		return "batch.account.retry_pending"
	}
	return "batch.account.failed"
}

func publishFailureJobError(cause error, result *meta.AccountPublishResult) error {
	if meta.IsRetryableError(cause) {
		return fmt.Errorf("retryable Meta publish failure: %w", cause)
	}
	if result == nil {
		return nil
	}
	for _, stage := range result.Stages {
		if stage.Failure != nil && stage.Failure.Retryable {
			return fmt.Errorf("retryable Meta publish failure: %w", cause)
		}
	}
	return nil
}

func publishedObjects(
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	result meta.AccountPublishResult,
	response domain.JSON,
) []domain.PublishedObject {
	desired := string(meta.StatusActive)
	if plan.LeavePaused {
		desired = string(meta.StatusPaused)
	}
	effective := "UNKNOWN"
	if result.Activated {
		effective = string(meta.StatusActive)
	} else if plan.LeavePaused || publishResultConfirmedPaused(result) {
		effective = string(meta.StatusPaused)
	}
	entries := []struct {
		objectType domain.PublishedObjectType
		id         string
		parent     string
		name       string
	}{
		{domain.PublishedCampaign, result.CampaignID, "", plan.Hierarchy.Campaign.Name},
		{domain.PublishedAdSet, result.AdSetID, result.CampaignID, plan.Hierarchy.AdSet.Name},
		{domain.PublishedCreative, result.CreativeID, "", plan.Hierarchy.Creative.Name},
		{domain.PublishedAd, result.AdID, result.AdSetID, plan.Hierarchy.Ad.Name},
	}
	objects := make([]domain.PublishedObject, 0, len(entries))
	for _, entry := range entries {
		if entry.id == "" {
			continue
		}
		objects = append(objects, domain.PublishedObject{
			BatchID:              batch.ID,
			BatchAccountResultID: accountResult.ID,
			ConnectionID:         batch.ConnectionID,
			AdAccountID:          accountResult.AdAccountID,
			ObjectType:           entry.objectType,
			MetaObjectID:         entry.id,
			ParentMetaObjectID:   entry.parent,
			Name:                 entry.name,
			DesiredStatus:        desired,
			EffectiveStatus:      effective,
			IdempotencyKey:       batch.IdempotencyKey + ":" + string(entry.objectType),
			RequestJSON:          accountResult.RequestJSON,
			ResponseJSON:         response,
		})
	}
	return objects
}

func publishResultConfirmedPaused(result meta.AccountPublishResult) bool {
	for index := len(result.Stages) - 1; index >= 0; index-- {
		stage := result.Stages[index]
		if stage.Name == "safety_pause_campaign" {
			return stage.State != meta.StageFailed
		}
	}
	return false
}

func lastPublishFailure(result meta.AccountPublishResult) error {
	for index := len(result.Stages) - 1; index >= 0; index-- {
		if result.Stages[index].Failure != nil {
			if result.Stages[index].Failure.Graph != nil {
				return result.Stages[index].Failure.Graph
			}
			return errors.New(result.Stages[index].Failure.Message)
		}
	}
	return errors.New("Meta publish did not complete")
}

func hierarchyForAccount(base meta.HierarchySpec, patch json.RawMessage) (meta.HierarchySpec, error) {
	if len(bytes.TrimSpace(patch)) == 0 || bytes.Equal(bytes.TrimSpace(patch), []byte("null")) {
		return base, nil
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return meta.HierarchySpec{}, err
	}
	var destination map[string]any
	if err := decodeJSONUseNumber(baseJSON, &destination); err != nil {
		return meta.HierarchySpec{}, err
	}
	var override map[string]any
	if err := decodeJSONUseNumber(patch, &override); err != nil {
		return meta.HierarchySpec{}, err
	}
	deepMerge(destination, override)
	merged, err := json.Marshal(destination)
	if err != nil {
		return meta.HierarchySpec{}, err
	}
	var hierarchy meta.HierarchySpec
	if err := json.Unmarshal(merged, &hierarchy); err != nil {
		return meta.HierarchySpec{}, err
	}
	return hierarchy, nil
}

func decodeJSONUseNumber(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func deepMerge(destination, override map[string]any) {
	for key, value := range override {
		overrideMap, overrideIsMap := value.(map[string]any)
		destinationMap, destinationIsMap := destination[key].(map[string]any)
		if overrideIsMap && destinationIsMap {
			deepMerge(destinationMap, overrideMap)
			continue
		}
		destination[key] = value
	}
}

func setHierarchyJSONPointer(hierarchy *meta.HierarchySpec, pointer string, value string) error {
	encoded, err := json.Marshal(hierarchy)
	if err != nil {
		return err
	}
	var document any
	if err := decodeJSONUseNumber(encoded, &document); err != nil {
		return err
	}
	tokens, err := jsonPointerTokens(pointer)
	if err != nil {
		return err
	}
	if err := setJSONPointerValue(document, tokens, value); err != nil {
		return err
	}
	updated, err := json.Marshal(document)
	if err != nil {
		return err
	}
	return json.Unmarshal(updated, hierarchy)
}

func jsonPointerTokens(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, errors.New("target must be a non-empty RFC 6901 JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	for index := range raw {
		raw[index] = strings.ReplaceAll(strings.ReplaceAll(raw[index], "~1", "/"), "~0", "~")
		if raw[index] == "" {
			return nil, errors.New("target contains an empty path segment")
		}
	}
	return raw, nil
}

func setJSONPointerValue(current any, tokens []string, value string) error {
	if len(tokens) == 0 {
		return errors.New("target cannot replace the hierarchy root")
	}
	switch node := current.(type) {
	case map[string]any:
		if len(tokens) == 1 {
			node[tokens[0]] = value
			return nil
		}
		next, ok := node[tokens[0]]
		if !ok {
			return fmt.Errorf("path segment %q does not exist", tokens[0])
		}
		return setJSONPointerValue(next, tokens[1:], value)
	case []any:
		index, err := strconv.Atoi(tokens[0])
		if err != nil || index < 0 || index >= len(node) {
			return fmt.Errorf("invalid array index %q", tokens[0])
		}
		if len(tokens) == 1 {
			node[index] = value
			return nil
		}
		return setJSONPointerValue(node[index], tokens[1:], value)
	default:
		return fmt.Errorf("path crosses scalar at %q", tokens[0])
	}
}
