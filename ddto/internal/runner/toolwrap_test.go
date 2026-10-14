package runner

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/version"
)

const (
	// fakeToolEnv makes this test binary print its arguments and exit with
	// fakeToolExitEnv's status: TestMain handles it.
	fakeToolEnv     = "DDTO_TOOLWRAP_FAKE_TOOL"
	fakeToolExitEnv = "DDTO_TOOLWRAP_FAKE_TOOL_EXIT"
	// fakeToolPIDEnv also makes it print its process ID first.
	fakeToolPIDEnv = "DDTO_TOOLWRAP_FAKE_TOOL_PID"
	// fakeToolOrphanEnv makes it start a child that keeps its output, and
	// exit; fakeToolSleepEnv makes it sleep first.
	fakeToolOrphanEnv = "DDTO_TOOLWRAP_FAKE_TOOL_ORPHAN"
	fakeToolSleepEnv  = "DDTO_TOOLWRAP_FAKE_TOOL_SLEEP"
	// fakeToolOrphansEnv names the file that lists those children.
	fakeToolOrphansEnv = "DDTO_TOOLWRAP_FAKE_TOOL_ORPHANS"
)

func testGoEnvironment(t *testing.T) *goEnvironment {
	t.Helper()
	env, err := readGoEnvironment(t.Context(), ".")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func toolWrapperKey() string {
	_, key := toolwrap.Sources(version.BuildLogPrefix)
	return key
}

func debugTo(t *testing.T, log *bytes.Buffer) *cliDebug {
	t.Helper()
	t.Setenv("DD_TRACE_DEBUG", "true")
	return debugFromContext(withCLIDebug(t.Context(), log))
}

// ensureTestToolWrapper returns a wrapper executable from the test binary's
// shared user cache.
func ensureTestToolWrapper(t *testing.T) string {
	t.Helper()
	path, lock, err := ensureToolWrapper(t.Context(), testGoEnvironment(t), ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	return path
}

// ddto builds the wrapper executable once in its user cache, keyed on the
// wrapper's sources and the host, and reuses it: the same file, which prints
// its key.
func TestToolWrapperCreatesAndReuses(t *testing.T) {
	cache := useTestUserCache(t)
	env := testGoEnvironment(t)
	var log bytes.Buffer
	debug := debugTo(t, &log)
	path, lock, err := ensureToolWrapper(t.Context(), env, ".", debug)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	key := toolWrapperKey()
	want := filepath.Join(cache, "ddto", "toolwraps", key[:16]+"-"+env.GOHOSTOS+"_"+env.GOHOSTARCH, toolWrapperName(env.GOHOSTOS))
	if path != want || !strings.Contains(log.String(), "tool wrapper state=created") {
		t.Fatalf("path %s, want %s:\n%s", path, want, log.String())
	}
	out, err := exec.Command(path, toolwrap.KeyFlag).Output()
	if err != nil || strings.TrimSpace(string(out)) != key {
		t.Fatalf("key %q, %v; want %q", out, err, key)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Reusing it refreshes its time, which keeps pruners away.
	stale := time.Now().Add(-gorootMirrorRetention - time.Hour)
	if err := os.Chtimes(filepath.Dir(path), stale, stale); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	again, lock, err := ensureToolWrapper(t.Context(), env, ".", debug)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	after, err := os.Stat(again)
	if err != nil {
		t.Fatal(err)
	}
	if again != path || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || !strings.Contains(log.String(), "tool wrapper state=reused") {
		t.Fatalf("not reused: %s:\n%s", again, log.String())
	}
	if root, err := os.Stat(filepath.Dir(path)); err != nil || !root.ModTime().After(stale.Add(time.Hour)) {
		t.Fatalf("reuse did not refresh the time: %v", err)
	}
}

// A cached executable that does not run, or is not this wrapper, is rebuilt.
func TestToolWrapperRebuildsUnusableExecutable(t *testing.T) {
	useTestUserCache(t)
	env := testGoEnvironment(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	// This test binary as the fake tool prints the key flag, not the key.
	t.Setenv(fakeToolEnv, "1")
	for name, content := range map[string][]byte{"not executable": []byte("not an executable"), "another executable": other} {
		path := ensureTestToolWrapper(t)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0700); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		again, lock, err := ensureToolWrapper(t.Context(), env, ".", debugTo(t, &log))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		lock.Close()
		if again != path || !strings.Contains(log.String(), "tool wrapper state=created") {
			t.Fatalf("%s: not rebuilt:\n%s", name, log.String())
		}
		if !validToolWrapper(t.Context(), path, toolWrapperKey()) {
			t.Fatalf("%s: rebuilt wrapper is not valid", name)
		}
	}
}

// A build that fails publishes nothing and leaves no staging directory.
func TestToolWrapperBuildFailure(t *testing.T) {
	cache := useTestUserCache(t)
	ctx := context.WithValue(t.Context(), goToolchainKey{}, goToolchain{path: filepath.Join(t.TempDir(), "missing-go")})
	if _, _, err := ensureToolWrapper(ctx, testGoEnvironment(t), ".", nil); err == nil || !strings.Contains(err.Error(), "building the tool wrapper") {
		t.Fatal("a build without go:", err)
	}
	entries, err := os.ReadDir(filepath.Join(cache, "ddto", "toolwraps"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lock") {
			t.Errorf("left %s", entry.Name())
		}
	}
}

// An executable that the host cannot run is not published.
func TestToolWrapperUnrunnableBuild(t *testing.T) {
	cache := useTestUserCache(t)
	toolWrapperBuildEnv = []string{"GOOS=plan9", "GOARCH=amd64"}
	defer func() { toolWrapperBuildEnv = nil }()
	if _, _, err := ensureToolWrapper(t.Context(), testGoEnvironment(t), ".", nil); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatal("an executable for another platform:", err)
	}
	entries, err := os.ReadDir(filepath.Join(cache, "ddto", "toolwraps"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".lock") {
			t.Errorf("left %s", entry.Name())
		}
	}
}

// The wrapper is built for the host, whatever the build targets, and ignores
// the caller's flags, workspace, toolchain selection and module mode.
func TestToolWrapperBuildsForTheHost(t *testing.T) {
	useTestUserCache(t)
	env := testGoEnvironment(t)
	goos, goarch := "windows", "arm64"
	if env.GOHOSTOS == "windows" {
		goos = "linux"
	}
	if env.GOHOSTARCH == "arm64" {
		goarch = "amd64"
	}
	t.Setenv("GOOS", goos)
	t.Setenv("GOARCH", goarch)
	t.Setenv("GOFLAGS", "-mod=invalid")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "missing.work"))
	t.Setenv("GOTOOLCHAIN", "go1.999.0")
	t.Setenv("GO111MODULE", "off")
	path, lock, err := ensureToolWrapper(t.Context(), env, ".", nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if !validToolWrapper(t.Context(), path, toolWrapperKey()) {
		t.Fatal("wrapper does not run on the host")
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range info.Settings {
		if setting.Key == "GOOS" && setting.Value != env.GOHOSTOS || setting.Key == "GOARCH" && setting.Value != env.GOHOSTARCH {
			t.Fatalf("built for %s=%s", setting.Key, setting.Value)
		}
	}
}

// A cache within the module is refused, as for toolchain mirrors.
func TestToolWrapperRejectsProtectedCache(t *testing.T) {
	env := *testGoEnvironment(t)
	module := t.TempDir()
	env.GOMOD = filepath.Join(module, "go.mod")
	previous := userCacheDir
	userCacheDir = func() (string, error) { return filepath.Join(module, ".cache"), nil }
	defer func() { userCacheDir = previous }()
	if _, _, err := ensureToolWrapper(t.Context(), &env, t.TempDir(), nil); err == nil {
		t.Fatal("a cache within the module was accepted")
	}
	if _, err := os.Stat(filepath.Join(module, ".cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the module changed: %v", err)
	}
}

// Concurrent runs build or reuse one executable.
func TestToolWrapperConcurrentCreators(t *testing.T) {
	useTestUserCache(t)
	env := testGoEnvironment(t)
	paths := make([]string, 4)
	errs := make([]error, 4)
	var wg sync.WaitGroup
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lock *os.File
			if paths[i], lock, errs[i] = ensureToolWrapper(t.Context(), env, ".", nil); errs[i] == nil {
				lock.Close()
			}
		}()
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("run %d: %s, %v", i, paths[i], errs[i])
		}
	}
	if !validToolWrapper(t.Context(), paths[0], toolWrapperKey()) {
		t.Fatal("wrapper is not valid")
	}
}

