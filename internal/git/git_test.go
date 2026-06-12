package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func resetGitHooks(t *testing.T) {
	t.Helper()

	origLookPath := LookPathFunc
	origRunGitOutput := RunGitOutputFunc
	origCreateTempFile := createTempFileFunc
	t.Cleanup(func() {
		LookPathFunc = origLookPath
		RunGitOutputFunc = origRunGitOutput
		createTempFileFunc = origCreateTempFile
	})
}

func mockGitDirectory(t *testing.T) string {
	t.Helper()

	gitDir := t.TempDir()
	RunGitOutputFunc = func(args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "rev-parse --absolute-git-dir":
			return []byte(gitDir + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected git command: %v", args)
		}
	}

	return gitDir
}

func TestCheckAvailable_Success(t *testing.T) {
	resetGitHooks(t)
	mockGitDirectory(t)

	// Mock both functions to succeed
	LookPathFunc = func(file string) (string, error) {
		if file == "git" {
			return "/usr/bin/git", nil
		}
		return "", errors.New("not found")
	}

	err := CheckAvailable()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCheckAvailable_GitNotInstalled(t *testing.T) {
	resetGitHooks(t)

	// Mock LookPath to fail
	LookPathFunc = func(file string) (string, error) {
		return "", errors.New("executable file not found in $PATH")
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
	resetGitHooks(t)

	// Mock LookPath to succeed, but git command to fail
	LookPathFunc = func(file string) (string, error) {
		if file == "git" {
			return "/usr/bin/git", nil
		}
		return "", errors.New("not found")
	}
	RunGitOutputFunc = func(args ...string) ([]byte, error) {
		return nil, errors.New("fatal: not a git repository")
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
	resetGitHooks(t)

	gitDir := t.TempDir()
	var capturedOutputArgs []string

	LookPathFunc = func(file string) (string, error) {
		return "/usr/bin/git", nil
	}
	RunGitOutputFunc = func(args ...string) ([]byte, error) {
		capturedOutputArgs = append([]string(nil), args...)
		switch strings.Join(args, " ") {
		case "rev-parse --absolute-git-dir":
			return []byte(gitDir + "\n"), nil
		default:
			return nil, fmt.Errorf("unexpected git command: %v", args)
		}
	}

	if err := CheckAvailable(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedArgs := []string{"rev-parse", "--absolute-git-dir"}
	if len(capturedOutputArgs) != len(expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, capturedOutputArgs)
	}
	for i, arg := range expectedArgs {
		if capturedOutputArgs[i] != arg {
			t.Errorf("expected arg[%d] = %q, got %q", i, arg, capturedOutputArgs[i])
		}
	}
}

func TestCheckAvailable_GitDirectoryNotWritable(t *testing.T) {
	resetGitHooks(t)
	gitDir := mockGitDirectory(t)

	LookPathFunc = func(file string) (string, error) {
		return "/usr/bin/git", nil
	}
	createTempFileFunc = func(dir, pattern string) (*os.File, error) {
		return nil, os.ErrPermission
	}

	err := CheckAvailable()
	if err == nil {
		t.Fatal("expected error when git directory is not writable")
	}
	if !strings.Contains(err.Error(), "git metadata directory is not writable") {
		t.Errorf("expected git metadata writability error, got: %v", err)
	}
	if !strings.Contains(err.Error(), gitDir) {
		t.Errorf("expected git directory path %q in error message, got: %v", gitDir, err)
	}
	if !strings.Contains(err.Error(), "ddtest needs write access") {
		t.Errorf("expected actionable write access message, got: %v", err)
	}
}

func TestCheckAvailable_Integration(t *testing.T) {
	// This test uses real git commands to verify the function works in a real environment
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available, skipping integration test")
	}

	// Save original functions to ensure we use real ones
	resetGitHooks(t)

	// Reset to real implementations
	LookPathFunc = exec.LookPath
	RunGitOutputFunc = func(args ...string) ([]byte, error) {
		return exec.Command("git", args...).CombinedOutput()
	}
	createTempFileFunc = os.CreateTemp

	// Since tests run in the project directory which is a git repo, this should succeed
	err := CheckAvailable()
	if err != nil {
		if strings.Contains(err.Error(), "git metadata directory is not writable") {
			t.Skipf("git metadata directory is not writable in this environment: %v", err)
		}
		t.Errorf("expected no error in git repository, got %v", err)
	}
}
