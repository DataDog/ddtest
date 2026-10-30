package instrument

// MiniHooks declares, with timeout, the function that testing's -timeout alarm
// calls, with the panic's message, just before it panics: Mini fails the
// running tests, sends its events and returns the message, so the alarm panics
// as before. The alarm's panic ends the process without running exit hooks or
// deferred calls, which loses every event that Mini buffered. Mini only.

// timeoutRuntimeAPI is what the timeout hook's linkname names in Mini's
// gotesting package. Released runtimes before it lack the variable.
var timeoutRuntimeAPI = miniTimeoutHook.runtimeAPI()

// TimeoutHookRuntimeError reports why the selected runtime's gotesting
// package, given as the sources that the build compiles, cannot receive the
// timeout hook's call, or nil.
func TimeoutHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, timeoutRuntimeAPI, "the runtime's gotesting package lacks %s")
}
