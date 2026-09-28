package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestRunSuccess(t *testing.T) {
	t.Setenv("DD_CIVISIBILITY_ENABLED", "")
	t.Setenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", "")

	calls := 0
	exitCode := run(func() error {
		calls++
		return nil
	})

	if exitCode != 0 {
		t.Fatalf("run() exit code = %d, want 0", exitCode)
	}
	if calls != 1 {
		t.Fatalf("expected execute to be called once, got %d", calls)
	}
	if got := os.Getenv("DD_CIVISIBILITY_ENABLED"); got != "1" {
		t.Fatalf("DD_CIVISIBILITY_ENABLED = %q, want 1", got)
	}
	if got := os.Getenv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED"); got != "0" {
		t.Fatalf("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED = %q, want 0", got)
	}
}

func TestRunFailure(t *testing.T) {
	exitCode := run(func() error {
		return errors.New("boom")
	})

	if exitCode != 1 {
		t.Fatalf("run() exit code = %d, want 1", exitCode)
	}
}

func TestRunPreservesProcessExitCode(t *testing.T) {
	if os.Getenv("DDTEST_EXIT_CODE_HELPER") == "1" {
		os.Exit(7)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestRunPreservesProcessExitCode$")
	command.Env = append(os.Environ(), "DDTEST_EXIT_CODE_HELPER=1")
	err = command.Run()
	code := run(func() error { return fmt.Errorf("test command failed: %w", err) })
	if code != 7 {
		t.Fatalf("run() exit code = %d, want 7", code)
	}
}

func TestRunPreservesProcessSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal exit status")
	}
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()
	code := run(func() error { return fmt.Errorf("test command failed: %w", err) })
	if code != 143 {
		t.Fatalf("run() exit code = %d, want 143", code)
	}
}
