package git

import (
	"fmt"
	"os/exec"
)

// LookPathFunc is the function used to look up executables in PATH.
// It can be overridden in tests.
var LookPathFunc = exec.LookPath

// RunGitCommandFunc is the function used to run git commands.
// It can be overridden in tests.
var RunGitCommandFunc = func(args ...string) error {
	return exec.Command("git", args...).Run()
}

// CheckAvailable verifies that git is available and the current directory is a git repository.
// Returns an error if git is not installed or the current directory is not a git repository.
func CheckAvailable() error {
	if _, err := LookPathFunc("git"); err != nil {
		return fmt.Errorf("git executable not found: git is required for ddtest to work")
	}

	if err := RunGitCommandFunc("rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("current directory is not a git repository: git is required for ddtest to work")
	}

	return nil
}
