package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

type CreateResult struct {
	ID       string                       `json:"id,omitempty"`
	Success  bool                         `json:"success,omitempty"`
	Warnings []map[string]json.RawMessage `json:"warnings,omitempty"`
	Raw      map[string]json.RawMessage   `json:"-"`
}

type EntityStatusResult struct {
	ID               string `json:"id"`
	Status           string `json:"status,omitempty"`
	ConfiguredStatus string `json:"configured_status,omitempty"`
	EffectiveStatus  string `json:"effective_status,omitempty"`
}

func (r *CreateResult) UnmarshalJSON(data []byte) error {
	type alias CreateResult
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = CreateResult(decoded)
	r.Raw = raw
	return nil
}

type AccountPublishRequest struct {
	AccountID    string                   `json:"account_id"`
	AccessToken  string                   `json:"-"`
	Hierarchy    HierarchySpec            `json:"hierarchy"`
	ValidateOnly bool                     `json:"validate_only,omitempty"`
	LeavePaused  bool                     `json:"leave_paused,omitempty"`
	Resume       PublishResume            `json:"-"`
	OnStage      func(PublishStage) error `json:"-"`
}

type PublishResume struct {
	CampaignID string
	AdSetID    string
	CreativeID string
	AdID       string
}

type BatchPublishRequest struct {
	AccessToken    string                  `json:"-"`
	Accounts       []AccountPublishRequest `json:"accounts"`
	MaxConcurrency int                     `json:"max_concurrency,omitempty"`
}

type PublishStageState string

const (
	StageCreated          PublishStageState = "created"
	StageActivated        PublishStageState = "activated"
	StageValidated        PublishStageState = "validated"
	StageLocallyValidated PublishStageState = "locally_validated"
	StageSkipped          PublishStageState = "skipped"
	StageFailed           PublishStageState = "failed"
)

type PublishFailure struct {
	Message   string      `json:"message"`
	Retryable bool        `json:"retryable"`
	Graph     *GraphError `json:"graph_error,omitempty"`
}

type PublishStage struct {
	Name       string            `json:"name"`
	State      PublishStageState `json:"state"`
	EntityID   string            `json:"entity_id,omitempty"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Failure    *PublishFailure   `json:"failure,omitempty"`
	Note       string            `json:"note,omitempty"`
}

type AccountPublishResult struct {
	AccountID  string         `json:"account_id"`
	CampaignID string         `json:"campaign_id,omitempty"`
	AdSetID    string         `json:"ad_set_id,omitempty"`
	CreativeID string         `json:"creative_id,omitempty"`
	AdID       string         `json:"ad_id,omitempty"`
	Success    bool           `json:"success"`
	Activated  bool           `json:"activated"`
	Validated  bool           `json:"validated"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	Stages     []PublishStage `json:"stages"`
}

type BatchPublishResult struct {
	StartedAt  time.Time              `json:"started_at"`
	FinishedAt time.Time              `json:"finished_at"`
	Accounts   []AccountPublishResult `json:"accounts"`
	Succeeded  int                    `json:"succeeded"`
	Failed     int                    `json:"failed"`
}

type Publisher struct {
	Client *Client
}

func (p Publisher) PublishBatch(ctx context.Context, request BatchPublishRequest) (BatchPublishResult, error) {
	if p.Client == nil {
		return BatchPublishResult{}, errors.New("meta: publisher client is nil")
	}
	if len(request.Accounts) == 0 {
		return BatchPublishResult{}, errors.New("meta: publish batch has no accounts")
	}
	maxConcurrency := request.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}

	result := BatchPublishResult{
		StartedAt: time.Now().UTC(),
		Accounts:  make([]AccountPublishResult, len(request.Accounts)),
	}
	type job struct {
		index   int
		request AccountPublishRequest
	}
	jobs := make(chan job)
	var workers sync.WaitGroup
	workerCount := min(maxConcurrency, len(request.Accounts))
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for item := range jobs {
				accountRequest := item.request
				if accountRequest.AccessToken == "" {
					accountRequest.AccessToken = request.AccessToken
				}
				result.Accounts[item.index] = p.PublishAccount(ctx, accountRequest)
			}
		}()
	}
