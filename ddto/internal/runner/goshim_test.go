package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// gotestsum's go is ddto: run as go, ddto runs as ddto go. Every ddto leaves
// the go shims off PATH, even when gotestsum's command filtered the
// environment, and keeps the other entries as written.
func TestGoShimArgs(t *testing.T) {
	cache := t.TempDir()
	defer func(previous func() (string, error)) { userCacheDir = previous }(userCacheDir)
	userCacheDir = func() (string, error) { return cache, nil }
	sep := string(os.PathListSeparator)
	shim := filepath.Join(cache, "ddto", "go-shim", "0123456789abcdef")
	other := "/usr/local/go/bin"
	if runtime.GOOS == "windows" {
		// A quoted entry may hold the separator.
		other = `"C:\Go;1.27\bin"`
	}
	for _, tc := range []struct {
		name, path string
		args       []string
		want       []string
		fromShim   bool
	}{
		{name: "ddto", path: other, args: []string{"/bin/ddto", "test", "./..."}, want: []string{"test", "./..."}},
		{name: "go shim", path: shim + sep + other + sep + shim, args: []string{"go", "test", "-json", "./..."}, want: []string{"go", "test", "-json", "./..."}, fromShim: true},
		{name: "ddto under gotestsum", path: shim + sep + other, args: []string{"/bin/ddto", "test", "-json"}, want: []string{"test", "-json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			goShimLeft = ""
			got, fromShim := GoShimArgs(tc.args)
			if !slices.Equal(got, tc.want) || fromShim != tc.fromShim {
				t.Fatalf("GoShimArgs(%q) = %q, %t; want %q, %t", tc.args, got, fromShim, tc.want, tc.fromShim)
			}
			if path := os.Getenv("PATH"); path != other {
				t.Fatalf("PATH=%q; want %q", path, other)
			}
			if strings.Contains(tc.path, shim) != (goShimLeft == shim) {
				t.Fatalf("left %q", goShimLeft)
			}
		})
	}
	// A go shim in another user cache, as in an environment that a command
	// filtered, is left too, and look-alike entries are kept.
	elsewhere := filepath.Join(t.TempDir(), "ddto", "go-shim", "fedcba9876543210")
	keep := []string{filepath.Join(t.TempDir(), "ddto", "go-shim", "not-a-shim"), filepath.Join(t.TempDir(), "go-shim", "0123456789abcdef"), "ddto/go-shim/0123456789abcdef"}
	t.Setenv("PATH", strings.Join(append([]string{elsewhere}, keep...), sep))
	if _, fromShim := GoShimArgs([]string{"go", "version"}); !fromShim || os.Getenv("PATH") != strings.Join(keep, sep) || goShimLeft != elsewhere {
		t.Fatalf("PATH=%q left %q", os.Getenv("PATH"), goShimLeft)
	}

	if !IsGotestsum([]string{"/home/user/go/bin/gotestsum", "--"}) || IsGotestsum([]string{"gotestsum-wrapper"}) || IsGotestsum(nil) {
		t.Fatal("IsGotestsum")
	}
}

// The shim is one go per ddto build in ddto's user cache, which ddto runs.
func TestUseGoShim(t *testing.T) {
	cache := t.TempDir()
	defer func(previous func() (string, error)) { userCacheDir = previous }(userCacheDir)
	userCacheDir = func() (string, error) { return cache, nil }
	defer func() { goShimFirst = "" }()
	t.Setenv("PATH", "/usr/bin")
	if command, tool, err := goShimSetup([]string{"gotestsum", "--"}); err != nil || tool != nil || !slices.Equal(command, []string{"gotestsum", "--"}) {
		t.Fatal(command, tool, err)
	}
	dir := goShimFirst
	if !strings.HasPrefix(dir, filepath.Join(cache, "ddto", "go-shim")) || os.Getenv("PATH") != dir+string(os.PathListSeparator)+"/usr/bin" {
		t.Fatalf("shim %q PATH=%q", dir, os.Getenv("PATH"))
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name = "go.exe"
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if executable, err := os.Stat(self); err != nil || shim.Size() != executable.Size() {
		t.Fatalf("the shim is not this executable: %v", err)
	}
	// A second command reuses it.
	if again, err := goShimDir(self); err != nil || again != dir {
		t.Fatalf("goShimDir again: %q %v", again, err)
	}
}

// A shim directory that holds the PATH separator would split on PATH, so
// ddto runs gotestsum without it, after a warning.
func TestUseGoShimRefusesSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows quotes the entry")
	}
	cache := filepath.Join(t.TempDir(), "cache"+string(os.PathListSeparator)+"ci")
	defer func(previous func() (string, error)) { userCacheDir = previous }(userCacheDir)
	userCacheDir = func() (string, error) { return cache, nil }
	t.Setenv("PATH", "/usr/bin")
	if _, _, err := goShimSetup([]string{"make", "test"}); err == nil || os.Getenv("PATH") != "/usr/bin" {
		t.Fatalf("goShimSetup: %v, PATH=%q", err, os.Getenv("PATH"))
	}
}

