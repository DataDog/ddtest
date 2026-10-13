package integration

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The fixture prints what tests that inspect their toolchain see.
const gorootMirrorFixture = `package mirror

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestToolchainEnvironment(t *testing.T) {
	goPath, _ := exec.LookPath("go")
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	goroot, set := os.LookupEnv("GOROOT")
	t.Logf("TOOLCHAIN goroot_env=%q goroot_set=%t first_path=%q lookpath=%q go_env=%q runtime=%q END",
		goroot, set, first, goPath, strings.TrimSpace(string(out)), runtime.GOROOT())
}
`

// The executor stands for a go test -exec program, or Go's cross-compilation
// helper, which only reports what it sees: those start before the test binary
// can restore anything.
const gorootMirrorExecutor = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	goroot, set := os.LookupEnv("GOROOT")
	first := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	fmt.Printf("EXECUTOR goroot_env=%q goroot_set=%t first_path=%q argv0=%q END\n", goroot, set, first, os.Args[0])
	if strings.Contains(filepath.Base(os.Args[0]), "_exec") {
		return
	}
	cmd := exec.Command(os.Args[1], os.Args[2:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
`

var executorLine = regexp.MustCompile(`(?m)^EXECUTOR .* END$`)

// fakeOrchestrion runs go and toolexec commands as Orchestrion does, without
// changing the code.
const fakeOrchestrion = `package main

import (
	"os"
	"os/exec"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "go" && os.Args[1] != "toolexec" {
		os.Exit(2)
	}
	name, args := "go", os.Args[2:]
	if os.Args[1] == "toolexec" {
		name, args = os.Args[2], os.Args[3:]
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
`

// buildExecutor builds the executor under each of names in a new directory,
// which it returns.
func buildExecutor(t *testing.T, names ...string) string {
	t.Helper()
	return buildProgram(t, gorootMirrorExecutor, names...)
}

// buildProgram builds source under each of names in a new directory, which it
// returns.
func buildProgram(t *testing.T, program string, names ...string) string {
	t.Helper()
	source, dir := t.TempDir(), t.TempDir()
	writeBuildFixture(t, source, map[string]string{"go.mod": "module example.com/program\n\ngo 1.21\n", "main.go": program})
	built := filepath.Join(source, executableName("program"))
	if out, stderr, code := command(t, source, testEnv("GOWORK=off", "GOTOOLCHAIN=local"), "go", "build", "-o", built, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	for _, name := range names {
		copyExecutable(t, built, filepath.Join(dir, executableName(name)))
	}
	return dir
}

// prependPATH puts dir first in env's PATH, the last entry, which applies.
func prependPATH(env []string, dir string) []string {
	env = append([]string(nil), env...)
	for i := len(env) - 1; i >= 0; i-- {
		if name, value, ok := strings.Cut(env[i], "="); ok && strings.EqualFold(name, "PATH") {
			env[i] = name + "=" + dir + string(os.PathListSeparator) + value
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// sameExecutorView runs go test natively and through ddto with args, and
// checks that the executor sees the same toolchain settings.
func sameExecutorView(t *testing.T, dir string, env, ciEnv []string, goCommand, driver string, args ...string) {
	t.Helper()
	nativeOut, nativeErr, code := command(t, dir, env, goCommand, args...)
	if code != 0 {
		t.Fatalf("native %v: %s%s", args, nativeOut, nativeErr)
	}
	out, stderr, code := command(t, dir, ciEnv, driver, args...)
	if code != 0 {
		t.Fatalf("ddto %v: %s%s", args, out, stderr)
	}
	native, got := executorLine.FindString(nativeOut), executorLine.FindString(out)
	if native == "" || got != native {
		t.Fatalf("%v: the executor sees %q; natively %q\n%s%s", args, got, native, out, stderr)
	}
}

var execFailure = regexp.MustCompile(`(?m)^exec: "[^"]+": .*$`)

var startFailure = regexp.MustCompile(`(?m)^fork/exec .*$`)

// sameStartFailure checks that ddto's wrapper reports an -exec path that
// cannot start as go test does.
func sameStartFailure(t *testing.T, dir string, env, ciEnv []string, goCommand, driver string, args ...string) {
	t.Helper()
	nativeOut, nativeErr, code := command(t, dir, env, goCommand, args...)
	want := startFailure.FindString(nativeOut + nativeErr)
	if code != 1 || want == "" {
		t.Fatalf("native %v: exit %d\n%s%s", args, code, nativeOut, nativeErr)
	}
	out, stderr, code := command(t, dir, ciEnv, driver, args...)
	if code != 1 || startFailure.FindString(out+stderr) != want || strings.Contains(stderr, "running go test without CI Visibility") {
		t.Fatalf("ddto %v: exit %d, want %q\n%s%s", args, code, want, out, stderr)
	}
}

// sameExecFailure checks that ddto, like go test, reports an -exec program
// that go test cannot run, rather than running another one: it runs go test
// natively. Each command is a program and its arguments.
func sameExecFailure(t *testing.T, dir string, env, ciEnv, native, ddto []string) {
	t.Helper()
	nativeOut, nativeErr, code := command(t, dir, env, native[0], native[1:]...)
	want := execFailure.FindString(nativeOut + nativeErr)
	if code != 1 || want == "" {
		t.Fatalf("native %v: exit %d\n%s%s", native, code, nativeOut, nativeErr)
	}
	out, stderr, code := command(t, dir, ciEnv, ddto[0], ddto[1:]...)
	if code != 1 || execFailure.FindString(out+stderr) != want || !strings.Contains(stderr, "running go test without CI Visibility") {
		t.Fatalf("ddto %v: exit %d, want %q\n%s%s", ddto, code, want, out, stderr)
	}
}

// Values are %q-quoted, so Windows paths arrive with escaped backslashes.
var toolchainLine = regexp.MustCompile(`TOOLCHAIN goroot_env=("(?:[^"\\]|\\.)*") goroot_set=(\w+) first_path=("(?:[^"\\]|\\.)*") lookpath=("(?:[^"\\]|\\.)*") go_env=("(?:[^"\\]|\\.)*") runtime=("(?:[^"\\]|\\.)*") END`)

type toolchainView struct{ gorootEnv, gorootSet, firstPath, lookPath, goEnv, runtime string }

func parseToolchainView(t *testing.T, output string) toolchainView {
	t.Helper()
	m := toolchainLine.FindStringSubmatch(output)
	if m == nil {
		t.Fatalf("no toolchain line:\n%s", output)
	}
	unquote := func(quoted string) string {
		value, err := strconv.Unquote(quoted)
		if err != nil {
			t.Fatalf("%s: %v", quoted, err)
		}
		return value
	}
	return toolchainView{unquote(m[1]), m[2], unquote(m[3]), unquote(m[4]), unquote(m[5]), unquote(m[6])}
}

// isolatedUserCache points os.UserCacheDir at cache on every platform, and
// keeps the build cache warm.
func isolatedUserCache(t *testing.T, cache string) []string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	return []string{"XDG_CACHE_HOME=" + cache, "HOME=" + cache, "LOCALAPPDATA=" + cache, "GOCACHE=" + strings.TrimSpace(string(out))}
}

// userCacheIn is os.UserCacheDir under isolatedUserCache's environment.
func userCacheIn(cache string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(cache, "Library", "Caches")
	}
	return cache
}

// cloneToolchain lays out the running toolchain as a downloaded one in a
// module cache: hard links where possible, copies otherwise, and read-only
// directories as Go leaves them.
func cloneToolchain(t *testing.T, modcache string) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\n")
	source := strings.TrimSpace(fields[0])
	// Released versions are path-safe; a distribution's, such as
	// go1.27.0-X:nodwarf5, would split PATH at its colon.
	version := strings.Map(func(r rune) rune {
		if r == ':' || r == ';' || r == filepath.Separator {
			return '_'
		}
		return r
	}, strings.TrimSpace(fields[1]))
	target := filepath.Join(modcache, "golang.org", "toolchain@v0.0.1-"+version+"."+runtime.GOOS+"-"+runtime.GOARCH)
	link := runtime.GOOS != "windows"
	// A hosted toolchain's GOROOT can be a Windows junction, which Lstat
	// reports as irregular unless the path ends in a separator.
	err = filepath.WalkDir(source+string(filepath.Separator), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		dest := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			return os.MkdirAll(dest, 0755)
		case info.Mode()&fs.ModeSymlink != 0:
			to, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(to, dest)
		}
		if link && os.Link(path, dest) == nil {
			return nil
		}
		link = false
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		outFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(outFile, in); err != nil {
			outFile.Close()
			return err
		}
		return outFile.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	setDirectoriesWritable(modcache, false)
	t.Cleanup(func() { setDirectoriesWritable(modcache, true) })
	return target
}

