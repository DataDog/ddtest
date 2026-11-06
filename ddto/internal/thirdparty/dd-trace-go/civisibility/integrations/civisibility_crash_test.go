package integrations

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/ddtest/ddto/internal/testassert/require"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
)

// resetCrashForTesting forgets an earlier test's crash.
func resetCrashForTesting(t *testing.T) {
	crashing.Store(false)
	crashClosed = make(chan struct{})
	t.Cleanup(func() {
		crashing.Store(false)
		crashClosed = make(chan struct{})
	})
}

// A crash fails the tests still open, keeping quarantined ones' final status
// skip, and closes CI Visibility before it returns.
func TestCloseForCrashFailsOpenTestsAndCloses(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	resetCrashForTesting(t)
	initializeCIVisibilityLifecycleForTesting()

	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("s")
	running := suite.CreateTest("Running").(*tslvTest)
	quarantined := suite.CreateTest("Quarantined").(*tslvTest)
	quarantined.SetTag(constants.TestIsQuarantined, "true")
	passed := suite.CreateTest("Passed").(*tslvTest)
	passed.Close(ResultStatusPass)

	CloseForCrash("timeout", "test timed out after 1s", "")

	require.Equal(t, civisibility.StateExited, civisibility.GetState())
	for _, tc := range []struct {
		test          *tslvTest
		status, final string
	}{
		{running, constants.TestStatusFail, constants.TestStatusFail},
		{quarantined, constants.TestStatusFail, constants.TestStatusSkip},
		{passed, constants.TestStatusPass, ""},
	} {
		status, _ := tc.test.span.Meta(constants.TestStatus)
		final, _ := tc.test.span.Meta(constants.TestFinalStatus)
		message, _ := tc.test.span.Meta("error.message")
		require.Equal(t, tc.status, status, tc.test.name)
		require.Equal(t, tc.final, final, tc.test.name)
		if tc.test != passed {
			require.Equal(t, "test timed out after 1s", message, tc.test.name)
		}
		_, open := openTests.Load(tc.test)
		require.False(t, open, tc.test.name)
	}
}

// A second crash waits for the first one's close, and a crash without CI
// Visibility running closes nothing.
func TestCloseForCrashOnce(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	resetCrashForTesting(t)

	CloseForCrash("panic", "panic: first", "")
	require.True(t, crashing.Load())
	select {
	case <-crashClosed:
	default:
		t.Fatal("the first crash's close did not end")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		CloseForCrash("panic", "panic: second", "")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the second crash waited for a close that had ended")
	}
}

// Until a crash, the hold that os.Exit takes returns at once.
func TestHoldForCrashReturnsWithoutACrash(t *testing.T) {
	resetCrashForTesting(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		holdForCrash("os.Exit")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("holdForCrash blocked without a crash")
	}
}

// An event that finished and that a barrier closes later, as the fuzz queue
// closes the seeds that completed, keeps its outcome: only tests still
// running fail.
func TestCloseForCrashKeepsFinishedEvents(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	resetCrashForTesting(t)
	initializeCIVisibilityLifecycleForTesting()

	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("s")
	completed := suite.CreateTest("Completed").(*tslvTest)
	running := suite.CreateTest("Running").(*tslvTest)
	MarkTestFinished(completed)
	require.True(t, TryPushCiVisibilityPreCloseAction(func() { completed.Close(ResultStatusPass) }))

	CloseForCrash("timeout", "test timed out after 1s", "")

	status, _ := completed.span.Meta(constants.TestStatus)
	require.Equal(t, constants.TestStatusPass, status)
	status, _ = running.span.Meta(constants.TestStatus)
	require.Equal(t, constants.TestStatusFail, status)
}

// A crash and the test's own close never mix their outcomes: a test that
// passes carries no crash error, and one that the crash fails has no pass.
func TestFailForCrashIsAtomicWithClose(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()

	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("s")
	for i := range 200 {
		test := suite.CreateTest("T" + strconv.Itoa(i)).(*tslvTest)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			test.failForCrash("timeout", "timed out", "")
		}()
		go func() {
			defer wg.Done()
			test.SetTag(constants.TestFinalStatus, constants.TestStatusPass)
			test.Close(ResultStatusPass)
		}()
		wg.Wait()
		status, _ := test.span.Meta(constants.TestStatus)
		final, _ := test.span.Meta(constants.TestFinalStatus)
		message, failed := test.span.Meta("error.message")
		switch {
		case status == constants.TestStatusPass && !failed && final == constants.TestStatusPass:
		case status == constants.TestStatusFail && message == "timed out" && final == constants.TestStatusFail:
		default:
			t.Fatalf("test %d: status %q, final %q, error %q", i, status, final, message)
		}
	}
}