sendLoop:
	for index, accountRequest := range request.Accounts {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- job{index: index, request: accountRequest}:
		}
	}
	close(jobs)
	workers.Wait()

	for index := range result.Accounts {
		if result.Accounts[index].AccountID == "" {
			result.Accounts[index] = canceledAccountResult(request.Accounts[index].AccountID, ctx.Err())
		}
		if result.Accounts[index].Success {
			result.Succeeded++
		} else {
			result.Failed++
		}
	}
	result.FinishedAt = time.Now().UTC()
	return result, ctx.Err()
}

func (p Publisher) PublishAccount(ctx context.Context, request AccountPublishRequest) (result AccountPublishResult) {
	result = AccountPublishResult{
		AccountID: accountIDWithoutPrefix(request.AccountID),
		StartedAt: time.Now().UTC(),
	}
	defer func() { result.FinishedAt = time.Now().UTC() }()

	if p.Client == nil {
		result.Stages = append(result.Stages, failedStage("validate", time.Now().UTC(), errors.New("meta: publisher client is nil")))
		return result
	}
	if strings.TrimSpace(request.AccountID) == "" {
		result.Stages = append(result.Stages, failedStage("validate", time.Now().UTC(), errors.New("meta: account ID is required")))
		return result
	}
	if request.AccessToken == "" {
		result.Stages = append(result.Stages, failedStage("validate", time.Now().UTC(), errors.New("meta: access token is required")))
		return result
	}
	validationStarted := time.Now().UTC()
	if err := request.Hierarchy.Validate(); err != nil {
		result.Stages = append(result.Stages, failedStage("validate", validationStarted, err))
		return result
	}
	result.Stages = append(result.Stages, PublishStage{
		Name:       "validate",
		State:      StageLocallyValidated,
		StartedAt:  validationStarted,
		FinishedAt: time.Now().UTC(),
	})

	if request.ValidateOnly {
		return p.validateOnly(ctx, request, result)
	}

	if request.Resume.CampaignID != "" {
		result.CampaignID = request.Resume.CampaignID
		result.Stages = append(result.Stages, resumedStage("campaign", result.CampaignID))
	} else {
		campaign, stage, err := p.createCampaign(ctx, request, false)
		result.Stages = append(result.Stages, stage)
		if err != nil {
			return result
		}
		result.CampaignID = campaign.ID
		if !notifyPublishStage(&result, request.OnStage, stage) {
			return result
		}
	}

	if request.Resume.AdSetID != "" {
		result.AdSetID = request.Resume.AdSetID
		result.Stages = append(result.Stages, resumedStage("ad_set", result.AdSetID))
	} else {
		adSet, stage, err := p.createAdSet(ctx, request, result.CampaignID, false)
		result.Stages = append(result.Stages, stage)
		if err != nil {
			return result
		}
		result.AdSetID = adSet.ID
		if !notifyPublishStage(&result, request.OnStage, stage) {
			return result
		}
	}

	if request.Resume.CreativeID != "" {
		result.CreativeID = request.Resume.CreativeID
		result.Stages = append(result.Stages, resumedStage("creative", result.CreativeID))
	} else {
		creative, stage, err := p.createCreative(ctx, request, false)
		result.Stages = append(result.Stages, stage)
		if err != nil {
			return result
		}
		result.CreativeID = creative.ID
		if !notifyPublishStage(&result, request.OnStage, stage) {
			return result
		}
	}

	if request.Resume.AdID != "" {
		result.AdID = request.Resume.AdID
		result.Stages = append(result.Stages, resumedStage("ad", result.AdID))
	} else {
		ad, stage, err := p.createAd(ctx, request, result.AdSetID, result.CreativeID, false)
		result.Stages = append(result.Stages, stage)
		if err != nil {
			return result
		}
		result.AdID = ad.ID
		if !notifyPublishStage(&result, request.OnStage, stage) {
			return result
		}
	}

	if request.LeavePaused {
		result.Success = true
		result.Stages = append(result.Stages, PublishStage{
			Name:       "activate",
			State:      StageSkipped,
			StartedAt:  time.Now().UTC(),
			FinishedAt: time.Now().UTC(),
			Note:       "leave_paused requested",
		})
		return result
	}

	// Activate bottom-up. The campaign remains PAUSED until the final call, so
	// no partially activated hierarchy can spend.
	for _, entity := range []struct {
		name string
		id   string
	}{
		{name: "activate_ad", id: result.AdID},
		{name: "activate_ad_set", id: result.AdSetID},
		{name: "activate_campaign", id: result.CampaignID},
	} {
		started := time.Now().UTC()
		err := p.Client.SetEntityStatus(ctx, request.AccessToken, entity.id, StatusActive)
		if err != nil {
			result.Stages = append(result.Stages, failedStage(entity.name, started, err))
			return result
		}
		stage := PublishStage{
			Name:       entity.name,
			State:      StageActivated,
			EntityID:   entity.id,
			StartedAt:  started,
			FinishedAt: time.Now().UTC(),
		}
		result.Stages = append(result.Stages, stage)
		if !notifyPublishStage(&result, request.OnStage, stage) {
			return result
		}
	}
	result.Activated = true
	result.Success = true
	return result
}

