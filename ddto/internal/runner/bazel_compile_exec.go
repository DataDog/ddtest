package runner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/DataDog/ddtest/ddto/internal/version"
)

// runBazelCompiler runs the real compiler with argv, whose argv[0] keeps the
// name compile: cmd/go parses the compiler's -V=full output, which names it.
// It returns the compiler's exit status, after which the caller removes the
// edited inputs; a compiler killed by signal N, such as by the kernel when
// memory runs out, gives 128+N, which Exit raises again.
func runBazelCompiler(real string, argv []string, stderr io.Writer) int {
	cmd := exec.Command(real, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code, signaled := signalExitCode(exit.ProcessState); signaled {
				fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR: the compiler ended:", err)
				return code
			}
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, version.BuildLogPrefix+" ERROR:", err)
		return 2
	}
	return 0
}
