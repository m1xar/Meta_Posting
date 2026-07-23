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
)

type TreePublishResult struct {
	AccountID  string               `json:"account_id"`
	CampaignID string               `json:"campaign_id,omitempty"`
	AdSets     []TreePublishedAdSet `json:"ad_sets,omitempty"`
	Success    bool                 `json:"success"`
	Activated  bool                 `json:"activated"`
	Validated  bool                 `json:"validated"`
	StartedAt  time.Time            `json:"started_at"`
	FinishedAt time.Time            `json:"finished_at"`
	Stages     []meta.PublishStage  `json:"stages"`
}

type TreePublishedAdSet struct {
	AdSetID string            `json:"ad_set_id,omitempty"`
	Ads     []TreePublishedAd `json:"ads,omitempty"`
}

type TreePublishedAd struct {
	CreativeID string `json:"creative_id,omitempty"`
	AdID       string `json:"ad_id,omitempty"`
}

func (s *Service) publishTreeAccountResult(
	ctx context.Context,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	account *domain.AdAccount,
	plan AccountPublishPlan,
) (returnErr error) {
	result := TreePublishResult{
		AccountID: account.AccountID,
		StartedAt: s.Now(),
	}
	defer func() {
		result.FinishedAt = s.Now()
	}()

	tree := *plan.Tree
	if err := applyTreeMediaBindings(ctx, s, batch.ConnectionID, account, &tree, plan.MediaBindings); err != nil {
		return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
	}
	tagCampaignTree(&tree, accountResult.ID)
	plan.Tree = &tree

	_, token, err := s.accessToken(ctx, batch.ConnectionID)
	if err != nil {
		return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
	}
	defer func() {
		token = ""
	}()

	if plan.ValidateOnly {
		if err := s.validateCampaignTree(ctx, token, account, tree, &result); err != nil {
			return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
		}
		result.Success = true
		result.Validated = true
		return s.finishTreePublishSuccess(ctx, batch, accountResult, result)
	}

	checkpointList, err := s.Repos.Batches.ListResultPublishedObjects(ctx, accountResult.ID)
	if err != nil {
		return err
	}
	checkpoints := make(map[string]*domain.PublishedObject, len(checkpointList))
	for index := range checkpointList {
		checkpoint := &checkpointList[index]
		checkpoints[checkpoint.IdempotencyKey] = checkpoint
	}
	accountGraphID := adAccountGraphID(account)
	campaignKey := treeObjectKey(batch, accountResult, "campaign")
	campaign, err := s.ensureTreeObject(
		ctx,
		token,
		accountGraphID,
		batch,
		accountResult,
		plan,
		checkpoints,
		treeObjectInput{
			Key:        campaignKey,
			ObjectType: domain.PublishedCampaign,
			RemoteKind: meta.PublishedObjectCampaign,
			Name:       tree.Campaign.Name,
			Create: func() (meta.CreateResult, error) {
				return s.Meta.CreateCampaign(ctx, token, accountGraphID, tree.Campaign, false)
			},
		},
		&result,
	)
	if err != nil {
		return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
	}
	result.CampaignID = campaign.MetaObjectID

	// A retry may resume after the campaign was previously activated. Keep the
	// entire tree unable to spend until every missing child is durable again.
	if accountResult.Attempts > 0 {
		stage, pauseErr := s.safetyPauseCampaign(token, campaign.MetaObjectID)
		result.Stages = append(result.Stages, stage)
		if pauseErr != nil {
			return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, pauseErr)
		}
		_ = s.Repos.Batches.UpdatePublishedStatus(
			ctx, campaign.ID, string(meta.StatusPaused), domain.MustJSON(stage), s.Now(),
		)
	}

	result.AdSets = make([]TreePublishedAdSet, len(tree.AdSets))
	adSetObjects := make([]*domain.PublishedObject, 0, len(tree.AdSets))
	adObjects := make([]*domain.PublishedObject, 0)
	for adSetIndex, adSetSpec := range tree.AdSets {
		adSetKey := treeObjectKey(batch, accountResult, fmt.Sprintf("adset:%d", adSetIndex))
		adSetObject, createErr := s.ensureTreeObject(
			ctx,
			token,
			accountGraphID,
			batch,
			accountResult,
			plan,
			checkpoints,
			treeObjectInput{
				Key:        adSetKey,
				ObjectType: domain.PublishedAdSet,
				RemoteKind: meta.PublishedObjectAdSet,
				Name:       adSetSpec.AdSet.Name,
				ParentID:   campaign.MetaObjectID,
				Create: func() (meta.CreateResult, error) {
					return s.Meta.CreateAdSet(
						ctx, token, accountGraphID, campaign.MetaObjectID, adSetSpec.AdSet, false,
					)
				},
			},
			&result,
		)
		if createErr != nil {
			return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, createErr)
		}
		adSetObjects = append(adSetObjects, adSetObject)
		result.AdSets[adSetIndex] = TreePublishedAdSet{
			AdSetID: adSetObject.MetaObjectID,
			Ads:     make([]TreePublishedAd, len(adSetSpec.Ads)),
		}

		for adIndex, adSpec := range adSetSpec.Ads {
			creativeKey := treeObjectKey(
				batch, accountResult, fmt.Sprintf("creative:%d:%d", adSetIndex, adIndex),
			)
			creativeObject, creativeErr := s.ensureTreeObject(
				ctx,
				token,
				accountGraphID,
				batch,
				accountResult,
				plan,
				checkpoints,
				treeObjectInput{
					Key:        creativeKey,
					ObjectType: domain.PublishedCreative,
					RemoteKind: meta.PublishedObjectCreative,
					Name:       adSpec.Creative.Name,
					Create: func() (meta.CreateResult, error) {
						return s.Meta.CreateCreative(ctx, token, accountGraphID, adSpec.Creative, false)
					},
				},
				&result,
			)
			if creativeErr != nil {
				return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, creativeErr)
			}

			adKey := treeObjectKey(
				batch, accountResult, fmt.Sprintf("ad:%d:%d", adSetIndex, adIndex),
			)
			adObject, adErr := s.ensureTreeObject(
				ctx,
				token,
				accountGraphID,
				batch,
				accountResult,
				plan,
				checkpoints,
				treeObjectInput{
					Key:        adKey,
					ObjectType: domain.PublishedAd,
					RemoteKind: meta.PublishedObjectAd,
					Name:       adSpec.Ad.Name,
					ParentID:   adSetObject.MetaObjectID,
					Create: func() (meta.CreateResult, error) {
						return s.Meta.CreateAd(
							ctx,
							token,
							accountGraphID,
							adSetObject.MetaObjectID,
							creativeObject.MetaObjectID,
							adSpec.Ad,
							false,
						)
					},
				},
				&result,
			)
			if adErr != nil {
				return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, adErr)
			}
			adObjects = append(adObjects, adObject)
			result.AdSets[adSetIndex].Ads[adIndex] = TreePublishedAd{
				CreativeID: creativeObject.MetaObjectID,
				AdID:       adObject.MetaObjectID,
			}
		}
	}

	if plan.LeavePaused {
		result.Success = true
		result.Stages = append(result.Stages, treeSkippedStage(
			"activate_tree", "leave_paused requested",
		))
		return s.finishTreePublishSuccess(ctx, batch, accountResult, result)
	}

	for _, object := range append(adObjects, adSetObjects...) {
		if err := s.activateTreeObject(ctx, token, object, &result); err != nil {
			return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
		}
	}
	if err := s.activateTreeObject(ctx, token, campaign, &result); err != nil {
		return s.finishTreePublishFailure(ctx, batch, accountResult, plan, result, err)
	}
	result.Activated = true
	result.Success = true
	return s.finishTreePublishSuccess(ctx, batch, accountResult, result)
}

