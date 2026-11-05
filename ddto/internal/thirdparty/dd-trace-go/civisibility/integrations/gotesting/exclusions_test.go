package gotesting

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// A protected descendant changes masking, never the retry family's recovery
// rule. An earlier failed attempt must not turn an ATR/EFD recovery into failure.
func TestProtectedProcessDescendantKeepsRetryFamilyAggregation(t *testing.T) {
	t.Run("protected-only-in-descendant-atf-continuation", func(t *testing.T) {
		group := &deferredProcessRetryGroup{protectedDescendantFailed: true, metadata: processRetryMetadataSnapshot{isAttemptToFix: true}}
		group.outcomes.observe(false, false)
		group.outcomes.observe(false, false)
		if !group.packageFailed() {
			t.Fatal("passing enclosing executions hid protected descendant continuation failure")
		}
	})
	for _, family := range []string{"atr", "efd", "attempt_to_fix"} {
		for _, quarantined := range []bool{false, true} {
			t.Run(family+map[bool]string{false: "", true: "-quarantined"}[quarantined], func(t *testing.T) {
				group := &deferredProcessRetryGroup{protectedDescendantFailed: true, metadata: processRetryMetadataSnapshot{protectedDescendantFailure: true, isQuarantined: quarantined, isFlakyTestRetriesEnabled: family == "atr", isEarlyFlakeDetectionEnabled: family == "efd", isANewTest: family == "efd", isAttemptToFix: family == "attempt_to_fix"}}
				group.outcomes.observe(true, false)
				group.outcomes.observe(false, false)
				if got, want := group.packageFailed(), family == "attempt_to_fix"; got != want {
					t.Fatalf("recovered %s package failure=%t want=%t", family, got, want)
				}
			})
		}
	}
}

func TestExactAttemptToFixAssignmentClearsInheritedProperty(t *testing.T) {
	parent := &testExecutionMetadata{isAttemptToFix: true, assignment: testManagementAssignment{attemptToFix: true}}
	child := &testExecutionMetadata{hasExplicitAttemptToFix: true}
	propagateTestExecutionMetadataFlags(child, parent)
	if child.isAttemptToFix || child.assignment.attemptToFix {
		t.Fatal("explicit false ATF assignment inherited parent's property")
	}
	child.assignment.attemptToFix = true
	applyAdditionalFeatureMetadataToExecution(child, &additionalFeatureMetadata{hasExplicitAttemptToFix: true})
	if child.assignment.attemptToFix {
		t.Fatal("explicit false ATF assignment retained stale inherited property")
	}
}

