package runner

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestWorkspaceProvisionResolvesExistingMainModules(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"client", "helper"} {
		if err := os.Mkdir(filepath.Join(root, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for file, data := range map[string]string{
		"go.work":          "go 1.25.0\nuse(\n./client\n./helper\n)\n",
		"client/go.mod":    "module example.com/client\ngo 1.21\nrequire example.com/helper v0.0.0\n",
		"helper/go.mod":    "module example.com/helper\ngo 1.21\n",
		"helper/helper.go": "package helper\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	work, err := provideMiniWorkspace(t.Context(), filepath.Join(root, "client"), filepath.Join(root, "go.work"), t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-e", "-json", miniPackage)
	cmd.Dir = filepath.Join(root, "client")
	cmd.Env = append(cmd.Environ(), "GOWORK="+work, "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(work)
	t.Logf("workspace:\n%s\npackage:\n%s", data, out)
	if err != nil {
		t.Fatal(err)
	}
	var pkg goPackage
	if err := json.Unmarshal(out, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Error != nil || pkg.ImportPath != miniPackage || pkg.Dir == "" {
		t.Fatalf("runtime not resolved: %+v", pkg)
	}
}

// A module proxy fixture checks actual Go selection without external requests.
func TestWorkspaceProvisionKeepsSelectedRuntime(t *testing.T) {
	for _, replacement := range []string{"none", "workspace", "client"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			client := filepath.Join(root, "client")
			proxy := filepath.Join(root, "proxy")
			cache := filepath.Join(root, "cache")
			t.Cleanup(func() {
				// Go extracts module directories readonly; restore only this fixture's
				// directory permissions so testing can remove its private cache.
				_ = filepath.WalkDir(cache, func(path string, entry fs.DirEntry, err error) error {
					if err == nil && entry.IsDir() {
						return os.Chmod(path, 0700)
					}
					return err
				})
			})
			if err := os.MkdirAll(client, 0700); err != nil {
				t.Fatal(err)
			}
			path, version := miniModule, "v0.1.0"
			if replacement != "none" {
				path, version = "example.com/mini-fork", "v0.2.0"
			}
			versionDir := filepath.Join(proxy, filepath.FromSlash(path), "@v")
			if err := os.MkdirAll(versionDir, 0700); err != nil {
				t.Fatal(err)
			}
			mod := "module " + miniModule + "\ngo 1.21\n"
			for file, data := range map[string]string{version + ".mod": mod, version + ".info": fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, version), "list": version + "\n"} {
				if err := os.WriteFile(filepath.Join(versionDir, file), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file, err := os.Create(filepath.Join(versionDir, version+".zip"))
			if err != nil {
				t.Fatal(err)
			}
			archive := zip.NewWriter(file)
			for name, data := range map[string]string{"go.mod": mod, "testopt/testopt.go": "package testopt\n"} {
				w, err := archive.Create(path + "@" + version + "/" + name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.Write([]byte(data)); err != nil {
					t.Fatal(err)
				}
			}
			if err = archive.Close(); err != nil {
				t.Fatal(err)
			}
			if err = file.Close(); err != nil {
				t.Fatal(err)
			}
			proxyPath := filepath.ToSlash(proxy)
			if !strings.HasPrefix(proxyPath, "/") {
				proxyPath = "/" + proxyPath
			}
			t.Setenv("GOPROXY", (&url.URL{Scheme: "file", Path: proxyPath}).String())
			t.Setenv("GOSUMDB", "off")
			t.Setenv("GOMODCACHE", cache)
			clientMod := "module example.com/client\ngo 1.21\nrequire " + miniModule + " v0.1.0\n"
			work := "go 1.25.0\nuse ./client\n"
			replace := "replace " + miniModule + " => " + path + " " + version + "\n"
			if replacement == "workspace" {
				work += replace
			}
			if replacement == "client" {
				clientMod += replace
			}
			original := filepath.Join(root, "go.work")
			t.Setenv("GOWORK", original)
			for file, data := range map[string]string{original: work, filepath.Join(client, "go.mod"): clientMod} {
				if err := os.WriteFile(file, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			target, err := provideMiniWorkspace(t.Context(), client, original, t.TempDir(), "", nil)
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			selected := filepath.ToSlash(filepath.Join(cache, path+"@"+version))
			if !strings.Contains(filepath.ToSlash(string(contents)), selected) {
				t.Fatalf("did not retain selected module %s: %s", selected, contents)
			}
			for file, want := range map[string]string{original: work, filepath.Join(client, "go.mod"): clientMod} {
				if got, err := os.ReadFile(file); err != nil || string(got) != want {
					t.Fatalf("changed %s: %s %v", file, got, err)
				}
			}
			for _, file := range []string{original + ".sum", filepath.Join(client, "go.sum")} {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatalf("created caller checksum %s: %v", file, err)
				}
			}
		})
	}
}

func TestVendorSnapshotPreservesSourceOverlaysAndSymlinkFiles(t *testing.T) {
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "original.go")
	if err := os.WriteFile(original, []byte("package fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, filepath.Join(source, "linked.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "modules.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	virtual := filepath.Join(source, "virtual.go")
	replacements := map[string]string{virtual: original}
	if err := snapshotVendor(source, target, []byte("## workspace\n"), replacements); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "linked.go")); err != nil || string(got) != "package fixture\n" {
		t.Fatalf("symlink contents: %s %v", got, err)
	}
	if replacements[filepath.Join(target, "virtual.go")] != original {
		t.Fatal("virtual source overlay lost")
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(filepath.Join(target, "linked.go")); err != nil {
		t.Fatalf("snapshot still relies on symlink target: %v", err)
	}
}

func TestWorkspaceNewerRuntimePreservesProgramDefaults(t *testing.T) {
	// A go.work without a go directive means Go 1.18 to the go command. Subtest
	// names stay out of the expected text: they appear in temporary paths.
	for _, tc := range []struct{ name, directive, client, want string }{
		{"declared", "go 1.21\n", "1.21", "godebug default=go1.21\n"},
		{"missing", "", "1.18", "godebug default=go1.18\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { testWorkspaceProgramDefaults(t, tc.directive, tc.client, tc.want) })
	}
}

func testWorkspaceProgramDefaults(t *testing.T, directive, clientGo, want string) {
	t.Setenv("GOPROXY", "off")
	root := t.TempDir()
	client := filepath.Join(root, "client")
	runtimeRoot := filepath.Join(root, "mini")
	for _, dir := range []string{client, filepath.Join(runtimeRoot, "testopt")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(root, "go.work")
	for path, data := range map[string]string{
		work:                            directive + "use ./client\nreplace " + miniModule + " => ./mini\n",
		filepath.Join(client, "go.mod"): "module example.com/client\ngo " + clientGo + "\n",
		// Go 1.27 no longer selects timer channels from GODEBUG; panicnil remains.
		filepath.Join(client, "main.go"):                    "package main\nimport(\"fmt\";\"time\")\nfunc main(){timer:=time.NewTimer(time.Hour);defer timer.Stop();fmt.Println(cap(timer.C));defer func(){fmt.Printf(\"%T\\n\",recover())}();panic(nil)}\n",
		filepath.Join(runtimeRoot, "go.mod"):                "module " + miniModule + "\ngo 1.25.0\n",
		filepath.Join(runtimeRoot, "testopt", "testopt.go"): "package testopt\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(work string) string {
		cmd := exec.Command("go", "run", ".")
		cmd.Dir = client
		cmd.Env = append(cmd.Environ(), "GOWORK="+work)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		return string(out)
	}
	native := run(work)
	supplied, err := provideMiniWorkspace(t.Context(), client, work, t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(supplied); got != native {
		t.Fatalf("program defaults changed: native=%s supplied=%s", native, got)
	}
	data, err := os.ReadFile(supplied)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go 1.25.0") || !strings.Contains(string(data), want) {
		t.Fatalf("workspace does not preserve defaults: %s", data)
	}
}

func TestFormattedGoDebug(t *testing.T) {
	text := "module example.com/client\ngo 1.21\ngodebug (\n\t// A runtime override.\n\tdefault=go1.25 // Keep modern timers.\n\t\"asynctimerchan=0\"\n)\nreplace example.com/helper => ../godebug\n"
	got, err := formattedGoDebug(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (goDebugSetting{"default", "go1.25"}) || got[1] != (goDebugSetting{"asynctimerchan", "0"}) {
		t.Fatalf("runtime defaults: %+v", got)
	}
}

func TestVendorWorkspaceKeepsNativeModuleRoot(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "vendor"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(root, "go.mod"):                "module example.com/nativevendor\ngo 1.21\n",
		filepath.Join(root, "vendor", "modules.txt"): "",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	work, lock, err := provideMiniVendorWorkspace(t.Context(), root, root, t.TempDir(), "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		defer lock.Close()
	}
	out, err := goTool(t.Context(), root, nil, "work", "edit", "-json", work)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct{ Use []struct{ DiskPath string } }
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	for _, use := range parsed.Use {
		if use.DiskPath == root {
			return
		}
	}
	t.Fatalf("native module root %s missing from workspace: %s", strconv.Quote(root), out)
}

// Mini is a main module of a workspace that ddto provides, where go's work
// pattern would also select its packages.
func TestMiniWorkPatternRunsNatively(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "client")
	if err := os.Mkdir(client, 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{
		"go.work":               "go 1.25.0\nuse ./client\n",
		"client/go.mod":         "module example.com/client\ngo 1.21\n",
		"client/client_test.go": "package client\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	for _, tc := range []struct {
		goflags string
		args    []string
		bypass  bool
	}{
		{"", []string{"work"}, true},
		{"", []string{"-coverpkg=work", "."}, true},
		{"", []string{"-gcflags= work =-lang=go1.21", "."}, true},
		{"-ldflags=work=-s", []string{"."}, true},
		{"", []string{"-gcflags=-d=work=1", "."}, false},
		{"", []string{"."}, false},
	} {
		t.Setenv("GOFLAGS", tc.goflags)
		args := tc.args
		plan, err := PrepareRuntime(t.Context(), client, args, Mini)
		if plan.Dir != "" {
			plan.Release()
			_ = os.RemoveAll(plan.Dir)
		}
		var native *nativeGoError
		bypassed := errors.As(err, &native) && native.bypass == "package_pattern=work"
		if bypassed != tc.bypass {
			t.Fatalf("%q %q: bypass=%t, want %t: %v", tc.goflags, args, bypassed, tc.bypass, err)
		}
		if !bypassed && err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
}

// Standard library tests, as go test fmt or all selects them, run natively.
func TestMiniStandardLibraryTestsRunNatively(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	goroot := strings.TrimSpace(string(out))
	root := t.TempDir()
	for file, data := range map[string]string{
		"go.mod":         "module example.com/client\ngo 1.21\n",
		"client_test.go": "package client\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOFLAGS", "")
	for _, tc := range []struct {
		work string
		args []string
	}{
		{"off", []string{"errors"}},
		// ddto lists testing for every run; selected, its tests run natively.
		{"off", []string{"testing"}},
		{"off", []string{filepath.Join(goroot, "src", "testing")}},
		{"off", []string{"testing/"}},
		// A workspace that ddto provides makes Mini a main module, whose
		// standard dependencies all then selects.
		{"workspace", []string{"all"}},
	} {
		gowork := "off"
		if tc.work == "workspace" {
			gowork = filepath.Join(root, "go.work")
			if err := os.WriteFile(gowork, []byte("go 1.25.0\nuse .\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("GOWORK", gowork)
		plan, err := PrepareRuntime(t.Context(), root, tc.args, Mini)
		if plan.Dir != "" {
			plan.Release()
			_ = os.RemoveAll(plan.Dir)
		}
		var native *nativeGoError
		if !errors.As(err, &native) || native.bypass != "stdlib_tests" {
			t.Fatalf("%s %q: %v; want the stdlib_tests bypass", tc.work, tc.args, err)
		}
	}
}

// Outside a module, go test runs natively, as for go test errors.
func TestMiniOutsideAModuleRunsNatively(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GO111MODULE", "")
	plan, err := PrepareRuntime(t.Context(), t.TempDir(), []string{"errors"}, Mini)
	if plan.Dir != "" {
		plan.Release()
		_ = os.RemoveAll(plan.Dir)
	}
	var native *nativeGoError
	if !errors.As(err, &native) || native.bypass != "no_module" {
		t.Fatalf("%v; want the no_module bypass", err)
	}
}

// A relative TMPDIR still gives the plan an absolute directory, which its
// temporary go.work needs.
func TestMiniRelativeTemporaryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.TempDir resolves a relative TMP from each process's directory
		// there, so go's own commands, which run in the module, take the
		// module as the system temp root and ignore its go.mod, under native
		// go test too. Elsewhere it stays relative, which ddto resolves.
		t.Skip("a relative TMP on Windows names each process's directory")
	}
	root := t.TempDir()
	module := filepath.Join(root, "client")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{
		"go.mod":         "module example.com/client\ngo 1.21\n",
		"client_test.go": "package client\n",
	} {
		if err := os.WriteFile(filepath.Join(module, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, ".")
	}
	plan, err := PrepareRuntime(t.Context(), module, []string{"."}, Mini)
	if plan.Dir != "" {
		plan.Release()
		_ = os.RemoveAll(plan.Dir)
	}
	if err != nil || !filepath.IsAbs(plan.Dir) {
		t.Fatalf("plan directory %q: %v", plan.Dir, err)
	}
}

// A vendor directory without modules.txt, which go reads as empty, runs
// natively when go selects it without the runtime, or when an older module
// can import a package from it; otherwise ddto's workspace loses nothing. An
// overlay with two spellings of one path is go's error, which go test reports.
func TestMiniVendorWithoutRuntimeAndDuplicateOverlay(t *testing.T) {
	root := t.TempDir()
	for file, data := range map[string]string{
		"go.mod":         "module example.com/client\ngo 1.25.0\n",
		"client.go":      "package client\n",
		"client_test.go": "package client\n",
		"vendor/.keep":   "",
		"_a.go":          "package client\n",
		"_b.go":          "package client\n",
		"overlay.json":   `{"Replace":{"client.go":"_a.go","./client.go":"_b.go"}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	prepare := func(args ...string) error {
		plan, err := PrepareRuntime(t.Context(), root, args, Mini)
		if plan.Dir != "" {
			plan.Release()
			_ = os.RemoveAll(plan.Dir)
		}
		return err
	}
	var native *nativeGoError
	if err := prepare("-mod=vendor", "."); !errors.As(err, &native) || native.bypass != "vendor_without_runtime" {
		t.Errorf("vendor without modules.txt: %v; want the vendor_without_runtime bypass", err)
	}
	// An older client too, which can import vendored packages without
	// modules.txt: ddto's workspace would not find them.
	vendored := filepath.Join(root, "vendor", "example.com", "helper")
	if err := os.MkdirAll(vendored, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendored, "helper.go"), []byte("package helper\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Without a go directive, which go reads as 1.16, only -mod=vendor selects
	// the directory.
	for _, tc := range []struct{ goMod, args string }{
		{"module example.com/client\ngo 1.21\n", "-count=1"},
		{"module example.com/client\ngo 1.21\n", "-mod=vendor"},
		{"module example.com/client\n", "-mod=vendor"},
	} {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(tc.goMod), 0600); err != nil {
			t.Fatal(err)
		}
		if err := prepare(tc.args, "."); !errors.As(err, &native) || native.bypass != "vendor_without_runtime" {
			t.Errorf("%q %s, a vendored package without modules.txt: %v; want the vendor_without_runtime bypass", tc.goMod, tc.args, err)
		}
	}
	// From Go 1.23, go ignores a vendored package that modules.txt does not
	// list, and a vendor directory without Go files, as one of web assets,
	// has none: the workspace loses nothing, and the tests are instrumented.
	for _, tc := range []struct {
		goVersion string
		goFile    bool
	}{{"1.23", true}, {"1.24", true}, {"1.21", false}, {"1.24", false}} {
		if !tc.goFile {
			if err := os.RemoveAll(filepath.Join(root, "vendor", "example.com")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "vendor", "app.js"), []byte("\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/client\ngo "+tc.goVersion+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"."}, {"-mod=vendor", "."}} {
			if err := prepare(args...); err != nil {
				t.Errorf("go %s %v, vendored Go files %t: %v; want an instrumented plan", tc.goVersion, args, tc.goFile, err)
			}
		}
	}
	// go rejects a requirement or a replacement that the missing modules.txt
	// does not list, as inconsistent vendoring, whatever the vendor directory
	// holds; the workspace would not.
	for _, directive := range []string{"require example.com/dep v1.0.0\n", "replace example.com/dep => ./dep\n", "require(\n\texample.com/dep v1.0.0\n)\n", "replace(\n\texample.com/dep => ./dep\n)\n", "require ()\n"} {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/client\ngo 1.24\n"+directive), 0600); err != nil {
			t.Fatal(err)
		}
		err := prepare(".")
		if directive == "require ()\n" {
			if err != nil {
				t.Errorf("go 1.24 with an empty require block: %v; want an instrumented plan", err)
			}
		} else if !errors.As(err, &native) || native.bypass != "vendor_without_runtime" {
			t.Errorf("go 1.24 with %q, a vendor directory without modules.txt: %v; want the vendor_without_runtime bypass", directive, err)
		}
	}
	// A linked asset is no package, while go reads packages through a linked
	// directory.
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "lib.js"), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "assets", "lib.js"), filepath.Join(root, "vendor", "lib.js")); err != nil {
		t.Logf("no symbolic link: %v", err)
	} else {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/client\ngo 1.21\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := prepare("."); err != nil {
			t.Errorf("go 1.21, a linked asset in vendor: %v; want an instrumented plan", err)
		}
		if err := os.Symlink(filepath.Join("..", "assets"), filepath.Join(root, "vendor", "linked")); err != nil {
			t.Fatal(err)
		}
		if err := prepare("."); !errors.As(err, &native) || native.bypass != "vendor_without_runtime" {
			t.Errorf("go 1.21, a linked directory in vendor: %v; want the vendor_without_runtime bypass", err)
		}
	}
	// An overlay that deletes go.mod gives go another module root, or none.
	if err := os.WriteFile(filepath.Join(root, "delete.json"), []byte(`{"Replace":{"go.mod":""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepare("-overlay=delete.json", "."); !errors.As(err, &native) || native.bypass != "overlay_deletes_go_mod" {
		t.Errorf("an overlay that deletes go.mod: %v; want the overlay_deletes_go_mod bypass", err)
	}
	// A client older than Go 1.25 gets a workspace whose overlay ddto writes,
	// so go itself would not see the duplicate.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/client\ngo 1.21\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "vendor")); err != nil {
		t.Fatal(err)
	}
	var rejected *goRejectedError
	if err := prepare("-overlay=overlay.json", "."); !errors.As(err, &rejected) {
		t.Errorf("duplicate overlay paths: %v; want go's rejection", err)
	}
}

// A workspace whose runtime ddto cannot resolve, as offline with a vendored
// one, runs natively: go test may still build it.
func TestMiniUnresolvedWorkspaceRuntimeRunsNatively(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "client"), 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{
		"go.work":               "go 1.25.0\nuse ./client\n",
		"client/go.mod":         "module example.com/client\ngo 1.25.0\nrequire " + miniModule + " v0.9.9\n",
		"client/client_test.go": "package client\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Cleanup(func() {
		// Go extracts module directories readonly.
		_ = filepath.WalkDir(os.Getenv("GOMODCACHE"), func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				return os.Chmod(path, 0700)
			}
			return err
		})
	})
	plan, err := PrepareRuntime(t.Context(), filepath.Join(root, "client"), []string{"."}, Mini)
	if plan.Dir != "" {
		plan.Release()
		_ = os.RemoveAll(plan.Dir)
	}
	var native *nativeGoError
	if !errors.As(err, &native) || native.bypass != "workspace_runtime_unavailable" {
		t.Fatalf("%v; want the workspace_runtime_unavailable bypass", err)
	}
}
