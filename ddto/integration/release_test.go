package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// A release executable carries the Mini runtime's sources and its tool
// wrapper: it provisions Mini with no module proxy, no copy in GOMODCACHE and
// no build of the wrapper, and reuses both on the next run. It reports the
// release's version, which the wrapper's key includes.
func TestReleaseExecutableIsSelfContained(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	const tag = "v9.8.7-test"
	dist := t.TempDir()
	out, stderr, code := command(t, root, testEnv("GOFLAGS="), "go", "run", "./scripts/release", "-version", tag, "-o", dist, "-target", runtime.GOOS+"/"+runtime.GOARCH)
	if code != 0 {
		t.Fatalf("release build: %d\n%s\n%s", code, out, stderr)
	}
	driver := filepath.Join(dist, "ddto-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		driver += ".exe"
	}
	if out, stderr, code := command(t, root, testEnv(), driver, "--version"); code != 0 || out != "ddto "+tag+"\n" {
		t.Fatalf("--version: %d %q %s", code, out, stderr)
	}
	var requests atomic.Int64
	proxy := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "a release executable should not need the module proxy", http.StatusForbidden)
	}))
	defer proxy.Close()
	dir := t.TempDir()
	original := "module example.com/release\n\ngo 1.25.0\n"
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":          original,
		"release_test.go": "package release\nimport \"testing\"\nfunc TestRelease(t *testing.T) {}\n",
	})
	cache := t.TempDir()
	env := testEnv(append(isolatedUserCache(t, cache), "DD_CIVISIBILITY_ENABLED=false", "DD_TRACE_DEBUG=true",
		"GOPROXY="+proxy.URL, "GONOPROXY=none", "GOMODCACHE="+t.TempDir(), "GOFLAGS=")...)
	for _, state := range []string{"created", "reused"} {
		// Covering testing runs go's compiles through the tool wrapper.
		out, stderr, code := command(t, dir, env, driver, "test", "-count=1", "-coverpkg=testing,./...", "-covermode=atomic", ".")
		if code != 0 || !strings.Contains(out, "ok ") {
			t.Fatalf("%s run: %d\n%s\n%s", state, code, out, stderr)
		}
		var debug []string
		for _, line := range strings.Split(stderr, "\n") {
			if strings.Contains(line, "TestOptimization.build "+tag+" DEBUG: ") {
				debug = append(debug, line)
			}
		}
		logs := strings.Join(debug, "\n")
		wrapper := "tool wrapper state=reused"
		if state == "created" {
			wrapper = "tool wrapper state=created source=bundle"
		}
		for _, want := range []string{"bundled runtime state=" + state, "mini source=cli-local", wrapper} {
			if !strings.Contains(logs, want) {
				t.Fatalf("%s run lacks %q:\n%s", state, want, logs)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("the module proxy received %d requests", requests.Load())
	}
	if contents, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(contents) != original {
		t.Fatalf("client go.mod changed: %s, %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("client go.sum was created: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(userCacheIn(cache), "ddto", "runtimes")); err != nil || len(entries) == 0 {
		t.Fatalf("no extracted runtime: %v", err)
	}
}
