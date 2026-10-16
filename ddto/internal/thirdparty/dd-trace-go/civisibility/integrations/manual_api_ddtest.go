// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	_ "unsafe" // for go:linkname

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/logs"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Test

// Ensures that tslvTest implements the Test interface.
var _ Test = (*tslvTest)(nil)

type (
	// tslvTest implements the DdTest interface and represents an individual test within a suite.
	tslvTest struct {
		ciVisibilityCommon
		testID uint64
		suite  *tslvTestSuite
		name   string
		// ended is set by the first Close, which a delayed close finishes
		// later: another Close does nothing.
		ended bool
	}

	// tslvTestDelayed is a struct that represents a delayed test close operation.
	tslvTestDelayed struct {
		*tslvTest
		finishTime time.Time
	}
)

var (
	finishedTests      []*tslvTestDelayed
	finishedTestsMutex sync.Mutex

	// openTests holds the tests that are not closed yet, which a crash fails:
	// see CloseForCrash.
	openTests sync.Map // *tslvTest -> struct{}

	globalTestEventStartHook func(any)
	globalEventFinishHook    func([]any)
)

// createTest initializes a new test within a given suite.
func createTest(suite *tslvTestSuite, name string, startTime time.Time) Test {
	if suite == nil {
		return nil
	}
	if log.DebugEnabled() {
		// With each test's finish line, shows which test ran when a process ended.
		log.Debug("civisibility: test started name=%s suite=%s", name, suite.name)
	}

	operationName := suite.module.testOperation

	resourceName := suite.name + "." + name

	// Test tags should include suite, module, and session tags so the backend can calculate the suite, module, and session fingerprint from the test.
	// StartTestSpan applies, in this order, what the test's option list held: ResourceName, SpanType and StartTime,
	// fillCommonTags' constant and common options and CI metrics, the suite's tags, the test name and the service.
	// It builds no list and no closure for each test, and its event encodes as the list's did. Tests share the clipped suite options.
	testSpan := tracer.TestSpan{
		Operation: operationName, Resource: resourceName, Type: constants.SpanTypeTest, Start: startTime,
		Constant: constantOptions, Common: commonTagOptions(), Metrics: utils.GetCIMetrics(),
		Suite: slices.Clip(suite.tags), NameKey: constants.TestName, Name: truncateCIVisibilityMetaValue(name),
		Service: suite.service,
	}

	span, ctx := tracer.StartTestSpan(context.Background(), &testSpan)
	if suite.module.session != nil {
		setCIVisibilitySpanStringTag(span, constants.TestSessionIDTag, suite.module.session.sessionIDText)
	}
	setCIVisibilitySpanStringTag(span, constants.TestModuleIDTag, suite.module.moduleIDText)
	setCIVisibilitySpanStringTag(span, constants.TestSuiteIDTag, suite.suiteIDText)
	testID := span.Context().SpanID()

	t := &tslvTest{
		testID: testID,
		suite:  suite,
		name:   name,
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: startTime,
			tags:      nil, // No event inherits a test's tags; its span reads the suite's.
			span:      span,
			ctx:       ctx,
		},
	}

	// If we have a global test event start hook we call it here.
	if globalTestEventStartHook != nil {
		globalTestEventStartHook(t)
	}

	// Note: if the process is killed some tests will not be closed and will be lost. This is a known limitation.
	// We will not close it because there's no a good test status to report in this case, and we don't want to report a false positive (pass, fail, or skip).
	// A crash that Go code still sees fails them: see CloseForCrash.
	openTests.Store(t, struct{}{})

	// Creating telemetry event created
	telemetry.EventCreated(t.suite.module.framework, telemetry.TestEventType)
	return t
}

// MarkTestFinished records that test has finished although its event
// closes later, as a fuzz seed's in the queue that holds it: a crash then
// keeps the outcome that the later close reports, instead of failing it.
func MarkTestFinished(test Test) {
	if t, ok := test.(*tslvTest); ok {
		t.mutex.Lock()
		openTests.Delete(t)
		t.mutex.Unlock()
	}
}

// TestID returns the ID of the test.
func (t *tslvTest) TestID() uint64 {
	return t.testID
}

// Name returns the name of the test.
func (t *tslvTest) Name() string { return t.name }

// Suite returns the suite to which the test belongs.
func (t *tslvTest) Suite() TestSuite { return t.suite }

// Close closes the test with the given status.
func (t *tslvTest) Close(status TestResultStatus, options ...TestCloseOption) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.closeLocked(status, options...)
}

