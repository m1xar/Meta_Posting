package meta

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ObjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type AdAccount struct {
	ID                       string         `json:"id"`
	AccountID                string         `json:"account_id"`
	Name                     string         `json:"name"`
	AccountStatus            int            `json:"account_status"`
	DisableReason            int            `json:"disable_reason,omitempty"`
	Currency                 string         `json:"currency"`
	TimezoneID               int            `json:"timezone_id"`
	TimezoneName             string         `json:"timezone_name"`
	TimezoneOffsetHoursUTC   float64        `json:"timezone_offset_hours_utc"`
	Business                 *ObjectRef     `json:"business,omitempty"`
	Owner                    string         `json:"owner,omitempty"`
	Capabilities             []string       `json:"capabilities,omitempty"`
	UserTasks                []string       `json:"user_tasks,omitempty"`
	AmountSpent              string         `json:"amount_spent,omitempty"`
	Balance                  string         `json:"balance,omitempty"`
	SpendCap                 string         `json:"spend_cap,omitempty"`
	FundingSource            string         `json:"funding_source,omitempty"`
	IsPrepayAccount          bool           `json:"is_prepay_account,omitempty"`
	FundingSourceDetails     map[string]any `json:"funding_source_details,omitempty"`
	CreatedTime              string         `json:"created_time,omitempty"`
	EndAdvertiser            string         `json:"end_advertiser,omitempty"`
	EndAdvertiserName        string         `json:"end_advertiser_name,omitempty"`
	MinCampaignGroupSpendCap string         `json:"min_campaign_group_spend_cap,omitempty"`
	DefaultDSABeneficiary    string         `json:"default_dsa_beneficiary,omitempty"`
	DefaultDSAPayor          string         `json:"default_dsa_payor,omitempty"`
}

func (a AdAccount) NodeID() string {
	if a.ID != "" {
		return AdAccountNodeID(a.ID)
	}
	return AdAccountNodeID(a.AccountID)
}

type Business struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	VerificationStatus string     `json:"verification_status,omitempty"`
	Vertical           string     `json:"vertical,omitempty"`
	CreatedTime        string     `json:"created_time,omitempty"`
	UpdatedTime        string     `json:"updated_time,omitempty"`
	PrimaryPage        *ObjectRef `json:"primary_page,omitempty"`
}

type InstagramAccount struct {
	ID                string     `json:"id"`
	IGUserID          string     `json:"ig_user_id,omitempty"`
	Username          string     `json:"username,omitempty"`
	Name              string     `json:"name,omitempty"`
	ProfilePic        string     `json:"profile_pic,omitempty"`
	ProfilePictureURL string     `json:"profile_picture_url,omitempty"`
	HasProfilePicture bool       `json:"has_profile_picture,omitempty"`
	IsPrivate         bool       `json:"is_private,omitempty"`
	IsPublished       bool       `json:"is_published,omitempty"`
	OwnerBusiness     *ObjectRef `json:"owner_business,omitempty"`
}

type Page struct {
	ID                       string            `json:"id"`
	Name                     string            `json:"name"`
	Category                 string            `json:"category,omitempty"`
	AccessToken              string            `json:"access_token,omitempty"`
	Tasks                    []string          `json:"tasks,omitempty"`
	InstagramBusinessAccount *InstagramAccount `json:"instagram_business_account,omitempty"`
}

type Pixel struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Description             string     `json:"description,omitempty"`
	LastFiredTime           string     `json:"last_fired_time,omitempty"`
	IsUnavailable           bool       `json:"is_unavailable,omitempty"`
	IsRestrictedUse         bool       `json:"is_restricted_use,omitempty"`
	DataUseSetting          string     `json:"data_use_setting,omitempty"`
	AutomaticMatchingFields []string   `json:"automatic_matching_fields,omitempty"`
	OwnerAdAccount          *ObjectRef `json:"owner_ad_account,omitempty"`
	OwnerBusiness           *ObjectRef `json:"owner_business,omitempty"`
	EventStats              any        `json:"event_stats,omitempty"`
	Config                  any        `json:"config,omitempty"`
}

type Dataset struct {
	ID                             string     `json:"id"`
	DatasetID                      string     `json:"dataset_id,omitempty"`
	Name                           string     `json:"name"`
	Description                    string     `json:"description,omitempty"`
	CreationTime                   string     `json:"creation_time,omitempty"`
	LastFiredTime                  string     `json:"last_fired_time,omitempty"`
	ServerLastFiredTime            string     `json:"server_last_fired_time,omitempty"`
	IsUnavailable                  bool       `json:"is_unavailable,omitempty"`
	IsRestrictedUse                bool       `json:"is_restricted_use,omitempty"`
	IsConsolidatedContainer        bool       `json:"is_consolidated_container,omitempty"`
	IsEligibleForValueOptimization bool       `json:"is_eligible_for_value_optimization,omitempty"`
	EnableAutoAssignToAccounts     bool       `json:"enable_auto_assign_to_accounts,omitempty"`
	OwnerAdAccount                 *ObjectRef `json:"owner_ad_account,omitempty"`
	OwnerBusiness                  *ObjectRef `json:"owner_business,omitempty"`
	EventStats                     any        `json:"event_stats,omitempty"`
	Permissions                    any        `json:"permissions,omitempty"`
	Usage                          any        `json:"usage,omitempty"`
	Config                         any        `json:"config,omitempty"`
}

