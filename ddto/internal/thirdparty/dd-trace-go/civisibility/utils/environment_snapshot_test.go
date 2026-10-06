package utils

import (
	"fmt"
	"sync"
	"testing"
)

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

func TestCITagsSnapshotRetainsLateDirectEditsAndNoopRevision(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)
	originalCiTags = map[string]string{"fixture": "one"}
	old, revision := GetCITagsSnapshot()
	AddCITags("fixture", "one")
	_, same := GetCITagsSnapshot()
	if same != revision {
		t.Fatal("no-op update changed revision")
	}
	mutable := GetCITags()
	GetCITagsSnapshot()
	mutable["fixture"] = "two"
	next, r := GetCITagsSnapshot()
	if next["fixture"] != "two" || old["fixture"] != "one" || r <= revision {
		t.Fatal("late direct edit lost or old snapshot mutated")
	}
	AddCITags("fixture", "three")
	current, _ := GetCITagsSnapshot()
	mutable["fixture"] = "obsolete"
	if GetCITagsReadOnly()["fixture"] != "three" || current["fixture"] != "three" {
		t.Fatal("obsolete exposed map changed current tags")
	}
}

func TestCITagsSnapshotsConcurrentReadersAndUpdates(t *testing.T) {
	ResetCITags()
	t.Cleanup(ResetCITags)
	originalCiTags = map[string]string{"fixture": "zero"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				snapshot := GetCITagsReadOnly()
				value := snapshot["fixture"]
				if snapshot["fixture"] != value {
					t.Error("snapshot changed")
				}
			}
		})
	}
	for i := range 100 {
		AddCITags("fixture", fmt.Sprint(i))
	}
	wg.Wait()
}

func BenchmarkCITagsSnapshot(b *testing.B) {
	for _, escaped := range []bool{false, true} {
		b.Run(fmt.Sprintf("mutable=%t", escaped), func(b *testing.B) {
			ResetCITags()
			b.Cleanup(ResetCITags)
			originalCiTags = make(map[string]string, 64)
			for i := range 64 {
				originalCiTags[fmt.Sprint(i)] = "fixture-value"
			}
			if escaped {
				GetCITags()
			}
			GetCITagsSnapshot()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				GetCITagsSnapshot()
			}
		})
	}
}
