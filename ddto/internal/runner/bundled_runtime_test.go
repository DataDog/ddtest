package runner

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/toolwrap"
)

// testRuntimeFiles is a minimal Mini module.
var testRuntimeFiles = map[string]string{
	"go.mod":             "module " + miniModule + "\n\ngo 1.25.0\n",
	"testopt/testopt.go": "package testopt\n",
}

func testRuntimeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A release build extracts its runtime sources once per content into the user
// cache and reuses them. A copy that misses a file, as an interrupted removal
// leaves it, is extracted again.
func TestBundledRuntimeExtractsOnceAndRepairs(t *testing.T) {
	cache := useTestUserCache(t)
	env := testGoEnvironment(t)
	var log bytes.Buffer
	debug := debugTo(t, &log)
	archive := testRuntimeArchive(t, testRuntimeFiles)
	ensure := func(want string) string {
		t.Helper()
		log.Reset()
		root, lock, err := ensureBundledRuntime(t.Context(), env, ".", archive, debug)
		if err != nil {
			t.Fatal(err)
		}
		lock.Close()
		if !strings.Contains(log.String(), "bundled runtime state="+want) {
			t.Fatalf("want state=%s:\n%s", want, log.String())
		}
		return root
	}
	root := ensure("created")
	source := filepath.Join(root, "testopt", "testopt.go")
	if filepath.Dir(root) != filepath.Join(cache, "ddto", "runtimes") || !validMiniSource(root) {
		t.Fatalf("unexpected root %s", root)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != testRuntimeFiles["testopt/testopt.go"] {
		t.Fatalf("source %q, %v", data, err)
	}
	if again := ensure("reused"); again != root {
		t.Fatalf("reused %s, want %s", again, root)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if again := ensure("created"); again != root {
		t.Fatalf("repaired %s, want %s", again, root)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal(err)
	}
}

// Without a usable user cache, the run's temporary directory holds the
// bundled sources.
func TestBundledRuntimeWithoutUserCache(t *testing.T) {
	previous := userCacheDir
	userCacheDir = func() (string, error) { return "", errors.New("no user cache") }
	t.Cleanup(func() { userCacheDir = previous })
	temp := t.TempDir()
	var log bytes.Buffer
	t.Setenv("DD_TRACE_DEBUG", "true")
	ctx := withCLIDebug(t.Context(), &log)
	bundled := &bundledMini{env: testGoEnvironment(t), dir: ".", archive: testRuntimeArchive(t, testRuntimeFiles), temp: temp}
	defer bundled.release()
	if root := bundled.sources(ctx); root != filepath.Join(temp, "runtime") || !validMiniSource(root) {
		t.Fatalf("root %s:\n%s", root, log.String())
	}
	if !strings.Contains(log.String(), "bundled runtime state=temporary") {
		t.Fatalf("missing temporary state:\n%s", log.String())
	}
}

// An archive entry outside the extraction root fails the extraction.
func TestBundledRuntimeRejectsEntriesOutsideItsRoot(t *testing.T) {
	cache := useTestUserCache(t)
	files := map[string]string{"../escape.go": "package escape\n"}
	for name, data := range testRuntimeFiles {
		files[name] = data
	}
	if _, _, err := ensureBundledRuntime(t.Context(), testGoEnvironment(t), ".", testRuntimeArchive(t, files), nil); err == nil {
		t.Fatal("extracted an entry outside its root")
	}
	if _, err := os.Stat(filepath.Join(cache, "ddto", "escape.go")); !os.IsNotExist(err) {
		t.Fatalf("escaped entry written: %v", err)
	}
}

// The CLI's runtime sources come from its checkout, then from the sources that
// a release build carries, then from its exact version in GOMODCACHE.
func TestCLIMiniRootOrder(t *testing.T) {
	useTestUserCache(t)
	_, checkoutFile, _, _ := goruntime.Caller(0)
	checkout := filepath.Dir(filepath.Dir(filepath.Dir(checkoutFile)))
	moduleCache := t.TempDir()
	cached := filepath.Join(moduleCache, miniModule+"@v1.2.3")
	if err := os.MkdirAll(filepath.Join(cached, "testopt"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cached, "go.mod"), []byte(testRuntimeFiles["go.mod"]), 0600); err != nil {
		t.Fatal(err)
	}
	bundled := &bundledMini{env: testGoEnvironment(t), dir: ".", archive: testRuntimeArchive(t, testRuntimeFiles)}
	defer bundled.release()
	ctx := withBundledMini(t.Context(), bundled)
	if got := cliMiniRoot(ctx, checkoutFile, moduleCache, "v1.2.3"); got != checkout {
		t.Fatalf("with a checkout: %s, want %s", got, checkout)
	}
	if got := cliMiniRoot(ctx, "", moduleCache, "v1.2.3"); filepath.Base(filepath.Dir(got)) != "runtimes" || !validMiniSource(got) {
		t.Fatalf("with bundled sources: %s", got)
	}
	if got := cliMiniRoot(t.Context(), "", moduleCache, "v1.2.3"); got != cached {
		t.Fatalf("without bundled sources: %s, want %s", got, cached)
	}
	unusable := &bundledMini{env: testGoEnvironment(t), dir: ".", archive: []byte("not a zip")}
	if got := cliMiniRoot(withBundledMini(t.Context(), unusable), "", moduleCache, "v1.2.3"); got != cached {
		t.Fatalf("with an unusable archive: %s, want %s", got, cached)
	}
}

// A release build writes the wrapper executable it carries instead of building
// one, and builds one when that executable does not run.
func TestToolWrapperInstallsBundledExecutable(t *testing.T) {
	built, err := os.ReadFile(ensureTestToolWrapper(t))
	if err != nil {
		t.Fatal(err)
	}
	previous := bundledToolWrapper
	t.Cleanup(func() { bundledToolWrapper = previous })
	for _, test := range []struct {
		name string
		data []byte
		want []string
	}{
		{"bundled", built, []string{"tool wrapper state=created source=bundle"}},
		{"unusable", []byte("not an executable"), []string{"tool wrapper bundled=unusable", "tool wrapper state=created source=build"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			useTestUserCache(t)
			bundledToolWrapper = test.data
			var log bytes.Buffer
			path, lock, err := ensureToolWrapper(t.Context(), testGoEnvironment(t), ".", debugTo(t, &log))
			if err != nil {
				t.Fatal(err)
			}
			lock.Close()
			for _, want := range test.want {
				if !strings.Contains(log.String(), want) {
					t.Fatalf("want %q:\n%s", want, log.String())
				}
			}
			out, err := exec.Command(path, toolwrap.KeyFlag).Output()
			if err != nil || strings.TrimSpace(string(out)) != toolWrapperKey() {
				t.Fatalf("key %q, %v", out, err)
			}
		})
	}
}
