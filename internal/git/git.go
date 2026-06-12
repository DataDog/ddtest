package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LookPathFunc is the function used to look up executables in PATH.
// It can be overridden in tests.
var LookPathFunc = exec.LookPath

// RunGitOutputFunc is the function used to run git commands and capture output.
// It can be overridden in tests.
var RunGitOutputFunc = func(args ...string) ([]byte, error) {
	return exec.Command("git", args...).CombinedOutput()
}

var createTempFileFunc = os.CreateTemp

// CheckAvailable verifies that git is available and the current directory is a git repository.
// Returns an error if git is not installed or the current directory is not a git repository.
func CheckAvailable() error {
	if _, err := LookPathFunc("git"); err != nil {
		return fmt.Errorf("git executable not found: git is required for ddtest to work")
	}

	gitDir, err := getGitDirectory()
	if err != nil {
		return fmt.Errorf("current directory is not a git repository: git is required for ddtest to work")
	}

	if err := checkWritable(gitDir); err != nil {
		return err
	}

	return nil
}

func getGitDirectory() (string, error) {
	out, err := RunGitOutputFunc("rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}

	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return "", fmt.Errorf("git returned an empty git directory")
	}

	return gitDir, nil
}

func checkWritable(path string) error {
	file, err := createTempFileFunc(path, ".ddtest-write-check-")
	if err != nil {
		return fmt.Errorf("git metadata directory is not writable: ddtest needs write access to %s to fetch repository metadata for Test Impact Analysis: %w", path, err)
	}

	fileName := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(fileName)
		return fmt.Errorf("git metadata directory write check failed: %s: %w", path, err)
	}
	if err := os.Remove(fileName); err != nil {
		return fmt.Errorf("git metadata directory write check cleanup failed: %s: %w", path, err)
	}

	return nil
}
