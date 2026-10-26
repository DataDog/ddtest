//go:build !windows

package toolwrap_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/toolwrap"
)

// The wrapper prints its key, rejects malformed invocations, and otherwise
// replaces itself: with the tool when the tool needs no plan, with ddto's
// tool-overlay when it does. Main runs in a helper process, since it execs.
func TestMainRunsTheToolOrDdto(t *testing.T) {
	if os.Getenv("TOOLWRAP_TEST_MAIN") == "1" {
		os.Exit(toolwrap.Main(strings.Split(os.Getenv("TOOLWRAP_TEST_ARGS"), "\x1f"), "the-key", "prefix"))
	}
	ddto := filepath.Join(t.TempDir(), "ddto")
	if err := os.WriteFile(ddto, []byte("#!/bin/sh\necho ddto \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		out  string
		code int
	}{
		{"key", []string{toolwrap.KeyFlag}, "the-key\n", 0},
		{"too few arguments", []string{ddto, "testify", "plan"}, "prefix ERROR: invalid tool wrapper arguments\n", 2},
		{"invalid mode", []string{ddto, "unknown", "plan", "compile"}, "prefix ERROR: invalid tool mode\n", 2},
		{"native tool", []string{ddto, "testify", "plan", "sh", "-c", "echo native"}, "native\n", 0},
		{"tool overlay", []string{ddto, "testify", "plan", "/goroot/pkg/tool/compile", "-p", "x"}, "ddto tool-overlay testify plan /goroot/pkg/tool/compile -p x\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestMainRunsTheToolOrDdto$")
			cmd.Env = append(os.Environ(), "TOOLWRAP_TEST_MAIN=1", "TOOLWRAP_TEST_ARGS="+strings.Join(tc.args, "\x1f"),
				toolwrap.UserToolexecEnv+"=", "TOOLEXEC_IMPORTPATH="+toolwrap.TestifySuiteImport)
			out, err := cmd.CombinedOutput()
			code := 0
			if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.out || code != tc.code {
				t.Fatalf("Main(%q) = %d, %q; want %d, %q", tc.args, code, out, tc.code, tc.out)
			}
		})
	}
}

// A tool that cannot run reports why and fails as go's -toolexec expects,
// without replacing the wrapper.
func TestExecNativeFailures(t *testing.T) {
	if code := toolwrap.ExecNative(nil, "prefix"); code != 2 {
		t.Fatalf("no tool: %d", code)
	}
	t.Setenv(toolwrap.UserToolexecEnv, "")
	if code := toolwrap.ExecNative([]string{filepath.Join(t.TempDir(), "missing-tool")}, "prefix"); code != 2 {
		t.Fatalf("missing tool: %d", code)
	}
	t.Setenv(toolwrap.UserToolexecEnv, `"unterminated`)
	if code := toolwrap.ExecNative([]string{"compile"}, "prefix"); code != 2 {
		t.Fatalf("invalid user -toolexec: %d", code)
	}
}
