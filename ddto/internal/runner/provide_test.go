package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyModuleFileUsesOverlayContentsAndDeletion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "go.sum", "alternate.mod", "alternate.sum"} {
		t.Run(name, func(t *testing.T) {
			logical, backing, output := filepath.Join(dir, name), filepath.Join(dir, name+".backing"), filepath.Join(dir, name+".copy")
			for path, contents := range map[string]string{logical: "physical", backing: "overlaid"} {
				if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := copyModuleFile(logical, output, map[string]string{logical: backing}); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(output); err != nil || string(data) != "overlaid" {
				t.Fatalf("copied %q, %v", data, err)
			}
			if err := copyModuleFile(logical, output, map[string]string{logical: ""}); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted overlay: %v", err)
			}
		})
	}
}

// A Windows checkout can convert go.mod to CRLF line endings.
func TestModulePathToleratesLineEndingsAndComments(t *testing.T) {
	for _, data := range []string{
		"module " + miniModule + "\n\ngo 1.25.0\n",
		"module " + miniModule + "\r\n\r\ngo 1.25.0\r\n",
		"// comment\r\nmodule \"" + miniModule + "\"\r\n",
	} {
		if got := modulePath([]byte(data)); got != miniModule {
			t.Errorf("modulePath(%q) = %q", data, got)
		}
	}
	if got := modulePath([]byte("go 1.25.0\n")); got != "" {
		t.Errorf("module path without a directive: %q", got)
	}
}

