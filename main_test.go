package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
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

// Run the real CLI in a child process to cover Cobra output and main's exit code.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("DDTEST_TEST_CLI_PROCESS") != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	os.Args = append([]string{"ddtest"}, os.Args[separator+1:]...)
	main()
}

func TestCLIOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name       string
		args       []string
		wantError  string
		wantHint   string
		missingGit bool
	}{
		{name: "unknown flag", args: []string{"plan", "--unknown"}, wantError: "unknown flag: --unknown", wantHint: "Run `ddtest plan --help` for usage"},
		{name: "invalid flag value", args: []string{"run", "--min-parallelism=nope"}, wantError: `invalid argument "nope"`, wantHint: "Run `ddtest run --help` for usage"},
		{name: "missing flag value", args: []string{"plan", "--framework"}, wantError: "flag needs an argument: --framework", wantHint: "Run `ddtest plan --help` for usage"},
		{name: "unknown command", args: []string{"nonexistent"}, wantError: `unknown command "nonexistent"`, wantHint: "Run 'ddtest --help' for usage."},
		{name: "prerequisite failure", args: []string{"plan"}, wantError: "[plan_git_unavailable] git executable not found", missingGit: true},
		{name: "help", args: []string{"plan", "--help"}},
		{name: "version", args: []string{"--version"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := exec.Command(executable, append([]string{"-test.run=^TestCLIProcess$", "--"}, tt.args...)...)
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "DDTEST_TEST_CLI_PROCESS=1", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false")
			if tt.missingGit {
				command.Env = append(command.Env, "PATH=")
			}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if tt.wantError == "" {
				if err != nil || stderr.Len() != 0 || stdout.Len() == 0 {
					t.Fatalf("help/version failed: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				return
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("exit error = %v, want code 1", err)
			}
			output := stderr.String()
			if stdout.Len() != 0 || strings.Count(output, tt.wantError) != 1 || !strings.HasPrefix(output, "Error: ") {
				t.Fatalf("expected one error on stderr: stdout=%q stderr=%q", stdout.String(), output)
			}
			if tt.wantHint != "" && !strings.Contains(output, tt.wantHint) {
				t.Errorf("missing help hint %q in %q", tt.wantHint, output)
			}
			for _, noise := range []string{"Usage:", "Global Flags:", "FAILURE", "level=ERROR"} {
				if strings.Contains(output, noise) {
					t.Errorf("unexpected %q in %q", noise, output)
				}
			}
			if tt.wantHint == "" && strings.Contains(output, "--help") {
				t.Errorf("runtime failure should not suggest usage: %q", output)
			}
		})
	}
}
