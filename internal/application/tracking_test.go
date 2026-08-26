package application

import "testing"

func TestEnsureTrackingTags(t *testing.T) {
	got := ensureTrackingTags("")
	if !hasSubID(got, "sub_id_7") || !hasSubID(got, "sub_id_3") {
		t.Fatalf("empty tags must gain both macros, got %q", got)
	}
	got = ensureTrackingTags("sub_id_5=buyer1")
	for _, want := range []string{"sub_id_5=buyer1", "sub_id_7=", "sub_id_3="} {
		if !hasSubID(got, want[:len(want)-1]) && want != "sub_id_5=buyer1" {
			t.Fatalf("merged tags missing %s: %q", want, got)
		}
	}
	// Idempotent: an already-tagged url is not doubled.
	pre := "sub_id_7={{campaign.id}}&sub_id_3={{campaign.name}}"
	if out := ensureTrackingTags(pre); out != pre {
		t.Fatalf("already-tagged url changed: %q", out)
	}
}

func TestTrackingLinkPresent(t *testing.T) {
	if !trackingLinkPresent("https://t.dom/?sub_id_7={{campaign.id}}", "") {
		t.Fatal("tagged link should count")
	}
	if !trackingLinkPresent("https://lander", "sub_id_7={{campaign.id}}") {
		t.Fatal("tagged url_tags should count")
	}
	if trackingLinkPresent("https://lander", "sub_id_5=x") {
		t.Fatal("untagged link+tags should not count")
	}
}

func TestCheckpointsUseTracker(t *testing.T) {
	if checkpointsUseTracker([]GuardCheckpoint{{Spend: 5, MinClicks: 10}}) {
		t.Fatal("fb-only checkpoint must not count as tracker")
	}
	if !checkpointsUseTracker([]GuardCheckpoint{{Spend: 5, MinTrackerLeads: 1}}) {
		t.Fatal("tracker-lead checkpoint must count")
	}
}