func TestMiniSourceRoot(t *testing.T) {
	cache := t.TempDir()
	version := "v0.0.0-20261006182246-a8e3f52b0606"
	checkout := t.TempDir()
	cached := filepath.Join(cache, miniModule+"@"+version)
	for _, root := range []string{checkout, cached} {
		if err := os.MkdirAll(filepath.Join(root, "testopt"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+miniModule+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, source, version, want string }{
		{"checkout", filepath.Join(checkout, "internal", "runner", "provide.go"), version, checkout},
		{"installed", filepath.Join(cached, "internal", "runner", "provide.go"), version, cached},
		{"trimpath", miniModule + "@" + version + "/internal/runner/provide.go", version, cached},
		{"missing-checkout", filepath.Join(t.TempDir(), "internal", "runner", "provide.go"), version, cached},
		{"other-version", "internal/runner/provide.go", "v1.0.0", ""},
		{"development-trimpath", "internal/runner/provide.go", "(devel)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := miniSourceRoot(tc.source, cache, tc.version); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module example.com/unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if validMiniSource(checkout) {
		t.Fatal("accepted another module as Mini")
	}
	if err := os.Remove(filepath.Join(cached, "testopt")); err != nil {
		t.Fatal(err)
	}
	if validMiniSource(cached) {
		t.Fatal("accepted an incomplete source directory")
	}
}

func TestRequireMiniPrefersLocalSourcesAndClientReplacement(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprintf("client-replace=%t", replacement), func(t *testing.T) {
			dir := t.TempDir()
			modfile := filepath.Join(dir, "go.mod")
			module := "module example.com/client\n\ngo 1.25.0\n"
			if replacement {
				module += "replace " + miniModule + " v1.2.3 => ./client-choice\n"
			}
			if err := os.WriteFile(modfile, []byte(module), 0600); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dir, "local sources")
			for _, source := range []string{root, filepath.Join(dir, "client-choice")} {
				if err := os.MkdirAll(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module "+miniModule+"\ngo 1.25.0\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var progress bytes.Buffer
			// A published version must not cause a lookup before a usable local
			// source. GOPROXY=off makes that regression fail without networking.
			if err := requireMini(t.Context(), dir, modfile, root, "v999.0.0", &progress); err != nil {
				t.Fatal(err)
			}
			if progress.Len() != 0 {
				t.Fatalf("unexpected go get: %s", &progress)
			}
			out, err := goTool(t.Context(), dir, nil, "mod", "edit", "-json", "-modfile="+modfile)
			if err != nil {
				t.Fatal(err)
			}
			var parsed struct {
				Require []struct{ Path, Version string }
				Replace []struct {
					Old, New struct{ Path, Version string }
				}
			}
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatal(err)
			}
			wantPath, wantVersion := root, "v0.0.0"
			if replacement {
				wantPath, wantVersion = "./client-choice", "v1.2.3"
			}
			if len(parsed.Require) != 1 || parsed.Require[0].Path != miniModule || parsed.Require[0].Version != wantVersion || len(parsed.Replace) != 1 || parsed.Replace[0].New.Path != wantPath {
				t.Fatalf("unexpected module selection: %s", out)
			}
		})
	}
}

// The selected runtime's minimum matters; the CLI may use a newer toolchain.
func TestRequireLocalMiniGoVersion(t *testing.T) {
	for _, tc := range []struct{ client, runtime, want string }{
		{"1.25.0", "1.21.0", "1.25.0"},
		{"1.26.1", "1.21.0", "1.26.1"},
		{"1.21.0", "1.26.0", "1.21.0"},
		{"", "1.26.0", ""},
	} {
		t.Run(tc.client+"-"+tc.runtime, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "runtime")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+miniModule+"\r\ngo "+tc.runtime+" // selected runtime\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			modfile := filepath.Join(dir, "temporary.mod")
			contents := "module example.com/client\n"
			if tc.client != "" {
				contents += "go " + tc.client + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(modfile, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			err := requireLocalMini(t.Context(), dir, modfile, "./runtime", "v0.0.0", tc.client, true)
			needsNewer := compareGoVersion(tc.client, tc.runtime) < 0
			if (err != nil) != needsNewer {
				t.Fatalf("newer=%t, error=%v", needsNewer, err)
			}
			got, err := os.ReadFile(modfile)
			if err != nil {
				t.Fatal(err)
			}
			if version := moduleDirective(got, "go"); version != tc.want {
				t.Fatalf("Go directive %q, want %q", version, tc.want)
			}
		})
	}
}

func TestProvideMiniRelativeReplacementFromSubdirectory(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	dir := t.TempDir()
	for _, child := range []string{"sub", "runtime"} {
		if err := os.Mkdir(filepath.Join(dir, child), 0700); err != nil {
			t.Fatal(err)
		}
	}
	original := "module example.com/client\n\ngo 1.25.0\nreplace " + miniModule + " => ./runtime\n"
	for path, contents := range map[string]string{
		filepath.Join(dir, "go.mod"):            original,
		filepath.Join(dir, "runtime", "go.mod"): "module " + miniModule + "\n\ngo 1.21.0\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	modfile, err := provideRuntime(t.Context(), filepath.Join(dir, "sub"), options{}, Mini, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(modfile)
	if err != nil || moduleDirective(contents, "go") != "1.25.0" {
		t.Fatalf("temporary Go directive: %s, %v", contents, err)
	}
	if contents, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(contents) != original {
		t.Fatalf("client module changed: %s, %v", contents, err)
	}
}

func TestPreprovideMiniRespectsEffectiveModuleAndWorkspace(t *testing.T) {
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	for _, scenario := range []string{"absent", "required", "modfile", "overlay", "workspace", "escaped"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			bare := "module example.com/preprovide\n\ngo 1.25.0\n"
			required := bare + "require " + miniModule + " v0.0.0\n"
			contents := bare
			if scenario == "required" {
				contents = required
			}
			if scenario == "escaped" {
				contents += "// conservative fallback \\ notation\n"
			}
			path := filepath.Join(dir, "go.mod")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			opts := options{}
			replacements := map[string]string{}
			if scenario == "modfile" || scenario == "overlay" {
				selected := filepath.Join(dir, "selected.mod")
				if err := os.WriteFile(selected, []byte(required), 0600); err != nil {
					t.Fatal(err)
				}
				if scenario == "modfile" {
					opts.modfile = selected
					opts.buildFlags = []string{"-modfile=" + selected}
				} else {
					replacements[path] = selected
				}
			}
			if scenario == "workspace" {
				root := filepath.Join(dir, "runtime")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+miniModule+"\ngo 1.25.0\n"), 0600); err != nil {
					t.Fatal(err)
				}
				work := filepath.Join(dir, "go.work")
				if err := os.WriteFile(work, []byte("go 1.25.0\nuse (\n.\n./runtime\n)\n"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOWORK", work)
			}
			modfile, err := preprovideMini(t.Context(), dir, opts, t.TempDir(), replacements, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (modfile != "") != (scenario == "absent") {
				t.Fatalf("scenario=%s modfile=%s", scenario, modfile)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != contents {
				t.Fatalf("client module changed: %s %v", got, err)
			}
		})
	}
}

func TestPreprovideMiniPreservesTransitiveSelectionAndChecksums(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GONOPROXY", "none")
	t.Setenv("GOMODCACHE", t.TempDir())
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + miniModule + "/@v/v1.2.3.mod":
			fmt.Fprintf(w, "module %s\ngo 1.25.0\n", miniModule)
		case "/" + miniModule + "/@v/v1.2.3.info":
			fmt.Fprint(w, `{"Version":"v1.2.3","Time":"2026-10-06T00:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer proxy.Close()
	t.Setenv("GOPROXY", proxy.URL)
	dir := t.TempDir()
	bridge := filepath.Join(dir, "bridge")
	if err := os.Mkdir(bridge, 0700); err != nil {
		t.Fatal(err)
	}
	original := "module example.com/transitive\ngo 1.25.0\nrequire example.com/bridge v0.0.0\nreplace example.com/bridge => ./bridge\n"
	for file, contents := range map[string]string{
		filepath.Join(dir, "go.mod"):    original,
		filepath.Join(bridge, "go.mod"): "module example.com/bridge\ngo 1.25.0\nrequire " + miniModule + " v1.2.3\n",
	} {
		if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	modfile, err := preprovideMini(t.Context(), dir, options{}, t.TempDir(), nil, nil)
	if err != nil || modfile != "" {
		t.Fatalf("replaced a transitive runtime: %s %v", modfile, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(got) != original {
		t.Fatalf("client module changed: %s %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("module probe created client checksums: %v", err)
	}
}
