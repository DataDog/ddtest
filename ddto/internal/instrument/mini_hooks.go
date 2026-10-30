package instrument

import (
	"fmt"
	"path/filepath"
	"strings"
)

// HookFileLine returns the line directive that names a generated hook file
// name in its package's directory dir. The compiler records that name instead
// of the temporary file that holds the hook, so builds stay the same from run
// to run, and -trimpath rewrites it as it rewrites the package's own files.
func HookFileLine(dir, name string) string {
	return "//line " + filepath.ToSlash(filepath.Join(dir, name)) + ":1\n"
}

// Mini's packages whose variables the edits read.
const (
	miniGotestingPackage    = "github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting"
	miniIntegrationsPackage = "github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations"
	miniCIDeliveryPackage   = "github.com/DataDog/ddtest/ddto/internal/cidelivery"
	miniTracerPackage       = "github.com/DataDog/ddtest/ddto/internal/minitracer"
)

// A miniHook is a call that an edited package makes into Mini. Mini defines
// Variable, in Package, with the function as its value. The edited package
// declares a variable of the same name with no value and calls it through
// Call, a wrapper that keeps the package's own behavior while the variable is
// nil. The linker keeps the definition that has a value, so a binary that
// links Mini calls Mini, and a binary without Mini, such as a Bazel tool built
// with the instrumented SDK, links and behaves as if nothing were edited.
// The variable is data in the executable, set before any package initializes.
type miniHook struct {
	// Call is the name that the edits call; Local is the edited package's
	// name for the variable.
	Call, Local string
	// Package and Variable name Mini's variable.
	Package, Variable string
	// Params and Results are the function's types in the edited package's
	// terms; MiniType is the variable's type in Mini's.
	Params, Results []string
	MiniType        string
	// Native is what Call returns while the variable is nil.
	Native string
	// SkipParam is one more than the index of a parameter that counts the
	// stack frames that Mini's function skips, or 0. The wrapper adds its
	// own frame to that count, so stacks and locations start where they did.
	SkipParam int
}

// source declares the variable and its wrapper.
func (h miniHook) source() string {
	names, params := make([]string, len(h.Params)), make([]string, len(h.Params))
	for i, typ := range h.Params {
		names[i] = fmt.Sprintf("a%d", i)
		params[i] = names[i] + " " + typ
	}
	results := ""
	switch len(h.Results) {
	case 0:
	case 1:
		results = " " + h.Results[0]
	default:
		results = " (" + strings.Join(h.Results, ", ") + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "//go:linkname %s %s.%s\nvar %s func(%s)%s\n", h.Local, h.Package, h.Variable, h.Local, strings.Join(h.Params, ", "), results)
	args := append([]string(nil), names...)
	if h.SkipParam > 0 {
		args[h.SkipParam-1] += "+1"
	}
	call := "h(" + strings.Join(args, ", ") + ")"
	if len(h.Results) == 0 {
		fmt.Fprintf(&b, "func %s(%s) { if h := %s; h != nil { %s } }\n", h.Call, strings.Join(params, ", "), h.Local, call)
	} else {
		fmt.Fprintf(&b, "func %s(%s)%s { if h := %s; h != nil { return %s }; return %s }\n", h.Call, strings.Join(params, ", "), results, h.Local, call, h.Native)
	}
	return b.String()
}

// runtimeAPI is what Mini must declare for h: the variable with its exact
// type, since the linker does not compare the two declarations' types.
func (h miniHook) runtimeAPI() []string {
	return []string{"var " + h.Variable + " " + h.MiniType, "linkname " + h.Variable}
}

func testingHook(call, variable string, params, results []string, miniType, native string) miniHook {
	return miniHook{Call: call, Local: "__dd_civisibility_" + variable, Package: miniGotestingPackage, Variable: variable, Params: params, Results: results, MiniType: miniType, Native: native}
}

// skipping marks h's parameter index as the count of frames that Mini skips.
func skipping(h miniHook, index int) miniHook {
	h.SkipParam = index + 1
	return h
}

