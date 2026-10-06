package utils

import "testing"

func TestCITagsSnapshotUpdatesAndRetainsOldValues(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)
	originalCiTags = map[string]string{"ci.job.name": "one"}
	one, revision := GetCITagsSnapshot()
	_, same := GetCITagsSnapshot()
	if same != revision {
		t.Fatal("unchanged tags invalidated the snapshot")
	}
	AddCITags("ci.job.name", "two")
	two, next := GetCITagsSnapshot()
	if next <= revision || two["ci.job.name"] != "two" || one["ci.job.name"] != "one" {
		t.Fatal("published update changed an older snapshot")
	}
	// The upstream API's cached map permits sequential direct edits. Preserve
	// that contract without letting an old immutable Mini snapshot change.
	GetCITags()["ci.job.name"] = "direct"
	direct, last := GetCITagsSnapshot()
	if last <= next || direct["ci.job.name"] != "direct" || two["ci.job.name"] != "two" {
		t.Fatal("direct cached-map update was missed")
	}
	ResetCITags()
	originalCiTags = map[string]string{"os.platform": "reset"}
	reset, last := GetCITagsSnapshot()
	if last <= next || reset["os.platform"] != "reset" || reset["ci.job.name"] != "" {
		t.Fatal("reset kept stale CI values")
	}
}