func applyTreeMediaBindings(
	ctx context.Context,
	service *Service,
	connectionID uuid.UUID,
	account *domain.AdAccount,
	tree *meta.CampaignTreeSpec,
	bindings []MediaBinding,
) error {
	if len(bindings) == 0 {
		return nil
	}
	_, token, err := service.accessToken(ctx, connectionID)
	if err != nil {
		return err
	}
	defer func() { token = "" }()
	uploaded := make(map[uuid.UUID]string, len(bindings))
	for _, binding := range bindings {
		replacement, ok := uploaded[binding.MediaID]
		if !ok {
			replacement, err = service.uploadMediaForAccount(ctx, token, account, binding.MediaID)
			if err != nil {
				return fmt.Errorf("upload media %s: %w", binding.MediaID, err)
			}
			uploaded[binding.MediaID] = replacement
		}
		if err := setCampaignTreeJSONPointer(tree, binding.Target, replacement); err != nil {
			return fmt.Errorf("apply media binding %s: %w", binding.Target, err)
		}
	}
	return nil
}

func (s *Service) validateCampaignTree(
	ctx context.Context,
	token string,
	account *domain.AdAccount,
	tree meta.CampaignTreeSpec,
	result *TreePublishResult,
) error {
	accountID := adAccountGraphID(account)
	started := s.Now()
	_, err := s.Meta.CreateCampaign(ctx, token, accountID, tree.Campaign, true)
	result.Stages = append(result.Stages, treeStage("validate_campaign", "", started, err))
	if err != nil {
		return err
	}
	for adSetIndex, adSet := range tree.AdSets {
		for adIndex, ad := range adSet.Ads {
			started = s.Now()
			_, err = s.Meta.CreateCreative(ctx, token, accountID, ad.Creative, true)
			result.Stages = append(result.Stages, treeStage(
				fmt.Sprintf("validate_creative:%d:%d", adSetIndex, adIndex), "", started, err,
			))
			if err != nil {
				return err
			}
		}
	}
	result.Stages = append(result.Stages, treeSkippedStage(
		"validate_adsets_and_ads",
		"Meta validate_only requires dependency IDs; full local tree validation passed",
	))
	return nil
}