// Assignment and user tags describe a test; only a configured execution
// policy can override the legacy manual-API crash suppression fallback.
func TestFailForCrashUsesEffectiveFailureSuppression(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("crash-policy")
	pfs := "test.test_management.prevent_failure_suppression"
	noRetry := "test.test_management.no_auto_retry"
	for _, tc := range []struct {
		name   string
		tags   []string
		policy []bool
		want   string
	}{
		{"legacy-unmanaged", nil, nil, constants.TestStatusFail},
		{"legacy-manual-quarantine", []string{constants.TestIsQuarantined}, nil, constants.TestStatusSkip},
		{"legacy-manual-disabled", []string{constants.TestIsDisabled}, nil, constants.TestStatusSkip},
		{"forged-pfs-only", []string{pfs}, nil, constants.TestStatusFail},
		{"forged-pfs-manual-quarantine", []string{pfs, constants.TestIsQuarantined}, nil, constants.TestStatusSkip},
		{"forged-pfs-manual-disabled", []string{pfs, constants.TestIsDisabled}, nil, constants.TestStatusSkip},
		{"effective-false-quarantine", []string{constants.TestIsQuarantined}, []bool{false}, constants.TestStatusFail},
		{"effective-false-disabled-atf", []string{constants.TestIsDisabled, constants.TestIsAttempToFix}, []bool{false}, constants.TestStatusFail},
		{"effective-true-no-retry-quarantine", []string{noRetry, constants.TestIsQuarantined}, []bool{true}, constants.TestStatusSkip},
		{"effective-true-no-retry-disabled-atf", []string{noRetry, constants.TestIsDisabled, constants.TestIsAttempToFix}, []bool{true}, constants.TestStatusSkip},
		{"effective-true-without-assignment-tags", nil, []bool{true}, constants.TestStatusSkip},
		{"forged-pfs-cannot-change-configured-policy", []string{pfs, constants.TestIsQuarantined}, []bool{true}, constants.TestStatusSkip},
		{"first-false-is-immutable", []string{constants.TestIsQuarantined}, []bool{false, true}, constants.TestStatusFail},
		{"first-true-is-immutable", nil, []bool{true, false}, constants.TestStatusSkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := suite.CreateTest(tc.name).(*tslvTest)
			for _, enabled := range tc.policy {
				SetTestCrashFailureSuppression(event, enabled)
			}
			for _, tag := range tc.tags {
				event.SetTag(tag, "true")
			}
			require.True(t, event.failForCrash("timeout", "crash policy sentinel", "original.go:42"))
			status, _ := event.span.Meta(constants.TestStatus)
			final, _ := event.span.Meta(constants.TestFinalStatus)
			message, _ := event.span.Meta("error.message")
			stack, _ := event.span.Meta("error.stack")
			require.Equal(t, constants.TestStatusFail, status)
			require.Equal(t, tc.want, final)
			require.Equal(t, "crash policy sentinel", message)
			require.Equal(t, "original.go:42", stack)
			for _, tag := range tc.tags {
				value, _ := event.span.Meta(tag)
				require.Equal(t, "true", value, tag)
			}
			_, open := openTests.Load(event)
			require.False(t, open)
			require.False(t, event.failForCrash("panic", "late crash must not replace outcome", "later.go:99"))
			SetTestCrashFailureSuppression(event, tc.want != constants.TestStatusSkip)
			final, _ = event.span.Meta(constants.TestFinalStatus)
			require.Equal(t, tc.want, final)
		})
	}
}

