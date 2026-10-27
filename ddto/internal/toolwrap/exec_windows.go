package toolwrap

import (
	"fmt"
	"os"
	"os/exec"
)

// ExecNative runs the tool, chained after the user's -toolexec: Windows has
// no exec replacement, so it keeps the streams and the tool's exit status.
// prefix starts ddto's error messages.
func ExecNative(args []string, prefix string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, prefix+" ERROR: missing tool executable")
		return 2
	}
	args, err := ChainUserToolexec(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return run(args[0], args[1:])
}

// runDDTO runs ddto, given its arguments, with the wrapper's streams and
// returns its exit status.
func runDDTO(ddto string, args []string) int {
	return run(ddto, args)
}

func run(name string, args []string) int {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}
