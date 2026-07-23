package meta

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHierarchyValidateRejectsLegacyObjectiveForCreation(t *testing.T) {
	t.Parallel()
	hierarchy := validHierarchy()
	hierarchy.Campaign.Objective = LegacyObjectiveConversions
	err := hierarchy.Validate()
	if err == nil || !strings.Contains(err.Error(), "six ODAX objectives") {
		t.Fatalf("Validate error = %v", err)
	}
	if !hierarchy.Campaign.Objective.IsLegacy() {
		t.Error("legacy objective not recognized for reads")
	}
}

func TestRawFieldsDeepMergeTypedPayload(t *testing.T) {
	t.Parallel()
	spec := AdSetSpec{
		Name: "Ad set",
		Targeting: Targeting{
			AgeMin:       21,
			GeoLocations: map[string]any{"countries": []string{"AE"}},
			Raw: RawFields{
				"age_min":       30,
				"geo_locations": map[string]any{"location_types": []string{"home"}},
			},
		},
		Raw: RawFields{
			"targeting":     RawFields{"age_max": 55},
			"new_v25_field": true,
		},
	}
	payload, err := adSetPayload(spec)
	if err != nil {
		t.Fatal(err)
	}
	targeting := payload["targeting"].(map[string]any)
	geo := targeting["geo_locations"].(map[string]any)
	if targeting["age_min"] != 30 || targeting["age_max"] != 55 ||
		geo["location_types"] == nil || geo["countries"] == nil || payload["new_v25_field"] != true {
		t.Errorf("payload = %#v", payload)
	}
}

func TestRawFieldsAreAvailableInRESTJSON(t *testing.T) {
	t.Parallel()
	hierarchy := validHierarchy()
	hierarchy.AdSet.Raw = RawFields{"new_field": "value"}
	encoded, err := json.Marshal(hierarchy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"raw":{"new_field":"value"}`) {
		t.Errorf("REST JSON does not expose raw: %s", encoded)
	}
}

func TestCampaignDefaultsSpecialCategoryNone(t *testing.T) {
	t.Parallel()
	payload, err := campaignPayload(CampaignSpec{Name: "Campaign", Objective: ObjectiveOutcomeTraffic})
	if err != nil {
		t.Fatal(err)
	}
	categories, ok := payload["special_ad_categories"].([]string)
	if !ok || len(categories) != 1 || categories[0] != "NONE" {
		t.Errorf("categories = %#v", payload["special_ad_categories"])
	}
}