type CustomConversion struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Description            string     `json:"description,omitempty"`
	CustomEventType        string     `json:"custom_event_type,omitempty"`
	EventSourceType        string     `json:"event_source_type,omitempty"`
	Pixel                  *ObjectRef `json:"pixel,omitempty"`
	Rule                   string     `json:"rule,omitempty"`
	DefaultConversionValue float64    `json:"default_conversion_value,omitempty"`
	IsArchived             bool       `json:"is_archived,omitempty"`
	CreationTime           string     `json:"creation_time,omitempty"`
}

type CustomAudience struct {
	ID                         string         `json:"id"`
	Name                       string         `json:"name"`
	Description                string         `json:"description,omitempty"`
	Subtype                    string         `json:"subtype,omitempty"`
	ApproximateCountLowerBound int64          `json:"approximate_count_lower_bound,omitempty"`
	ApproximateCountUpperBound int64          `json:"approximate_count_upper_bound,omitempty"`
	DeliveryStatus             map[string]any `json:"delivery_status,omitempty"`
	OperationStatus            map[string]any `json:"operation_status,omitempty"`
	CustomerFileSource         string         `json:"customer_file_source,omitempty"`
	RetentionDays              int            `json:"retention_days,omitempty"`
	Rule                       any            `json:"rule,omitempty"`
	LookalikeSpec              map[string]any `json:"lookalike_spec,omitempty"`
	TimeCreated                int64          `json:"time_created,omitempty"`
	TimeUpdated                int64          `json:"time_updated,omitempty"`
}

type AdvertisableApplication struct {
	ID                      string         `json:"id"`
	Name                    string         `json:"name,omitempty"`
	Namespace               string         `json:"namespace,omitempty"`
	Link                    string         `json:"link,omitempty"`
	IconURL                 string         `json:"icon_url,omitempty"`
	AppInstallTracked       bool           `json:"app_install_tracked,omitempty"`
	AppEventsFeatureBitmask int64          `json:"app_events_feature_bitmask,omitempty"`
	ObjectStoreURLs         map[string]any `json:"object_store_urls,omitempty"`
}

type AccountAssets struct {
	AdAccount         AdAccount                 `json:"ad_account"`
	InstagramAccounts []InstagramAccount        `json:"instagram_accounts,omitempty"`
	Pixels            []Pixel                   `json:"pixels,omitempty"`
	Datasets          []Dataset                 `json:"datasets,omitempty"`
	CustomConversions []CustomConversion        `json:"custom_conversions,omitempty"`
	CustomAudiences   []CustomAudience          `json:"custom_audiences,omitempty"`
	Applications      []AdvertisableApplication `json:"applications,omitempty"`
}

type DiscoveryFailure struct {
	Scope      string      `json:"scope"`
	AccountID  string      `json:"account_id,omitempty"`
	BusinessID string      `json:"business_id,omitempty"`
	Message    string      `json:"message"`
	Graph      *GraphError `json:"graph_error,omitempty"`
}

type DiscoveryResult struct {
	User             User                     `json:"user"`
	Businesses       []Business               `json:"businesses"`
	Pages            []Page                   `json:"pages"`
	AdAccounts       []AdAccount              `json:"ad_accounts"`
	Assets           map[string]AccountAssets `json:"assets"`
	BusinessDatasets map[string][]Dataset     `json:"business_datasets,omitempty"`
	Failures         []DiscoveryFailure       `json:"failures,omitempty"`
}

