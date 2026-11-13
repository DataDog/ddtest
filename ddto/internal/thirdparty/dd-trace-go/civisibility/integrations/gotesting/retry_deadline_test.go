package gotesting

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

type deadlineFaultySession struct {
	state func() earlyFlakeDetectionAdmission
}

func (s deadlineFaultySession) admitNewTest(*testIdentity) earlyFlakeDetectionAdmission {
	return s.retryState()
}

func (s deadlineFaultySession) retryState() earlyFlakeDetectionAdmission { return s.state() }

func TestDeferredEFDDeadlineDrainsActiveChild(t *testing.T) {
	restoreGate := resetProcessRetryLaunchGateForTesting(t)
	defer restoreGate()
	oldRegistered := processRetryActiveChildren.closeActionRegistered.Swap(true)
	defer processRetryActiveChildren.closeActionRegistered.Store(oldRegistered)
	lease, err := acquireProcessRetryGroupLease()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	oldSession := session
	recording := &processRetryRecordingSession{}
	session = recording
	defer func() { session = oldSession }()
	module := recording.GetOrCreateModule(t.Name()).(*processRetryRecordingModule)
	suite := module.GetOrCreateSuite("deadline_test.go").(*processRetryRecordingSuite)
	initial := suite.CreateTest("TestBudget").(*processRetryRecordingTest)
	addModulesCounters(module.Name(), 1)
	addSuitesCounters(module.Name(), suite.Name(), 1)
	defer func() {
		modulesCountersMutex.Lock()
		delete(modulesCounters, module.Name())
		modulesCountersMutex.Unlock()
		suitesCountersMutex.Lock()
		delete(suitesCounters, testSuiteCounterKey{module.Name(), suite.Name()})
		suitesCountersMutex.Unlock()
	}()
	g := &deferredProcessRetryGroup{
		identity: testIdentity{FullName: "TestBudget"},
		testInfo: commonInfo{testName: "TestBudget", moduleName: module.Name(), suiteName: suite.Name()},
		metadata: processRetryMetadataSnapshot{
			identity:                     &testIdentity{FullName: "TestBudget"},
			isEarlyFlakeDetectionEnabled: true, isANewTest: true,
		},
		parentDeadline: time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC), parentDeadlineOK: true,
		initialDuration: time.Second, retryCount: 2, parallelEFD: true,
		launchBaseline: &processRetryLaunchBaseline{maxConcurrency: 2, maxConcurrencySet: true},
		lease:          lease, module: module, suite: suite,
		tailEvent: &deferredProcessRetryEvent{event: initial, status: integrations.ResultStatusPass, ready: true},
	}
	g.outcomes.observe(false, false)
	cut := make(chan struct{})
	checks := 0
	g.efdFaultySessionGuard = deadlineFaultySession{state: func() earlyFlakeDetectionAdmission {
		checks++
		if checks == 2 {
			// Only the scheduler writes this field; no wall-clock wait is needed.
			g.parentDeadline = time.Unix(1, 0)
			close(cut)
		}
		return earlyFlakeDetectionAdmissionAllowed
	}}
	started := make(chan struct{})
	release := make(chan struct{})
	releaseChild := sync.OnceFunc(func() { close(release) })
	var children atomic.Int32
	runner := func(context.Context, *deferredProcessRetryGroup, deferredProcessRetryPreparedAttempt) processRetryAttemptResult {
		if children.Add(1) == 1 {
			close(started)
		}
		<-release
		start := time.Unix(100, 0)
		return processRetryAttemptResult{
			Result:             processRetryResult{Status: processRetryStatusPass},
			ExitStatusObserved: true, BodyAdmitted: true,
			StartTime: start, FinishTime: start.Add(time.Second),
		}
	}
	coordinator := newProcessRetryCoordinator(func() bool { return false }, runner)
	done := make(chan deferredProcessRetryDrainOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- coordinator.drainScheduledBatch([]*deferredProcessRetryGroup{g}, runner)
	}()
	defer func() { releaseChild(); <-finished }()
	select {
	case <-cut:
	case <-finished:
		t.Fatal("group finished before the next admission check")
	}
	<-started
	if lease.released.Load() {
		t.Fatal("released the group while its child was active")
	}
	releaseChild()
	if outcome := <-done; outcome.deferredFailed {
		t.Fatal("deadline truncation invented a failed sample")
	}
	if children.Load() != 1 || len(recording.tests) != 2 || !lease.released.Load() {
		t.Fatalf("unexpected drain: children=%d events=%d released=%t", children.Load(), len(recording.tests), lease.released.Load())
	}
	g.finish() // Finalization and lease release must be idempotent.
	last := recording.tests[1]
	if initial.closeCount != 1 || last.closeCount != 1 || module.closeCount != 1 || suite.closeCount != 1 {
		t.Fatal("draining did not close each real event and container exactly once")
	}
	if last.name != "TestBudget" || last.status != processRetryStatusPass || last.tags[constants.TestIsRetry] != "true" || last.tags[constants.TestRetryReason] != constants.EarlyFlakeDetectionRetryReason || last.tags[constants.TestFinalStatus] != constants.TestStatusPass {
		t.Fatalf("lost final sample identity or metadata: %s %v", last.name, last.tags)
	}
	processRetryLaunchGate.mu.Lock()
	active := processRetryLaunchGate.activeGroups
	processRetryLaunchGate.mu.Unlock()
	if active != 0 {
		t.Fatalf("group lease released incorrectly: active=%d", active)
	}
}