// --go-shim runs any command with the go shim; gotestsum gets it in any form,
// and so do go run and go tool that reach ddto through the shim.
func TestGoShimCommand(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
		fromShim   bool
		// reason is why the command runs with the shim, as the go_shim
		// reason= debug line says, or "" without it.
		reason string
	}{
		{args: []string{"--go-shim", "make", "test"}, want: []string{"make", "test"}, reason: "flag"},
		{args: []string{"--go-shim"}, want: []string{}, reason: "flag"},
		{args: []string{"gotestsum", "--", "./..."}, reason: "gotestsum"},
		{args: []string{"go", "tool", "gotestsum", "--format", "testname"}, reason: "gotestsum"},
		{args: []string{"go", "tool", "gotest.tools/gotestsum"}, reason: "gotestsum"},
		{args: []string{"go", "-C", "project", "tool", "gotestsum"}, reason: "gotestsum"},
		{args: []string{"go", "run", "gotest.tools/gotestsum@v1.13.0", "--", "./..."}, reason: "gotestsum"},
		{args: []string{"go", "run", "-mod=mod", "gotest.tools/gotestsum"}, reason: "gotestsum"},
		{args: []string{"go", "run", "-tags", "smoke", "gotest.tools/gotestsum@v1.13.0"}, reason: "gotestsum"},
		{args: []string{"go", "run", "-C", "tools", "gotest.tools/gotestsum"}, reason: "gotestsum"},
		{args: []string{"go", "run", "./cmd/gotestsum"}},
		{args: []string{"go", "tool", "cover"}},
		{args: []string{"go", "build", "gotest.tools/gotestsum"}},
		{args: []string{"make", "test"}},
		{args: []string{"make", "--go-shim"}},
		// Run as the go shim, a launcher keeps the shim for its program.
		{args: []string{"go", "run", "./tools/testrunner"}, fromShim: true, reason: "launcher"},
		{args: []string{"go", "tool", "mytool"}, fromShim: true, reason: "launcher"},
		{args: []string{"go", "build", "./..."}, fromShim: true},
	} {
		want := tc.want
		if want == nil {
			want = tc.args
		}
		if got, reason := GoShimCommand(tc.args, tc.fromShim); !slices.Equal(got, want) || reason != tc.reason {
			t.Errorf("GoShimCommand(%q, %t) = %q, %q; want %q, %q", tc.args, tc.fromShim, got, reason, want, tc.reason)
		}
	}
}

// A go command with the go shim runs the go that PATH names before the shim,
// which then stays first on PATH for the commands that go starts.
func TestUseGoShimRunsTheToolchain(t *testing.T) {
	cache := t.TempDir()
	defer func(previous func() (string, error)) { userCacheDir = previous }(userCacheDir)
	userCacheDir = func() (string, error) { return cache, nil }
	defer func() { goShimFirst, goShimGo = "", "" }()
	toolchain, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	command, _, err := goShimSetup([]string{"go", "version"})
	if err != nil || !slices.Equal(command, []string{toolchain, "version"}) {
		t.Fatalf("goShimSetup = %q, %v; want %s first", command, err, toolchain)
	}
	if found, err := exec.LookPath("go"); err != nil || filepath.Dir(found) != goShimFirst {
		t.Fatalf("go on PATH is %q, %v; want the shim in %s", found, err, goShimFirst)
	}
}

