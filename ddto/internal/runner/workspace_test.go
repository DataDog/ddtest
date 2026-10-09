package runner

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	work, lock, err := provideMiniVendorWorkspace(t.Context(), root, root, t.TempDir(), "", nil)
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