type treeObjectInput struct {
	Key        string
	ObjectType domain.PublishedObjectType
	RemoteKind meta.PublishedObjectKind
	Name       string
	ParentID   string
	Create     func() (meta.CreateResult, error)
}

func (s *Service) ensureTreeObject(
	ctx context.Context,
	token string,
	accountGraphID string,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	checkpoints map[string]*domain.PublishedObject,
	input treeObjectInput,
	result *TreePublishResult,
) (*domain.PublishedObject, error) {
	if checkpoint := checkpoints[input.Key]; checkpoint != nil {
		result.Stages = append(result.Stages, treeSkippedStage(
			"resume_"+string(input.ObjectType), input.Key,
		))
		return checkpoint, nil
	}

	if accountResult.Attempts > 0 {
		remote, found, err := s.Meta.FindPublishedObjectByName(
			ctx, token, accountGraphID, input.RemoteKind, input.Name, input.ParentID,
		)
		if err != nil {
			return nil, err
		}
		if found {
			checkpoint := treeCheckpoint(
				batch, accountResult, plan, input, remote.ID,
				domain.MustJSON(map[string]any{"reconciled": true, "remote": remote}),
			)
			checkpoint.EffectiveStatus = reconciledEffectiveStatus(remote)
			if err := s.Repos.Batches.CheckpointPublishedObject(ctx, &checkpoint); err != nil {
				return nil, err
			}
			checkpoints[input.Key] = &checkpoint
			result.Stages = append(result.Stages, treeSkippedStage(
				"reconcile_"+string(input.ObjectType), remote.ID,
			))
			return &checkpoint, nil
		}
	}

	started := s.Now()
	created, err := input.Create()
	stage := treeStage("create_"+string(input.ObjectType), created.ID, started, err)
	result.Stages = append(result.Stages, stage)
	if err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, fmt.Errorf("Meta create %s returned an empty ID", input.ObjectType)
	}
	checkpoint := treeCheckpoint(
		batch, accountResult, plan, input, created.ID, domain.MustJSON(created),
	)
	if err := s.Repos.Batches.CheckpointPublishedObject(ctx, &checkpoint); err != nil {
		return nil, err
	}
	checkpoints[input.Key] = &checkpoint
	return &checkpoint, nil
}

func treeCheckpoint(
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	input treeObjectInput,
	metaID string,
	response domain.JSON,
) domain.PublishedObject {
	desired := string(meta.StatusActive)
	if plan.LeavePaused {
		desired = string(meta.StatusPaused)
	}
	return domain.PublishedObject{
		BatchID:              batch.ID,
		BatchAccountResultID: accountResult.ID,
		ConnectionID:         batch.ConnectionID,
		AdAccountID:          accountResult.AdAccountID,
		ObjectType:           input.ObjectType,
		MetaObjectID:         metaID,
		ParentMetaObjectID:   input.ParentID,
		Name:                 input.Name,
		DesiredStatus:        desired,
		EffectiveStatus:      string(meta.StatusPaused),
		IdempotencyKey:       input.Key,
		RequestJSON:          domain.MustJSON(plan),
		ResponseJSON:         response,
	}
}

