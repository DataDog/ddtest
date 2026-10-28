package instrument

// sessionRuntimeAPI is what a test process of the selected runtime needs to
// report in the test session that ddto owns: the utils function that joins
// it. Released runtimes before it ignore the session and report their own.
var sessionRuntimeAPI = []string{"func HandedCLISession() (*CLISession, *CLISessionRecord)"}

// SessionRuntimeError reports why the test processes of the selected runtime,
// whose civisibility/utils package is given as the sources that the build
// compiles, cannot report in ddto's test session, or nil.
func SessionRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, sessionRuntimeAPI, "the runtime's civisibility/utils package lacks %s")
}