func TestEFDDeadlinePreservesFallbackToFlakyRetries(t *testing.T) {
	flaky, _ := useFlakyRetrySettings(t)
	flaky.RetryCount, flaky.DynamicRetryCounts, flaky.RemainingTotalRetryCount = 3, nil, 10
	guard := deadlineFaultySession{state: func() earlyFlakeDetectionAdmission { return earlyFlakeDetectionAdmissionFaulty }}
	failed := new(testing.T)
	failed.Fail()
	metadata := &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, isFlakyTestRetriesEnabled: true}
	opts := &executionOptions{
		options: &runTestWithRetryOptions{
			t:               testWithRetryDeadline(t, time.Unix(1, 0)),
			failfastEnabled: func() bool { return false }, efdFaultySessionGuard: guard,
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool { return true },
		},
		executionMetadata: metadata, initialDuration: time.Second, retryCount: 4,
	}
	defer refundFlakyRetryBudgetReservation(opts)
	if !reserveRetryBudgetIfNeeded(opts, failed, metadata, 0) || !metadata.efdFellBackToFlakyRetries {
		t.Fatal("EFD-only deadline veto hid the existing fallback to flaky retries")
	}

	g := &deferredProcessRetryGroup{
		parentDeadline: time.Unix(1, 0), parentDeadlineOK: true,
		retryCount: 4, initialDuration: time.Second,
		efdFaultySessionGuard: guard,
		latest:                retryAttemptObservation{failed: true},
		metadata: processRetryMetadataSnapshot{
			identity:                     &testIdentity{FullName: "TestBudget"},
			isEarlyFlakeDetectionEnabled: true, isANewTest: true, isFlakyTestRetriesEnabled: true,
		},
	}
	g.outcomes.observe(true, false)
	prepared, ok, _ := g.prepareAttempt(true)
	if !ok || !g.metadata.efdFellBackToFlakyRetries || prepared.retryReason != constants.AutoTestRetriesRetryReason {
		t.Fatal("deferred EFD-only deadline veto hid the existing fallback to flaky retries")
	}
}

// Own the state instead of changing the running test's shared deadline or alarm.
func testWithRetryDeadline(t *testing.T, deadline time.Time) *testing.T {
	t.Helper()
	layout, reason := getRetryAttemptLayout()
	if reason != "" {
		t.Fatal(reason)
	}
	state := reflect.New(layout.tstate.typ.Elem())
	ptr, err := getFieldPointerFromWithType(state.Interface(), "deadline", reflect.TypeOf(deadline))
	if err != nil {
		t.Fatal(err)
	}
	*(*time.Time)(ptr) = deadline
	local := new(testing.T)
	setPrivatePointerField(layout.tstate.typ, fieldRawPtr(unsafe.Pointer(local), layout.tstate.unsafeField), state.UnsafePointer())
	return local
}

