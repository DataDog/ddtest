package git

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestCheckAvailable_Success(t *testing.T) {
	// Save original functions
	origLookPath := LookPathFunc
	origRunGit := RunGitCommandFunc
	defer func() {
		LookPathFunc = origLookPath
		RunGitCommandFunc = origRunGit
	}()

	// Mock both functions to succeed
	LookPathFunc = func(file string) (string, error) {
		if file == "git" {
			return "/usr/bin/git", nil
		}
		return "", errors.New("not found")
	}
	RunGitCommandFunc = func(args ...string) error {
		return nil
	}

	err := CheckAvailable()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckAvailable_GitNotInstalled(t *testing.T) {
	// Save original functions
	origLookPath := LookPathFunc
	origRunGit := RunGitCommandFunc
	defer func() {
		LookPathFunc = origLookPath
		RunGitCommandFunc = origRunGit
	}()

	// Mock LookPath to fail
	LookPathFunc = func(file string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
	}
	RunGitCommandFunc = func(args ...string) error {
		return nil
	}

	err := CheckAvailable()
	if err == nil {
		t.Error("expected error when git is not installed")
	}
	if !strings.Contains(err.Error(), "git executable not found") {
		t.Errorf("expected 'git executable not found' in error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "git is required for ddtest to work") {
		t.Errorf("expected 'git is required for ddtest to work' in error message, got: %v", err)
	}
}

func TestCheckAvailable_NotAGitRepo(t *testing.T) {
	// Save original functions
	origLookPath := LookPathFunc
	origRunGit := RunGitCommandFunc
	defer func() {
		LookPathFunc = origLookPath
		RunGitCommandFunc = origRunGit
	}()

	// Mock LookPath to succeed, but git command to fail
	LookPathFunc = func(file string) (string, error) {
		if file == "git" {
			return "/usr/bin/git", nil
		}
		return "", errors.New("not found")
	}
	RunGitCommandFunc = func(args ...string) error {
		return errors.New("fatal: not a git repository")
	}

	err := CheckAvailable()
	if err == nil {
		t.Error("expected error when not in a git repository")
	}
	if !strings.Contains(err.Error(), "current directory is not a git repository") {
		t.Errorf("expected 'current directory is not a git repository' in error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "git is required for ddtest to work") {
		t.Errorf("expected 'git is required for ddtest to work' in error message, got: %v", err)
	}
}

func TestCheckAvailable_CorrectGitCommand(t *testing.T) {
	// Save original functions
	origLookPath := LookPathFunc
	origRunGit := RunGitCommandFunc
	defer func() {
		LookPathFunc = origLookPath
		RunGitCommandFunc = origRunGit
	}()

	var capturedArgs []string

	LookPathFunc = func(file string) (string, error) {
		return "/usr/bin/git", nil
	}
	RunGitCommandFunc = func(args ...string) error {
		capturedArgs = args
		return nil
	}

	_ = CheckAvailable()

	expectedArgs := []string{"rev-parse", "--git-dir"}
	if len(capturedArgs) != len(expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
	for i, arg := range expectedArgs {
		if capturedArgs[i] != arg {
			t.Errorf("expected arg[%d] = %q, got %q", i, arg, capturedArgs[i])
		}
	}
}

func TestCheckAvailable_Integration(t *testing.T) {
	// This test uses real git commands to verify the function works in a real environment
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available, skipping integration test")
	}

	// Save original functions to ensure we use real ones
	origLookPath := LookPathFunc
	origRunGit := RunGitCommandFunc
	defer func() {
		LookPathFunc = origLookPath
		RunGitCommandFunc = origRunGit
	}()

	// Reset to real implementations
	LookPathFunc = exec.LookPath
	RunGitCommandFunc = func(args ...string) error {
		return exec.Command("git", args...).Run()
	}

	// Since tests run in the project directory which is a git repo, this should succeed
	err := CheckAvailable()
	if err != nil {
		t.Errorf("expected no error in git repository, got %v", err)
	}
}
