package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindPublishedObjectByNameMatchesParentAcrossPages(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v25.0/act_123/adsets" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.URL.Query().Get("after") == "" {
			if request.URL.Query().Get("fields") == "" || request.URL.Query().Get("limit") != "500" {
				t.Fatalf("query = %v", request.URL.Query())
			}
			_, _ = writer.Write([]byte(`{
				"data":[{"id":"wrong-parent","name":"Set [RP:id]","campaign_id":"other"}],
				"paging":{"next":"` + server.URL + `/v25.0/act_123/adsets?after=cursor"}
			}`))
			return
		}
		_, _ = writer.Write([]byte(`{
			"data":[{"id":"right","name":"Set [RP:id]","campaign_id":"campaign"}]
		}`))
	}))
	defer server.Close()

	object, found, err := testClient(t, server.URL, 1, nil).FindPublishedObjectByName(
		context.Background(),
		"token",
		"123",
		PublishedObjectAdSet,
		"Set [RP:id]",
		"campaign",
	)
	if err != nil || !found || object.ID != "right" {
		t.Fatalf("object=%#v found=%t err=%v", object, found, err)
	}
}

func TestFindPublishedObjectByNameRefusesAmbiguousMatch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{
			"data":[
				{"id":"one","name":"Campaign [RP:id]"},
				{"id":"two","name":"Campaign [RP:id]"}
			]
		}`))
	}))
	defer server.Close()

	_, found, err := testClient(t, server.URL, 1, nil).FindPublishedObjectByName(
		context.Background(),
		"token",
		"123",
		PublishedObjectCampaign,
		"Campaign [RP:id]",
		"",
	)
	if err == nil || found {
		t.Fatalf("found=%t err=%v", found, err)
	}
}
