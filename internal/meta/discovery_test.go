package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverKeepsPerEdgePartialResults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v25.0/me":
			_, _ = writer.Write([]byte(`{"id":"user","name":"Buyer"}`))
		case "/v25.0/me/adaccounts":
			_, _ = writer.Write([]byte(`{"data":[{"id":"act_1","account_id":"1","name":"Cab","currency":"USD","business":{"id":"business","name":"Connect"}}]}`))
		case "/v25.0/me/businesses":
			_, _ = writer.Write([]byte(`{"data":[{"id":"business","name":"Connect"}]}`))
		case "/v25.0/me/accounts":
			_, _ = writer.Write([]byte(`{"data":[{"id":"page","name":"Page"}]}`))
		case "/v25.0/business/ads_dataset":
			_, _ = writer.Write([]byte(`{"data":[{"id":"dataset","name":"Dataset","config":"opaque"}]}`))
		case "/v25.0/act_1/instagram_accounts":
			_, _ = writer.Write([]byte(`{"data":[{"id":"instagram","username":"ig"}]}`))
		case "/v25.0/act_1/adspixels":
			_, _ = writer.Write([]byte(`{"data":[{"id":"pixel","name":"Pixel","config":"opaque"}]}`))
		case "/v25.0/act_1/customconversions":
			_, _ = writer.Write([]byte(`{"data":[{"id":"conversion","name":"Registration"}]}`))
		case "/v25.0/act_1/customaudiences":
			_, _ = writer.Write([]byte(`{"data":[{"id":"audience","name":"Audience"}]}`))
		case "/v25.0/act_1/advertisable_applications":
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":{"message":"apps unavailable","code":200}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	result, err := testClient(t, server.URL, 1, nil).Discover(context.Background(), "token", 2)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if result.User.ID != "user" || len(result.AdAccounts) != 1 || len(result.Pages) != 1 {
		t.Fatalf("base discovery = %#v", result)
	}
	assets := result.Assets["1"]
	if len(assets.Pixels) != 1 || len(assets.Datasets) != 1 ||
		len(assets.InstagramAccounts) != 1 || len(assets.CustomConversions) != 1 ||
		len(assets.CustomAudiences) != 1 {
		t.Errorf("assets = %#v", assets)
	}
	if len(result.Failures) != 1 ||
		result.Failures[0].Scope != "advertisable_applications" ||
		result.Failures[0].Graph == nil {
		t.Errorf("failures = %#v", result.Failures)
	}
}