// failForCrash fails the test with a crash and closes it, unless it finished
// first, in one critical section with Close: the test then has either its
// own outcome or the crash's, never a mix. It reports whether it failed it.
func (t *tslvTest) failForCrash(errType, message, stack string) bool {
	failed, final := func() (bool, string) {
		t.mutex.Lock()
		defer t.mutex.Unlock()
		if _, open := openTests.Load(t); !open || t.ended || t.closed {
			return false, ""
		}
		final := constants.TestStatusFail
		if t.getContextValue(constants.TestIsQuarantined) == "true" || t.getContextValue(constants.TestIsDisabled) == "true" {
			final = constants.TestStatusSkip
		}
		// The test's own SetTag and SetError do not take t.mutex: the span sets
		// these tags as it finishes, so theirs land before and are replaced, or
		// after and are ignored.
		tags := map[string]any{ext.Error: true, ext.ErrorType: errType, ext.ErrorMsg: truncateCIVisibilityTagValue(message), constants.TestFinalStatus: final}
		if stack != "" {
			tags[ext.ErrorStack] = truncateCIVisibilityTagValue(stack)
		}
		t.Suite().SetTag(ext.Error, true)
		t.Suite().Module().SetTag(ext.Error, true)
		t.closeLocked(ResultStatusFail, func(o *tslvTestCloseOptions) { o.finishTags = tags })
		return true, final
	}()
	if failed {
		// The logger, which a client can replace, runs after the lock.
		log.Debug("civisibility: failed the test name=%s suite=%s for the crash (%s) final_status=%s", t.name, t.suite.name, errType, final)
	}
	return failed
}

// closeLocked is Close, with t.mutex held.
func (t *tslvTest) closeLocked(status TestResultStatus, options ...TestCloseOption) {
	if t.ended || t.closed {
		return
	}
	t.ended = true
	openTests.Delete(t)

	defaults := &tslvTestCloseOptions{}
	for _, opt := range options {
		opt(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	switch status {
	case ResultStatusPass:
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusPass)
	case ResultStatusFail:
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusFail)
	case ResultStatusSkip:
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusSkip)
	}
	if defaults.finishTags != nil {
		defaults.finishTags[constants.TestStatus] = constants.TestStatusFail
	}

	if defaults.skipReason != "" {
		setCIVisibilitySpanTag(t.span, constants.TestSkipReason, defaults.skipReason)
	}

	if globalEventFinishHook != nil {
		// delayed close
		for key, value := range defaults.finishTags {
			setCIVisibilitySpanTag(t.span, key, value)
		}
		finishedTestsMutex.Lock()
		defer finishedTestsMutex.Unlock()
		finishedTests = append(finishedTests, &tslvTestDelayed{
			tslvTest:   t,
			finishTime: defaults.finishTime,
		})
		return
	}
	t.internalClose(defaults.finishTags, tracer.FinishTime(defaults.finishTime))
}

// internalClose is a helper function to close the test and report the telemetry event.
func (t *tslvTest) internalClose(tags map[string]any, options ...tracer.FinishOption) {
	t.span.FinishWithTags(tags, options...)
	t.closed = true

	// Creating telemetry event finished
	t.ctxMutex.Lock()
	defer t.ctxMutex.Unlock()
	testingEventType := telemetry.TestEventType
	if t.ctx.Value(constants.TestIsNew) == "true" {
		testingEventType = append(testingEventType, telemetry.IsNewEventType...)
	}
	if t.ctx.Value(constants.TestIsRetry) == "true" {
		testingEventType = append(testingEventType, telemetry.IsRetryEventType...)
	}
	if t.ctx.Value(constants.TestEarlyFlakeDetectionRetryAborted) == "slow" {
		testingEventType = append(testingEventType, telemetry.EfdAbortSlowEventType...)
	}
	if t.ctx.Value(constants.TestType) == constants.TestTypeBenchmark {
		testingEventType = append(testingEventType, telemetry.IsBenchmarkEventType...)
	}
	if t.ctx.Value(constants.TestIsAttempToFix) == "true" {
		testingEventType = append(testingEventType, telemetry.IsAttemptToFixEventType...)
	}
	if t.ctx.Value(constants.TestIsQuarantined) == "true" {
		testingEventType = append(testingEventType, telemetry.IsQuarantinedEventType...)
	}
	if t.ctx.Value(constants.TestIsDisabled) == "true" {
		testingEventType = append(testingEventType, telemetry.IsDisabledEventType...)
	}
	if t.ctx.Value(constants.TestHasFailedAllRetries) == "true" {
		testingEventType = append(testingEventType, telemetry.HasFailedAllRetriesEventType...)
	}
	if retryReason, ok := t.ctx.Value(constants.TestRetryReason).(string); ok {
		testingEventType = append(testingEventType, []string{"retry_reason:" + retryReason}...)
	}
	telemetry.EventFinished(t.suite.module.framework, testingEventType)
}