func TestExcludedResultsKeepDeclarationIdentity(t *testing.T) {
	t.Run("excluded-execution-byte-budget", func(t *testing.T) {
		cfg := processRetryChildConfig{TestName: "Root", Attempt: 1, RetryReason: "retry", ExclusionPolicy: &processRetrySubtreeConfig{Version: processRetrySubtreeVersion, SelectedRoot: "Root", Root: processRetrySubtreeDirective{TestName: "Root", ModuleName: "fixture", SuiteName: "fixture_test.go"}}}
		result := processRetryResult{Version: 1, TestName: cfg.TestName, Attempt: cfg.Attempt, RetryReason: cfg.RetryReason, Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1}
		for i := 0; i < 4097; i++ {
			result.ExcludedTests = append(result.ExcludedTests, processRetrySubtreeResult{TestName: fmt.Sprintf("Root/Excluded/row-%04d", i), ModuleName: "example.com/fixture_test", SuiteName: "fixture_test.go/Suite", Exclusions: TestExclusions{NoAutoRetry: true}, Status: processRetryStatusPass, StartUnixNano: 1, FinishUnixNano: 2, DurationNanos: 1})
		}
		payload, err := marshalProcessRetryResult(result)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("excluded execution records=%d bytes=%d wire-limit=%d", len(result.ExcludedTests), len(payload), processRetryWireMaxBytes(true))
		path := filepath.Join(t.TempDir(), "result.json")
		if err = os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		read, _, err := readProcessRetryResult(path, cfg)
		if err != nil || len(read.ExcludedTests) != 4097 {
			t.Fatalf("valid execution records rejected/lost: count=%d err=%v", len(read.ExcludedTests), err)
		}
		result.ExcludedTests[len(result.ExcludedTests)-1].TestName = "Other/row"
		if validateProcessRetryResult(result, cfg) == nil {
			t.Fatal("unrelated execution identity accepted")
		}
		result.ExcludedTests[len(result.ExcludedTests)-1].TestName = result.ExcludedTests[0].TestName
		if validateProcessRetryResult(result, cfg) == nil {
			t.Fatal("duplicate execution identity accepted")
		}
		if err = os.WriteFile(path, bytes.Repeat([]byte{' '}, processRetryWireMaxBytes(true)+1), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err = readProcessRetryResult(path, cfg); err == nil {
			t.Fatal("oversized excluded execution frame accepted")
		}
		result.ExcludedTests[0].TestName = "Root/" + string(bytes.Repeat([]byte{'x'}, processRetryWireMaxBytes(true)))
		if _, err = marshalProcessRetryResult(result); err == nil {
			t.Fatal("oversized execution writer dropped events or exceeded budget")
		}
	})
	t.Run("isolation-root-identity", func(t *testing.T) {
		for _, tc := range []struct {
			key  string
			want bool
		}{{"Root\x00declaration", true}, {"Root/child\x00declaration", true}, {"Root/child", true}, {"Root", false}, {"Root\x00", false}, {"RootSibling\x00declaration", false}, {"Other/Root\x00declaration", false}} {
			if got := excludedResultWithinRoot(tc.key, "Root"); got != tc.want {
				t.Fatalf("cache key%q withinroot=%t want%t", tc.key, got, tc.want)
			}
		}
		cache := &excludedTestResults{items: map[string]excludedTestResult{"Root\x00declaration": {}, "Other\x00declaration": {}}}
		if snapshot := cache.snapshotForRoot("Root"); len(snapshot) != 1 {
			t.Fatalf("selected-root cache lost: %v", snapshot)
		}
	})
	t.Run("compact-transported-subtree", func(t *testing.T) {
		cache := &excludedTestResults{items: map[string]excludedTestResult{
			"Root/Excluded\x00parent":            {Failed: true},
			"Root/Excluded/row\x00callback":      {Failed: true, Protected: true, Inherited: true},
			"Root/Excluded/row/deep\x00callback": {Failed: true, Protected: true, Inherited: true},
			"Root/Other\x00first":                {},
			"Root/Other\x00second":               {},
			"Root/Other/row\x00callback":         {},
			"Root/Prefix\x00passed":              {},
			"Root/Prefix/failed\x00generated":    {Failed: true, Protected: true, Inherited: true},
		}}
		snapshot := cache.snapshot()
		if len(snapshot) != 6 || !snapshot["Root/Excluded\x00parent"].Protected || len(cache.items) != 8 {
			t.Fatalf("compaction lost protected outcome or declaration history: %v local=%v", snapshot, cache.items)
		}
		if !snapshot["Root/Prefix/failed\x00generated"].Protected {
			t.Fatal("native slash-label failure was lost")
		}
		if _, ok := snapshot["Root/Other/row\x00callback"]; !ok {
			t.Fatal("ambiguous ancestor discarded declaration-specific result")
		}
	})
	results := &excludedTestResults{}
	t.Run("same-native-name", func(t *testing.T) {
		results.record(t, false, "first-declaration")
		if results.replay(t, "ordinary-other-declaration") {
			t.Fatal("cache crossed declarations")
		}
		results.record(t, true, "second-declaration")
		if len(results.snapshot()) != 2 {
			t.Fatal("one declaration overwrote another")
		}
		if !results.replay(t, "first-declaration") || !results.replay(t, "second-declaration") {
			t.Fatal("declared native result missing")
		}
	})
}

func TestRetainedExcludedFailuresKeepFamilyScope(t *testing.T) {
	for _, tc := range []struct {
		name, key                                    string
		failed, protected, wantFailed, wantProtected bool
	}{
		{"descendant", "Root/child\x00declaration", true, true, true, true},
		{"inherited-owner", "Root/row\x00owner\x01callback", true, false, true, false},
		{"independent-at-root", "Root\x00another-declaration", true, true, false, false},
		{"other-native-root", "RootOther/child\x00declaration", true, true, false, false},
		{"passing-protected", "Root/child\x00declaration", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &excludedTestResults{items: map[string]excludedTestResult{tc.key: {Failed: tc.failed, Protected: tc.protected}}}
			failed, protected := cache.failedDescendants("Root")
			if failed != tc.wantFailed || protected != tc.wantProtected {
				t.Fatalf("retained outcome=(%t,%t) want(%t,%t)", failed, protected, tc.wantFailed, tc.wantProtected)
			}
		})
	}
	for _, quarantined := range []bool{false, true} {
		for _, protected := range []bool{false, true} {
			t.Run(fmt.Sprintf("deferred/quarantined=%t/protected=%t", quarantined, protected), func(t *testing.T) {
				group := &deferredProcessRetryGroup{identity: *newTestIdentity("fixture", "fixture_test.go", "Root"), metadata: processRetryMetadataSnapshot{isQuarantined: quarantined, isFlakyTestRetriesEnabled: true, exclusionResults: &excludedTestResults{items: map[string]excludedTestResult{"Root/child\x00declaration": {Failed: true, Protected: protected}}}}}
				group.outcomes.observe(true, false)
				group.outcomes.observe(false, false)
				if got, want := group.packageFailed(), !quarantined || protected; got != want {
					t.Fatalf("retained group failure=%t want%t", got, want)
				}
			})
		}
	}
}