// miniTestingHooks are the calls of Transform's edits into Mini. Without Mini,
// M.Run's hook returns no exit function, and M.Run then runs as native.
var miniTestingHooks = []miniHook{
	testingHook("__dd_civisibility_instrumentTestingMWithControl", "testingHookMWithControl", []string{"*M"}, []string{"bool", "func(int) int"}, "func(*testing.M) (bool, func(int) int)", "true, nil"),
	testingHook("__dd_civisibility_instrumentTestingMAbnormalExitCode", "testingHookMAbnormalExitCode", nil, []string{"int"}, "func() int", "0"),
	testingHook("__dd_civisibility_instrumentTestingTFunc", "testingHookTFunc", []string{"func(*T)"}, []string{"func(*T)"}, "func(func(*testing.T)) func(*testing.T)", "a0"),
	skipping(testingHook("__dd_civisibility_instrumentSetErrorInfo", "testingHookSetErrorInfo", []string{"TB", "string", "string", "int"}, nil, "func(testing.TB, string, string, int)", ""), 3),
	skipping(testingHook("__dd_civisibility_instrumentCaptureFormattedError", "testingHookCaptureFormattedError", []string{"TB", "string", "string", "int"}, []string{"string"}, "func(testing.TB, string, string, int) string", "a2"), 3),
	testingHook("__dd_civisibility_instrumentCaptureFormattedSkip", "testingHookCaptureFormattedSkip", []string{"TB", "string", "string"}, []string{"string"}, "func(testing.TB, string, string) string", "a2"),
	testingHook("__dd_civisibility_instrumentSkipNow", "testingHookSkipNow", []string{"TB"}, nil, "func(testing.TB)", ""),
	testingHook("__dd_civisibility_instrumentTestingParallel", "testingHookParallel", []string{"*T"}, []string{"bool"}, "func(*testing.T) bool", "false"),
	testingHook("__dd_civisibility_instrumentTestingBFunc", "testingHookBFunc", []string{"*B", "string", "func(*B)"}, []string{"string", "func(*B)"}, "func(*testing.B, string, func(*testing.B)) (string, func(*testing.B))", "a1, a2"),
	testingHook("__dd_civisibility_instrumentTestingFuzzFunc", "testingHookFuzzFunc", []string{"any"}, []string{"any"}, "func(any) any", "a0"),
}

// miniTimeoutHook is TimeoutHook's call: the alarm's panic keeps its message.
var miniTimeoutHook = testingHook("__dd_civisibility_instrumentTestTimeout", "testingHookTestTimeout", []string{"string"}, []string{"string"}, "func(string) string", "a0")

// miniExitHook is ExitHook's call.
var miniExitHook = miniHook{Call: "__dd_civisibility_forcedExit", Local: "__dd_civisibility_forcedExitHook", Package: miniIntegrationsPackage, Variable: "forcedExitHook", MiniType: "func()"}

// miniTestifyHook is the scoped registration at the entry of Testify's
// suite.Run. Without Mini it registers nothing and releases nothing.
var miniTestifyHook = miniHook{Call: TestifyRegisterName, Local: "__dd_ci_testifyHook", Package: miniGotestingPackage, Variable: "testifyHookSuiteRunScoped", Params: []string{"*testing.T", "interface{}"}, Results: []string{"func()"}, MiniType: "func(*testing.T, any) func()", Native: "func() {}"}

// miniGoleakHook is the variable that goleak's Find reads: without Mini, Find
// neither pauses CI delivery nor adds CI filters.
var miniGoleakHook = miniHook{Local: GoleakHookName, Package: miniCIDeliveryPackage, Variable: "leakCheckHook", MiniType: "func() func()"}

// MiniHooks returns testing's hooks for Mini. parallelStop reports that
// testing declares the counter Mini's in-process retries must balance, and
// timeout that its -timeout alarm calls Mini.
func MiniHooks(parallelStop, timeout bool) string {
	var b strings.Builder
	b.WriteString("package testing\nimport (\n\t\"sync/atomic\"\n\t_ \"unsafe\"\n)\n")
	b.WriteString(MiniOrchestrionMark)
	for _, h := range miniTestingHooks {
		b.WriteString(h.source())
	}
	if parallelStop {
		b.WriteString(ParallelStopHook)
	}
	if timeout {
		b.WriteString(miniTimeoutHook.source())
	}
	return b.String()
}

// TestingHooksRuntimeError reports why the selected runtime's gotesting
// package, given as the sources that the build compiles, cannot receive
// MiniHooks' calls, or nil. Without the variables, the calls would do nothing.
func TestingHooksRuntimeError(sources map[string][]byte) error {
	var want []string
	for _, h := range miniTestingHooks {
		want = append(want, h.runtimeAPI()...)
	}
	return declarationsError(sources, want, "the runtime's gotesting package lacks %s")
}

// TestifyHookRuntimeError reports why the selected runtime's gotesting package
// cannot receive the scoped Testify registration, or nil.
func TestifyHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, miniTestifyHook.runtimeAPI(), "the runtime's gotesting package lacks %s")
}

// GoleakHookRuntimeError reports why the selected runtime's cidelivery package
// cannot receive goleak's call, or nil.
func GoleakHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, miniGoleakHook.runtimeAPI(), "the runtime's cidelivery package lacks %s")
}
