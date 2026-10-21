package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
)

// fakeModuleCacheToolchain lays out the files a mirror needs, read-only like
// the module cache's, and returns its environment.
func fakeModuleCacheToolchain(t *testing.T) *goEnvironment {
	t.Helper()
	return fakeModuleCacheToolchainFor(t, goruntime.GOOS)
}

func fakeModuleCacheToolchainFor(t *testing.T, hostOS string) *goEnvironment {
	t.Helper()
	modcache := filepath.Join(t.TempDir(), "mod")
	goroot := filepath.Join(modcache, "golang.org", "toolchain@v0.0.1-go1.99.0.test")
	exe := ""
	if hostOS == "windows" {
		exe = ".exe"
	}
	tools := filepath.Join("pkg", "tool", hostOS+"_"+goruntime.GOARCH)
	for rel, content := range map[string]string{
		filepath.Join("bin", "go"+exe):                "go",
		filepath.Join(tools, "compile"+exe):           "compile",
		filepath.Join(tools, "link"+exe):              "link",
		filepath.Join("src", "testing", "testing.go"): "package testing\n",
		filepath.Join("src", "fmt", "print.go"):       "package fmt\n",
		"VERSION":                                     "go1.99.0\n",
	} {
		path := filepath.Join(goroot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0555); err != nil {
			t.Fatal(err)
		}
	}
	setTreeWritable(t, modcache, false)
	t.Cleanup(func() { setTreeWritable(t, modcache, true) })
	return &goEnvironment{GOROOT: goroot, GOMODCACHE: modcache, GOVERSION: "go1.99.0", GOHOSTOS: hostOS, GOHOSTARCH: goruntime.GOARCH}
}

// setTreeWritable makes the module cache's directories read-only, as Go does,
// or writable again so that the test can remove it.
func setTreeWritable(t *testing.T, root string, writable bool) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			mode := fs.FileMode(0555)
			if writable {
				mode = 0755
			}
			_ = os.Chmod(path, mode)
		}
		return nil
	})
}

// sourceModes records the source's file modes and contents, which no mirror
// operation may change.
func sourceModes(t *testing.T, root string) map[string]string {
	t.Helper()
	modes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		modes[path] = info.Mode().String() + ":" + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return modes
}

func useTestUserCache(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	previous := userCacheDir
	userCacheDir = func() (string, error) { return cache, nil }
	t.Cleanup(func() { userCacheDir = previous })
	return cache
}

func TestFilePathHasPrefix(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join(sep+"cache", "mod")
	for _, tc := range []struct {
		path string
		want bool
	}{
		{root, true},
		{filepath.Join(root, "golang.org", "toolchain@v0.0.1-go1.27.1.linux-amd64"), true},
		{root + "2", false},
		{filepath.Join(sep+"cache", "mo"), false},
		{sep + "other", false},
	} {
		if got := filePathHasPrefix(tc.path, root); got != tc.want {
			t.Errorf("filePathHasPrefix(%q, %q) = %t; want %t", tc.path, root, got, tc.want)
		}
	}
	if goruntime.GOOS == "windows" && !filePathHasPrefix(`c:\cache\mod\x`, `C:\cache\mod`) {
		t.Error("volume names must compare without case, as Go's check does")
	}
}

func TestNeedsGOROOTMirror(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	if !needsGOROOTMirror(env) {
		t.Fatal("a toolchain in GOMODCACHE needs a mirror")
	}
	outside := *env
	outside.GOMODCACHE = filepath.Join(filepath.Dir(env.GOMODCACHE), "mod2")
	if needsGOROOTMirror(&outside) {
		t.Fatal("a toolchain outside GOMODCACHE needs no mirror")
	}
	empty := *env
	empty.GOMODCACHE = ""
	if needsGOROOTMirror(&empty) {
		t.Fatal("no GOMODCACHE, no mirror")
	}
}

