package instrument

import (
	"go/ast"
	"go/token"
)

// Hooks uses the exact link targets and signatures in the SDK's Orchestrion
// advice. Keeping these symbols also preserves the SDK's woven ownership gate.
const Hooks = `package testing
import _ "unsafe"
//go:linkname __dd_civisibility_instrumentTestingBuiltWithOrchestrion github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingBuiltWithOrchestrion
func __dd_civisibility_instrumentTestingBuiltWithOrchestrion()
//go:linkname __dd_civisibility_instrumentTestingMWithControl github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingMWithControl
func __dd_civisibility_instrumentTestingMWithControl(*M) (bool, func(int) int)
//go:linkname __dd_civisibility_instrumentTestingMAbnormalExitCode github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingMAbnormalExitCode
func __dd_civisibility_instrumentTestingMAbnormalExitCode() int
//go:linkname __dd_civisibility_instrumentTestingTFunc github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingTFunc
func __dd_civisibility_instrumentTestingTFunc(func(*T)) func(*T)
//go:linkname __dd_civisibility_instrumentSetErrorInfo github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentSetErrorInfo
func __dd_civisibility_instrumentSetErrorInfo(TB, string, string, int)
//go:linkname __dd_civisibility_instrumentCaptureFormattedError github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentCaptureFormattedError
func __dd_civisibility_instrumentCaptureFormattedError(TB, string, string, int) string
//go:linkname __dd_civisibility_instrumentCaptureFormattedSkip github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentCaptureFormattedSkip
func __dd_civisibility_instrumentCaptureFormattedSkip(TB, string, string) string
//go:linkname __dd_civisibility_instrumentCloseAndSkip github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentCloseAndSkip
func __dd_civisibility_instrumentCloseAndSkip(TB, string)
//go:linkname __dd_civisibility_instrumentSkipNow github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentSkipNow
func __dd_civisibility_instrumentSkipNow(TB)
//go:linkname __dd_civisibility_instrumentTestingParallel github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingParallel
func __dd_civisibility_instrumentTestingParallel(*T) bool
//go:linkname __dd_civisibility_instrumentTestingBFunc github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingBFunc
func __dd_civisibility_instrumentTestingBFunc(*B, string, func(*B)) (string, func(*B))
func init() { __dd_civisibility_instrumentTestingBuiltWithOrchestrion() }
`

// OrchestrionMarkCall is the line of Hooks that tells the SDK, while testing
// initializes, that testing carries the hooks. MiniOrchestrionMark replaces it
// for Mini: it sets gotesting's flag through a linknamed variable instead of
// calling Mini. testing initializes before Mini, and a Mini function that
// -coverpkg instruments and that runs first records its coverage counters
// without a package: Go then prints "internal error in coverage meta-data
// tracking". Mini declares the same variable in every released version.
const (
	OrchestrionMarkCall = "func init() { __dd_civisibility_instrumentTestingBuiltWithOrchestrion() }\n"
	MiniOrchestrionMark = `//go:linkname __dd_civisibility_testingBuiltWithOrchestrion github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.testingBuiltWithOrchestrion
var __dd_civisibility_testingBuiltWithOrchestrion atomic.Bool
func init() { __dd_civisibility_testingBuiltWithOrchestrion.Store(true) }
`
)

// ParallelStopHook lets Mini's in-process retries record the end of a parallel
// test in testing's own accounting, which only testing's tRunner updates. A
// retry attempt that calls Parallel ends outside tRunner; without the record,
// testing.AllocsPerRun panics in every later test of the binary. The linker
// rejects any other access to the counter. Like MiniOrchestrionMark, it sets
// Mini's variable instead of calling Mini while testing initializes.
const ParallelStopHook = `//go:linkname __dd_civisibility_testingParallelStop github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.testingParallelStop
var __dd_civisibility_testingParallelStop func()
func init() { __dd_civisibility_testingParallelStop = func() { parallelStop.Add(1) } }
`

// declaresParallelStop reports whether this AST declares the counter with the
// exact type used by ParallelStopHook. Locals, comments and other types do not
// enable the hook.
func declaresParallelStop(file *ast.File) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value := spec.(*ast.ValueSpec)
			selector, ok := value.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Int64" {
				continue
			}
			if pkg, ok := selector.X.(*ast.Ident); !ok || pkg.Name != "atomic" {
				continue
			}
			for _, ident := range value.Names {
				if ident.Name == "parallelStop" {
					return true
				}
			}
		}
	}
	return false
}

// FuzzHook preserves the callback's concrete type through the native F.Fuzz hook.
const FuzzHook = `
//go:linkname __dd_civisibility_instrumentTestingFuzzFunc github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting.instrumentTestingFuzzFunc
func __dd_civisibility_instrumentTestingFuzzFunc(any) any
`
