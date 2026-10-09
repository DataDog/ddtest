//go:build !windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

// ExecWithCallerEnvironment restores the caller's Go settings, then replaces
// this process with args: a go test -exec program sees what native go test
// gives it, rather than ddto's temporary workspace.
func ExecWithCallerEnvironment(args []string) int {
	goenv.Restore()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing -exec program")
		return 2
	}
	executable, err := exec.LookPath(args[0])
	if err == nil {
		err = syscall.Exec(executable, args, os.Environ())
	}
	fmt.Fprintln(os.Stderr, err)
	return 2
}

// ExecNativeTool replaces the wrapper: no second process, wait loop or plan I/O.
// It is only for the CLI entrypoint, never an in-process library caller.
func ExecNativeTool(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: missing tool executable")
		return 2
	}
	args, err := chainUserToolexec(args)
	if err == nil {
		var executable string
		if executable, err = exec.LookPath(args[0]); err == nil {
			err = syscall.Exec(executable, args, os.Environ())
		}
	}
	fmt.Fprintln(os.Stderr, err)
	return 2
}