// A mirror links the toolchain's files, leaves the module cache unchanged and
// is reused by later runs.
func TestGOROOTMirrorCreatesAndReuses(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	useTestUserCache(t)
	before := sourceModes(t, env.GOMODCACHE)
	var links atomic.Int32
	previous := gorootMirrorLink
	gorootMirrorLink = func(old, new string) error { links.Add(1); return previous(old, new) }
	t.Cleanup(func() { gorootMirrorLink = previous })

	root, lock, err := ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	if !validGOROOTMirror(root, env) {
		t.Fatal("new mirror is invalid")
	}
	mirrored, err := os.Stat(filepath.Join(root, "src", "testing", "testing.go"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(filepath.Join(env.GOROOT, "src", "testing", "testing.go"))
	if err != nil {
		t.Fatal(err)
	}
	if linked := os.SameFile(mirrored, original); linked != (goruntime.GOOS != "windows") {
		t.Fatalf("hard-linked=%t on %s", linked, goruntime.GOOS)
	}
	created := links.Load()
	root2, lock2, err := ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lock2.Close()
	if root2 != root || links.Load() != created {
		t.Fatalf("reuse rebuilt the mirror: %s %s links %d→%d", root, root2, created, links.Load())
	}
	if after := sourceModes(t, env.GOMODCACHE); !equalMaps(before, after) {
		t.Fatalf("the module cache changed:\n%v\n%v", before, after)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Without hard links, as across file systems, the mirror copies every file.
func TestGOROOTMirrorCopiesWithoutLinks(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	useTestUserCache(t)
	previous := gorootMirrorLink
	gorootMirrorLink = func(string, string) error { return errors.New("invalid cross-device link") }
	t.Cleanup(func() { gorootMirrorLink = previous })
	root, lock, err := ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	mirrored, _ := os.Stat(filepath.Join(root, "bin", filepath.Base(validToolPath(env, "go"))))
	original, _ := os.Stat(validToolPath(env, "go"))
	if mirrored == nil || original == nil || os.SameFile(mirrored, original) || mirrored.Mode() != original.Mode() {
		t.Fatalf("not a copy with the same mode: %v %v", mirrored, original)
	}
	data, err := os.ReadFile(filepath.Join(root, gorootMirrorMarker))
	if err != nil || !bytes.Contains(data, []byte(`"copied":6`)) {
		t.Fatalf("record %s: %v", data, err)
	}
}

func validToolPath(env *goEnvironment, name string) string {
	if env.GOHOSTOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(env.GOROOT, "bin", name)
}

// A mirror whose record or files are missing is replaced, not reused.
func TestGOROOTMirrorRebuildsIncompleteMirror(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	useTestUserCache(t)
	for _, damage := range []string{gorootMirrorMarker, filepath.Join("src", "testing", "testing.go"), "other source"} {
		root, lock, err := ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		lock.Close()
		if damage == "other source" {
			// A record of another toolchain, even at this path, is not reused.
			err = os.WriteFile(filepath.Join(root, gorootMirrorMarker), []byte(`{"source":"/other","go_version":"go1.99.0","files":6}`), 0600)
		} else {
			err = os.Remove(filepath.Join(root, damage))
		}
		if err != nil {
			t.Fatal(err)
		}
		if validGOROOTMirror(root, env) {
			t.Fatalf("mirror without %s is valid", damage)
		}
		root, lock, err = ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		lock.Close()
		if !validGOROOTMirror(root, env) {
			t.Fatalf("mirror without %s was not rebuilt", damage)
		}
	}
}

// Concurrent runs build one mirror; every run gets a valid one.
func TestGOROOTMirrorConcurrentCreators(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	useTestUserCache(t)
	var links atomic.Int32
	previous := gorootMirrorLink
	gorootMirrorLink = func(old, new string) error { links.Add(1); return previous(old, new) }
	t.Cleanup(func() { gorootMirrorLink = previous })
	var wg sync.WaitGroup
	roots := make([]string, 8)
	errs := make([]error, 8)
	for i := range roots {
		wg.Go(func() {
			var lock *os.File
			roots[i], lock, errs[i] = ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
			if lock != nil {
				defer lock.Close()
				if !validGOROOTMirror(roots[i], env) {
					errs[i] = errors.New("invalid mirror while held")
				}
			}
		})
	}
	wg.Wait()
	for i := range roots {
		if errs[i] != nil || roots[i] != roots[0] {
			t.Fatalf("run %d: %s %v", i, roots[i], errs[i])
		}
	}
	if goruntime.GOOS != "windows" && links.Load() != 6 {
		t.Fatalf("%d links for 6 files: more than one build", links.Load())
	}
}

// Pruning removes unused mirrors and abandoned staging directories, keeps
// mirrors in use and never changes the module cache.
func TestGOROOTMirrorPruning(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	cache := useTestUserCache(t)
	before := sourceModes(t, env.GOMODCACHE)
	old := *env
	old.GOVERSION = "go1.98.0"
	used := *env
	used.GOVERSION = "go1.97.0"
	var roots []string
	var usedLock *os.File
	for _, e := range []*goEnvironment{&old, &used, env} {
		root, lock, err := ensureGOROOTMirror(context.Background(), e, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if e == &used {
			usedLock = lock
		} else {
			lock.Close()
		}
		roots = append(roots, root)
	}
	defer usedLock.Close()
	parent := filepath.Join(cache, "ddto", "goroots")
	stale := time.Now().Add(-gorootMirrorRetention - time.Hour)
	for _, root := range roots[:2] {
		if err := os.Chtimes(root, stale, stale); err != nil {
			t.Fatal(err)
		}
	}
	abandoned := filepath.Join(parent, filepath.Base(roots[0])+".tmp-123")
	if err := os.MkdirAll(filepath.Join(abandoned, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	// Staging holds what a mirror would: links, and read-only copies on Windows.
	staged := filepath.Join(abandoned, "src", "VERSION")
	var err error
	if goruntime.GOOS == "windows" {
		err = os.WriteFile(staged, []byte("go1.99.0\n"), 0444)
	} else {
		err = os.Link(filepath.Join(env.GOROOT, "VERSION"), staged)
	}
	if err != nil {
		t.Fatal(err)
	}
	// A creator that holds its key's lock is still building its staging.
	building := filepath.Join(parent, filepath.Base(roots[1])+".tmp-456")
	if err := os.MkdirAll(building, 0700); err != nil {
		t.Fatal(err)
	}
	creator, err := holdGOROOTMirrorLock(context.Background(), roots[1]+".lock", false)
	if err != nil {
		t.Fatal(err)
	}
	defer creator.Close()
	pruneGOROOTMirrors(parent, filepath.Base(roots[2]))
	if _, err := os.Stat(building); err != nil {
		t.Fatalf("a creator's staging was removed: %v", err)
	}
	if _, err := os.Stat(roots[0]); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unused mirror kept: %v", err)
	}
	if _, err := os.Stat(roots[0] + ".lock"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unused mirror's lock kept: %v", err)
	}
	if _, err := os.Stat(abandoned); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("abandoned staging kept: %v", err)
	}
	if !validGOROOTMirror(roots[1], &used) {
		t.Fatal("a held mirror was removed")
	}
	if !validGOROOTMirror(roots[2], env) {
		t.Fatal("the kept mirror was removed")
	}
	if after := sourceModes(t, env.GOMODCACHE); !equalMaps(before, after) {
		t.Fatalf("pruning changed the module cache:\n%v\n%v", before, after)
	}
}

// The mirror never goes into GOMODCACHE, the toolchain or the module.
func TestGOROOTMirrorRejectsProtectedCache(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	module := t.TempDir()
	env.GOMOD = filepath.Join(module, "go.mod")
	for _, cache := range []string{env.GOMODCACHE, filepath.Join(env.GOROOT, "cache"), filepath.Join(module, ".cache")} {
		previous := userCacheDir
		userCacheDir = func() (string, error) { return cache, nil }
		_, _, err := gorootMirrorLocation(env, t.TempDir())
		userCacheDir = previous
		if err == nil {
			t.Errorf("cache %s accepted", cache)
		}
	}
	work := t.TempDir()
	previous := userCacheDir
	userCacheDir = func() (string, error) { return filepath.Join(work, ".cache"), nil }
	defer func() { userCacheDir = previous }()
	if _, _, err := gorootMirrorLocation(env, work); err == nil {
		t.Error("a cache within the working directory was accepted")
	}
}

// Test processes see the GOROOT that go test gives them natively: the
// caller's when its go is the toolchain, even through a link; unset after a
// switch to a go<version> on PATH; the toolchain's after a download switch.
// This process's environment never changes.
func TestGOROOTMirrorEnvironment(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	mirror := filepath.Join(t.TempDir(), "mirror")
	sep := string(os.PathListSeparator)
	exe := ""
	if goruntime.GOOS == "windows" {
		exe = ".exe"
	}
	toolchainBin := filepath.Join(env.GOROOT, "bin")
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go"+exe), []byte("other"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOROOT", "/caller/goroot")
	cases := []struct {
		name      string
		path      string
		goroot    string
		gorootSet bool
	}{
		{"downloaded switch", other, env.GOROOT, true},
		{"direct", toolchainBin, "/caller/goroot", true},
	}
	// A hard link is the same executable under another name.
	hardLinked := t.TempDir()
	if err := os.Link(filepath.Join(toolchainBin, "go"+exe), filepath.Join(hardLinked, env.GOVERSION+exe)); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		name      string
		path      string
		goroot    string
		gorootSet bool
	}{"go<version> hard link on PATH", other + sep + hardLinked, "", false})
	if goruntime.GOOS != "windows" {
		linked := t.TempDir()
		if err := os.Symlink(filepath.Join(toolchainBin, "go"), filepath.Join(linked, "go")); err != nil {
			t.Fatal(err)
		}
		named := t.TempDir()
		if err := os.Symlink(filepath.Join(toolchainBin, "go"), filepath.Join(named, env.GOVERSION)); err != nil {
			t.Fatal(err)
		}
		cases = append(cases,
			struct {
				name      string
				path      string
				goroot    string
				gorootSet bool
			}{"direct through a link", linked, "/caller/goroot", true},
			struct {
				name      string
				path      string
				goroot    string
				gorootSet bool
			}{"go<version> on PATH", other + sep + named, "", false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PATH", tc.path)
			saved, toolchain := gorootMirrorEnvironment(mirror, env)
			want := []string{
				goenv.SaveValue("GOROOT", tc.goroot, tc.gorootSet),
				goenv.SaveValue("PATH", toolchainBin+sep+tc.path, true),
			}
			if strings.Join(saved, "\n") != strings.Join(want, "\n") {
				t.Fatalf("saved %q; want %q", saved, want)
			}
			if toolchain.path != filepath.Join(mirror, "bin", "go"+exe) || toolchain.env[0] != "GOROOT="+mirror ||
				toolchain.env[1] != "PATH="+filepath.Join(mirror, "bin")+sep+tc.path {
				t.Fatalf("toolchain %+v", toolchain)
			}
			if os.Getenv("PATH") != tc.path || os.Getenv("GOROOT") != "/caller/goroot" {
				t.Fatal("the process environment changed")
			}
		})
	}
}

// A plan's go commands run the mirror's go with its GOROOT and PATH, and keep
// the PWD that os/exec computes for their directory; other commands are
// unchanged.
func TestGoCommandUsesThePlansToolchain(t *testing.T) {
	dir := t.TempDir()
	plain := goCommand(context.Background(), dir, "version")
	if filepath.Base(plain.Args[0]) != "go" || plain.Env != nil || plain.Dir != dir {
		t.Fatalf("without a mirror: %v env=%v dir=%s", plain.Args, plain.Env, plain.Dir)
	}
	mirror := &gorootMirror{toolchain: goToolchain{path: "/mirror/bin/go", root: "/mirror", env: []string{"GOROOT=/mirror", "PATH=/mirror/bin"}}}
	cmd := goCommand(withGoToolchain(context.Background(), mirror), dir, "version")
	env := strings.Join(cmd.Env, "\n")
	if cmd.Path != "/mirror/bin/go" || !strings.Contains(env, "GOROOT=/mirror") || !strings.Contains(env, "PATH=/mirror/bin") {
		t.Fatalf("with a mirror: %s %v", cmd.Path, cmd.Env)
	}
	if goruntime.GOOS != "windows" && !strings.Contains(env, "PWD=") {
		t.Fatal("PWD lost")
	}
}

// Each plan holds its own lease: releasing one keeps another's mirror.
func TestGOROOTMirrorOverlappingPlans(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	cache := useTestUserCache(t)
	a, err := prepareGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := prepareGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.release()
	a.release()
	stale := time.Now().Add(-gorootMirrorRetention - time.Hour)
	if err := os.Chtimes(b.root, stale, stale); err != nil {
		t.Fatal(err)
	}
	pruneGOROOTMirrors(filepath.Join(cache, "ddto", "goroots"), "another-key")
	if !validGOROOTMirror(b.root, env) {
		t.Fatal("a plan's mirror was pruned after another plan released it")
	}
	// Releasing the last plan lets pruning remove the mirror.
	Plan{gorootMirror: b}.Release()
	pruneGOROOTMirrors(filepath.Join(cache, "ddto", "goroots"), "another-key")
	if _, err := os.Stat(b.root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a released mirror was kept: %v", err)
	}
}

// Only Mini's test runtime restores the caller's GOROOT and PATH; SDK tests
// get no saved values to leak.
func TestSavedEnvironmentRestoresTheToolchainForMini(t *testing.T) {
	mirror := &gorootMirror{saved: []string{goenv.SaveValue("GOROOT", "", false), goenv.SaveValue("PATH", "/bin", true)}}
	if saved := (Plan{gorootMirror: mirror}).savedEnvironment(); len(saved) != 0 {
		t.Fatalf("SDK runtime: %v", saved)
	}
	if saved := (Plan{gorootMirror: mirror, mini: true}).savedEnvironment(); !slices.Equal(saved, mirror.saved) {
		t.Fatalf("Mini: %v", saved)
	}
}

// TestRunGoHelperProcess prints the toolchain settings runGo started it with.
func TestRunGoHelperProcess(t *testing.T) {
	if os.Getenv("DDTO_RUN_GO_HELPER") != "1" {
		return
	}
	goroot, set := os.LookupEnv("GOROOT")
	fmt.Printf("GOROOT=%t:%s\nPATH=%s\n", set, goroot, os.Getenv("PATH"))
	os.Exit(0)
}

// go runs from the mirror with its GOROOT and the caller's PATH, so that it
// resolves an -exec program as natively. A launcher such as Orchestrion runs
// go from PATH, so it also gets the mirror's bin first.
func TestRunGoUsesThePlansToolchain(t *testing.T) {
	t.Setenv("DDTO_RUN_GO_HELPER", "1")
	callerPath := os.Getenv("PATH")
	mirrorPath := prependPath(filepath.FromSlash("/mirror/bin"), callerPath)
	mirror := &gorootMirror{toolchain: goToolchain{path: os.Args[0], root: "/mirror", env: []string{"GOROOT=/mirror", "PATH=" + mirrorPath}}}
	ctx := withGoToolchain(context.Background(), mirror)
	launched := context.WithValue(ctx, goLauncherKey{}, goLauncher{command: []string{os.Args[0]}})
	for _, tc := range []struct {
		ctx  context.Context
		path string
	}{{ctx, callerPath}, {launched, mirrorPath}} {
		var out bytes.Buffer
		code := runGo(tc.ctx, t.TempDir(), []string{"-test.run=^TestRunGoHelperProcess$"}, nil, nil, nil, &out, &out)
		if want := "GOROOT=true:/mirror\nPATH=" + tc.path + "\n"; code != 0 || out.String() != want {
			t.Fatalf("exit %d:\n%s\nwant:\n%s", code, out.String(), want)
		}
	}
}

// Cancellation stops preparation; only a mirror failure runs go test natively.
func TestFallsBackToNativeGo(t *testing.T) {
	mirrorErr := mirrorUnavailable(errors.New("no mirror"))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx  context.Context
		err  error
		want bool
	}{
		{context.Background(), mirrorErr, true},
		{context.Background(), errors.New("other"), false},
		{context.Background(), mirrorUnavailable(context.Canceled), false},
		{context.Background(), mirrorUnavailable(context.DeadlineExceeded), false},
		{canceled, mirrorErr, false},
	} {
		if got := fallsBackToNativeGo(tc.ctx, tc.err); got != tc.want {
			t.Errorf("fallsBackToNativeGo(%v) = %t", tc.err, got)
		}
	}
}

// A module nested in a repository protects the repository: a user cache
// beside the module would leave files in the checkout.
func TestGOROOTMirrorProtectsTheCheckout(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	repo := t.TempDir()
	module := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(module, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	env.GOMOD = filepath.Join(module, "go.mod")
	previous := userCacheDir
	userCacheDir = func() (string, error) { return filepath.Join(repo, ".cache"), nil }
	defer func() { userCacheDir = previous }()
	if _, _, err := gorootMirrorLocation(env, module); err == nil {
		t.Fatal("a cache in the module's checkout was accepted")
	}
	// A symbolic link in the checkout can lead to a module outside it.
	if goruntime.GOOS == "windows" {
		return
	}
	linked := filepath.Join(repo, "linked")
	if err := os.Symlink(t.TempDir(), linked); err != nil {
		t.Fatal(err)
	}
	env.GOMOD = filepath.Join(linked, "go.mod")
	if _, _, err := gorootMirrorLocation(env, linked); err == nil {
		t.Fatal("a cache in the checkout of a linked module was accepted")
	}
	// And a link outside the checkout can lead into it.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Symlink(module, outside); err != nil {
		t.Fatal(err)
	}
	env.GOMOD = filepath.Join(outside, "go.mod")
	if _, _, err := gorootMirrorLocation(env, outside); err == nil {
		t.Fatal("a cache in the checkout of a module reached through a link was accepted")
	}
}

// Builds that use a plan run its toolchain: the mirror's go with its GOROOT.
func TestPlanGoCommand(t *testing.T) {
	if name, env := (Plan{}).GoCommand(); name != "go" || env != nil {
		t.Fatalf("without a mirror: %s %v", name, env)
	}
	mirror := &gorootMirror{toolchain: goToolchain{path: "/mirror/bin/go", root: "/mirror", env: []string{"GOROOT=/mirror", "PATH=/mirror/bin"}}}
	if name, env := (Plan{gorootMirror: mirror}).GoCommand(); name != "/mirror/bin/go" || !slices.Equal(env, []string{"GOROOT=/mirror"}) {
		t.Fatalf("with a mirror: %s %v", name, env)
	}
}

// Windows mirrors copy: deleting a read-only hard link there would clear the
// module cache file's attribute.
func TestGOROOTMirrorCopiesForWindows(t *testing.T) {
	env := fakeModuleCacheToolchainFor(t, "windows")
	useTestUserCache(t)
	root, lock, err := ensureGOROOTMirror(context.Background(), env, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	mirrored, err := os.Stat(filepath.Join(root, "bin", "go.exe"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(filepath.Join(env.GOROOT, "bin", "go.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(mirrored, original) {
		t.Fatal("a Windows mirror shares a file with the module cache")
	}
}

// A testing package in GOMODCACHE, or outside the mirror, would make Go reject
// the overlay: preparation stops and ddto runs go test natively.
func TestCheckTestingGOROOT(t *testing.T) {
	env := fakeModuleCacheToolchain(t)
	mirror := &gorootMirror{root: filepath.Join(t.TempDir(), "mirror")}
	var native *nativeGoError
	for _, tc := range []struct {
		dir    string
		mirror *gorootMirror
		ok     bool
	}{
		{filepath.Join(mirror.root, "src", "testing"), mirror, true},
		{filepath.Join(t.TempDir(), "goroot", "src", "testing"), nil, true},
		{filepath.Join(env.GOROOT, "src", "testing"), nil, false},
		{filepath.Join(t.TempDir(), "other", "src", "testing"), mirror, false},
	} {
		err := checkTestingGOROOT(tc.dir, env, tc.mirror)
		if (err == nil) != tc.ok || err != nil && !errors.As(err, &native) {
			t.Errorf("%s with mirror %t: %v", tc.dir, tc.mirror != nil, err)
		}
	}
}

// ddto wraps a -exec program only when it must restore the caller's settings
// first. go test runs natively for a program that it cannot run, unless it
// starts no test binary, as with -c or -n.
func TestWrapExecProgram(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	mirror := &gorootMirror{saved: []string{goenv.SaveValue("PATH", "/bin", true)}}
	for _, tc := range []struct {
		name     string
		plan     Plan
		opts     options
		fallback bool
		wrapped  bool
	}{
		{"nothing to restore", Plan{}, options{exec: "missing"}, false, false},
		{"run", Plan{gorootMirror: mirror, mini: true}, options{exec: "missing"}, true, false},
		{"-c", Plan{gorootMirror: mirror, mini: true}, options{exec: "missing", compileOnly: true}, false, true},
		{"-n", Plan{gorootMirror: mirror, mini: true}, options{exec: "missing", printOnly: true}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapExecProgram(&tc.plan, tc.opts, t.TempDir())
			var native *nativeGoError
			if errors.As(err, &native) != tc.fallback || !tc.fallback && err != nil || strings.Contains(tc.plan.execWrapper, "'missing'") != tc.wrapped {
				t.Fatalf("wrapExecProgram: %v, wrapper %q", err, tc.plan.execWrapper)
			}
		})
	}
	// A launcher runs go with the mirror's bin first in PATH. Without values to
	// restore, as with the SDK runtime, the wrapper still starts the caller's
	// program by its path, under the name that go test gives it.
	bin := t.TempDir()
	exe := ""
	if goruntime.GOOS == "windows" {
		exe = ".exe"
	}
	program := filepath.Join(bin, "prog"+exe)
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	launcher := &goLauncher{command: []string{"/orchestrion", "go"}}
	for _, tc := range []struct {
		name   string
		plan   Plan
		opts   options
		suffix string
		native bool
	}{
		{"launcher", Plan{gorootMirror: mirror, launcher: launcher}, options{exec: "prog -x"}, "'test-exec' '" + program + "' 'prog' '-x'", false},
		{"launcher, missing", Plan{gorootMirror: mirror, launcher: launcher}, options{exec: "missing"}, "", true},
		{"launcher with -c", Plan{gorootMirror: mirror, launcher: launcher}, options{exec: "missing", compileOnly: true}, "", false},
		{"without a launcher", Plan{gorootMirror: mirror}, options{exec: "prog"}, "", false},
		{"a path", Plan{gorootMirror: mirror, mini: true}, options{exec: "./prog"}, "'test-exec' './prog' './prog'", false},
		{"no program", Plan{gorootMirror: mirror, mini: true}, options{exec: " "}, "", false},
		{"an empty name", Plan{gorootMirror: mirror, mini: true}, options{exec: "''"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapExecProgram(&tc.plan, tc.opts, t.TempDir())
			var native *nativeGoError
			if errors.As(err, &native) != tc.native || !tc.native && err != nil ||
				!strings.HasSuffix(tc.plan.execWrapper, tc.suffix) || (tc.suffix == "") != (tc.plan.execWrapper == "") {
				t.Fatalf("wrapExecProgram: %v, wrapper %q", err, tc.plan.execWrapper)
			}
		})
	}
	// A path that the wrapper cannot quote leaves the run to go test.
	if goruntime.GOOS == "windows" {
		return
	}
	unquotable := filepath.Join(t.TempDir(), `it's "bin"`)
	if err := os.Mkdir(unquotable, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unquotable, "prog"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", unquotable)
	plan := Plan{gorootMirror: mirror, mini: true}
	var native *nativeGoError
	if err := wrapExecProgram(&plan, options{exec: "prog"}, t.TempDir()); !errors.As(err, &native) {
		t.Fatalf("an unquotable program: %v, wrapper %q", err, plan.execWrapper)
	}
}

// After go rejected the command while ddto prepared it, go test runs
// natively and keeps its own status. Only when go test passes, the failure
// was ddto's own, and ddto warns that the tests ran without CI Visibility.
func TestRunGoAfterError(t *testing.T) {
	t.Setenv(fakeToolEnv, "1")
	ctx := context.WithValue(context.Background(), goLauncherKey{}, goLauncher{command: []string{os.Args[0]}})
	cause := errors.New("resolve packages: exit status 1\ngo: -p must be a positive integer: 0")
	for _, status := range []int{0, 1, 2} {
		t.Setenv(fakeToolExitEnv, strconv.Itoa(status))
		var stdout, stderr bytes.Buffer
		code := runGoAfterError(ctx, t.TempDir(), []string{"-p=0", "./..."}, cause, nil, nil, &stdout, &stderr)
		if code != status || stdout.String() != "test\n-p=0\n./...\n" {
			t.Fatalf("exit %d, want %d; stdout %q", code, status, stdout.String())
		}
		warned := strings.Contains(stderr.String(), "WARN: resolve packages: exit status 1 - go test ran without CI Visibility")
		if warned != (status == 0) || strings.Contains(stderr.String(), "positive integer") {
			t.Fatalf("exit %d: stderr %q", status, stderr.String())
		}
	}
	// Cancellation starts no go test.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	var stdout bytes.Buffer
	if code := runGoAfterError(canceled, t.TempDir(), []string{"./..."}, cause, nil, nil, &stdout, io.Discard); code != 2 || stdout.Len() != 0 {
		t.Fatalf("canceled: exit %d, stdout %q", code, stdout.String())
	}
}

// go reports a missing working directory itself, a file given as one, and a
// -modfile without the .mod extension.
func TestMissingGoInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(file, []byte("module example.com/m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir, modfile string
		missing      bool
	}{
		{dir: dir},
		{dir: dir, modfile: "alt.mod"},
		{dir: filepath.Join(dir, "nonexistent"), missing: true},
		{dir: file, missing: true},
		{dir: dir, modfile: "alternate.txt", missing: true},
	} {
		if err := missingGoInput(tc.dir, tc.modfile); (err != nil) != tc.missing {
			t.Errorf("missingGoInput(%q, %q) = %v", tc.dir, tc.modfile, err)
		}
	}
}

// Only go's own rejection of the caller's command or its input runs go test
// natively; a -modfile that an overlay provides is not missing.
func TestGoRejected(t *testing.T) {
	exit := exec.Command(os.Args[0], "-test.run=^$", "-test.count=x").Run()
	var rejected *goRejectedError
	if !errors.As(goRejected(fmt.Errorf("resolve packages: %w", exit)), &rejected) {
		t.Fatalf("go's exit status %v is not a rejection", exit)
	}
	if errors.As(goRejected(errors.New("other")), &rejected) || goRejected(nil) != nil {
		t.Fatal("a failure without go's exit status is a rejection")
	}
	missing := &os.PathError{Op: "open", Path: "missing.mod", Err: os.ErrNotExist}
	if !errors.As(userModfileError(&options{modfile: "missing.mod"}, missing), &rejected) || errors.As(userModfileError(&options{}, missing), &rejected) {
		t.Fatal("userModfileError")
	}
	overlaid := filepath.Join(t.TempDir(), "virtual.mod")
	if _, err := readModuleFile(overlaid, map[string]string{filepath.Clean(overlaid): os.Args[0]}); err != nil {
		t.Fatalf("an overlay's -modfile: %v", err)
	}
}