func (s *Service) activateTreeObject(
	ctx context.Context,
	token string,
	object *domain.PublishedObject,
	result *TreePublishResult,
) error {
	started := s.Now()
	err := s.Meta.SetEntityStatus(ctx, token, object.MetaObjectID, meta.StatusActive)
	result.Stages = append(result.Stages, treeStage(
		"activate_"+string(object.ObjectType), object.MetaObjectID, started, err,
	))
	if err != nil {
		return err
	}
	if err := s.Repos.Batches.UpdatePublishedStatus(
		ctx,
		object.ID,
		string(meta.StatusActive),
		domain.MustJSON(map[string]any{"status": meta.StatusActive}),
		s.Now(),
	); err != nil {
		return err
	}
	object.EffectiveStatus = string(meta.StatusActive)
	return nil
}

func (s *Service) finishTreePublishSuccess(
	ctx context.Context,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	result TreePublishResult,
) error {
	result.FinishedAt = s.Now()
	response := domain.MustJSON(result)
	if err := s.Repos.Batches.FinishAccountResult(
		ctx,
		accountResult.ID,
		database.AccountResultCompletion{
			Status:       domain.BatchAccountSucceeded,
			ResponseJSON: response,
		},
		nil,
		s.Now(),
	); err != nil {
		if result.Activated && result.CampaignID != "" {
			_, pauseErr := s.safetyPauseCampaignFromConnection(batch.ConnectionID, result.CampaignID)
			return errors.Join(err, pauseErr)
		}
		return err
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &batch.ConnectionID,
		ActorType:    "worker",
		Action:       "batch.account.tree_published",
		EntityType:   "batch_account_result",
		EntityID:     accountResult.ID.String(),
		After:        response,
	})
	return nil
}

func (s *Service) finishTreePublishFailure(
	ctx context.Context,
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	plan AccountPublishPlan,
	result TreePublishResult,
	cause error,
) error {
	if cause == nil {
		cause = errors.New("Meta tree publish failed")
	}
	if result.CampaignID != "" && !plan.LeavePaused {
		_, token, tokenErr := s.accessToken(ctx, batch.ConnectionID)
		if tokenErr == nil {
			stage, pauseErr := s.safetyPauseCampaign(token, result.CampaignID)
			token = ""
			result.Stages = append(result.Stages, stage)
			cause = errors.Join(cause, pauseErr)
		}
	}
	result.Success = false
	result.FinishedAt = s.Now()
	response := domain.MustJSON(result)
	graphErr := metaAccessTokenError(cause)
	code, subcode := "", ""
	if graphErr != nil {
		code = strconv.Itoa(graphErr.Code)
		subcode = strconv.Itoa(graphErr.ErrorSubcode)
	}
	if meta.IsRetryableError(cause) {
		finishErr := s.Repos.Batches.RecordAccountRetry(
			ctx,
			accountResult.ID,
			database.AccountResultRetry{
				ResponseJSON: response,
				ErrorCode:    code,
				ErrorSubcode: subcode,
				ErrorMessage: cause.Error(),
			},
			nil,
			s.Now(),
		)
		return errors.Join(cause, finishErr)
	}
	finishErr := s.Repos.Batches.FinishAccountResult(
		ctx,
		accountResult.ID,
		database.AccountResultCompletion{
			Status:       domain.BatchAccountFailed,
			ResponseJSON: response,
			ErrorCode:    code,
			ErrorSubcode: subcode,
			ErrorMessage: cause.Error(),
		},
		nil,
		s.Now(),
	)
	if finishErr != nil {
		return errors.Join(cause, finishErr)
	}
	s.audit(ctx, domain.AuditEvent{
		ConnectionID: &batch.ConnectionID,
		ActorType:    "worker",
		Action:       "batch.account.tree_failed",
		EntityType:   "batch_account_result",
		EntityID:     accountResult.ID.String(),
		Severity:     domain.AuditWarning,
		After:        response,
		Metadata:     domain.MustJSON(map[string]any{"error": cause.Error()}),
	})
	// A Meta validation/business rejection is terminal for this account but
	// should not make the queue retry a completed failure record.
	return nil
}