// SetTag sets a tag on the test event.
func (t *tslvTest) SetTag(key string, value any) {
	t.ciVisibilityCommon.SetTag(key, value)
	if key == constants.TestIsNew ||
		key == constants.TestIsRetry ||
		key == constants.TestEarlyFlakeDetectionRetryAborted ||
		key == constants.TestIsAttempToFix ||
		key == constants.TestIsQuarantined ||
		key == constants.TestIsDisabled ||
		key == constants.TestHasFailedAllRetries ||
		key == constants.TestRetryReason {
		t.setContextValue(key, value)
	}
}

// SetError sets an error on the test and marks the suite and module as having an error.
func (t *tslvTest) SetError(options ...ErrorOption) {
	t.ciVisibilityCommon.SetError(options...)
	t.Suite().SetTag(ext.Error, true)
	t.Suite().Module().SetTag(ext.Error, true)
}

// SetTestFunc sets the function to be tested and records its source location.
func (t *tslvTest) SetTestFunc(fn *runtime.Func) {
	if fn == nil {
		log.Debug("civisibility: SetTestFunc called with nil runtime function")
		return
	}

	// Resolve immutable function metadata once. Go -trimpath can return a logical
	// module path while source parsing still needs a local file path.
	functionMetadata := loadSourceFunctionMetadata(fn)
	runtimePath := functionMetadata.runtimePath
	runtimeStartLine := functionMetadata.runtimeStartLine
	sourcePath := functionMetadata.sourcePath
	file := sourcePath.RelativePath
	if log.DebugEnabled() {
		log.Debug("civisibility: resolving test source location [function:%s file:%s start_line:%d relative_file:%s runtime_file:%s filesystem_file:%s filesystem_known:%t entry:%#x]",
			fn.Name(), runtimePath, runtimeStartLine, file, sourcePath.RuntimePath, sourcePath.FilesystemPath, sourcePath.FilesystemKnown, fn.Entry())
	}
	setCIVisibilitySpanStringTag(t.span, constants.TestSourceFile, file)
	t.span.SetMetric(constants.TestSourceStartLine, float64(runtimeStartLine))
	setCIVisibilitySpanStringTag(t.suite.span, constants.TestSourceFile, file)

	// Source inspection is cached per file so repeated retries/subtests do not reparse the same file.
	metadata := functionMetadata.fileMetadata
	if !metadata.parseOK {
		if log.DebugEnabled() {
			log.Debug("civisibility: failed parsing test source file [function:%s file:%s runtime_file:%s relative_file:%s start_line:%d error:%s]",
				fn.Name(), sourcePath.FilesystemPath, runtimePath, file, runtimeStartLine, metadata.parseErr.Error())
		}
	}
	if metadata.parseOK {
		// let's check if the suite was marked as unskippable before
		isUnskippable, hasUnskippableValue := t.suite.getContextValue(constants.TestUnskippable).(bool)
		if !hasUnskippableValue {
			isUnskippable = metadata.suiteUnskippable
			t.suite.setContextValue(constants.TestUnskippable, isUnskippable)
		}

		// get the function name without the package name
		fullName := functionMetadata.fullName
		name := functionMetadata.shortName
		if log.DebugEnabled() {
			log.Debug("civisibility: scanning AST for test source range [function:%s short_name:%s file:%s runtime_file:%s relative_file:%s runtime_start_line:%d]",
				fullName, name, sourcePath.FilesystemPath, runtimePath, file, runtimeStartLine)
		}

		// Resolve the source range from cached metadata but keep the existing declaration/literal
		// matching rules and the same debug logs the tests already assert.
		resolution := functionMetadata.resolution
		startLine := resolution.startLine
		endLine := resolution.endLine
		if resolution.matchedDeclaration != nil {
			if log.DebugEnabled() {
				log.Debug("civisibility: matched AST function declaration [function:%s decl_name:%s decl_start_line:%d body_start_line:%d body_end_line:%d runtime_start_line:%d]",
					fullName, name, resolution.matchedDeclaration.declStartLine, resolution.matchedDeclaration.bodyStartLine, resolution.matchedDeclaration.endLine, runtimeStartLine)
			}
		}
		for _, literal := range functionLiteralsToLog(functionMetadata.fileMetadata.functionLiterals, resolution.inspectedLiteralCount, log.DebugEnabled()) {
			delta := literal.bodyStartLine - runtimeStartLine
			log.Debug("civisibility: inspecting AST function literal candidate [function:%s literal_start_line:%d literal_end_line:%d runtime_start_line:%d delta:%d]",
				fullName, literal.bodyStartLine, literal.endLine, runtimeStartLine, delta)
		}
		if resolution.matchedLiteral != nil {
			if log.DebugEnabled() {
				log.Debug("civisibility: matched AST function literal [function:%s adjusted_start_line:%d end_line:%d]",
					fullName, startLine, endLine)
			}
		}
		if !isUnskippable && resolution.functionUnskippable {
			isUnskippable = true
		}

		// Only publish the AST-derived range when it is complete. Otherwise keep the runtime start
		// line that was already tagged above; this does not clear any previous end-line tag if the
		// same test object is reused.
		if endLine >= startLine {
			t.span.SetMetric(constants.TestSourceStartLine, float64(startLine))
			t.span.SetMetric(constants.TestSourceEndLine, float64(endLine))
			if log.DebugEnabled() {
				log.Debug("civisibility: resolved test source range [function:%s file:%s start_line:%d end_line:%d]",
					fullName, file, startLine, endLine)
			}
		} else {
			if log.DebugEnabled() {
				log.Debug("civisibility: test source range incomplete [function:%s file:%s start_line:%d end_line:%d]",
					fullName, file, startLine, endLine)
			}
		}

		// if the function is marked as unskippable, set the appropriate tag
		if isUnskippable {
			t.SetTag(constants.TestUnskippable, "true")
			telemetry.ITRUnskippable(telemetry.TestEventType)
			t.setContextValue(constants.TestUnskippable, true)
		}

		// if impacted tests analyzer was loaded, we run it
		if analyzer := GetImpactedTestsAnalyzer(); analyzer != nil {
			if analyzer.IsImpacted(t.Name(), file, startLine, endLine) {
				t.SetTag(constants.TestIsModified, "true")
				telemetry.ImpactedTestsModified()
				t.setContextValue(constants.TestIsModified, true)
			}
		}
	}

	// get the codeowner of the function
	if codeOwner, found := loadSourceFunctionCodeOwner(functionMetadata); found {
		t.SetTag(constants.TestCodeOwners, codeOwner)
		t.suite.SetTag(constants.TestCodeOwners, codeOwner)
	}
}