const (
	userFields             = "id,name"
	adAccountFields        = "id,account_id,name,account_status,disable_reason,currency,timezone_id,timezone_name,timezone_offset_hours_utc,business,owner,capabilities,user_tasks,amount_spent,balance,spend_cap,funding_source,funding_source_details,is_prepay_account,created_time,end_advertiser,end_advertiser_name,min_campaign_group_spend_cap"
	businessFields         = "id,name,verification_status,vertical,created_time,updated_time,primary_page{id,name}"
	pageFields             = "id,name,category,access_token,tasks,instagram_business_account{id,username,name,profile_picture_url}"
	instagramFields        = "id,ig_user_id,username,profile_pic,has_profile_picture,is_private,is_published,owner_business{id,name}"
	pixelFields            = "id,name,description,last_fired_time,is_unavailable,is_restricted_use,data_use_setting,automatic_matching_fields,owner_ad_account{id,name},owner_business{id,name},event_stats,config"
	datasetFields          = "id,dataset_id,name,description,creation_time,last_fired_time,server_last_fired_time,is_unavailable,is_restricted_use,is_consolidated_container,is_eligible_for_value_optimization,enable_auto_assign_to_accounts,owner_ad_account{id,name},owner_business{id,name},event_stats,permissions,usage,config"
	customConversionFields = "id,name,description,custom_event_type,event_source_type,pixel{id,name},rule,default_conversion_value,is_archived,creation_time"
	customAudienceFields   = "id,name,description,subtype,approximate_count_lower_bound,approximate_count_upper_bound,delivery_status,operation_status,customer_file_source,retention_days,rule,lookalike_spec,time_created,time_updated"
	applicationFields      = "id,name,namespace,link,icon_url,app_install_tracked,app_events_feature_bitmask,object_store_urls"
)

func (c *Client) GetMe(ctx context.Context, accessToken string) (User, error) {
	var user User
	err := c.Get(ctx, "/me", accessToken, fieldsQuery(userFields), &user)
	return user, err
}

func (c *Client) ListAdAccounts(ctx context.Context, accessToken string) ([]AdAccount, error) {
	query := fieldsQuery(adAccountFields)
	// Large media-buying profiles can expose hundreds or thousands of accounts.
	// Meta rejects 250 fully-expanded account records in one response, so page
	// this edge more conservatively while preserving the complete field set.
	query.Set("limit", "50")
	return CollectPages[AdAccount](ctx, c, "/me/adaccounts", accessToken, query)
}

func (c *Client) GetAdAccount(ctx context.Context, accessToken, accountID string) (AdAccount, error) {
	var account AdAccount
	err := c.Get(ctx, "/"+AdAccountNodeID(accountID), accessToken, fieldsQuery(adAccountFields), &account)
	return account, err
}

func (c *Client) ListBusinesses(ctx context.Context, accessToken string) ([]Business, error) {
	return CollectPages[Business](ctx, c, "/me/businesses", accessToken, fieldsQuery(businessFields))
}

func (c *Client) ListPages(ctx context.Context, accessToken string) ([]Page, error) {
	return CollectPages[Page](ctx, c, "/me/accounts", accessToken, fieldsQuery(pageFields))
}

func (c *Client) ListInstagramAccounts(ctx context.Context, accessToken, accountID string) ([]InstagramAccount, error) {
	return CollectPages[InstagramAccount](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/instagram_accounts",
		accessToken,
		fieldsQuery(instagramFields),
	)
}

func (c *Client) ListConnectedInstagramAccounts(ctx context.Context, accessToken, accountID string) ([]InstagramAccount, error) {
	return CollectPages[InstagramAccount](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/connected_instagram_accounts",
		accessToken,
		fieldsQuery(instagramFields),
	)
}

func (c *Client) ListPixels(ctx context.Context, accessToken, accountID string) ([]Pixel, error) {
	return CollectPages[Pixel](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/adspixels",
		accessToken,
		fieldsQuery(pixelFields),
	)
}

// ListBusinessDatasets uses the v25 /{business-id}/ads_dataset edge. Meta
// exposes datasets on the business rather than directly on an ad account.
func (c *Client) ListBusinessDatasets(ctx context.Context, accessToken, businessID string) ([]Dataset, error) {
	return CollectPages[Dataset](
		ctx,
		c,
		"/"+strings.TrimPrefix(businessID, "/")+"/ads_dataset",
		accessToken,
		fieldsQuery(datasetFields),
	)
}

func (c *Client) ListCustomConversions(ctx context.Context, accessToken, accountID string) ([]CustomConversion, error) {
	return CollectPages[CustomConversion](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/customconversions",
		accessToken,
		fieldsQuery(customConversionFields),
	)
}

func (c *Client) ListCustomAudiences(ctx context.Context, accessToken, accountID string) ([]CustomAudience, error) {
	return CollectPages[CustomAudience](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/customaudiences",
		accessToken,
		fieldsQuery(customAudienceFields),
	)
}

func (c *Client) ListAdvertisableApplications(ctx context.Context, accessToken, accountID string) ([]AdvertisableApplication, error) {
	return CollectPages[AdvertisableApplication](
		ctx,
		c,
		"/"+AdAccountNodeID(accountID)+"/advertisable_applications",
		accessToken,
		fieldsQuery(applicationFields),
	)
}

