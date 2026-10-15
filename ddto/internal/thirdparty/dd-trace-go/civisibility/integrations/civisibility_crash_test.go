package integrations

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/testassert/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
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
