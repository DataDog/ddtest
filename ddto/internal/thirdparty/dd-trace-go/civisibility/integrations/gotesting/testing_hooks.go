package gotesting

import (
	"testing"
	_ "unsafe" // ddtest instrument's testing overlay names these variables
)

// The variables that ddtest instrument's testing overlay and Testify edit
// call. Each edited package declares the same variable with no value and
// calls it only when it is not nil; the linker keeps these definitions, which
// have one. The values are data in the executable, set before any package
// initializes. A binary that does not link Mini, such as a Bazel tool built
// with the instrumented SDK, only has the declarations without a value, and
// its testing keeps its own behavior. The types must stay as written: the
// linker does not compare them, and the CLI checks them in a runtime that a
// client selects.

//go:linkname testingHookMWithControl
var testingHookMWithControl func(*testing.M) (bool, func(int) int) = instrumentTestingMWithControl

//go:linkname testingHookMAbnormalExitCode
var testingHookMAbnormalExitCode func() int = instrumentTestingMAbnormalExitCode

//go:linkname testingHookTFunc
var testingHookTFunc func(func(*testing.T)) func(*testing.T) = instrumentTestingTFunc

//go:linkname testingHookSetErrorInfo
var testingHookSetErrorInfo func(testing.TB, string, string, int) = instrumentSetErrorInfo

//go:linkname testingHookCaptureFormattedError
var testingHookCaptureFormattedError func(testing.TB, string, string, int) string = instrumentCaptureFormattedError

//go:linkname testingHookCaptureFormattedSkip
var testingHookCaptureFormattedSkip func(testing.TB, string, string) string = instrumentCaptureFormattedSkip

//go:linkname testingHookSkipNow
var testingHookSkipNow func(testing.TB) = instrumentSkipNow

//go:linkname testingHookParallel
var testingHookParallel func(*testing.T) bool = instrumentTestingParallel

//go:linkname testingHookBFunc
var testingHookBFunc func(*testing.B, string, func(*testing.B)) (string, func(*testing.B)) = instrumentTestingBFunc

//go:linkname testingHookFuzzFunc
var testingHookFuzzFunc func(any) any = instrumentTestingFuzzFunc

//go:linkname testingHookTestTimeout
var testingHookTestTimeout func(string) string = instrumentTestTimeout

//go:linkname testifyHookSuiteRunScoped
var testifyHookSuiteRunScoped func(*testing.T, any) func() = instrumentTestifySuiteRunScoped