func resumedStage(entity, entityID string) PublishStage {
	now := time.Now().UTC()
	return PublishStage{
		Name:       "resume_" + entity,
		State:      StageSkipped,
		EntityID:   entityID,
		StartedAt:  now,
		FinishedAt: now,
		Note:       "reused durable checkpoint",
	}
}

func notifyPublishStage(
	result *AccountPublishResult,
	callback func(PublishStage) error,
	stage PublishStage,
) bool {
	if callback == nil {
		return true
	}
	if err := callback(stage); err != nil {
		result.Stages = append(result.Stages, failedStage("checkpoint_"+stage.Name, time.Now().UTC(), err))
		return false
	}
	return true
}

func (p Publisher) validateOnly(
	ctx context.Context,
	request AccountPublishRequest,
	result AccountPublishResult,
) AccountPublishResult {
	_, campaignStage, campaignErr := p.createCampaign(ctx, request, true)
	result.Stages = append(result.Stages, campaignStage)
	_, creativeStage, creativeErr := p.createCreative(ctx, request, true)
	result.Stages = append(result.Stages, creativeStage)

	now := time.Now().UTC()
	for _, stageName := range []string{"validate_ad_set", "validate_ad"} {
		result.Stages = append(result.Stages, PublishStage{
			Name:       stageName,
			State:      StageLocallyValidated,
			StartedAt:  now,
			FinishedAt: now,
			Note:       "Graph validate_only requires dependency IDs that are intentionally not created",
		})
	}
	if campaignErr == nil && creativeErr == nil {
		result.Validated = true
		result.Success = true
	}
	return result
}

func (p Publisher) createCampaign(
	ctx context.Context,
	request AccountPublishRequest,
	validateOnly bool,
) (CreateResult, PublishStage, error) {
	started := time.Now().UTC()
	payload, err := campaignPayload(request.Hierarchy.Campaign)
	if err != nil {
		return CreateResult{}, failedStage("create_campaign", started, err), err
	}
	// Name is also the upstream reconciliation key. Raw extension fields must
	// not be able to replace it after the application adds the unique marker.
	payload["name"] = request.Hierarchy.Campaign.Name
	payload["status"] = string(StatusPaused)
	addExecutionOptions(payload, validateOnly)
	var response CreateResult
	err = p.Client.PostJSONNoRetry(
		ctx,
		"/"+AdAccountNodeID(request.AccountID)+"/campaigns",
		request.AccessToken,
		nil,
		payload,
		&response,
	)
	stage := completedCreateStage("campaign", response.ID, validateOnly, started, err)
	if err == nil && !validateOnly && response.ID == "" {
		err = emptyCreateIDResponseError("campaign")
		stage = failedStage("create_campaign", started, err)
	}
	return response, stage, err
}

