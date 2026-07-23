package meta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func TestPublisherCreatesPausedThenActivatesBottomUp(t *testing.T) {
	t.Parallel()
	var mutex sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := decodeJSONBody(t, request)
		mutex.Lock()
		calls = append(calls, request.URL.Path)
		mutex.Unlock()

		switch request.URL.Path {
		case "/v25.0/act_123/campaigns":
			if payload["status"] != "PAUSED" {
				t.Errorf("campaign status = %#v", payload["status"])
			}
			if _, exists := payload["raw"]; exists {
				t.Errorf("campaign leaked REST-only raw control field: %#v", payload)
			}
			categories, _ := payload["special_ad_categories"].([]any)
			if len(categories) != 1 || categories[0] != "ONLINE_GAMBLING_AND_GAMING" {
				t.Errorf("special categories = %#v", payload["special_ad_categories"])
			}
			_, _ = writer.Write([]byte(`{"id":"campaign-id"}`))
		case "/v25.0/act_123/adsets":
			if payload["status"] != "PAUSED" || payload["campaign_id"] != "campaign-id" {
				t.Errorf("ad set payload = %#v", payload)
			}
			targeting, _ := payload["targeting"].(map[string]any)
			if _, exists := targeting["raw"]; exists {
				t.Errorf("targeting leaked REST-only raw control field: %#v", targeting)
			}
			_, _ = writer.Write([]byte(`{"id":"adset-id"}`))
		case "/v25.0/act_123/adcreatives":
			_, _ = writer.Write([]byte(`{"id":"creative-id"}`))
		case "/v25.0/act_123/ads":
			creative, _ := payload["creative"].(map[string]any)
			if payload["status"] != "PAUSED" || payload["adset_id"] != "adset-id" ||
				creative["creative_id"] != "creative-id" {
				t.Errorf("ad payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"id":"ad-id"}`))
		case "/v25.0/ad-id", "/v25.0/adset-id", "/v25.0/campaign-id":
			if payload["status"] != "ACTIVE" {
				t.Errorf("activation payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1, nil)
	result := (Publisher{Client: client}).PublishAccount(context.Background(), AccountPublishRequest{
		AccountID:   "123",
		AccessToken: "token",
		Hierarchy:   validHierarchy(),
	})
	if !result.Success || !result.Activated {
		t.Fatalf("result = %#v", result)
	}
	if result.CampaignID != "campaign-id" || result.AdSetID != "adset-id" ||
		result.CreativeID != "creative-id" || result.AdID != "ad-id" {
		t.Errorf("IDs = %#v", result)
	}
	expectedCalls := []string{
		"/v25.0/act_123/campaigns",
		"/v25.0/act_123/adsets",
		"/v25.0/act_123/adcreatives",
		"/v25.0/act_123/ads",
		"/v25.0/ad-id",
		"/v25.0/adset-id",
		"/v25.0/campaign-id",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Errorf("calls = %#v, want %#v", calls, expectedCalls)
	}
	if result.FinishedAt.IsZero() {
		t.Error("FinishedAt was not set")
	}
}

func TestPublisherPartialResultLeavesFailedHierarchyPaused(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v25.0/act_123/campaigns":
			_, _ = writer.Write([]byte(`{"id":"campaign-id"}`))
		case "/v25.0/act_123/adsets":
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":{"message":"invalid targeting","code":100}}`))
		default:
			t.Errorf("unexpected call after ad set failure: %s", request.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1, nil)
	result := (Publisher{Client: client}).PublishAccount(context.Background(), AccountPublishRequest{
		AccountID:   "123",
		AccessToken: "token",
		Hierarchy:   validHierarchy(),
	})
	if result.Success || result.Activated {
		t.Fatalf("result = %#v", result)
	}
	if result.CampaignID != "campaign-id" || result.AdSetID != "" {
		t.Errorf("partial IDs = %#v", result)
	}
	last := result.Stages[len(result.Stages)-1]
	if last.Name != "create_ad_set" || last.Failure == nil || last.Failure.Graph == nil {
		t.Errorf("failure stage = %#v", last)
	}
}

func TestPublisherDoesNotRetryNonIdempotentCreate(t *testing.T) {
	t.Parallel()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/v25.0/act_123/campaigns" {
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"temporary","code":2,"is_transient":true}}`))
	}))
	defer server.Close()
	result := (Publisher{Client: testClient(t, server.URL, 4, nil)}).PublishAccount(
		context.Background(),
		AccountPublishRequest{
			AccountID:   "123",
			AccessToken: "token",
			Hierarchy:   validHierarchy(),
		},
	)
	if result.Success || calls != 1 {
		t.Fatalf("result=%#v calls=%d", result, calls)
	}
	failure := result.Stages[len(result.Stages)-1].Failure
	if failure == nil || !failure.Retryable {
		t.Fatalf("transient failure was not marked retryable: %#v", result.Stages)
	}
}

func TestCreateCampaignForcesTypedNameOverRawForReconciliation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := decodeJSONBody(t, request)
		if payload["name"] != "typed [RP:marker]" {
			t.Fatalf("name = %#v", payload["name"])
		}
		_, _ = writer.Write([]byte(`{"id":"campaign-id"}`))
	}))
	defer server.Close()

	result, err := testClient(t, server.URL, 1, nil).CreateCampaign(
		context.Background(),
		"token",
		"123",
		CampaignSpec{
			Name:      "typed [RP:marker]",
			Objective: ObjectiveOutcomeSales,
			Raw:       RawFields{"name": "unsafe raw name"},
		},
		false,
	)
	if err != nil || result.ID != "campaign-id" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestStandaloneCreatesReturnRetryableResponseErrorForSuccessfulEmptyID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		call func(*Client) (CreateResult, error)
	}{
		{
			name: "campaign",
			path: "/v25.0/act_123/campaigns",
			call: func(client *Client) (CreateResult, error) {
				return client.CreateCampaign(
					context.Background(),
					"token",
					"123",
					CampaignSpec{Name: "Campaign", Objective: ObjectiveOutcomeSales},
					false,
				)
			},
		},
		{
			name: "ad set",
			path: "/v25.0/act_123/adsets",
			call: func(client *Client) (CreateResult, error) {
				return client.CreateAdSet(
					context.Background(),
					"token",
					"123",
					"campaign-id",
					AdSetSpec{Name: "Ad set"},
					false,
				)
			},
		},
		{
			name: "creative",
			path: "/v25.0/act_123/adcreatives",
			call: func(client *Client) (CreateResult, error) {
				return client.CreateCreative(
					context.Background(),
					"token",
					"123",
					CreativeSpec{Name: "Creative"},
					false,
				)
			},
		},
		{
			name: "ad",
			path: "/v25.0/act_123/ads",
			call: func(client *Client) (CreateResult, error) {
				return client.CreateAd(
					context.Background(),
					"token",
					"123",
					"ad-set-id",
					"creative-id",
					AdSpec{Name: "Ad"},
					false,
				)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.URL.Path != test.path {
					t.Errorf("path = %q, want %q", request.URL.Path, test.path)
				}
				_, _ = writer.Write([]byte(`{}`))
			}))
			defer server.Close()

			_, err := test.call(testClient(t, server.URL, 4, nil))
			var responseErr *ResponseError
			if !errors.As(err, &responseErr) {
				t.Fatalf("error = %T %v, want *ResponseError", err, err)
			}
			if !IsRetryableError(err) || !responseErr.Retryable() {
				t.Fatalf("empty-ID response is not durable-retryable: %v", err)
			}
			if calls != 1 {
				t.Fatalf("non-idempotent create calls = %d, want 1", calls)
			}
		})
	}
}

func TestValidateOnlyAllowsSuccessfulResponseWithoutID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	_, err := testClient(t, server.URL, 1, nil).CreateCampaign(
		context.Background(),
		"token",
		"123",
		CampaignSpec{Name: "Campaign", Objective: ObjectiveOutcomeSales},
		true,
	)
	if err != nil {
		t.Fatalf("validate-only response without ID: %v", err)
	}
}

func TestPublisherResumesDurableCheckpoint(t *testing.T) {
	t.Parallel()
	var calls []string
	var stages []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.URL.Path)
		switch request.URL.Path {
		case "/v25.0/act_123/adsets":
			payload := decodeJSONBody(t, request)
			if payload["campaign_id"] != "saved-campaign" {
				t.Errorf("campaign_id = %#v", payload["campaign_id"])
			}
			_, _ = writer.Write([]byte(`{"id":"adset-id"}`))
		case "/v25.0/act_123/adcreatives":
			_ = decodeJSONBody(t, request)
			_, _ = writer.Write([]byte(`{"id":"creative-id"}`))
		case "/v25.0/act_123/ads":
			_ = decodeJSONBody(t, request)
			_, _ = writer.Write([]byte(`{"id":"ad-id"}`))
		case "/v25.0/ad-id", "/v25.0/adset-id", "/v25.0/saved-campaign":
			_ = decodeJSONBody(t, request)
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
	}))
	defer server.Close()
	result := (Publisher{Client: testClient(t, server.URL, 1, nil)}).PublishAccount(
		context.Background(),
		AccountPublishRequest{
			AccountID:   "123",
			AccessToken: "token",
			Hierarchy:   validHierarchy(),
			Resume:      PublishResume{CampaignID: "saved-campaign"},
			OnStage: func(stage PublishStage) error {
				stages = append(stages, stage.Name)
				return nil
			},
		},
	)
	if !result.Success || result.CampaignID != "saved-campaign" {
		t.Fatalf("result = %#v", result)
	}
	for _, call := range calls {
		if call == "/v25.0/act_123/campaigns" {
			t.Fatal("resumed campaign was created again")
		}
	}
	expectedStages := []string{
		"create_ad_set",
		"create_creative",
		"create_ad",
		"activate_ad",
		"activate_ad_set",
		"activate_campaign",
	}
	if !reflect.DeepEqual(stages, expectedStages) {
		t.Fatalf("callback stages = %#v, want %#v", stages, expectedStages)
	}
}

func TestPublisherStopsWhenCheckpointFails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/act_123/campaigns" {
			t.Fatalf("unexpected request after checkpoint failure: %s", request.URL.Path)
		}
		_ = decodeJSONBody(t, request)
		_, _ = writer.Write([]byte(`{"id":"campaign-id"}`))
	}))
	defer server.Close()
	result := (Publisher{Client: testClient(t, server.URL, 1, nil)}).PublishAccount(
		context.Background(),
		AccountPublishRequest{
			AccountID:   "123",
			AccessToken: "token",
			Hierarchy:   validHierarchy(),
			OnStage: func(PublishStage) error {
				return errors.New("database unavailable")
			},
		},
	)
	if result.Success {
		t.Fatalf("result = %#v", result)
	}
	last := result.Stages[len(result.Stages)-1]
	if last.Name != "checkpoint_create_campaign" || last.Failure == nil {
		t.Fatalf("last stage = %#v", last)
	}
}

func TestPublisherValidateOnlyUsesGraphWhereDependenciesNotNeeded(t *testing.T) {
	t.Parallel()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := decodeJSONBody(t, request)
		paths = append(paths, request.URL.Path)
		options, _ := payload["execution_options"].([]any)
		if len(options) != 1 || options[0] != "validate_only" {
			t.Errorf("execution_options = %#v", payload["execution_options"])
		}
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	result := (Publisher{Client: testClient(t, server.URL, 1, nil)}).PublishAccount(
		context.Background(),
		AccountPublishRequest{
			AccountID:    "123",
			AccessToken:  "token",
			Hierarchy:    validHierarchy(),
			ValidateOnly: true,
		},
	)
	if !result.Success || !result.Validated || result.Activated {
		t.Fatalf("result = %#v", result)
	}
	expected := []string{"/v25.0/act_123/campaigns", "/v25.0/act_123/adcreatives"}
	if !reflect.DeepEqual(paths, expected) {
		t.Errorf("paths = %#v, want %#v", paths, expected)
	}
}

func TestPauseEntity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := decodeJSONBody(t, request)
		if request.URL.Path != "/v25.0/42" || payload["status"] != "PAUSED" {
			t.Errorf("request %s %#v", request.URL.Path, payload)
		}
		_, _ = writer.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	if err := testClient(t, server.URL, 1, nil).PauseEntity(context.Background(), "token", "42"); err != nil {
		t.Fatalf("PauseEntity: %v", err)
	}
}

func TestGetEntityStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/42" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("fields") != "id,status,configured_status,effective_status" {
			t.Errorf("fields = %q", request.URL.Query().Get("fields"))
		}
		_, _ = writer.Write([]byte(`{"id":"42","status":"ACTIVE","configured_status":"ACTIVE","effective_status":"PENDING_REVIEW"}`))
	}))
	defer server.Close()
	status, err := testClient(t, server.URL, 1, nil).GetEntityStatus(context.Background(), "token", "42")
	if err != nil {
		t.Fatalf("GetEntityStatus: %v", err)
	}
	if status.ID != "42" || status.EffectiveStatus != "PENDING_REVIEW" {
		t.Fatalf("status = %#v", status)
	}
}

func validHierarchy() HierarchySpec {
	return HierarchySpec{
		Campaign: CampaignSpec{
			Name:                "Campaign",
			Objective:           ObjectiveOutcomeSales,
			SpecialAdCategories: []SpecialAdCategory{SpecialAdCategoryOnlineGamblingGaming},
			Raw:                 RawFields{"status": "ACTIVE"},
		},
		AdSet: AdSetSpec{
			Name:             "Ad set",
			BillingEvent:     BillingEventImpressions,
			OptimizationGoal: OptimizationGoalOffsiteConversions,
			DestinationType:  DestinationWebsite,
			DailyBudget:      1000,
			Targeting: Targeting{
				GeoLocations: map[string]any{"countries": []string{"AE"}},
			},
			PromotedObject: &PromotedObject{
				PixelID:         "pixel",
				CustomEventType: "COMPLETE_REGISTRATION",
			},
			Raw: RawFields{
				"status":      "ACTIVE",
				"campaign_id": "unsafe-campaign",
			},
		},
		Creative: CreativeSpec{
			Name: "Creative",
			ObjectStorySpec: &ObjectStorySpec{
				PageID: "page",
				LinkData: &LinkData{
					Link:      "https://example.com",
					Message:   "Message",
					ImageHash: "hash",
				},
			},
		},
		Ad: AdSpec{
			Name: "Ad",
			Raw: RawFields{
				"status":   "ACTIVE",
				"adset_id": "unsafe-adset",
				"creative": map[string]any{"creative_id": "unsafe-creative"},
			},
		},
	}
}

func TestCreateResultPreservesRawResponse(t *testing.T) {
	t.Parallel()
	var result CreateResult
	if err := json.Unmarshal([]byte(`{"id":"1","unknown":{"x":true}}`), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != "1" || string(result.Raw["unknown"]) != `{"x":true}` {
		t.Errorf("result = %#v", result)
	}
}