func setDirectoriesWritable(root string, writable bool) {
	mode := fs.FileMode(0555)
	if writable {
		mode = 0755
	}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			_ = os.Chmod(path, mode)
		}
		return nil
	})
}

// treeModes records every file's mode below root, which the mirror must not
// change: hard links share it with the module cache.
func treeModes(t *testing.T, root string) map[string]fs.FileMode {
	t.Helper()
	modes := map[string]fs.FileMode{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := os.Lstat(path)
		if err == nil {
			modes[path] = info.Mode()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return modes
}

// changedModes lists the files whose modes differ between a and b, or that
// only one has.
func changedModes(a, b map[string]fs.FileMode) []string {
	var changed []string
	for path, mode := range a {
		if other, ok := b[path]; !ok || other != mode {
			changed = append(changed, path)
		}
	}
	for path := range b {
		if _, ok := a[path]; !ok {
			changed = append(changed, path)
		}
	}
	return changed
}

func gorootMirrorFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":         "module example.com/mirror\n\ngo 1.25.0\n",
		"mirror_test.go": gorootMirrorFixture,
	})
	return dir
}

// runMirroredToolchain runs native go test, then ddto test twice, and checks
// that ddto mirrors the toolchain once, reports tests, keeps the result cache
// and shows tests the toolchain view of native go test, except
// runtime.GOROOT, which names the mirror.
// goCommand is the caller's go: os/exec resolves a bare name with this
// process's PATH, not env's.
func runMirroredToolchain(t *testing.T, dir string, env []string, goCommand, cache, modcache string) {
	t.Helper()
	before := treeModes(t, modcache)
	out, stderr, code := command(t, dir, env, goCommand, "test", "-count=1", "-v", "./...")
	if code != 0 {
		t.Fatalf("native go test: %s%s", out, stderr)
	}
	native := parseToolchainView(t, out)
	driver := sharedDriver(t, "..")
	receiver := &parityReceiver{side: map[string][][]byte{}, requests: map[string]int{}}
	server := newServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	ciEnv := append(append([]string(nil), env...), "DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL)
	for i, want := range []string{"state=created", "state=reused"} {
		out, stderr, code := command(t, dir, ciEnv, driver, "test", "-count=1", "-v", "./...")
		if code != 0 || !strings.Contains(stderr, "goroot mirror "+want) {
			t.Fatalf("run %d: exit %d, want %s:\n%s%s", i, code, want, out, stderr)
		}
		got := parseToolchainView(t, out)
		mirrors := filepath.Join(userCacheIn(cache), "ddto", "goroots")
		if !strings.HasPrefix(got.runtime, mirrors) {
			t.Errorf("runtime.GOROOT %q is not a mirror in %s", got.runtime, mirrors)
		}
		got.runtime, native.runtime = "", ""
		if got != native {
			t.Errorf("tests see %+v; natively %+v", got, native)
		}
	}
	// Under -trimpath the linker records no GOROOT; runtime.GOROOT() comes
	// from the environment that go test starts tests with.
	nativeOut, nativeErr, code := command(t, dir, env, goCommand, "test", "-count=1", "-v", "-trimpath", "./...")
	if code != 0 {
		t.Fatalf("native -trimpath: %s%s", nativeOut, nativeErr)
	}
	trimmedOut, trimmedErr, code := command(t, dir, ciEnv, driver, "test", "-count=1", "-v", "-trimpath", "./...")
	if code != 0 {
		t.Fatalf("ddto -trimpath: %s%s", trimmedOut, trimmedErr)
	}
	if parseToolchainView(t, nativeOut).runtime != "" && parseToolchainView(t, trimmedOut).runtime == "" {
		t.Fatal("runtime.GOROOT() lost under -trimpath")
	}
	// go test resolves a bare -exec program on the caller's PATH, here before
	// the toolchain's own gofmt, which comes first in the tests' PATH.
	executors := buildExecutor(t, "gofmt")
	sameExecutorView(t, dir, prependPATH(env, executors), prependPATH(ciEnv, executors), goCommand, driver, "test", "-count=1", "-v", "-exec=gofmt", "./...")
	receiver.sideMu.Lock()
	events := 0
	for path, n := range receiver.requests {
		if strings.HasSuffix(path, "/citestcycle") {
			events += n
		}
	}
	receiver.sideMu.Unlock()
	if events == 0 {
		t.Fatal("no test events through the mirrored toolchain")
	}
	// The saved native environment keeps Go's result cache valid.
	for i := 0; i < 2; i++ {
		out, stderr, code = command(t, dir, ciEnv, driver, "test", "./...")
		if code != 0 {
			t.Fatalf("cached run %d: %s%s", i, out, stderr)
		}
	}
	if !strings.Contains(out, "(cached)") {
		t.Fatalf("result not cached:\n%s", out)
	}
	// go test looks -exec programs up from -C's directory, where a relative
	// PATH entry matches one that Go refuses to run, ahead of the same name
	// elsewhere on PATH.
	executors = buildExecutor(t, "ddtoexec")
	copyExecutable(t, filepath.Join(executors, executableName("ddtoexec")), filepath.Join(dir, "bin", executableName("ddtoexec")))
	relative := func(env []string) []string { return prependPATH(prependPATH(env, executors), "bin") }
	args := []string{"test", "-C", filepath.Base(dir), "-count=1", "-exec=ddtoexec", "./..."}
	sameExecFailure(t, filepath.Dir(dir), relative(env), relative(ciEnv), append([]string{goCommand}, args...), append([]string{driver}, args...))
	// A -exec path that cannot start fails as natively.
	missing := "./missing"
	if runtime.GOOS == "windows" {
		// With an extension that PATHEXT lists, Windows also starts it as given.
		missing = `.\missing.exe`
	}
	sameStartFailure(t, dir, env, ciEnv, goCommand, driver, "test", "-count=1", "-exec="+missing, "./...")
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(filepath.Join(dir, "notexec"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		sameStartFailure(t, dir, env, ciEnv, goCommand, driver, "test", "-count=1", "-exec=./notexec", "./...")
	}
	// Provisioning runs go mod edit, which takes Go's own lock in the module
	// cache, as other module commands do.
	after := treeModes(t, modcache)
	lock := filepath.Join(modcache, "cache", "lock")
	delete(before, lock)
	delete(after, lock)
	if changed := changedModes(before, after); len(changed) != 0 {
		t.Fatalf("the module cache's files changed: %v", changed)
	}
}

// A toolchain whose go runs directly from the module cache.
func TestMiniGOROOTMirrorDirectToolchain(t *testing.T) {
	modcache := filepath.Join(t.TempDir(), "mod")
	goroot := cloneToolchain(t, modcache)
	cache := t.TempDir()
	env := testEnv(append(isolatedUserCache(t, cache), "PATH="+filepath.Join(goroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOMODCACHE="+modcache, "GOTOOLCHAIN=local", "GOPROXY=off")...)
	goCommand := filepath.Join(goroot, "bin", executableName("go"))
	dir := gorootMirrorFixtureDir(t)
	runMirroredToolchain(t, dir, env, goCommand, cache, modcache)
	// Without -exec, go test runs a cross-compiled test binary through
	// go_$GOOS_$GOARCH_exec from PATH. The helper here only reports.
	cross := "amd64"
	if runtime.GOARCH == "amd64" {
		cross = "arm64"
	}
	helper := "go_" + runtime.GOOS + "_" + cross + "_exec"
	executors := buildExecutor(t, helper)
	crossEnv := append(prependPATH(env, executors), "GOARCH="+cross, "CGO_ENABLED=0")
	ciEnv := append(crossEnv, "DD_CIVISIBILITY_ENABLED=false")
	sameExecutorView(t, dir, crossEnv, ciEnv, goCommand, sharedDriver(t, ".."), "test", "-count=1", "-v", "./...")
	// With GODEBUG=execerrdot=0, go test runs a helper that it finds relative
	// to its directory, which ddto leaves to native go test.
	copyExecutable(t, filepath.Join(executors, executableName(helper)), filepath.Join(dir, "bin", executableName(helper)))
	relativeEnv := append(prependPATH(crossEnv, "bin"), "GODEBUG=execerrdot=0")
	sameExecutorView(t, dir, relativeEnv, append(relativeEnv, "DD_CIVISIBILITY_ENABLED=false"), goCommand, sharedDriver(t, ".."), "test", "-count=1", "-v", "./...")
	// An empty -exec turns the helper off: go test runs the binaries itself.
	nativeOut, nativeErr, nativeCode := command(t, dir, crossEnv, goCommand, "test", "-count=1", "-v", "-exec=", "./...")
	out, stderr, code := command(t, dir, ciEnv, sharedDriver(t, ".."), "test", "-count=1", "-v", "-exec=", "./...")
	if code != nativeCode || strings.Contains(nativeOut, "EXECUTOR") || strings.Contains(out, "EXECUTOR") {
		t.Fatalf("-exec=: exit %d, natively %d\n%s%s\nnative:\n%s%s", code, nativeCode, out, stderr, nativeOut, nativeErr)
	}
	// With -c, go test runs no test binary, and the helper does not matter.
	out, stderr, code = command(t, dir, append(relativeEnv, "DD_CIVISIBILITY_ENABLED=false"), sharedDriver(t, ".."), "test", "-c", "-o", filepath.Join(t.TempDir(), executableName("mirror.test")), ".")
	if code != 0 || strings.Contains(stderr, "running go test without CI Visibility") {
		t.Fatalf("-c with a relative helper: exit %d\n%s%s", code, out, stderr)
	}
	// An -exec program missing from the caller's PATH stays missing, although
	// the toolchain's bin, first in the tests' PATH, has one by that name.
	alone := t.TempDir()
	copyExecutable(t, goCommand, filepath.Join(alone, executableName("go")))
	aloneEnv := append(append([]string(nil), env...), "PATH="+alone, "GOROOT="+goroot, "CGO_ENABLED=0")
	aloneCIEnv := append(aloneEnv, "DD_CIVISIBILITY_ENABLED=false")
	args := []string{"test", "-count=1", "-exec=gofmt", "./..."}
	driver := sharedDriver(t, "..")
	sameExecFailure(t, dir, aloneEnv, aloneCIEnv, append([]string{filepath.Join(alone, executableName("go"))}, args...), append([]string{driver}, args...))
	// Also through Orchestrion, which runs go from PATH, where ddto puts the
	// mirror's bin first: ddto runs the caller's command.
	orchestrion := buildProgram(t, fakeOrchestrion, "orchestrion")
	withOrchestrion := func(env []string) []string { return prependPATH(env, orchestrion) }
	sameExecFailure(t, dir, withOrchestrion(aloneEnv), withOrchestrion(aloneCIEnv),
		append([]string{filepath.Join(orchestrion, executableName("orchestrion")), "go"}, args...), append([]string{driver, "orchestrion", "go"}, args...))
}

// switchingGo stands for a module-cache toolchain whose go list switches to
// another toolchain in the module cache, as after provisioning raises the Go
// version: it reports packages from there whatever GOROOT it ran with.
const switchingGo = `package main

import (
	"os"
	"os/exec"
)

func main() {
	if _, set := os.LookupEnv("GOROOT"); !set || len(os.Args) > 1 && os.Args[1] == "list" {
		os.Setenv("GOROOT", os.Getenv("DDTO_TEST_SWITCH_GOROOT"))
	}
	cmd := exec.Command(os.Getenv("DDTO_TEST_REAL_GO"), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(1)
	}
}
`

// When go selects testing outside the mirror after all, ddto warns and runs go
// test natively: Go would reject the overlay.
func TestMiniGOROOTMirrorLaterToolchainRunsGoTest(t *testing.T) {
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	realGo := filepath.Join(strings.TrimSpace(string(root)), "bin", executableName("go"))
	source := t.TempDir()
	writeBuildFixture(t, source, map[string]string{"go.mod": "module example.com/switchinggo\n\ngo 1.21\n", "main.go": switchingGo})
	built := filepath.Join(source, executableName("go"))
	if out, stderr, code := command(t, source, testEnv("GOWORK=off", "GOTOOLCHAIN=local"), "go", "build", "-o", built, "."); code != 0 {
		t.Fatal(out, stderr)
	}
	modcache := filepath.Join(t.TempDir(), "mod")
	goroot := cloneToolchain(t, modcache)
	bin := filepath.Join(goroot, "bin")
	setDirectoriesWritable(bin, true)
	// The clone's go is a link to the real one: replace the link only.
	if err := os.Remove(filepath.Join(bin, executableName("go"))); err != nil {
		t.Fatal(err)
	}
	copyExecutable(t, built, filepath.Join(bin, executableName("go")))
	setDirectoriesWritable(bin, false)
	cache := t.TempDir()
	env := testEnv(append(isolatedUserCache(t, cache), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOMODCACHE="+modcache, "GOTOOLCHAIN=local", "GOPROXY=off", "DDTO_TEST_SWITCH_GOROOT="+goroot, "DDTO_TEST_REAL_GO="+realGo)...)
	dir := gorootMirrorFixtureDir(t)
	out, stderr, code := command(t, dir, env, sharedDriver(t, ".."), "test", "-count=1", "-v", "./...")
	if code != 0 || !strings.Contains(out, "TOOLCHAIN goroot_env=") || !strings.Contains(stderr, "outside a usable GOROOT - running go test without CI Visibility") {
		t.Fatalf("exit %d:\n%s%s", code, out, stderr)
	}
}

// Without a usable mirror location, ddto warns and runs go test natively,
// with its exit status.
func TestMiniGOROOTMirrorUnavailableRunsGoTest(t *testing.T) {
	modcache := filepath.Join(t.TempDir(), "mod")
	goroot := cloneToolchain(t, modcache)
	// A user cache inside GOMODCACHE: the mirror would write there.
	cache := filepath.Join(modcache, "cache")
	env := testEnv(append(isolatedUserCache(t, cache), "PATH="+filepath.Join(goroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GOMODCACHE="+modcache, "GOTOOLCHAIN=local", "GOPROXY=off")...)
	dir := gorootMirrorFixtureDir(t)
	driver := sharedDriver(t, "..")
	for _, tc := range []struct {
		fail   bool
		status int
		output string
	}{{false, 0, "TOOLCHAIN goroot_env="}, {true, 1, "--- FAIL: TestFails"}} {
		if tc.fail {
			writeBuildFixture(t, dir, map[string]string{"fail_test.go": "package mirror\nimport \"testing\"\nfunc TestFails(t *testing.T) { t.Fatal(\"fails\") }\n"})
		}
		nativeOut, nativeErr, native := command(t, dir, env, filepath.Join(goroot, "bin", executableName("go")), "test", "-count=1", "-v", "./...")
		if native != tc.status || !strings.Contains(nativeOut, tc.output) {
			t.Fatalf("native fail=%t: exit %d:\n%s%s", tc.fail, native, nativeOut, nativeErr)
		}
		out, stderr, code := command(t, dir, env, driver, "test", "-count=1", "-v", "./...")
		if code != tc.status || !strings.Contains(out, tc.output) || !strings.Contains(stderr, "running go test without CI Visibility") {
			t.Fatalf("fail=%t: exit %d, native %d:\n%s%s", tc.fail, code, native, out, stderr)
		}
	}
}
