package gotesting

import (
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

// TestExclusions describes per-declaration execution exclusions. Generated
// package initialization registers these before testing chooses execution policy.
type TestExclusions struct {
	PreventFailureSuppression bool `json:"prevent_failure_suppression,omitempty"`
	NoAutoRetry               bool `json:"no_auto_retry,omitempty"`
}

type testManagementAssignment struct{ quarantined, disabled, attemptToFix bool }

var hasNoAutoRetryDeclarations atomic.Bool

var testExclusionDeclarations sync.Map // runtime function name -> TestExclusions

// RegisterTestExclusions records a compiled declaration, not mutable span tags.
func RegisterTestExclusions(fn any, exclusions TestExclusions) {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func || v.IsNil() {
		return
	}
	f := runtime.FuncForPC(v.Pointer())
	if f == nil {
		return
	}
	RegisterTestExclusionsByName(f.Name(), exclusions)
	if v.Type().NumIn() == 0 {
		return
	}
	receiver := v.Type().In(0)
	methodName := f.Name()[strings.LastIndexByte(f.Name(), '.')+1:]
	method, ok := receiver.MethodByName(methodName)
	// A package function can have the same name/signature as a method expression.
	// Only the exact reflected method may register its receiver counterparts.
	if !ok || !method.Func.IsValid() || method.Func.Pointer() != v.Pointer() {
		return
	}
	registerMethod := func(typ reflect.Type) {
		m, exists := typ.MethodByName(methodName)
		if !exists || !m.Func.IsValid() {
			return
		}
		if compiled := runtime.FuncForPC(m.Func.Pointer()); compiled != nil {
			RegisterTestExclusionsByName(compiled.Name(), exclusions)
		}
		if declaration := testifyMethodDeclaration(typ, m); declaration != nil {
			RegisterTestExclusionsByName(declaration.Name(), exclusions)
		}
	}
	registerMethod(receiver)
	if receiver.Kind() == reflect.Pointer {
		registerMethod(receiver.Elem())
	} else {
		registerMethod(reflect.PointerTo(receiver))
	}
}

func exclusionsForFunc(fn *runtime.Func) TestExclusions {
	if fn != nil {
		if value, ok := testExclusionDeclarations.Load(fn.Name()); ok {
			return value.(TestExclusions)
		}
	}
	return TestExclusions{}
}

func (e TestExclusions) any() bool { return e.PreventFailureSuppression || e.NoAutoRetry }

func applyFeatureExclusions(meta *additionalFeatureMetadata) {
	if meta.exclusions.PreventFailureSuppression {
		meta.isDisabled, meta.isQuarantined = false, false
	}
	if meta.exclusions.NoAutoRetry {
		meta.isEarlyFlakeDetectionEnabled, meta.isFlakyTestRetriesEnabled = false, false
		meta.isAttemptToFix, meta.shouldOrchestrateAttemptToFix = false, false
	}
}

func applyExecutionExclusions(meta *testExecutionMetadata) {
	if meta == nil {
		return
	}
	if meta.exclusions.PreventFailureSuppression {
		meta.isDisabled, meta.isQuarantined = false, false
	}
	if meta.exclusions.NoAutoRetry {
		meta.isEarlyFlakeDetectionEnabled, meta.isFlakyTestRetriesEnabled = false, false
		meta.isAttemptToFix, meta.shouldOrchestrateAttemptToFix = false, false
		meta.isARetry, meta.isEfdInParallel = false, false
		meta.suppressParentRetryMetadata = true
	}
}

// Excluded descendants retain their native result across retries of their parent.
// Each native parent invocation owns a fresh cache; a caller's -count and ordinary
// repeated suite invocation therefore keep their native execution behavior.
type excludedTestResult struct {
	Failed    bool `json:"failed,omitempty"`
	Skipped   bool `json:"skipped,omitempty"`
	Protected bool `json:"protected,omitempty"`
	Inherited bool `json:"inherited,omitempty"`
}

type excludedTestResults struct {
	mu       sync.Mutex
	items    map[string]excludedTestResult
	active   int
	terminal bool
}

// begin records an entered no-retry callback, unless terminal publication has
// already frozen admission. A rejected callback has no completed result.
func (results *excludedTestResults) begin() bool {
	if results == nil {
		return true
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	if results.terminal {
		return false
	}
	results.active++
	return true
}

// end runs after the existing deferred record has stored the completed result.
func (results *excludedTestResults) end() {
	if results == nil {
		return
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	results.active--
}

// freezeForTerminal closes admission and permits recovery only when every
// entered callback has completed. It never waits for in-flight callbacks.
func (results *excludedTestResults) freezeForTerminal() bool {
	if results == nil {
		return true
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	results.terminal = true
	return results.active == 0
}

func (results *excludedTestResults) record(t *testing.T, protected bool, declaration string) {
	if results == nil {
		return
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	if results.items == nil {
		results.items = map[string]excludedTestResult{}
	}
	direct, _ := testExclusionDeclarations.Load(declaration)
	inherited := direct == nil || !direct.(TestExclusions).NoAutoRetry
	if data := getTestifyTest(t); data != nil && declaration == exclusionDeclarationName(t, nil) {
		if fields := getTestPrivateFields(t); fields != nil && fields.parent != nil && getBoundTestifyTest(*fields.parent) == data {
			inherited = true
		}
	}
	results.items[t.Name()+"\x00"+declaration] = excludedTestResult{Failed: t.Failed(), Skipped: t.Skipped(), Protected: protected, Inherited: inherited}
}

func (results *excludedTestResults) snapshot() map[string]excludedTestResult {
	if results == nil {
		return nil
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	if len(results.items) == 0 {
		return nil
	}
	out := make(map[string]excludedTestResult, len(results.items))
	paths := make(map[string][]string, len(results.items))
	keys := make([]string, 0, len(results.items))
	for name, result := range results.items {
		out[name] = result
		path, _, _ := strings.Cut(name, "\x00")
		paths[path] = append(paths[path], name)
		keys = append(keys, name)
	}
	// Completed excluded ancestors replay their whole native subtree. Compact
	// only inherited generated rows in the transported copy. Independently
	// excluded declarations and local declaration-specific history remain intact.
	sort.Slice(keys, func(i, j int) bool {
		a, _, _ := strings.Cut(keys[i], "\x00")
		b, _, _ := strings.Cut(keys[j], "\x00")
		return len(a) > len(b)
	})
	for _, key := range keys {
		if !out[key].Inherited {
			continue
		}
		path, _, _ := strings.Cut(key, "\x00")
		for slash := strings.LastIndexByte(path, '/'); slash >= 0; slash = strings.LastIndexByte(path, '/') {
			path = path[:slash]
			ancestors := paths[path]
			if len(ancestors) > 1 {
				// Distinct declarations at one native path retain separate history.
				break
			}
			if len(ancestors) == 1 {
				ancestor := ancestors[0]
				parent := out[ancestor]
				child := out[key]
				if child.Failed && !parent.Failed {
					// A slash in a native label can share a lexical prefix
					// without denoting its actual native parent.
					break
				}
				if parent.Failed && child.Failed && child.Protected {
					parent.Protected = true
					out[ancestor] = parent
				}
				delete(out, key)
				break
			}
		}
	}
	return out
}

func (results *excludedTestResults) replay(t *testing.T, declaration string) bool {
	if results == nil {
		return false
	}
	results.mu.Lock()
	result, ok := results.items[t.Name()+"\x00"+declaration]
	results.mu.Unlock()
	if !ok {
		return false
	}
	if result.Failed {
		if result.Protected {
			markProtectedFailureAncestors(t)
		}
		t.Fail()
	}
	if result.Skipped {
		t.SkipNow()
	}
	return true
}

// Automatic retries of a parent retain each excluded descendant's first native
// result even when a later parent attempt no longer discovers that descendant.
func (results *excludedTestResults) failedDescendants(root string) (failed, protected bool) {
	if results == nil {
		return false, false
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	for key, result := range results.items {
		path, _, _ := strings.Cut(key, "\x00")
		if result.Failed && strings.HasPrefix(path, root+"/") {
			failed = true
			protected = protected || result.Protected
		}
	}
	return failed, protected
}

func applyRetainedExcludedFailure(t *testing.T, meta *testExecutionMetadata) bool {
	// In-process automatic families currently own only fresh top-level roots.
	// Independent declarations at the same subtest path keep their own outcomes.
	if meta == nil || !meta.usesFreshRetryAttemptRuntime || meta.identity == nil || len(meta.identity.Segments) != 1 {
		return false
	}
	failed, protected := meta.exclusionResults.failedDescendants(t.Name())
	if failed {
		if fields := getTestPrivateFields(t); fields != nil {
			fields.SetFailed(true)
		}
		if protected {
			markProtectedFailure(meta)
		}
	}
	return failed
}

// A selected protected descendant must remain blocking when a surrounding suite
// is quarantined. The marker belongs to one concrete attempt, so a legitimate
// retry which recovers the descendant does not retain an earlier failure.
func markProtectedFailureAncestors(t *testing.T) {
	markProtectedFailureAncestorsFromFields(getTestPrivateFields(t))
}

func markProtectedFailureAncestorsFromFields(fields *commonPrivateFields) {
	for fields != nil && fields.parent != nil && *fields.parent != nil {
		if meta := getTestMetadataFromPointer(*fields.parent); meta != nil {
			markProtectedFailure(meta)
		}
		fields = getCommonParentPrivateFields(fields)
	}
}

func applyProcessDirectiveExclusions(d *processRetrySubtreeDirective, e TestExclusions) {
	d.AssignedDisabled = d.AssignedDisabled || d.Disabled
	d.AssignedQuarantined = d.AssignedQuarantined || d.Quarantined
	d.AssignedAttemptToFix = d.AssignedAttemptToFix || d.AttemptToFix
	d.Exclusions = e
	if e.PreventFailureSuppression {
		d.Disabled, d.Quarantined = false, false
	}
	if e.NoAutoRetry {
		d.AttemptToFix = false
	}
}

// exclusionPolicySource keeps source attribution separate from declaration
// ownership. Only an actual Testify method boundary substitutes a reflected
// method for the framework callback; native rows keep their callback identity.
func exclusionPolicySource(t *testing.T, source *runtime.Func, data *TestifyTest) *runtime.Func {
	if isTestifyMethodBoundary(t, data) && data.methodFunc != nil && !exclusionsForFunc(source).any() {
		return data.methodFunc
	}
	return source
}

func exclusionsForChild(t *testing.T, source *runtime.Func, parent *testExecutionMetadata) TestExclusions {
	data := getTestifyTest(t)
	boundary := isTestifyMethodBoundary(t, data)
	source = exclusionPolicySource(t, source, data)
	e := exclusionsForFunc(source)
	if parent != nil && (data == nil || !boundary && !e.any()) {
		// An unannotated native row belongs to its immediate callback, even
		// when Testify supplies the row's reporting attribution. Explicit
		// callbacks and distinct Testify methods keep their own policy.
		e.PreventFailureSuppression = e.PreventFailureSuppression || parent.exclusions.PreventFailureSuppression
		e.NoAutoRetry = e.NoAutoRetry || parent.exclusions.NoAutoRetry
	}
	return e
}

func (results *excludedTestResults) merge(incoming map[string]excludedTestResult) {
	if results == nil || len(incoming) == 0 {
		return
	}
	results.mu.Lock()
	defer results.mu.Unlock()
	if results.items == nil {
		results.items = map[string]excludedTestResult{}
	}
	for name, result := range incoming {
		if _, known := results.items[name]; !known {
			results.items[name] = result
		}
	}
}

// Send only cached management/selection data already owned by the parent. A
// retry child performs no new feature requests and reports no ordinary subtests.
func processRetryExclusionPolicy(meta *testExecutionMetadata) *processRetrySubtreeConfig {
	if !hasNoAutoRetryDeclarations.Load() || meta == nil || meta.identity == nil {
		return nil
	}
	cfg, err := buildProcessRetrySubtreeConfig(&quarantinedRaceProcessContext{}, &commonInfo{identity: meta.identity}, meta, nil)
	if err != nil {
		return nil
	}
	return cfg
}

func validateProcessRetryExclusionPolicy(cfg *processRetrySubtreeConfig, root string) error {
	if cfg == nil {
		return nil
	}
	if cfg.Version != processRetrySubtreeVersion || cfg.SelectedRoot != root || cfg.Root.TestName != root || len(cfg.Directives) > processRetrySubtreeMaxDirectives || len(cfg.ITR) > processRetrySubtreeMaxDirectives {
		return errProcessRetryControlInvalid
	}
	for _, directive := range cfg.Directives {
		if !processRetryNameWithinRoot(directive.TestName, root) {
			return errProcessRetryControlInvalid
		}
	}
	for _, entry := range cfg.ITR {
		if !processRetryNameWithinRoot(entry.TestName, root) {
			return errProcessRetryControlInvalid
		}
	}
	return nil
}

func replayNewExcludedProcessTests(parent *testExecutionMetadata, attempt processRetryAttemptResult) {
	parent.exclusionResults.merge(attempt.Result.ExclusionResults)
	for _, result := range attempt.Result.ExcludedTests {
		identity := newTestIdentity(result.ModuleName, result.SuiteName, result.TestName)
		meta := &testExecutionMetadata{identity: identity, exclusions: result.Exclusions, assignment: testManagementAssignment{disabled: result.AssignedDisabled, quarantined: result.AssignedQuarantined, attemptToFix: result.AssignedAttemptToFix}, isDisabled: result.Disabled, isQuarantined: result.Quarantined, isItrSkipped: result.SkippedByITR, isItrForcedRun: result.ITRForcedRun, retryContinuationDecided: true}
		info := &commonInfo{moduleName: result.ModuleName, suiteName: result.SuiteName, testName: result.TestName, identity: identity}
		if settings := integrations.GetSettings(); settings != nil {
			if settings.KnownTestsEnabled {
				known, hasData := isKnownTest(info)
				meta.isANewTest = hasData && !known
			}
			raw := &additionalFeatureMetadata{identity: identity, isTestManagementEnabled: settings.TestManagement.Enabled}
			populateTestManagementMetadata(raw)
			meta.assignment.disabled = meta.assignment.disabled || raw.isDisabled
			meta.assignment.quarantined = meta.assignment.quarantined || raw.isQuarantined
			if raw.managementMatchKind == testManagementMatchExact {
				meta.assignment.attemptToFix = raw.isAttemptToFix
			}
		}
		finishProcessRetryTestEvent(info, meta, processRetryAttemptFromSubtreeResult(result), nil, nil)
	}
}

func (results *excludedTestResults) snapshotForRoot(root string) map[string]excludedTestResult {
	snapshot := results.snapshot()
	for name := range snapshot {
		if !excludedResultWithinRoot(name, root) {
			delete(snapshot, name)
		}
	}
	return snapshot
}

// RegisterTestExclusionsByName is the generated-bootstrap bridge for reachable
// helper declarations. Canonical names avoid importing the helper back into its
// own runtime dependency. Call only during initialization, before tests execute.
func RegisterTestExclusionsByName(name string, exclusions TestExclusions) {
	if strings.TrimSpace(name) == "" {
		return
	}
	testExclusionDeclarations.Store(name, exclusions)
	if exclusions.NoAutoRetry {
		hasNoAutoRetryDeclarations.Store(true)
	}
}

func registerExcludedResultCleanup(t *testing.T, parent *testExecutionMetadata, e TestExclusions, declaration string) {
	t.Cleanup(func() {
		if t.Failed() && e.PreventFailureSuppression {
			markProtectedFailureAncestors(t)
		}
		parent.exclusionResults.record(t, e.PreventFailureSuppression, declaration)
	})
}

func registerProtectedChildCleanup(t *testing.T, meta *testExecutionMetadata) {
	if !meta.exclusions.PreventFailureSuppression {
		return
	}
	t.Cleanup(func() {
		if t.Failed() {
			markProtectedFailureAncestors(t)
		}
	})
}

func exclusionDeclarationName(t *testing.T, source *runtime.Func) string {
	if data := getTestifyTest(t); isTestifyMethodBoundary(t, data) && !(source != nil && (data.methodFunc == nil || source.Name() != data.methodFunc.Name()) && exclusionsForFunc(source).any()) {
		if data.declarationFunc != nil {
			source = data.declarationFunc
		} else if data.methodFunc != nil {
			source = data.methodFunc
		}
	}
	if source != nil {
		return source.Name()
	}
	return ""
}

// Generated native rows inherit their owning declaration's exclusion. Keep that
// owner in the cache identity when another declaration later uses the same row
// callback and native path. Explicit declarations retain their independent key.
func exclusionResultDeclaration(t *testing.T, source *runtime.Func, parent *testExecutionMetadata) string {
	declaration := exclusionDeclarationName(t, source)
	direct, _ := testExclusionDeclarations.Load(declaration)
	if parent != nil && parent.exclusions.NoAutoRetry && parent.exclusionDeclaration != "" &&
		(direct == nil || !direct.(TestExclusions).NoAutoRetry) {
		return parent.exclusionDeclaration + "\x01" + declaration
	}
	return declaration
}

func setExecutionExclusions(t *testing.T, meta *testExecutionMetadata, source *runtime.Func, parent *testExecutionMetadata, exclusions TestExclusions) {
	meta.exclusions = exclusions
	meta.exclusionDeclaration = exclusionResultDeclaration(t, source, parent)
}

// Cache identities append a compiled declaration after the native test path.
// A selected isolation root may itself be the excluded declaration.
func excludedResultWithinRoot(key, root string) bool {
	path, declaration, declared := strings.Cut(key, "\x00")
	if declared && declaration == "" {
		return false
	}
	return declared && path == root || strings.HasPrefix(path, root+"/")
}

// markProtectedFailure bridges a trusted execution fact to its bound event
// without racing the execution metadata's ordinary test interface assignment.
func markProtectedFailure(meta *testExecutionMetadata) {
	if meta == nil {
		return
	}
	meta.protectedDescendantFailure.Store(true)
	if event := meta.protectedFailureEvent.Load(); event != nil {
		integrations.MarkTestProtectedFailure(*event)
	}
}

// bindProtectedFailureEvent publishes the event before rechecking the fact, so
// concurrent marking cannot be lost in either binding order. Bind before the
// base crash policy is registered, which may otherwise suppress the event.
func bindProtectedFailureEvent(meta *testExecutionMetadata, test integrations.Test) {
	if meta == nil {
		return
	}
	meta.protectedFailureEvent.Store(&test)
	if meta.protectedDescendantFailure.Load() {
		integrations.MarkTestProtectedFailure(test)
	}
}

// markNativeProtectedFailure observes an original native Fail, before recursive
// parent propagation or finalization can obscure the protected callback owner.
func markNativeProtectedFailure(tb testing.TB) {
	meta := getTestMetadata(tb)
	if meta == nil || !meta.exclusions.PreventFailureSuppression || processRetryChildFailureIsPropagated() {
		return
	}
	markProtectedFailure(meta)
	markProtectedFailureAncestorsFromFields(getFailureHookPrivateFields(tb))
}