// resolveTestSourcePath resolves a runtime source path using the production CI tag context.
func resolveTestSourcePath(runtimePath string) utils.SourceFilePath {
	return utils.ResolveSourceFilePathFromCITags(runtimePath)
}

// SetBenchmarkData sets benchmark data for the test.
func (t *tslvTest) SetBenchmarkData(measureType string, data map[string]any) {
	setCIVisibilitySpanTag(t.span, constants.TestType, constants.TestTypeBenchmark)
	t.setContextValue(constants.TestType, constants.TestTypeBenchmark)
	for k, v := range data {
		setCIVisibilitySpanTag(t.span, fmt.Sprintf("benchmark.%s.%s", measureType, k), v)
	}
}

// Log writes a log message for the test.
func (t *tslvTest) Log(message string, tags string) {
	logs.WriteLog(t.testID, t.suite.module.name, t.suite.name, t.name, message, tags)
}

// close closes the test and reports the telemetry event.
func (d *tslvTestDelayed) close() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if !d.closed {
		d.internalClose(nil, tracer.FinishTime(d.finishTime))
	}
}

// SetGlobalTestEventStartHook sets a global hook to be called when a test event is started.

//go:linkname SetGlobalTestEventStartHook
func SetGlobalTestEventStartHook(hook func(any)) {
	globalTestEventStartHook = hook
}

// SetGlobalEventFinishHook sets a global hook to be called when all test events are finished.
//
//go:linkname SetGlobalEventFinishHook
func SetGlobalEventFinishHook(hook func([]any)) {
	globalEventFinishHook = hook
}

func init() {
	if IsProcessRetryChild() {
		return
	}
	PushCiVisibilityCloseAction(func() {
		finishedTestsMutex.Lock()
		defer finishedTestsMutex.Unlock()
		if len(finishedTests) > 0 {
			// Create a slice with the tests
			tests := make([]any, len(finishedTests))
			for i, test := range finishedTests {
				tests[i] = test.tslvTest
			}

			// Close all tests that were delayed in a defer function to ensure they are closed even if the hook panics.
			defer func() {
				// Close all tests that were delayed.
				log.Debug("Closing delayed tests")
				for _, test := range finishedTests {
					test.close()
				}

				// Clear the finished tests slice.
				finishedTests = nil
			}()

			// If we have a global test event finish hook, we call it here.
			if globalEventFinishHook != nil {
				log.Debug("Calling global tests event finish hook")
				globalEventFinishHook(tests)
			}
		}

		// Reset the global hooks to avoid memory leaks.
		globalTestEventStartHook = nil
		globalEventFinishHook = nil
	})
}

// testOperationName returns the test span operation for a module's framework.
func testOperationName(framework string) string {
	if framework == "" {
		return "test"
	}
	return strings.ToLower(framework) + ".test"
}
