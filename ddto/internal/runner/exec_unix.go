//go:build !windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

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