// Discover synchronizes all user-level assets and all selected account edges.
// Per-edge failures are returned in DiscoveryResult.Failures instead of
// discarding assets already fetched from the same or other accounts.
func (c *Client) Discover(ctx context.Context, accessToken string, maxConcurrency int) (DiscoveryResult, error) {
	if accessToken == "" {
		return DiscoveryResult{}, errors.New("meta: access token is required")
	}
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	result := DiscoveryResult{
		Assets:           make(map[string]AccountAssets),
		BusinessDatasets: make(map[string][]Dataset),
	}

	var err error
	if result.User, err = c.GetMe(ctx, accessToken); err != nil {
		return result, fmt.Errorf("meta: discover user: %w", err)
	}
	if result.AdAccounts, err = c.ListAdAccounts(ctx, accessToken); err != nil {
		return result, fmt.Errorf("meta: discover ad accounts: %w", err)
	}
	if result.Businesses, err = c.ListBusinesses(ctx, accessToken); err != nil {
		result.Failures = append(result.Failures, discoveryFailure("businesses", "", "", err))
	}
	if result.Pages, err = c.ListPages(ctx, accessToken); err != nil {
		result.Failures = append(result.Failures, discoveryFailure("pages", "", "", err))
	}
	for _, business := range result.Businesses {
		datasets, datasetErr := c.ListBusinessDatasets(ctx, accessToken, business.ID)
		if datasetErr != nil {
			result.Failures = append(result.Failures, discoveryFailure("datasets", "", business.ID, datasetErr))
			continue
		}
		result.BusinessDatasets[business.ID] = datasets
	}

	type accountOutput struct {
		key      string
		assets   AccountAssets
		failures []DiscoveryFailure
	}
	jobs := make(chan AdAccount)
	outputs := make(chan accountOutput)
	workerCount := min(maxConcurrency, max(1, len(result.AdAccounts)))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for account := range jobs {
				assets, failures := c.discoverAccountAssets(ctx, accessToken, account)
				if account.Business != nil {
					assets.Datasets = append([]Dataset(nil), result.BusinessDatasets[account.Business.ID]...)
				}
				outputs <- accountOutput{
					key:      accountKey(account),
					assets:   assets,
					failures: failures,
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, account := range result.AdAccounts {
			select {
			case <-ctx.Done():
				return
			case jobs <- account:
			}
		}
	}()
	go func() {
		workers.Wait()
		close(outputs)
	}()

	for output := range outputs {
		result.Assets[output.key] = output.assets
		result.Failures = append(result.Failures, output.failures...)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) discoverAccountAssets(
	ctx context.Context,
	accessToken string,
	account AdAccount,
) (AccountAssets, []DiscoveryFailure) {
	assets := AccountAssets{AdAccount: account}
	accountID := accountKey(account)
	var failures []DiscoveryFailure

	instagram, err := c.ListInstagramAccounts(ctx, accessToken, accountID)
	if err != nil {
		connected, connectedErr := c.ListConnectedInstagramAccounts(ctx, accessToken, accountID)
		if connectedErr != nil {
			failures = append(failures, discoveryFailure("instagram_accounts", accountID, "", err))
		} else {
			assets.InstagramAccounts = connected
		}
	} else {
		assets.InstagramAccounts = instagram
	}
	if assets.Pixels, err = c.ListPixels(ctx, accessToken, accountID); err != nil {
		failures = append(failures, discoveryFailure("pixels", accountID, "", err))
	}
	if assets.CustomConversions, err = c.ListCustomConversions(ctx, accessToken, accountID); err != nil {
		failures = append(failures, discoveryFailure("custom_conversions", accountID, "", err))
	}
	if assets.CustomAudiences, err = c.ListCustomAudiences(ctx, accessToken, accountID); err != nil {
		failures = append(failures, discoveryFailure("custom_audiences", accountID, "", err))
	}
	if assets.Applications, err = c.ListAdvertisableApplications(ctx, accessToken, accountID); err != nil {
		failures = append(failures, discoveryFailure("advertisable_applications", accountID, "", err))
	}
	return assets, failures
}

func discoveryFailure(scope, accountID, businessID string, err error) DiscoveryFailure {
	failure := DiscoveryFailure{
		Scope:      scope,
		AccountID:  accountID,
		BusinessID: businessID,
		Message:    err.Error(),
	}
	var graphErr *GraphError
	if errors.As(err, &graphErr) {
		failure.Graph = graphErr
	}
	return failure
}

func fieldsQuery(fields string) url.Values {
	return url.Values{
		"fields": {fields},
		"limit":  {"250"},
	}
}

func AdAccountNodeID(accountID string) string {
	accountID = strings.TrimSpace(strings.TrimPrefix(accountID, "/"))
	if strings.HasPrefix(accountID, "act_") {
		return accountID
	}
	return "act_" + accountID
}

func accountKey(account AdAccount) string {
	if account.AccountID != "" {
		return account.AccountID
	}
	return strings.TrimPrefix(account.ID, "act_")
}
