//go:build !windows

package toolwrap

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// ExecNative replaces the wrapper with the tool, chained after the user's
// -toolexec: no second process, wait loop or plan I/O. prefix starts ddto's
// error messages.
func ExecNative(args []string, prefix string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, prefix+" ERROR: missing tool executable")
		return 2
	}
	args, err := ChainUserToolexec(args)
	if err == nil {
		var executable string
		if executable, err = exec.LookPath(args[0]); err == nil {
			err = syscall.Exec(executable, args, os.Environ())
		}
	}
	fmt.Fprintln(os.Stderr, err)
	return 2
}

// runDDTO replaces the wrapper with ddto, given its arguments.
func runDDTO(ddto string, args []string) int {
	err := syscall.Exec(ddto, append([]string{ddto}, args...), os.Environ())
	fmt.Fprintln(os.Stderr, err)
	return 2
}
