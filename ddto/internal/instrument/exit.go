package instrument

import "fmt"

// ExitHook is an overlay file for package testing. It registers Mini's
// forcedExit as a runtime exit hook, which os.Exit runs before the process
// ends, whatever its status. A test that calls os.Exit or log.Fatal, or a
// goroutine that does while tests run, then still sends the events of the
// tests that ran and closes the session. After the tests, CI Visibility is
// already closed and forcedExit returns at once. The hook runs only at exit,
// after every package has initialized.
//
// os.Exit(0) during a test already panics in testing and takes the panic path.
// A signal or a crash ends the process without exit hooks.
const ExitHook = `package testing

import (
	"internal/runtime/exithook"
	_ "unsafe"
)

//go:linkname __dd_civisibility_forcedExit github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations.forcedExit
func __dd_civisibility_forcedExit()

func init() {
	exithook.Add(exithook.Hook{F: __dd_civisibility_forcedExit, RunOnFailure: true})
}
`

// exitHookAPI is every declaration of internal/runtime/exithook that
// ExitHook uses, rendered as sourceDeclarations renders them.
var exitHookAPI = []string{
	"field Hook.F func()",
	"field Hook.RunOnFailure bool",
	"func Add(Hook)",
}

// exitRuntimeAPI is what ExitHook's linkname names in Mini's integrations
// package.
var exitRuntimeAPI = []string{"func forcedExit()", "linkname forcedExit"}

// ExitHookToolchainError reports why the toolchain's internal/runtime/exithook,
// given as the sources that the build compiles, cannot build ExitHook, or nil.
func ExitHookToolchainError(sources map[string][]byte) error {
	return declarationsError(sources, exitHookAPI, "internal/runtime/exithook lacks %s")
}

// ExitHookRuntimeError reports why the selected runtime's integrations
// package, given as the sources that the build compiles, cannot receive
// ExitHook's call, or nil.
func ExitHookRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, exitRuntimeAPI, "the runtime's integrations package lacks %s")
}

// declarationsError reports the first of want that sources do not declare.
func declarationsError(sources map[string][]byte, want []string, format string) error {
	have, err := sourceDeclarations(declaringSources(sources, want))
	if err != nil {
		return err
	}
	for _, decl := range want {
		if !have[decl] {
			return fmt.Errorf(format, decl)
		}
	}
	return nil
}
