package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// releaseTag is the version of the release executable that these tests build.
const releaseTag = "v9.8.7-test"

var releaseBuild struct {
	once      sync.Once
	path, err string
}

// releaseExecutable builds the host's release executable once per test
// process, in TestMain's directory.
func releaseExecutable(t *testing.T) string {
	t.Helper()
	releaseBuild.once.Do(func() {
		dist, err := os.MkdirTemp(sharedParityRoot, "release-")
		if err != nil {
			releaseBuild.err = err.Error()
			return
		}
		out, stderr, code, _, err := runCommand("..", testEnv("GOFLAGS="), "go", "run", "./scripts/release", "-version", releaseTag, "-o", dist, "-target", runtime.GOOS+"/"+runtime.GOARCH)
		switch {
		case err != nil:
			releaseBuild.err = err.Error()
		case code != 0:
			releaseBuild.err = out + "\n" + stderr
		default:
			releaseBuild.path = filepath.Join(dist, "ddto-"+runtime.GOOS+"-"+runtime.GOARCH)
			if runtime.GOOS == "windows" {
				releaseBuild.path += ".exe"
			}
		}
	})
	if releaseBuild.path == "" {
		t.Fatalf("release build: %s", releaseBuild.err)
	}
	return releaseBuild.path
}

// A release executable carries the Mini runtime's sources and its tool
// wrapper: it provisions Mini with no module proxy, no copy in GOMODCACHE and
// no build of the wrapper, and reuses both on the next run. It reports the
// release's version, which the wrapper's key includes.
func TestReleaseExecutableIsSelfContained(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	const tag = releaseTag
	driver := releaseExecutable(t)
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

// The install scripts install the host's release executable from a release's
// files after checking SHA256SUMS, keep it on the next run and add its
// directory to GITHUB_PATH: install.sh in Unix shells, install.ps1 in
// PowerShell, Windows included. Git Bash on Windows is left out, as bash there
// can also name WSL's launcher.
func TestInstallScriptsInstallTheReleaseExecutable(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	driver := releaseExecutable(t)
	data, err := os.ReadFile(driver)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Base(driver)
	files := t.TempDir()
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(files, asset), data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	url := "file://" + filepath.ToSlash(files)
	if runtime.GOOS == "windows" {
		url = "file:///" + filepath.ToSlash(files)
	}
	scripts := map[string][]string{}
	if _, err := exec.LookPath("bash"); err == nil && runtime.GOOS != "windows" {
		scripts["install.sh"] = []string{"bash", filepath.Join(root, "scripts", "install.sh")}
	}
	if _, err := exec.LookPath("pwsh"); err == nil {
		scripts["install.ps1"] = []string{"pwsh", "-NoProfile", "-NonInteractive", "-File", filepath.Join(root, "scripts", "install.ps1")}
	}
	if len(scripts) == 0 {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatal("no shell for the install scripts")
		}
		t.Skip("neither bash nor pwsh is available")
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			githubPath := filepath.Join(t.TempDir(), "github_path")
			if err := os.WriteFile(githubPath, nil, 0644); err != nil {
				t.Fatal(err)
			}
			env := testEnv("DDTO_DOWNLOAD_URL="+url, "DDTO_INSTALL_DIR="+dir, "DDTO_VERSION=", "GITHUB_PATH="+githubPath)
			for _, want := range []string{"(release latest)", "(already installed)"} {
				out, stderr, code := command(t, root, env, script[0], script[1:]...)
				if code != 0 || !strings.Contains(stderr, want) {
					t.Fatalf("want %s: %d\n%s\n%s", want, code, out, stderr)
				}
			}
			installed := filepath.Join(dir, "ddto")
			if runtime.GOOS == "windows" {
				installed += ".exe"
			}
			if out, stderr, code := command(t, root, testEnv(), installed, "--version"); code != 0 || out != "ddto "+releaseTag+"\n" {
				t.Fatalf("installed --version: %d %q %s", code, out, stderr)
			}
			// A script can write another spelling of the directory, such as
			// Windows' long form of an 8.3 temporary path.
			want, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(githubPath)
			listed := false
			for _, line := range strings.Split(string(data), "\n") {
				if info, err := os.Stat(strings.TrimSpace(line)); err == nil && os.SameFile(info, want) {
					listed = true
				}
			}
			if err != nil || !listed {
				t.Fatalf("GITHUB_PATH %q, want %s: %v", data, dir, err)
			}
		})
	}
}
