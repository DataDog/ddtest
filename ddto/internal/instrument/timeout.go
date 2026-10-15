package instrument

// TimeoutHook declares the function that testing's -timeout alarm calls, with
// the panic's message, just before it panics: Mini fails the running tests,
// sends its events and returns the message, so the alarm panics as before. The
// alarm's panic ends the process without running exit hooks or deferred
// calls, which loses every event that Mini buffered. Mini only.
const TimeoutHook = `//go:linkname __dd_civisibility_instrumentTestTimeout github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.instrumentTestTimeout
func __dd_civisibility_instrumentTestTimeout(string) string
`

// timeoutRuntimeAPI is what TimeoutHook's linkname names in Mini's gotesting
// package. Released runtimes before it lack the function.
var timeoutRuntimeAPI = []string{"func instrumentTestTimeout(string) string", "linkname instrumentTestTimeout"}

// TimeoutHookRuntimeError reports why the selected runtime's gotesting
// package, given as the sources that the build compiles, cannot receive
// TimeoutHook's call, or nil.
func TimeoutHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, timeoutRuntimeAPI, "the runtime's gotesting package lacks %s")
}