func TestExcludedResultsTerminalAdmission(t *testing.T) {
	t.Run("nil-keeps-legacy-admission", func(t *testing.T) {
		var results *excludedTestResults
		if !results.begin() {
			t.Fatal("nil result owner blocked legacy callback")
		}
		results.end()
		if !results.freezeForTerminal() {
			t.Fatal("nil result owner blocked legacy terminal recovery")
		}
	})
	t.Run("quiescent-freeze-closes-admission", func(t *testing.T) {
		results := &excludedTestResults{}
		if !results.freezeForTerminal() {
			t.Fatal("quiescent terminal snapshot was not eligible")
		}
		calls := 0
		for range 10 {
			if results.begin() {
				calls++
				results.end()
			}
		}
		if calls != 0 {
			t.Fatalf("terminal snapshot admitted%d new callback bodies", calls)
		}
		if len(results.snapshot()) != 0 {
			t.Fatal("terminal freeze synthesized a completed declaration")
		}
	})
	t.Run("active-callback-prevents-ready", func(t *testing.T) {
		results := &excludedTestResults{}
		if !results.begin() {
			t.Fatal("initial callback was not admitted")
		}
		if results.freezeForTerminal() {
			t.Fatal("terminal snapshot was eligible while admitted callback remained active")
		}
		if results.begin() {
			results.end()
			t.Fatal("terminal freeze admitted another callback")
		}
		if len(results.snapshot()) != 0 {
			t.Fatal("active callback synthesized a completed declaration")
		}
		results.end()
		if results.begin() {
			results.end()
			t.Fatal("ending active callback reopened terminal admission")
		}
	})
	t.Run("completed-record-precedes-end", func(t *testing.T) {
		results := &excludedTestResults{}
		if !results.begin() {
			t.Fatal("callback was not admitted")
		}
		func() {
			defer results.end()
			defer results.record(t, true, "completed-before-end")
		}()
		if !results.freezeForTerminal() {
			t.Fatal("completed callback left terminal snapshot ineligible")
		}
		snapshot := results.snapshot()
		if len(snapshot) != 1 {
			t.Fatalf("completed callback record lost: %v", snapshot)
		}
		for _, result := range snapshot {
			if !result.Protected {
				t.Fatalf("completed callback protection lost: %v", snapshot)
			}
		}
		if results.begin() {
			results.end()
			t.Fatal("completed record reopened terminal admission")
		}
	})
	t.Run("recorded-but-still-active-remains-conservative", func(t *testing.T) {
		results := &excludedTestResults{}
		if !results.begin() {
			t.Fatal("callback was not admitted")
		}
		results.record(t, true, "record-before-end")
		if results.freezeForTerminal() {
			t.Fatal("terminal snapshot ignored an admitted callback before its end")
		}
		if len(results.snapshot()) != 1 {
			t.Fatal("terminal snapshot lost a completed record while another callback boundary remained active")
		}
		results.end()
		if len(results.snapshot()) != 1 {
			t.Fatal("ending callback discarded completed history")
		}
	})
}

// The accepted callback stays active until both competing operations return.
// A Ready-eligible freeze and an accepted active callback must never coexist,
// regardless of which operation wins admission's serialization boundary.
func TestExcludedResultsAdmissionCompetesWithTerminalFreeze(t *testing.T) {
	for i := range 200 {
		results := &excludedTestResults{}
		start, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		admitted, ready := make(chan bool, 1), make(chan bool, 1)
		go func() {
			<-start
			accepted := results.begin()
			admitted <- accepted
			if accepted {
				<-release
				results.record(t, false, "racing-completed-declaration")
				results.end()
			}
			close(done)
		}()
		go func() { <-start; ready <- results.freezeForTerminal() }()
		close(start)
		accepted, eligible := <-admitted, <-ready
		if accepted == eligible {
			close(release)
			<-done
			t.Fatalf("iteration%d accepted-active=%t terminal-Ready=%t", i, accepted, eligible)
		}
		if len(results.snapshot()) != 0 {
			close(release)
			<-done
			t.Fatalf("iteration%d terminal synthesized an unfinished declaration", i)
		}
		if results.begin() {
			results.end()
			close(release)
			<-done
			t.Fatalf("iteration%d terminal admitted a later callback", i)
		}
		close(release)
		<-done
		wantRecords := 0
		if accepted {
			wantRecords = 1
		}
		if got := len(results.snapshot()); got != wantRecords {
			t.Fatalf("iteration%d completed history count%d want%d", i, got, wantRecords)
		}
	}
}