// Finished fuzz events can remain buffered until the pre-close barrier. A
// subsequent setter or process crash must not turn their completed pass into
// a crash result; ordinary already-closed events have the same guarantee.
func TestCrashFailureSuppressionKeepsClosedAndFinishedEvents(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	resetCrashForTesting(t)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("finished-policy")
	closed := suite.CreateTest("Closed").(*tslvTest)
	closed.Close(ResultStatusPass)
	SetTestCrashFailureSuppression(closed, true)
	require.False(t, closed.failForCrash("panic", "closed event", ""))
	finished := suite.CreateTest("FuzzCompleted").(*tslvTest)
	finished.SetTag(constants.TestIsQuarantined, "true")
	MarkTestFinished(finished)
	SetTestCrashFailureSuppression(finished, true)
	require.False(t, finished.failForCrash("panic", "finished seed", ""))
	require.True(t, TryPushCiVisibilityPreCloseAction(func() { finished.Close(ResultStatusPass) }))
	running := suite.CreateTest("RunningProtected").(*tslvTest)
	SetTestCrashFailureSuppression(running, false)
	running.SetTag(constants.TestIsQuarantined, "true")
	CloseForCrash("timeout", "active crash sentinel", "")
	for _, event := range []*tslvTest{closed, finished} {
		status, _ := event.span.Meta(constants.TestStatus)
		final, _ := event.span.Meta(constants.TestFinalStatus)
		_, hasError := event.span.Meta("error.message")
		require.Equal(t, constants.TestStatusPass, status, event.name)
		require.Equal(t, "", final, event.name)
		require.False(t, hasError, event.name)
	}
	status, _ := running.span.Meta(constants.TestStatus)
	final, _ := running.span.Meta(constants.TestFinalStatus)
	require.Equal(t, constants.TestStatusFail, status)
	require.Equal(t, constants.TestStatusFail, final)
}

// Once an effective policy is configured, a concurrent opposite setter may
// neither revive suppression nor mix a normal close with a crash's outcome.
func TestCrashFailureSuppressionIsAtomicWithClose(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("concurrent-policy")
	for i := range 200 {
		event := suite.CreateTest("Policy" + strconv.Itoa(i)).(*tslvTest)
		SetTestCrashFailureSuppression(event, false)
		event.SetTag(constants.TestIsQuarantined, "true")
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); SetTestCrashFailureSuppression(event, true) }()
		go func() { defer wg.Done(); event.failForCrash("timeout", "immutable crash sentinel", "original.go:42") }()
		go func() {
			defer wg.Done()
			event.SetTag(constants.TestFinalStatus, constants.TestStatusPass)
			event.Close(ResultStatusPass)
		}()
		wg.Wait()
		status, _ := event.span.Meta(constants.TestStatus)
		final, _ := event.span.Meta(constants.TestFinalStatus)
		message, failed := event.span.Meta("error.message")
		switch {
		case status == constants.TestStatusPass && !failed && final == constants.TestStatusPass:
		case status == constants.TestStatusFail && message == "immutable crash sentinel" && final == constants.TestStatusFail:
		default:
			t.Fatalf("iteration%d mixed crash policy status%q final%q error%q", i, status, final, message)
		}
	}
}

// Trusted execution facts override the immutable base management policy; raw
// tags remain descriptive and cannot manufacture a protected descendant.
func TestTrustedProtectedFailureOverridesCrashSuppression(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("trusted-descendant")
	MarkTestProtectedFailure(nil)
	for _, tc := range []struct {
		name                                         string
		configured, mark, markBeforePolicy, forgePFS bool
		want                                         string
	}{
		{"configured-quarantine-trusted", true, true, false, false, constants.TestStatusFail},
		{"legacy-manual-quarantine-trusted", false, true, false, false, constants.TestStatusFail},
		{"trusted-before-base-policy", true, true, true, false, constants.TestStatusFail},
		{"configured-quarantine-without-fact", true, false, false, false, constants.TestStatusSkip},
		{"legacy-manual-quarantine-without-fact", false, false, false, false, constants.TestStatusSkip},
		{"forged-pfs-is-not-trusted-fact", true, false, false, true, constants.TestStatusSkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := suite.CreateTest(tc.name).(*tslvTest)
			if tc.markBeforePolicy {
				MarkTestProtectedFailure(event)
			}
			if tc.configured {
				SetTestCrashFailureSuppression(event, true)
			}
			event.SetTag(constants.TestIsQuarantined, "true")
			if tc.forgePFS {
				event.SetTag("test.test_management.prevent_failure_suppression", "true")
			}
			if tc.mark && !tc.markBeforePolicy {
				MarkTestProtectedFailure(event)
				MarkTestProtectedFailure(event)
			}
			require.True(t, event.failForCrash("timeout", "trusted descendant crash sentinel", "original.go:42"))
			status, _ := event.span.Meta(constants.TestStatus)
			final, _ := event.span.Meta(constants.TestFinalStatus)
			message, _ := event.span.Meta("error.message")
			require.Equal(t, constants.TestStatusFail, status)
			require.Equal(t, tc.want, final)
			require.Equal(t, "trusted descendant crash sentinel", message)
			q, _ := event.span.Meta(constants.TestIsQuarantined)
			require.Equal(t, "true", q)
			if !tc.forgePFS {
				_, synthetic := event.span.Meta("test.test_management.prevent_failure_suppression")
				require.False(t, synthetic, "trusted descendant fact is not a declaration annotation")
			}
		})
	}
	// A prior event's true fact must not be imported into a fresh manual event.
	first := suite.CreateTest("FirstProtected").(*tslvTest)
	SetTestCrashFailureSuppression(first, true)
	first.SetTag(constants.TestIsQuarantined, "true")
	MarkTestProtectedFailure(first)
	first.Close(ResultStatusPass)
	fresh := suite.CreateTest("FreshWithoutProtectedFailure").(*tslvTest)
	SetTestCrashFailureSuppression(fresh, true)
	fresh.SetTag(constants.TestIsQuarantined, "true")
	require.True(t, fresh.failForCrash("timeout", "fresh event crash", ""))
	final, _ := fresh.span.Meta(constants.TestFinalStatus)
	require.Equal(t, constants.TestStatusSkip, final)
}

