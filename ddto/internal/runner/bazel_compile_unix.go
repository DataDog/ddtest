//go:build !windows

package runner

import (
	"fmt"
	"os"
	"syscall"

	"github.com/DataDog/ddtest/ddto/internal/version"
)

// execBazelCompiler replaces this process with the real compiler, with argv.
func execBazelCompiler(real string, argv []string) int {
	err := syscall.Exec(real, argv, os.Environ())
	fmt.Fprintln(os.Stderr, version.BuildLogPrefix+" ERROR: run "+real+":", err)
	return 2
}
