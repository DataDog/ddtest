package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A test that fails and then skips fails, as testing reports it, also without
// a retry or test management feature, which close the event themselves.
func TestMiniSkipAfterFailureReportsFailure(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "zz_skip_after_failure_test.go"), []byte(`package fixture_test

import "testing"

func TestFailThenSkip(t *testing.T) {
	t.Error("failure")
	t.Skip("reason")
}

func TestFailThenSkipNow(t *testing.T) {
	t.Error("failure")
	t.SkipNow()
}

func TestOnlySkip(t *testing.T) { t.Skip("reason") }

// A deferred call that fails the test after it skips, as a leak check can.
func TestSkipThenDeferredFailure(t *testing.T) {
	defer t.Error("deferred failure")
	t.Skip("reason")
}

func TestSkipNowThenDeferredFailure(t *testing.T) {
	defer t.Error("deferred failure")
	t.SkipNow()
}

// A subtest that -skip excludes is never called, nil included.
func TestNilSkippedSubtest(t *testing.T) { t.Run("excluded", nil) }

func BenchmarkSkipThenDeferredFailure(b *testing.B) {
	defer b.Error("deferred failure")
	b.Skip("reason")
}

func BenchmarkOnlySkip(b *testing.B) { b.Skip("reason") }

func BenchmarkSkippingSub(b *testing.B) {
	b.Run("sub", func(b *testing.B) {
		defer b.Error("deferred failure")
		b.SkipNow()
	})
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := compileMini(t, dir, driver)
	if nilSubtest := execute(t, dir, bin, []string{"-test.run=^TestNilSkippedSubtest$", "-test.skip=^TestNilSkippedSubtest$/^excluded$"}, true, false); nilSubtest.code != 0 {
		t.Fatalf("a nil subtest that -skip excludes: exit %d\n%s", nilSubtest.code, nilSubtest.stderr)
	}
	got := execute(t, dir, bin, []string{"-test.run=^Test(FailThenSkip|FailThenSkipNow|OnlySkip|SkipThenDeferredFailure|SkipNowThenDeferredFailure)$"}, true, false)
	statuses := map[string]string{}
	for _, event := range got.wireEvents {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] == "test" {
			statuses[fmt.Sprint(meta["test.name"])] = fmt.Sprint(meta["test.status"], "/", meta["test.final_status"])
			if meta["test.name"] == "TestOnlySkip" && meta["test.skip_reason"] != "reason" {
				t.Errorf("TestOnlySkip skip reason %v", meta["test.skip_reason"])
			}
		}
	}
	want := map[string]string{"TestFailThenSkip": "fail/fail", "TestFailThenSkipNow": "fail/fail", "TestOnlySkip": "skip/skip", "TestSkipThenDeferredFailure": "fail/fail", "TestSkipNowThenDeferredFailure": "fail/fail"}
	if got.code != 1 || fmt.Sprint(statuses) != fmt.Sprint(want) {
		t.Fatalf("exit %d, statuses %v; want 1, %v\n%s", got.code, statuses, want, got.stderr)
	}

	// Benchmarks too: their finalization closes them after deferred calls.
	got = execute(t, dir, compileMini(t, dir, driver), []string{"-test.run=^$", "-test.bench=^Benchmark(SkipThenDeferredFailure|OnlySkip|SkippingSub)$", "-test.benchtime=1x"}, true, false)
	statuses = map[string]string{}
	for _, event := range got.wireEvents {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		if event["type"] == "test" {
			statuses[fmt.Sprint(meta["test.name"])] = fmt.Sprint(meta["test.status"])
			if meta["test.status"] == "skip" && (meta["test.skip_reason"] != "reason" || meta["test.final_status"] != "skip") {
				t.Errorf("%v: skip reason %v, final status %v", meta["test.name"], meta["test.skip_reason"], meta["test.final_status"])
			}
		}
	}
	want = map[string]string{"BenchmarkSkipThenDeferredFailure": "fail", "BenchmarkOnlySkip": "skip", "BenchmarkSkippingSub/sub": "fail"}
	for name, status := range want {
		if statuses[name] != status {
			t.Fatalf("exit %d, benchmark statuses %v; want %v\n%s", got.code, statuses, want, got.stderr)
		}
	}
}
