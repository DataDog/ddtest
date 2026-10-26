package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
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

func TestCLIPrintsErrorsOnce(t *testing.T) {
	if scenario := os.Getenv("DDTEST_ERROR_HELPER"); scenario != "" {
		os.Args = []string{"ddtest", "testdrive", scenario}
		os.Exit(run(executeCommand))
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"--unknown", "--yes"} {
		t.Run(scenario, func(t *testing.T) {
			command := exec.Command(executable, "-test.run=^TestCLIPrintsErrorsOnce$")
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "DDTEST_ERROR_HELPER="+scenario)
			output, err := command.CombinedOutput()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("error = %v, output = %s", err, output)
			}
			if strings.Count(string(output), "Error:") != 1 || strings.Contains(string(output), "FAILURE") {
				t.Fatalf("expected one error, got:\n%s", output)
			}
		})
	}
}

type validationStatusError struct{ error }

func (e validationStatusError) Unwrap() error { return e.error }
func (e validationStatusError) ExitCode() int { return 1 }
func TestRunUsesOuterValidationStatusInsteadOfBuildExitCode(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestRunPreservesProcessExitCode$")
	command.Env = append(os.Environ(), "DDTEST_EXIT_CODE_HELPER=1")
	cause := command.Run()
	if cause == nil {
		t.Fatal("expected helper failure")
	}
	if code := run(func() error { return validationStatusError{cause} }); code != 1 {
		t.Fatalf("validation exit=%d, want1 despite nested tool exit7", code)
	}
}

// TestDispatchHandsInstrumentToDdto runs ddtest instrument in a helper
// process: ddto handles it, before ddtest sets anything, and exits.
func TestDispatchHandsInstrumentToDdto(t *testing.T) {
	stdout, _, code := dispatchHelper(t, "instrument", "--version")
	if code != 0 || !strings.HasPrefix(stdout, "ddto v") {
		t.Fatalf("instrument --version: exit %d, stdout %q", code, stdout)
	}
	_, stderr, code := dispatchHelper(t, "instrument")
	if code != 2 || !strings.Contains(stderr, " instrument [go | orchestrion go") {
		t.Fatalf("instrument without a command: exit %d, stderr %q", code, stderr)
	}
}

// TestDispatchKeepsCallerEnvironment checks that ddtest's own commands keep
// the environment that the process received: the SDK that ddto incorporates
// sets its root session variable when it starts.
func TestDispatchKeepsCallerEnvironment(t *testing.T) {
	stdout, stderr, code := dispatchHelper(t, "plan")
	if code != 0 || stdout != "root session unset\n" {
		t.Fatalf("plan: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestDispatchHelper(t *testing.T) {
	arguments, ok := os.LookupEnv("DDTEST_DISPATCH_HELPER")
	if !ok {
		t.Skip("runs only as dispatchHelper's process")
	}
	dispatch(append([]string{os.Args[0]}, strings.Split(arguments, "\x1f")...))
	if _, set := os.LookupEnv("_DD_ROOT_GO_SESSION_ID"); set {
		fmt.Println("root session set")
	} else {
		fmt.Println("root session unset")
	}
	os.Exit(0)
}

// dispatchHelper runs dispatch with args in a process of this test binary,
// with the environment of a caller that started no root session.
func dispatchHelper(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestDispatchHelper$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "_DD_ROOT_GO_SESSION_ID=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "DDTEST_DISPATCH_HELPER="+strings.Join(args, "\x1f"))
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), command.ProcessState.ExitCode()
}