func (p Publisher) createAdSet(
	ctx context.Context,
	request AccountPublishRequest,
	campaignID string,
	validateOnly bool,
) (CreateResult, PublishStage, error) {
	started := time.Now().UTC()
	payload, err := adSetPayload(request.Hierarchy.AdSet)
	if err != nil {
		return CreateResult{}, failedStage("create_ad_set", started, err), err
	}
	payload["name"] = request.Hierarchy.AdSet.Name
	payload["campaign_id"] = campaignID
	payload["status"] = string(StatusPaused)
	addExecutionOptions(payload, validateOnly)
	var response CreateResult
	err = p.Client.PostJSONNoRetry(
		ctx,
		"/"+AdAccountNodeID(request.AccountID)+"/adsets",
		request.AccessToken,
		nil,
		payload,
		&response,
	)
	stage := completedCreateStage("ad_set", response.ID, validateOnly, started, err)
	if err == nil && !validateOnly && response.ID == "" {
		err = emptyCreateIDResponseError("ad set")
		stage = failedStage("create_ad_set", started, err)
	}
	return response, stage, err
}

func (p Publisher) createCreative(
	ctx context.Context,
	request AccountPublishRequest,
	validateOnly bool,
) (CreateResult, PublishStage, error) {
	started := time.Now().UTC()
	payload, err := creativePayload(request.Hierarchy.Creative)
	if err != nil {
		return CreateResult{}, failedStage("create_creative", started, err), err
	}
	payload["name"] = request.Hierarchy.Creative.Name
	addExecutionOptions(payload, validateOnly)
	var response CreateResult
	err = p.Client.PostJSONNoRetry(
		ctx,
		"/"+AdAccountNodeID(request.AccountID)+"/adcreatives",
		request.AccessToken,
		nil,
		payload,
		&response,
	)
	stage := completedCreateStage("creative", response.ID, validateOnly, started, err)
	if err == nil && !validateOnly && response.ID == "" {
		err = emptyCreateIDResponseError("creative")
		stage = failedStage("create_creative", started, err)
	}
	return response, stage, err
}

func (p Publisher) createAd(
	ctx context.Context,
	request AccountPublishRequest,
	adSetID string,
	creativeID string,
	validateOnly bool,
) (CreateResult, PublishStage, error) {
	started := time.Now().UTC()
	payload, err := adPayload(request.Hierarchy.Ad)
	if err != nil {
		return CreateResult{}, failedStage("create_ad", started, err), err
	}
	payload["name"] = request.Hierarchy.Ad.Name
	payload["adset_id"] = adSetID
	payload["creative"] = map[string]any{"creative_id": creativeID}
	payload["status"] = string(StatusPaused)
	addExecutionOptions(payload, validateOnly)
	var response CreateResult
	err = p.Client.PostJSONNoRetry(
		ctx,
		"/"+AdAccountNodeID(request.AccountID)+"/ads",
		request.AccessToken,
		nil,
		payload,
		&response,
	)
	stage := completedCreateStage("ad", response.ID, validateOnly, started, err)
	if err == nil && !validateOnly && response.ID == "" {
		err = emptyCreateIDResponseError("ad")
		stage = failedStage("create_ad", started, err)
	}
	return response, stage, err
}

// CreateCampaign validates a standalone campaign via Graph or creates it in
// PAUSED status. The hierarchy publisher is preferred for normal launches.
func (c *Client) CreateCampaign(
	ctx context.Context,
	accessToken, accountID string,
	spec CampaignSpec,
	validateOnly bool,
) (CreateResult, error) {
	request := AccountPublishRequest{
		AccountID:   accountID,
		AccessToken: accessToken,
		Hierarchy:   HierarchySpec{Campaign: spec},
	}
	result, _, err := (Publisher{Client: c}).createCampaign(ctx, request, validateOnly)
	return result, err
}

func (c *Client) CreateAdSet(
	ctx context.Context,
	accessToken, accountID, campaignID string,
	spec AdSetSpec,
	validateOnly bool,
) (CreateResult, error) {
	request := AccountPublishRequest{
		AccountID:   accountID,
		AccessToken: accessToken,
		Hierarchy:   HierarchySpec{AdSet: spec},
	}
	result, _, err := (Publisher{Client: c}).createAdSet(ctx, request, campaignID, validateOnly)
	return result, err
}

func (c *Client) CreateCreative(
	ctx context.Context,
	accessToken, accountID string,
	spec CreativeSpec,
	validateOnly bool,
) (CreateResult, error) {
	request := AccountPublishRequest{
		AccountID:   accountID,
		AccessToken: accessToken,
		Hierarchy:   HierarchySpec{Creative: spec},
	}
	result, _, err := (Publisher{Client: c}).createCreative(ctx, request, validateOnly)
	return result, err
}