func (s *Service) safetyPauseCampaignFromConnection(
	connectionID uuid.UUID,
	campaignID string,
) (meta.PublishStage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, token, err := s.accessToken(ctx, connectionID)
	if err != nil {
		return meta.PublishStage{}, err
	}
	defer func() { token = "" }()
	return s.safetyPauseCampaign(token, campaignID)
}

func treeStage(name, entityID string, started time.Time, err error) meta.PublishStage {
	stage := meta.PublishStage{
		Name:       name,
		State:      meta.StageCreated,
		EntityID:   entityID,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
	}
	if strings.HasPrefix(name, "activate_") {
		stage.State = meta.StageActivated
	}
	if strings.HasPrefix(name, "validate_") {
		stage.State = meta.StageValidated
	}
	if err != nil {
		stage.State = meta.StageFailed
		stage.Failure = &meta.PublishFailure{
			Message:   err.Error(),
			Retryable: meta.IsRetryableError(err),
		}
		var graphErr *meta.GraphError
		if errors.As(err, &graphErr) {
			stage.Failure.Graph = graphErr
		}
	}
	return stage
}

func treeSkippedStage(name, note string) meta.PublishStage {
	now := time.Now().UTC()
	return meta.PublishStage{
		Name:       name,
		State:      meta.StageSkipped,
		StartedAt:  now,
		FinishedAt: now,
		Note:       note,
	}
}

func treeObjectKey(
	batch *domain.Batch,
	accountResult *domain.BatchAccountResult,
	path string,
) string {
	return batch.IdempotencyKey + ":tree:" + accountResult.ID.String() + ":" + path
}

func tagCampaignTree(tree *meta.CampaignTreeSpec, resultID uuid.UUID) {
	if tree == nil || resultID == uuid.Nil {
		return
	}
	base := resultID.String()
	tree.Campaign.Name = taggedPublishName(tree.Campaign.Name, " [RP:"+base+":C]")
	for adSetIndex := range tree.AdSets {
		adSet := &tree.AdSets[adSetIndex]
		adSet.AdSet.Name = taggedPublishName(
			adSet.AdSet.Name, fmt.Sprintf(" [RP:%s:S%d]", base, adSetIndex),
		)
		for adIndex := range adSet.Ads {
			ad := &adSet.Ads[adIndex]
			ad.Creative.Name = taggedPublishName(
				ad.Creative.Name,
				fmt.Sprintf(" [RP:%s:S%d:A%d:CR]", base, adSetIndex, adIndex),
			)
			ad.Ad.Name = taggedPublishName(
				ad.Ad.Name,
				fmt.Sprintf(" [RP:%s:S%d:A%d:AD]", base, adSetIndex, adIndex),
			)
		}
	}
}

func campaignTreeForAccount(
	base meta.CampaignTreeSpec,
	patch json.RawMessage,
) (meta.CampaignTreeSpec, error) {
	if len(bytes.TrimSpace(patch)) == 0 || bytes.Equal(bytes.TrimSpace(patch), []byte("null")) {
		return base, nil
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return meta.CampaignTreeSpec{}, err
	}
	var destination map[string]any
	if err := decodeJSONUseNumber(baseJSON, &destination); err != nil {
		return meta.CampaignTreeSpec{}, err
	}
	var override map[string]any
	if err := decodeJSONUseNumber(patch, &override); err != nil {
		return meta.CampaignTreeSpec{}, err
	}
	deepMerge(destination, override)
	merged, err := json.Marshal(destination)
	if err != nil {
		return meta.CampaignTreeSpec{}, err
	}
	var tree meta.CampaignTreeSpec
	if err := json.Unmarshal(merged, &tree); err != nil {
		return meta.CampaignTreeSpec{}, err
	}
	return tree, nil
}

func setCampaignTreeJSONPointer(
	tree *meta.CampaignTreeSpec,
	pointer string,
	value string,
) error {
	encoded, err := json.Marshal(tree)
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
	return json.Unmarshal(updated, tree)
}
