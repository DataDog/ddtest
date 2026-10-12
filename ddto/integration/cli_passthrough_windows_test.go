//go:build windows

package integration

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A command receives ddto's command line after ddto's name as the caller
// wrote it, so a batch file starts as if the caller had started it with that
// command line. Go's own quoting left a quoted & bare, where cmd.exe, which
// runs batch files, starts another command at it.
func TestCLIPassesWindowsCommandLineThrough(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	script := filepath.Join(t.TempDir(), "script.cmd")
	if strings.ContainsAny(script, " \t\"") {
		t.Skipf("the batch file's path %s needs quoting", script)
	}
	if err := os.WriteFile(script, []byte("@echo batch %*\r\n@exit /b 5\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(name, cmdline string) (string, int) {
		t.Helper()
		cmd := exec.Command(name)
		cmd.Dir, cmd.Env = dir, testEnv()
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		var exit *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exit) {
			return stdout.String(), exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v %s", cmdline, err, stderr.String())
		}
		return stdout.String(), 0
	}
	// cmd.exe keeps a quoted & in the argument. With the batch file's path
	// quoted as well, cmd /c removes the first and last quotes, directly as
	// through ddto.
	for _, arguments := range []string{script + ` "one&echo.INJECTED"`, `"` + script + `" "one&echo.INJECTED"`} {
		want, wantCode := run(script, arguments)
		got, code := run(driver, `"`+driver+`"  `+arguments)
		if got != want || code != wantCode {
			t.Fatalf("%s through ddto: %q %d; directly: %q %d", arguments, got, code, want, wantCode)
		}
		if !strings.HasPrefix(arguments, `"`) && (code != 5 || strings.TrimSpace(got) != `batch "one&echo.INJECTED"`) {
			t.Fatalf("%s: %q %d; want the quoted argument and 5", arguments, got, code)
		}
	}
}

// A command gets ddto's environment as it is. os/exec adds SYSTEMROOT to an
// environment without it, so both runs start without os/exec.
func TestCLIPassesWindowsEnvironmentThrough(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	program := buildPassthroughProgram(t)
	var env []string
	for _, v := range testEnv("PASSTHROUGH_REPORT=SYSTEMROOT") {
		if name, _, _ := strings.Cut(v, "="); !strings.EqualFold(name, "SYSTEMROOT") {
			env = append(env, v)
		}
	}
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	run := func(name string, argv ...string) string {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		process, err := os.StartProcess(name, argv, &os.ProcAttr{Dir: dir, Env: env, Files: []*os.File{stdin, w, w}})
		w.Close()
		if err != nil {
			t.Fatal(err)
		}
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := process.Wait(); err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	want := run(program, program, "0")
	if !strings.Contains(want, `SYSTEMROOT="" set=false`) {
		t.Fatalf("directly: %q; want no SYSTEMROOT", want)
	}
	if got := run(driver, driver, program, "0"); got != want {
		t.Fatalf("through ddto: %q; directly: %q", got, want)
	}
}