func TestTrustedProtectedFailureKeepsClosedAndFinishedEvents(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	resetCrashForTesting(t)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("closed-trusted-descendant")
	closed := suite.CreateTest("Closed").(*tslvTest)
	closed.Close(ResultStatusPass)
	MarkTestProtectedFailure(closed)
	finished := suite.CreateTest("FinishedFuzzSeed").(*tslvTest)
	MarkTestFinished(finished)
	MarkTestProtectedFailure(finished)
	require.True(t, TryPushCiVisibilityPreCloseAction(func() { finished.Close(ResultStatusPass) }))
	running := suite.CreateTest("OpenQuarantined").(*tslvTest)
	SetTestCrashFailureSuppression(running, true)
	running.SetTag(constants.TestIsQuarantined, "true")
	MarkTestProtectedFailure(running)
	CloseForCrash("timeout", "open protected descendant crash", "")
	for _, event := range []*tslvTest{closed, finished} {
		status, _ := event.span.Meta(constants.TestStatus)
		_, hasError := event.span.Meta("error.message")
		require.Equal(t, constants.TestStatusPass, status, event.name)
		require.False(t, hasError, event.name)
		require.False(t, event.failForCrash("panic", "late crash", ""))
	}
	final, _ := running.span.Meta(constants.TestFinalStatus)
	require.Equal(t, constants.TestStatusFail, final)
}

func TestTrustedProtectedFailureIsAtomicWithCrashAndClose(t *testing.T) {
	resetCIVisibilityBootstrapStateForTesting()
	disableAdditionalFeaturesForBootstrapTest()
	t.Cleanup(restoreCIVisibilityBootstrapForTesting)
	initializeCIVisibilityLifecycleForTesting()
	suite := CreateTestSession().GetOrCreateModule("m").GetOrCreateSuite("racing-trusted-descendant")
	for i := range 200 {
		event := suite.CreateTest("Trusted" + strconv.Itoa(i)).(*tslvTest)
		SetTestCrashFailureSuppression(event, true)
		event.SetTag(constants.TestIsQuarantined, "true")
		MarkTestProtectedFailure(event)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); MarkTestProtectedFailure(event) }()
		go func() {
			defer wg.Done()
			event.failForCrash("timeout", "already-observed protected failure", "original.go:42")
		}()
		go func() {
			defer wg.Done()
			event.SetTag(constants.TestFinalStatus, constants.TestStatusPass)
			event.Close(ResultStatusPass)
		}()
		wg.Wait()
		status, _ := event.span.Meta(constants.TestStatus)
		final, _ := event.span.Meta(constants.TestFinalStatus)
		message, failed := event.span.Meta("error.message")
		switch {
		case status == constants.TestStatusPass && !failed && final == constants.TestStatusPass:
		case status == constants.TestStatusFail && message == "already-observed protected failure" && final == constants.TestStatusFail:
		default:
			t.Fatalf("iteration%d lost trusted fact or mixed outcomes status%q final%q message%q", i, status, final, message)
		}
	}
}