func (c *Client) CreateAd(
	ctx context.Context,
	accessToken, accountID, adSetID, creativeID string,
	spec AdSpec,
	validateOnly bool,
) (CreateResult, error) {
	request := AccountPublishRequest{
		AccountID:   accountID,
		AccessToken: accessToken,
		Hierarchy:   HierarchySpec{Ad: spec},
	}
	result, _, err := (Publisher{Client: c}).createAd(ctx, request, adSetID, creativeID, validateOnly)
	return result, err
}

func (c *Client) SetEntityStatus(
	ctx context.Context,
	accessToken string,
	entityID string,
	status EntityStatus,
) error {
	if strings.TrimSpace(entityID) == "" {
		return errors.New("meta: entity ID is required")
	}
	switch status {
	case StatusActive, StatusPaused, StatusArchived, StatusDeleted:
	default:
		return fmt.Errorf("meta: unsupported entity status %q", status)
	}
	var response struct {
		Success bool `json:"success"`
	}
	if err := c.PostJSON(
		ctx,
		"/"+strings.TrimPrefix(strings.TrimSpace(entityID), "/"),
		accessToken,
		nil,
		map[string]any{"status": string(status)},
		&response,
	); err != nil {
		return err
	}
	if !response.Success {
		return errors.New("meta: status update returned success=false")
	}
	return nil
}

func (c *Client) PauseEntity(ctx context.Context, accessToken, entityID string) error {
	return c.SetEntityStatus(ctx, accessToken, entityID, StatusPaused)
}

func (c *Client) GetEntityStatus(
	ctx context.Context,
	accessToken string,
	entityID string,
) (EntityStatusResult, error) {
	if strings.TrimSpace(entityID) == "" {
		return EntityStatusResult{}, errors.New("meta: entity ID is required")
	}
	var response EntityStatusResult
	err := c.Get(
		ctx,
		"/"+strings.TrimPrefix(strings.TrimSpace(entityID), "/"),
		accessToken,
		url.Values{"fields": {"id,status,configured_status,effective_status"}},
		&response,
	)
	return response, err
}

func addExecutionOptions(payload map[string]any, validateOnly bool) {
	if validateOnly {
		payload["execution_options"] = []string{"validate_only"}
	}
}

func emptyCreateIDResponseError(entity string) *ResponseError {
	return &ResponseError{
		Message: fmt.Sprintf(
			"meta: create %s returned a successful response with an empty ID",
			entity,
		),
	}
}

func completedCreateStage(
	entity string,
	entityID string,
	validateOnly bool,
	started time.Time,
	err error,
) PublishStage {
	name := "create_" + entity
	if validateOnly {
		name = "validate_" + entity
	}
	if err != nil {
		return failedStage(name, started, err)
	}
	state := StageCreated
	if validateOnly {
		state = StageValidated
	}
	return PublishStage{
		Name:       name,
		State:      state,
		EntityID:   entityID,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
	}
}

func failedStage(name string, started time.Time, err error) PublishStage {
	return PublishStage{
		Name:       name,
		State:      StageFailed,
		StartedAt:  started,
		FinishedAt: time.Now().UTC(),
		Failure:    publishFailure(err),
	}
}

func publishFailure(err error) *PublishFailure {
	if err == nil {
		return nil
	}
	failure := &PublishFailure{
		Message:   err.Error(),
		Retryable: IsRetryableError(err),
	}
	var graphErr *GraphError
	if errors.As(err, &graphErr) {
		failure.Graph = graphErr
	}
	return failure
}

func canceledAccountResult(accountID string, err error) AccountPublishResult {
	now := time.Now().UTC()
	if err == nil {
		err = context.Canceled
	}
	return AccountPublishResult{
		AccountID:  accountIDWithoutPrefix(accountID),
		StartedAt:  now,
		FinishedAt: now,
		Stages:     []PublishStage{failedStage("publish", now, err)},
	}
}

func accountIDWithoutPrefix(accountID string) string {
	return strings.TrimPrefix(strings.TrimSpace(accountID), "act_")
}
