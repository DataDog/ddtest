package integration

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// passthroughProgram reports its arguments, input and environment, then exits
// with the status in its first argument. PASSTHROUGH_REPORT names one more
// variable to report.
const passthroughProgram = `package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
)

func main() {
	input, _ := io.ReadAll(os.Stdin)
	civisibility, set := os.LookupEnv("DD_CIVISIBILITY_ENABLED")
	if !set {
		civisibility = "unset"
	}
	fmt.Printf("args=%q stdin=%q mark=%q civisibility=%s\n", os.Args[1:], input, os.Getenv("PASSTHROUGH_MARK"), civisibility)
	if name := os.Getenv("PASSTHROUGH_REPORT"); name != "" {
		value, set := os.LookupEnv(name)
		fmt.Printf("%s=%q set=%t\n", name, value, set)
	}
	fmt.Fprintln(os.Stderr, "to stderr")
	status, _ := strconv.Atoi(os.Args[1])
	os.Exit(status)
}
`

// ddto runs every command but go test unchanged: with its arguments, standard
// streams, environment and exit status, and without the CI Visibility default
// that ddto test sets or any output of its own. A go command runs as go would,
// and a command that does not run exits as from env.
func TestCLIPassesOtherCommandsThrough(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	program := buildPassthroughProgram(t)
	// An invalid DD_LOGGING_RATE made ddto's initialization warn.
	env := testEnv("PASSTHROUGH_MARK=kept", "DD_LOGGING_RATE=bad")

	cmd := exec.Command(driver, program, "7", "a b")
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader("input")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	var exit *exec.ExitError
	if err := cmd.Run(); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit %v; want status 7", err)
	}
	if want := `args=["7" "a b"] stdin="input" mark="kept" civisibility=unset` + "\n"; stdout.String() != want {
		t.Fatalf("stdout %q; want %q", stdout.String(), want)
	}
	if got := strings.TrimSpace(stderr.String()); got != "to stderr" {
		t.Fatalf("stderr %q", got)
	}

	for _, args := range [][]string{{"version"}, {"env", "GOFLAGS", "GOWORK"}, {"tool", "orchestrion", "go"}} {
		want, wantErr, wantCode := command(t, dir, env, "go", args...)
		got, gotErr, gotCode := command(t, dir, env, driver, append([]string{"go"}, args...)...)
		if got != want || gotErr != wantErr || gotCode != wantCode {
			t.Fatalf("ddto go %v: %q %q %d; go: %q %q %d", args, got, gotErr, gotCode, want, wantErr, wantCode)
		}
	}

	// Orchestrion's commands other than go test are Orchestrion's.
	bin := t.TempDir()
	copyExecutable(t, program, filepath.Join(bin, executableName("orchestrion")))
	pathEnv := append(env[:len(env):len(env)], "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, _, code := command(t, dir, pathEnv, driver, "orchestrion", "go", "build", "./..."); code != 0 || !strings.HasPrefix(out, `args=["go" "build" "./..."]`) {
		t.Fatalf("ddto orchestrion go build: %d %q", code, out)
	}

	// With DD_TRACE_DEBUG, ddto logs what it passed through, and how.
	debugEnv := append(env[:len(env):len(env)], "DD_TRACE_DEBUG=true")
	debugOut, debugErr, debugCode := command(t, dir, debugEnv, driver, program, "0", "secret")
	want := []string{fmt.Sprintf("pass-through command=%q args=2", program), fmt.Sprintf("pass-through exec path=%q", program)}
	if runtime.GOOS == "windows" {
		want = []string{fmt.Sprintf("pass-through command=%q args=2", program), "pass-through caller_command_line=true", "job=true breakaway=true", "pass-through exited code=0"}
	}
	for _, line := range want {
		if debugCode != 0 || !strings.HasPrefix(debugOut, "args=") || !strings.Contains(debugErr, line) {
			t.Fatalf("with DD_TRACE_DEBUG: %d %q %q; want %q", debugCode, debugOut, debugErr, line)
		}
	}
	if strings.Contains(debugErr, "secret") {
		t.Fatalf("the debug log shows an argument: %q", debugErr)
	}
	if runtime.GOOS != "windows" {
		if _, stderr, code := command(t, dir, debugEnv, driver, "ddto-no-such-command"); code != 127 || !strings.Contains(stderr, "pass-through search path_entries=") || !strings.Contains(stderr, "/ddto-no-such-command\" failed error=") {
			t.Fatalf("the debug log of a failed search: %d %q", code, stderr)
		}
		// A PATH entry too long for the libc is skipped, and logged.
		longEnv := append(debugEnv[:len(debugEnv):len(debugEnv)], "PATH=/"+strings.Repeat("a", 4096)+":"+filepath.Dir(program))
		if _, stderr, code := command(t, dir, longEnv, driver, filepath.Base(program), "0"); code != 0 || !strings.Contains(stderr, "pass-through skip path_entry=0 bytes=4097") {
			t.Fatalf("the debug log of a skipped entry: %d %q", code, stderr)
		}
		// A debug line to a stderr that nobody reads does not end ddto
		// before the command: Go ends a process for that only on fd 2.
		for _, name := range []string{"true", driver} {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			r.Close()
			cmd := exec.Command(name)
			if name == driver {
				cmd.Args = append(cmd.Args, "true")
			}
			cmd.Dir, cmd.Env, cmd.Stderr = dir, debugEnv, w
			err = cmd.Run()
			w.Close()
			if err != nil {
				t.Fatalf("%s with a closed stderr: %v", cmd.Args, err)
			}
		}
	}

	for _, missing := range []string{"ddto-no-such-command", filepath.Join(t.TempDir(), "ddto-no-such-command")} {
		if _, stderr, code := command(t, dir, env, driver, missing); code != 127 || !strings.Contains(stderr, "ddto-no-such-command") {
			t.Fatalf("a missing command %s: %d %q; want 127", missing, code, stderr)
		}
	}
	if runtime.GOOS == "windows" {
		// Batch files run through cmd.exe, as Windows starts them.
		script := filepath.Join(t.TempDir(), "script.cmd")
		if err := os.WriteFile(script, []byte("@echo batch %*\r\n@exit /b 5\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, stderr, code := command(t, dir, env, driver, script, "one"); code != 5 || strings.TrimSpace(out) != "batch one" {
			t.Fatalf("a batch file: %d %q %q; want its output and 5", code, out, stderr)
		}
	} else {
		notExecutable := filepath.Join(t.TempDir(), "notes")
		if err := os.WriteFile(notExecutable, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := command(t, dir, env, driver, notExecutable); code != 126 {
			t.Fatalf("a file that cannot run: %d %q; want 126", code, stderr)
		}
		// An executable file without a #! line runs with /bin/sh.
		script := filepath.Join(t.TempDir(), "script")
		if err := os.WriteFile(script, []byte("echo \"script $1\"\nexit 7\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if out, stderr, code := command(t, dir, env, driver, script, "one"); code != 7 || out != "script one\n" {
			t.Fatalf("a script without #!: %d %q %q; want its output and 7", code, out, stderr)
		}
		if _, stderr, code := command(t, dir, append(env[:len(env):len(env)], "DD_TRACE_DEBUG=true"), driver, script, "one"); code != 7 || !strings.Contains(stderr, `shell=/bin/sh reason="exec format error"`) {
			t.Fatalf("the debug log of a script without #!: %d %q", code, stderr)
		}
		lookupThroughPath(t, env, driver, program, filepath.Dir(notExecutable))
	}
	if _, stderr, code := command(t, dir, env, driver); code != 2 || !strings.Contains(stderr, "usage: ddto") {
		t.Fatalf("no command: %d %q; want the usage and 2", code, stderr)
	}
}

// ddto go test is ddto test: both report the same tests, also with go's -C
// before test.
func TestCLIGoTestIsTest(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	// The debug log names the form even when ddto's own options are invalid.
	if _, stderr, code := command(t, dir, testEnv("DD_TRACE_DEBUG=true"), driver, "go", "-C", dir, "test", "--runtime=broken"); code != 2 || !strings.Contains(stderr, `command="go -C test"`) {
		t.Fatalf("an invalid option: %d %q", code, stderr)
	}
	elsewhere := t.TempDir()
	for _, form := range [][]string{{"test"}, {"go", "test"}, {"go", "-C", dir, "test"}} {
		receiver := &capture{}
		server := newServer(http.HandlerFunc(receiver.handler))
		env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
		cwd := dir
		if len(form) > 2 {
			cwd = elsewhere
			// The debug log names the form.
			env = append(env, "DD_TRACE_DEBUG=true")
		}
		out, stderr, code := command(t, cwd, env, driver, append(form, "--runtime=mini", "-count=1", "-run=^TestPass$", ".")...)
		server.Close()
		if code != 0 || !strings.Contains(out, "ok") || len(form) > 2 && !strings.Contains(stderr, `command="go -C test"`) {
			t.Fatalf("ddto %v: %d\n%s\n%s", form, code, out, stderr)
		}
		receiver.mu.Lock()
		tests := 0
		for _, event := range receiver.events {
			if event["type"] == "test" {
				tests++
			}
		}
		failures := receiver.failures
		receiver.mu.Unlock()
		if len(failures) != 0 || tests != 1 {
			t.Fatalf("ddto %v reported %d tests, failures %v\n%s\n%s", form, tests, failures, out, stderr)
		}
	}
}

// copyExecutable copies the executable at source to target, creating its directory.
func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err == nil {
		err = os.WriteFile(target, data, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// lookupThroughPath checks that ddto finds commands through PATH as the
// platform's execvp does, against env, which uses it: its status and output
// must be env's, and where every libc agrees, the expected status. notes is a
// directory with a file named notes that cannot run.
func lookupThroughPath(t *testing.T, env []string, driver, program, notes string) {
	t.Helper()
	native, err := exec.LookPath("env")
	if err != nil {
		t.Fatal(err)
	}
	native, err = filepath.Abs(native)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	bin := filepath.Join(cwd, "bin")
	copyExecutable(t, program, filepath.Join(bin, "passthrough-path"))
	copyExecutable(t, program, filepath.Join(cwd, "passthrough-path"))
	// The script's $0 is the candidate that the libc started.
	for _, dir := range []string{cwd, bin} {
		if err := os.WriteFile(filepath.Join(dir, "passthrough-script"), []byte("#!/bin/sh\necho \"$0 $1\"\nexit 3\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	closed, empty, loop := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Chmod(closed, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o700) })
	// A link to itself fails with ELOOP, after which only BSD libcs search on.
	if err := os.Symlink("passthrough-path", filepath.Join(loop, "passthrough-path")); err != nil {
		t.Fatal(err)
	}
	long := "/" + strings.Repeat("a", 4096)
	// The kernel reports ENOENT for a missing directory before a long name.
	missing := filepath.Join(empty, "missing")
	const unset = "\x00"
	var noPath []string
	for _, v := range env {
		if !strings.HasPrefix(v, "PATH=") {
			noPath = append(noPath, v)
		}
	}
	// The BSD libcs search PATH for a name longer than NAME_MAX, as env shows.
	longWant, longUnlike := -1, false
	if runtime.GOOS == "linux" {
		longWant, longUnlike = 126, true
	}
	for _, tc := range []struct {
		path, name string
		want       int // -1 where libcs differ
		// unlike marks where env's status depends on the glibc release: a
		// stale errno after skipping every entry, and the NAME_MAX check,
		// which glibc 2.39 has but never meets, as it compares the length
		// that strnlen caps at NAME_MAX. ddto follows the current glibc.
		unlike bool
	}{
		{"bin", "passthrough-path", 3, false},                     // relative entry
		{"", "passthrough-path", 3, false},                        // empty: the current directory
		{"bin", "passthrough-script", 3, false},                   // the script's $0
		{"", "passthrough-script", 3, false},                      // glibc's file, BSD's ./file
		{unset, "true", 0, false},                                 // the libc's default path
		{bin, "", 127, false},                                     // an empty name
		{notes, "notes", 126, false},                              // a file that cannot run
		{closed + ":" + bin, "passthrough-path", 3, false},        // past a directory that cannot be searched
		{closed + ":" + empty, "passthrough-path", -1, false},     // reported by glibc only
		{long + ":" + bin, "passthrough-path", 3, false},          // past an entry longer than PATH_MAX
		{long, "passthrough-path", 127, true},                     // only such entries
		{loop + ":" + bin, "passthrough-path", -1, false},         // ELOOP ends glibc's search only
		{missing, strings.Repeat("a", 255), 127, false},           // a name of NAME_MAX
		{missing, strings.Repeat("a", 256), longWant, longUnlike}, // longer: current glibc does not search
	} {
		caseEnv := noPath
		if tc.path != unset {
			caseEnv = append(noPath[:len(noPath):len(noPath)], "PATH="+tc.path)
		}
		out, stderr, code := command(t, cwd, caseEnv, driver, tc.name, "3")
		wantOut, wantErr, wantCode := command(t, cwd, caseEnv, native, tc.name, "3")
		if tc.unlike {
			wantOut, wantCode = out, code
		}
		// Where the command ran, its stderr holds only its own output and the
		// libc's, which env and ddto print alike; their own errors differ.
		if code == 126 || code == 127 || tc.unlike {
			wantErr = stderr
		}
		if out != wantOut || stderr != wantErr || code != wantCode || tc.want >= 0 && code != tc.want {
			t.Errorf("%q with PATH %.40q: %d %q %q; env: %d %q %q; want %d", tc.name, tc.path, code, out, stderr, wantCode, wantOut, wantErr, tc.want)
		}
	}
}

// buildPassthroughProgram builds passthroughProgram and returns its path.
func buildPassthroughProgram(t *testing.T) string {
	t.Helper()
	source := t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example.com/passthrough\n\ngo 1.25\n", "main.go": passthroughProgram} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	program := filepath.Join(t.TempDir(), executableName("passthrough"))
	if out, stderr, code := command(t, source, testEnv(), "go", "build", "-o", program, "."); code != 0 {
		t.Fatalf("build: %s %s", out, stderr)
	}
	return program
}