// Creating a wrapper prunes others unused for the retention period, and
// abandoned staging directories, but not one that a run holds.
func TestToolWrapperPruning(t *testing.T) {
	cache := useTestUserCache(t)
	parent := filepath.Join(cache, "ddto", "toolwraps")
	stale := time.Now().Add(-gorootMirrorRetention - time.Hour)
	for _, name := range []string{"old", "held", "old.tmp-1"} {
		if err := os.MkdirAll(filepath.Join(parent, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(parent, name), stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	held, err := holdGOROOTMirrorLock(t.Context(), filepath.Join(parent, "held.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	path := ensureTestToolWrapper(t)
	for name, want := range map[string]bool{"old": false, "old.tmp-1": false, "held": true, filepath.Base(filepath.Dir(path)): true} {
		if _, err := os.Stat(filepath.Join(parent, name)); (err == nil) != want {
			t.Errorf("%s exists=%t, want %t", name, err == nil, want)
		}
	}
}

// runToolWrapper runs the wrapper with args and the environment additions,
// and returns its output lines and exit status.
func runToolWrapper(t *testing.T, path string, args []string, env ...string) ([]string, string, int) {
	t.Helper()
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(stdout.String()), stderr.String(), code
}

// The wrapper runs tools that need no plan itself, after the user's
// -toolexec, with their status, and runs ddto's tool-overlay for the others;
// it notes test links for the Git worker, as ddto does.
func TestToolWrapperRuns(t *testing.T) {
	wrapper := ensureTestToolWrapper(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fake := []string{fakeToolEnv + "=1", toolwrap.UserToolexecEnv + "=", toolwrap.OrchestrionBypassEnv + "=", "TOOLEXEC_IMPORTPATH=", toolwrap.GitStateHandoff + "="}
	t.Run("tool", func(t *testing.T) {
		lines, stderr, code := runToolWrapper(t, wrapper, []string{"/ddto", "testify", "/plan.json", self, "a", "b"},
			append(fake, fakeToolExitEnv+"=7", toolwrap.UserToolexecEnv+"='"+self+"' chained")...)
		if want := []string{"chained", self, "a", "b"}; code != 7 || strings.Join(lines, " ") != strings.Join(want, " ") {
			t.Fatalf("exit %d, output %q, want %q:\n%s", code, lines, want, stderr)
		}
	})
	t.Run("plan", func(t *testing.T) {
		compile := filepath.Join(t.TempDir(), "compile")
		lines, stderr, code := runToolWrapper(t, wrapper, []string{self, "testify", "/plan.json", compile, "-o", "out.a"},
			append(fake, fakeToolExitEnv+"=3", "TOOLEXEC_IMPORTPATH="+toolwrap.TestifySuiteImport, toolwrap.UserToolexecEnv+"=chained")...)
		if want := []string{"tool-overlay", "testify", "/plan.json", compile, "-o", "out.a"}; code != 3 || strings.Join(lines, " ") != strings.Join(want, " ") {
			t.Fatalf("exit %d, output %q, want %q:\n%s", code, lines, want, stderr)
		}
	})
	t.Run("link", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "link")
		if goruntime.GOOS == "windows" {
			link += ".exe"
		}
		data, err := os.ReadFile(self)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(link, data, 0700); err != nil {
			t.Fatal(err)
		}
		state := t.TempDir()
		lines, stderr, code := runToolWrapper(t, wrapper, []string{"/ddto", "testify-link", "/plan.json", link, "-o", "pkg.test"},
			append(fake, toolwrap.GitStateHandoff+"="+state)...)
		if want := []string{"-o", "pkg.test"}; code != 0 || strings.Join(lines, " ") != strings.Join(want, " ") {
			t.Fatalf("exit %d, output %q, want %q:\n%s", code, lines, want, stderr)
		}
		if _, err := os.Stat(filepath.Join(state, toolwrap.LinkingFile)); err != nil {
			t.Fatal("link not noted:", err)
		}
	})
	// On Unix, the wrapper replaces itself with the tool or with ddto.
	t.Run("same process", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("Windows has no exec replacement")
		}
		compile := filepath.Join(t.TempDir(), "compile")
		for name, args := range map[string][]string{
			"tool": {"/ddto", "testify", "/plan.json", self},
			"ddto": {self, "testify", "/plan.json", compile},
		} {
			cmd := exec.Command(wrapper, args...)
			cmd.Env = append(append(os.Environ(), fake...), fakeToolPIDEnv+"=1", "TOOLEXEC_IMPORTPATH="+toolwrap.TestifySuiteImport)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if first, _, _ := strings.Cut(string(out), "\n"); first != "pid="+strconv.Itoa(cmd.Process.Pid) {
				t.Fatalf("%s: tool %s, wrapper pid=%d", name, first, cmd.Process.Pid)
			}
		}
	})
	for name, args := range map[string][]string{
		"invalid tool mode":              {"/ddto", "unknown", "/plan.json", self},
		"invalid tool wrapper arguments": {"/ddto", "testify", "/plan.json"},
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := runToolWrapper(t, wrapper, args, fake...)
			if code != 2 || !strings.Contains(stderr, version.BuildLogPrefix+" ERROR: "+name) {
				t.Fatalf("exit %d:\n%s", code, stderr)
			}
		})
	}
}

