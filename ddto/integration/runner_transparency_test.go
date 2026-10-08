package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBuildFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMiniRuntimeProvisionUsesModuleOverlay(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	helper := t.TempDir()
	writeBuildFixture(t, helper, map[string]string{
		"go.mod":    "module example.com/overlayhelper\n\ngo 1.21\n",
		"helper.go": "package overlayhelper\nconst Value = 7\n",
	})
	for _, language := range []string{"1.21", "1.25.0"} {
		t.Run(language, func(t *testing.T) {
			for _, modfile := range []string{"go.mod", "custom.mod"} {
				t.Run(modfile, func(t *testing.T) {
					// Go resolves its working directory physically. macOS temp paths may
					// contain /var -> /private/var; overlay keys must use that same path.
					dir, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					original := "module example.com/overlayclient\n\ngo " + language + "\n"
					writeBuildFixture(t, dir, map[string]string{
						"go.mod": original, modfile: original,
						"client_test.go": "package overlayclient\nimport (\"testing\"; \"example.com/overlayhelper\")\nfunc TestOverlay(t *testing.T) { if overlayhelper.Value != 7 { t.Fatal(\"overlay helper missing\") } }\n",
					})
					backing := filepath.Join(t.TempDir(), "backing.mod")
					if err := os.WriteFile(backing, []byte(original+fmt.Sprintf("\nrequire example.com/overlayhelper v0.0.0\nreplace example.com/overlayhelper => %q\n", filepath.ToSlash(helper))), 0600); err != nil {
						t.Fatal(err)
					}
					data, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(dir, modfile): backing}})
					if err != nil {
						t.Fatal(err)
					}
					overlay := filepath.Join(t.TempDir(), "overlay.json")
					if err := os.WriteFile(overlay, data, 0600); err != nil {
						t.Fatal(err)
					}
					flags := []string{"-mod=readonly", "-overlay=" + overlay, "-count=1"}
					if modfile != "go.mod" {
						flags = append(flags, "-modfile="+modfile)
					}
					for _, prefix := range [][]string{{"go", "test"}, {driver, "test", "--runtime=mini"}} {
						args := append(append(append([]string(nil), prefix[1:]...), flags...), ".")
						out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), prefix[0], args...)
						if code != 0 {
							t.Fatalf("%v: exit %d\n%s\n%s", prefix, code, out, stderr)
						}
					}
					if after, err := os.ReadFile(filepath.Join(dir, modfile)); err != nil || string(after) != original {
						t.Fatalf("module changed: %q %v", after, err)
					}
				})
			}
		})
	}
}

func TestMiniChdirSymlinkWithUserTool(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":         "module example.com/symbolic\n\ngo 1.25.0\n",
		"client_test.go": "package symbolic\nimport \"testing\"\nfunc TestProbe(t *testing.T){}\n",
	})
	link := filepath.Join(t.TempDir(), "symbolic")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	toolDir := t.TempDir()
	writeBuildFixture(t, toolDir, map[string]string{"main.go": `package main
import ("os";"os/exec")
func main() { c:=exec.Command(os.Args[1],os.Args[2:]...); c.Stdin=os.Stdin; c.Stdout=os.Stdout; c.Stderr=os.Stderr; if err:=c.Run(); err!=nil {if e,ok:=err.(*exec.ExitError);ok {os.Exit(e.ExitCode())};os.Exit(1)} }
`})
	tool := filepath.Join(toolDir, executableName("passthrough"))
	if out, stderr, code := command(t, toolDir, testEnv(), "go", "build", "-o", tool, "main.go"); code != 0 {
		t.Fatal(out, stderr)
	}
	for _, target := range []string{dir, link} {
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-C", target, "-mod=readonly", "-coverpkg=testing", "-count=1", "-toolexec="+quoteToolArgument(t, tool), ".")
		if code != 0 {
			t.Fatalf("-C %s: exit %d\n%s\n%s", target, code, out, stderr)
		}
	}
}

func TestMiniGoleakAbsoluteTargetRetainsCompilerFlags(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	library := t.TempDir()
	// Only compiler invocation is under test; the real goleak runtime has its
	// own integration matrix. Keep this source/API fixture small and offline.
	writeBuildFixture(t, library, map[string]string{
		"go.mod":        "module go.uber.org/goleak\n\ngo 1.25.0\n",
		"leaks.go":      "package goleak\ntype Option func()\nfunc IgnoreAnyFunction(string) Option { return func(){} }\nfunc Find(options ...Option) error { return nil }\n",
		"leaks_test.go": "package goleak\nimport \"testing\"\nfunc TestFind(t *testing.T){if Find()!=nil{t.Fatal(\"Find\")}}\n",
	})
	if out, stderr, code := command(t, dir, testEnv(), "go", "mod", "edit", "-require=go.uber.org/goleak@v1.3.0", "-replace=go.uber.org/goleak="+library); code != 0 {
		t.Fatal(out, stderr)
	}
	for _, prefix := range [][]string{{"go", "test"}, {driver, "test", "--runtime=mini"}} {
		args := append(append([]string(nil), prefix[1:]...), "-mod=readonly", "-gcflags=-N -l", "-x", "-c", "-o", filepath.Join(t.TempDir(), executableName("test")), library)
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), prefix[0], args...)
		if code != 0 {
			t.Fatal(code, out, stderr)
		}
		found := false
		for _, line := range compilerTraceLines(stderr) {
			if strings.Contains(line, " -p go.uber.org/goleak ") {
				found = true
				if !strings.Contains(line, " -N ") || !strings.Contains(line, " -l ") {
					t.Fatalf("compiler flags lost: %s", line)
				}
			}
		}
		if !found {
			t.Fatalf("goleak compilation absent from trace:\n%s", stderr)
		}
	}
}

func TestMiniModModeLeavesModuleFiles(t *testing.T) {
	_, driver := prepareMiniFixture(t)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// go mod tidy keeps a replace without its requirement. With -mod=mod, a
	// query that resolves the runtime would add that requirement to go.mod.
	module := fmt.Sprintf("module example.com/modmode\n\ngo 1.25.0\n\nreplace github.com/tonyredondo/dd-ci-testing-poc => %q\n", filepath.ToSlash(root))
	for _, mode := range []struct {
		name       string
		flags, env []string
	}{
		{"flag", []string{"-mod=mod"}, nil},
		{"GOFLAGS", nil, []string{"GOFLAGS=-buildvcs=false -mod=mod"}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			dir := t.TempDir()
			writeBuildFixture(t, dir, map[string]string{
				"go.mod":         module,
				"client_test.go": "package modmode\nimport \"testing\"\nfunc TestMode(t *testing.T) {}\n",
			})
			for _, prefix := range [][]string{{"go", "test"}, {driver, "test", "--runtime=mini"}} {
				args := append(append(append([]string(nil), prefix[1:]...), mode.flags...), "-count=1", ".")
				out, stderr, code := command(t, dir, testEnv(append([]string{"DD_CIVISIBILITY_ENABLED=false"}, mode.env...)...), prefix[0], args...)
				if code != 0 {
					t.Fatalf("%v: exit %d\n%s\n%s", prefix, code, out, stderr)
				}
				if after, err := os.ReadFile(filepath.Join(dir, "go.mod")); err != nil || string(after) != module {
					t.Fatalf("%v changed go.mod: %q %v", prefix, after, err)
				}
				if _, err := os.Stat(filepath.Join(dir, "go.sum")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%v created go.sum: %v", prefix, err)
				}
			}
		})
	}
}