// go run and go tool put $GOROOT/bin before the shim on their program's
// PATH: go run runs its program through ddto, and ddto runs a module tool's
// program itself. What ddto's -exec would replace keeps go's own command.
func TestGoShimGoCommand(t *testing.T) {
	defer func(previous func(string, []string) (goShimEnv, error)) { readGoShimEnv = previous }(readGoShimEnv)
	env := goShimEnv{GOROOT: "/go", GOOS: "linux", GOARCH: "amd64", GOHOSTOS: "linux", GOHOSTARCH: "amd64"}
	readGoShimEnv = func(string, []string) (goShimEnv, error) { return env, nil }
	self := filepath.Join(t.TempDir(), "dd to")
	exec := "-exec='" + self + "' 'go-shim-exec'"
	defer func() { goShimGoMode = "" }()
	for _, tc := range []struct {
		command, want []string
	}{
		{[]string{"go", "run", "-mod=mod", "gotest.tools/gotestsum@v1.13.0", "--", "./..."}, []string{"/go/bin/go", "run", exec, "-mod=mod", "gotest.tools/gotestsum@v1.13.0", "--", "./..."}},
		// -C stays first.
		{[]string{"go", "run", "-C", "tools", "./runner"}, []string{"/go/bin/go", "run", "-C", "tools", exec, "./runner"}},
		{[]string{"go", "run", "-C=tools", "./runner"}, []string{"/go/bin/go", "run", "-C=tools", exec, "./runner"}},
		{[]string{"go", "-C", "project", "run", "./runner"}, []string{"/go/bin/go", "-C", "project", "run", exec, "./runner"}},
		// The program's own -exec argument.
		{[]string{"go", "run", "./cmd", "-exec"}, []string{"/go/bin/go", "run", exec, "./cmd", "-exec"}},
		{[]string{"go", "generate", "./..."}, []string{"/go/bin/go", "generate", "./..."}},
	} {
		// The go_shim go_command= debug line's.
		mode := "run_exec"
		if !slices.Contains(tc.command, "run") {
			mode = "native"
		}
		if got, tool, err := goShimGoCommand(self, "/go/bin/go", tc.command); err != nil || tool != nil || !slices.Equal(got, tc.want) || goShimGoMode != mode {
			t.Errorf("%q: %q, %v, mode %q; want %q, mode %q", tc.command, got, err, goShimGoMode, tc.want, mode)
		}
	}
	for name, command := range map[string][]string{
		"the caller's -exec": {"go", "run", "-exec=wasmtime", "./cmd"},
		"-exec after a flag": {"go", "run", "-tags", "x", "-exec", "wasmtime", "./cmd"},
		"go tool flags":      {"go", "tool", "-n", "gotestsum"},
	} {
		if _, _, err := goShimGoCommand(self, "/go/bin/go", command); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// GOFLAGS as go env reports it, with go env -w settings.
	env.GOFLAGS = "-exec=wasmtime"
	if _, _, err := goShimGoCommand(self, "/go/bin/go", []string{"go", "run", "./cmd"}); err == nil {
		t.Error("-exec in GOFLAGS: no error")
	}
	env.GOFLAGS, env.GOARCH = "", "arm64"
	if _, _, err := goShimGoCommand(self, "/go/bin/go", []string{"go", "run", "./cmd"}); err == nil {
		t.Error("cross-compilation: no error")
	}

	// The fake go prints its arguments for go tool -n, which are not one
	// program; one that fails leaves the error to go tool itself.
	t.Setenv(fakeToolEnv, "1")
	if _, _, err := goShimGoCommand(self, os.Args[0], []string{"go", "tool", "gotestsum"}); err == nil {
		t.Error("go tool -n printed more than one program")
	}
	t.Setenv(fakeToolExitEnv, "1")
	if native, tool, err := goShimGoCommand(self, os.Args[0], []string{"go", "tool", "nope"}); err != nil || tool != nil || !slices.Equal(native, []string{os.Args[0], "tool", "nope"}) || goShimGoMode != "tool_not_built" {
		t.Errorf("an unknown tool: %q, %v, mode %q", native, err, goShimGoMode)
	}
}

// ddto runs a module tool as go tool does: with the tool's exit status, and
// go tool's report of a tool that did not run or exit cleanly.
func TestModuleToolRun(t *testing.T) {
	t.Setenv(fakeToolEnv, "1")
	t.Setenv(fakeToolExitEnv, "3")
	tool := &moduleTool{name: "gotestsum", path: os.Args[0], args: []string{"--format", "testname"}, diagnostics: []byte("built\n")}
	if code := tool.run(); code != 3 {
		t.Fatalf("exit %d; want the tool's 3", code)
	}
	missing := &moduleTool{name: "gotestsum", path: filepath.Join(t.TempDir(), "missing")}
	if code := missing.run(); code != 1 {
		t.Fatalf("a tool that did not start: exit %d; want 1", code)
	}
	if !slices.Contains(goToolSignals, os.Signal(syscall.SIGTERM)) || !slices.Contains(goToolSignals, os.Signal(syscall.SIGHUP)) {
		t.Fatalf("go tool forwards SIGTERM and SIGHUP: %v", goToolSignals)
	}

	// A toolchain switch sets GOROOT for the tool to a downloaded toolchain,
	// and removes it for one on PATH, as go's execGoToolchain does.
	t.Setenv(fakeToolEnv, "")
	t.Setenv("DDTO_RUN_GO_HELPER", "1")
	for _, tc := range []struct {
		tool *moduleTool
		want string
	}{
		{&moduleTool{switchGOROOT: "/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64"}, "GOROOT=true:/mod/golang.org/toolchain@v0.0.1-go1.27.0.linux-amd64\n"},
		{&moduleTool{unsetGOROOT: true}, "GOROOT=false:\n"},
		{&moduleTool{}, "GOROOT=true:/opt/go1.25\n"},
	} {
		t.Setenv("GOROOT", "/opt/go1.25")
		tc.tool.name, tc.tool.path, tc.tool.args = "runner", os.Args[0], []string{"-test.run=^TestRunGoHelperProcess$"}
		if got := runToolReport(t, tc.tool); !strings.HasPrefix(got, tc.want) {
			t.Errorf("tool saw %q; want %q", got, tc.want)
		}
	}
}

// runToolReport runs tool and returns what it printed.
func runToolReport(t *testing.T, tool *moduleTool) string {
	t.Helper()
	report, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	stdout := os.Stdout
	os.Stdout = report
	code := tool.run()
	os.Stdout = stdout
	data, err := os.ReadFile(report.Name())
	if err != nil || code != 0 {
		t.Fatalf("exit %d, %v", code, err)
	}
	return string(data)
}

// go env reports an exported GOROOT, or the go command's own root, unless
// go switched toolchains.
func TestSwitchedToolchain(t *testing.T) {
	root := t.TempDir()
	toolchain := filepath.Join(root, "bin", "go")
	t.Setenv("GOROOT", root)
	if switchedToolchain(toolchain, root) || !switchedToolchain(toolchain, filepath.Join(t.TempDir(), "other")) {
		t.Fatal("with GOROOT exported")
	}
	if err := os.MkdirAll(filepath.Dir(toolchain), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toolchain, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("GOROOT")
	if switchedToolchain(toolchain, root) || !switchedToolchain(toolchain, t.TempDir()) {
		t.Fatal("without GOROOT")
	}
}

// PATH gets the shim first, then a module tool's toolchain bin.
func TestPathWith(t *testing.T) {
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", "/usr/bin")
	if got, err := pathWith([]string{"/shim", "/go/bin"}); err != nil || got != "/shim"+sep+"/go/bin"+sep+"/usr/bin" {
		t.Fatalf("pathWith = %q, %v", got, err)
	}
}

// The toolchain's tools are in its pkg/tool directory; module tools are not.
func TestIsToolchainTool(t *testing.T) {
	for path, want := range map[string]bool{
		filepath.FromSlash("/usr/lib/go/pkg/tool/linux_amd64/cover"):         true,
		filepath.FromSlash("/home/user/.cache/go-build/41/4127-d/gotestsum"): false,
		filepath.FromSlash("/home/user/go/bin/gotestsum"):                    false,
	} {
		if got := isToolchainTool(path); got != want {
			t.Errorf("isToolchainTool(%q) = %t", path, got)
		}
	}
}

// go run's -exec program runs with the go shim first on PATH again.
func TestExecWithGoShim(t *testing.T) {
	cache := t.TempDir()
	defer func(previous func() (string, error)) { userCacheDir = previous }(userCacheDir)
	userCacheDir = func() (string, error) { return cache, nil }
	defer func() { goShimFirst = "" }()
	t.Setenv(fakeToolEnv, "1")
	t.Setenv(fakeToolExitEnv, "3")
	t.Setenv("PATH", "/usr/lib/go/bin")
	if runtime.GOOS != "windows" {
		t.Skip("ExecWithGoShim replaces this process on Unix")
	}
	if code := ExecWithGoShim([]string{os.Args[0], "arg"}); code != 3 || !strings.HasPrefix(os.Getenv("PATH"), goShimFirst) {
		t.Fatalf("exit %d, PATH=%q", code, os.Getenv("PATH"))
	}
}