// Releasing a plan lets pruners remove its wrapper executable.
func TestPlanReleaseClosesToolWrapperLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wrapper.lock")
	lock, err := holdGOROOTMirrorLock(t.Context(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := tryLockFile(other, true); errors.Is(err, errors.ErrUnsupported) {
		t.Skip("no file locks")
	} else if err == nil {
		t.Fatal("exclusive lock granted while the plan holds the wrapper")
	}
	Plan{toolWrapperLock: lock}.Release()
	if err := tryLockFile(other, true); err != nil {
		t.Fatal("lock still held after Release:", err)
	}
}

// A process that the key check or the build leaves holding their output
// delays them by at most toolWrapperWaitDelay.
func TestToolWrapperDoesNotWaitForLeftoverProcesses(t *testing.T) {
	useTestUserCache(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeToolEnv, "1")
	t.Setenv(fakeToolOrphanEnv, "1")
	orphans := filepath.Join(t.TempDir(), "orphans")
	t.Setenv(fakeToolOrphansEnv, orphans)
	t.Cleanup(func() {
		data, _ := os.ReadFile(orphans)
		for _, field := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				continue
			}
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
				_, _ = process.Wait()
			}
		}
	})
	started := time.Now()
	if validToolWrapper(t.Context(), self, toolWrapperKey()) {
		t.Fatal("a leftover's output passed the check")
	}
	ctx := context.WithValue(t.Context(), goToolchainKey{}, goToolchain{path: self})
	if _, _, err := ensureToolWrapper(ctx, testGoEnvironment(t), ".", nil); err == nil {
		t.Fatal("a build that left a process succeeded")
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("waited %s for leftover processes", elapsed)
	}
}