func TestEFDDeadlinePreservesFailfastFailure(t *testing.T) {
	for _, nativeFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed_sample", true: "native_failure"}[nativeFailure], func(t *testing.T) {
			completed := new(testing.T)
			if !nativeFailure {
				completed.Fail()
			}
			metadata := &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true, anyExecutionPassed: true}
			opts := &executionOptions{
				options: &runTestWithRetryOptions{
					t:                      testWithRetryDeadline(t, time.Unix(1, 0)),
					failfastEnabled:        func() bool { return true },
					nativeFailfastObserved: func() bool { return nativeFailure },
				},
				executionMetadata: metadata, executionIndex: 1, retryCount: 4,
				initialDuration: time.Second,
			}
			if reserveRetryBudgetIfNeeded(opts, completed, metadata, 1) {
				t.Fatal("continued after a failfast failure")
			}
			if nativeFailure && !opts.nativeFailfastStop || !nativeFailure && !opts.failfastRawFailure {
				t.Fatalf("deadline hid failfast failure: raw=%t native=%t", opts.failfastRawFailure, opts.nativeFailfastStop)
			}
		})
	}
}

func TestEFDDeadlineWithoutFailfast(t *testing.T) {
	opts := &executionOptions{
		options: &runTestWithRetryOptions{
			t:               testWithRetryDeadline(t, time.Unix(1, 0)),
			failfastEnabled: func() bool { return false },
			postShouldRetry: func(*testing.T, *testExecutionMetadata, int, int64) bool { return true },
		},
		executionMetadata: &testExecutionMetadata{isEarlyFlakeDetectionEnabled: true, isANewTest: true},
		retryCount:        4, initialDuration: time.Second,
	}
	if reserveRetryBudgetIfNeeded(opts, opts.options.t, opts.executionMetadata, 0) || opts.failfastRawFailure || opts.nativeFailfastStop {
		t.Fatal("expired EFD deadline must stop retries without inventing a failfast failure")
	}
	for _, metadata := range []*testExecutionMetadata{
		{isFlakyTestRetriesEnabled: true},
		{isEarlyFlakeDetectionEnabled: true, isANewTest: true, isAttemptToFix: true},
	} {
		opts.executionMetadata = metadata
		if retryEFDDeadlineStopped(opts) {
			t.Fatal("EFD admission changed another retry policy")
		}
	}
}

func TestRetryFitsDeadline(t *testing.T) {
	now := time.Unix(123, 0)
	for _, test := range []struct {
		name                string
		present             bool
		remaining, estimate time.Duration
		want                bool
	}{
		{"unlimited", false, 0, time.Hour, true},
		{"enough", true, 3 * time.Second, 2 * time.Second, true},
		{"insufficient", true, time.Second, 2 * time.Second, false},
		{"expired", true, -time.Second, 0, false},
		{"reserve_only", true, retryFinalizationReserve, 0, false},
		{"exact_fit_has_no_slack", true, time.Second + retryFinalizationReserve, time.Second, false},
		{"duration_overflow", true, time.Second, time.Duration(1<<63 - 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := retryFitsDeadline(now.Add(test.remaining), test.present, now, test.estimate, retryFinalizationReserve); got != test.want {
				t.Fatalf("retryFitsDeadline=%t, want %t", got, test.want)
			}
		})
	}
}

func TestProcessRetryAdmissionIncludesExistingTeardownReserve(t *testing.T) {
	now := time.Unix(123, 0)
	reserve := processRetryParentDeadlineReserve()
	if retryFitsDeadline(now.Add(time.Second+reserve), true, now, time.Second, reserve) {
		t.Fatal("admitted a child with no teardown slack")
	}
	if !retryFitsDeadline(now.Add(2*time.Second+reserve), true, now, time.Second, reserve) {
		t.Fatal("rejected a child with enough time for execution and teardown")
	}
}

func TestDeferredEFDDeadlineStopsBeforePreparingAChild(t *testing.T) {
	g := &deferredProcessRetryGroup{
		parentDeadline: time.Unix(1, 0), parentDeadlineOK: true,
		retryCount: 4, initialDuration: time.Second,
	}
	g.metadata.isEarlyFlakeDetectionEnabled = true
	g.metadata.isANewTest = true
	_, prepared, stopped := g.prepareAttempt(true)
	if prepared || !stopped || !g.truncated || g.executionIndex != 0 {
		t.Fatalf("expired group prepared work: prepared=%t stopped=%t truncated=%t index=%d", prepared, stopped, g.truncated, g.executionIndex)
	}
}
