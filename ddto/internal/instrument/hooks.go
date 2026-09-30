package instrument

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