// Waiting for another run's build ends with the context, as a signal ends it.
func TestToolWrapperWaitIsInterruptible(t *testing.T) {
	useTestUserCache(t)
	env := testGoEnvironment(t)
	path := ensureTestToolWrapper(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0700); err != nil {
		t.Fatal(err)
	}
	// Another run uses the stale executable.
	held, err := holdGOROOTMirrorLock(t.Context(), filepath.Dir(path)+".lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	signals := make(chan os.Signal, 1)
	done := make(chan os.Signal)
	var ensureErr error
	go func() {
		done <- interruptibly(t.Context(), signals, func(ctx context.Context) {
			_, _, ensureErr = ensureToolWrapper(ctx, env, ".", nil)
		})
	}()
	time.Sleep(100 * time.Millisecond)
	signals <- os.Interrupt
	select {
	case interrupted := <-done:
		if interrupted != os.Interrupt || !errors.Is(ensureErr, context.Canceled) {
			t.Fatalf("interrupted=%v err=%v", interrupted, ensureErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait ignored the signal")
	}
}

// A wrapper path that go's -toolexec cannot quote leaves ddto as the wrapper
// and releases the executable.
func TestSelectToolCommand(t *testing.T) {
	plan := &Plan{File: "/plan.json", testify: true}
	for _, tc := range []struct {
		wrapper string
		want    []string
	}{
		{"/cache/ddto-toolwrap", []string{"/cache/ddto-toolwrap", "/ddto", "testify", "/plan.json"}},
		{`/cache/it's "x"/ddto-toolwrap`, []string{"/ddto", "tool-overlay", "testify", "/plan.json"}},
		{"", []string{"/ddto", "tool-overlay", "testify", "/plan.json"}},
	} {
		path := filepath.Join(t.TempDir(), "wrapper.lock")
		lock, err := holdGOROOTMirrorLock(t.Context(), path, false)
		if err != nil {
			t.Fatal(err)
		}
		plan.toolWrapperLock = nil
		if tc.wrapper == "" {
			lock.Close()
			lock = nil
		}
		tool, err := selectToolCommand(plan, "/ddto", tc.wrapper, lock, nil)
		if err != nil {
			t.Fatal(err)
		}
		words, err := splitFlags(tool)
		if err != nil || strings.Join(words, "|") != strings.Join(tc.want, "|") {
			t.Fatalf("%q: %q, %v", tc.wrapper, words, err)
		}
		if kept := plan.toolWrapperLock != nil; kept != (tc.want[0] == tc.wrapper) {
			t.Fatalf("%q: lock kept=%t", tc.wrapper, kept)
		}
		if lock != nil && plan.toolWrapperLock == nil {
			other, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := tryLockFile(other, true); err != nil && !errors.Is(err, errors.ErrUnsupported) {
				t.Errorf("%q: lock still held: %v", tc.wrapper, err)
			}
			other.Close()
		}
		if plan.toolWrapperLock != nil {
			plan.toolWrapperLock.Close()
		}
	}
}
