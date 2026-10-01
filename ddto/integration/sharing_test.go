package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/runner"
)

func addSharedPackages(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []struct{ dir, name string }{{"alpha", "same"}, {"beta", "same"}, {"gamma", "different"}} {
		path := filepath.Join(dir, p.dir)
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"value.go": "package " + p.name + "\nfunc Value() int { return 1 }\n",
			"value_test.go": "package " + p.name + `
import "testing"
func TestShared(t *testing.T) {
	if Value() != 1 { t.Error("bad value") }
	t.Run("child", func(t *testing.T) {})
}
func TestFailure(t *testing.T) { t.Error("shared diagnostics") }
`,
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestSharedGeneratedFiles(t *testing.T) {
	dir, _ := prepareFixture(t, false)
	addSharedPackages(t, dir)
	t.Setenv("GOFLAGS", "")
	prepare := func() (runner.Plan, runner.Overlay) {
		t.Helper()
		plan, err := runner.Prepare(t.Context(), dir, []string{"./alpha", "./beta", "./gamma"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(plan.Dir); err != nil {
				t.Error(err)
			}
		})
		data, err := os.ReadFile(plan.File)
		if err != nil {
			t.Fatal(err)
		}
		var overlay runner.Overlay
		if err := json.Unmarshal(data, &overlay); err != nil {
			t.Fatal(err)
		}
		return plan, overlay
	}
	plan, overlay := prepare()
	backing := func(overlay runner.Overlay, packageDir string) string {
		t.Helper()
		path := overlay.Replace[filepath.Join(dir, packageDir, "zz_dd_ci_visibility_test.go")]
		if path == "" {
			t.Fatalf("missing logical overlay entry for %s", packageDir)
		}
		return path
	}
	alpha, beta, gamma := backing(overlay, "alpha"), backing(overlay, "beta"), backing(overlay, "gamma")
	if alpha != beta || alpha == gamma {
		t.Fatalf("identical content must share a file; different content must not: %q %q %q", alpha, beta, gamma)
	}
	for path, name := range map[string]string{alpha: "same", gamma: "different"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("package %s_test\nimport _ %q\n", name, "github.com/DataDog/dd-trace-go/v2/civisibility")
		if string(data) != want || filepath.Dir(path) != plan.Dir {
			t.Fatalf("backing content/ownership: %s: %q", path, data)
		}
	}
	otherPlan, otherOverlay := prepare()
	if otherPlan.Dir == plan.Dir || backing(otherOverlay, "alpha") == alpha {
		t.Fatal("separate plans must own separate backing files")
	}
	if err := os.RemoveAll(plan.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(backing(otherOverlay, "alpha")); err != nil {
		t.Fatalf("removing one plan affected another: %v", err)
	}
}

func TestSharedBackingCompatibility(t *testing.T) {
	reference := os.Getenv("ORCHESTRION_BIN")
	if reference != "" {
		var err error
		reference, err = filepath.Abs(reference)
		if err != nil {
			t.Fatal(err)
		}
	}
	dir, driver := prepareFixture(t, reference != "")
	addSharedPackages(t, dir)
	for _, variant := range []struct {
		name  string
		flags []string
	}{
		{"normal", nil},
		{"race", []string{"-race"}},
		{"coverage", []string{"-cover", "-covermode=atomic"}},
	} {
		t.Run(variant.name, func(t *testing.T) {
			args := append([]string{"test", "-count=1", "-run=^TestShared$"}, variant.flags...)
			args = append(args, "./alpha", "./beta")
			got := execute(t, dir, driver, args, true, false)
			if got.code != 0 || len(got.events) == 0 {
				t.Fatalf("shared backing: %d %s\n%s", got.code, got.out, got.stderr)
			}
			for _, name := range []string{"alpha", "beta"} {
				if !strings.Contains(got.out, "example.com/dd-ci-testing-fixture/"+name) || !strings.Contains(strings.Join(got.events, "\n"), "/"+name) {
					t.Fatalf("missing package output/events for %s: %s\n%v", name, got.out, got.events)
				}
			}
			if reference != "" {
				referenceArgs := append([]string{"test", "-toolexec=" + reference + " toolexec"}, args[1:]...)
				want := execute(t, dir, "go", referenceArgs, true, false)
				if got.code != want.code || got.out != want.out || !reflect.DeepEqual(got.events, want.events) {
					t.Fatalf("shared backing differs from Orchestrion: %d/%d\n%s\n%s\n%v\n%v\n%s", got.code, want.code, got.out, want.out, got.events, want.events, want.stderr)
				}
			}
		})
	}
	t.Run("failure-diagnostics", func(t *testing.T) {
		args := []string{"test", "-count=1", "-run=^TestFailure$", "./alpha", "./beta"}
		want, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", args...)
		if code != 1 || strings.Count(want, "shared diagnostics") != 2 || strings.Count(want, "value_test.go:") != 2 {
			t.Fatalf("native failure: %d %s\n%s", code, want, stderr)
		}
		got, stderr, gotCode := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
		if gotCode != code || normalizedOutput(got) != normalizedOutput(want) {
			t.Fatalf("failure diagnostics differ: %d/%d\n%s\n%s\n%s", gotCode, code, got, want, stderr)
		}
	})
}

func TestSharedBackingCollisions(t *testing.T) {
	dir, driver := prepareFixture(t, false)
	addSharedPackages(t, dir)
	// Alpha is processed first, so beta's identical content is already cached.
	logical := filepath.Join(dir, "beta", "zz_dd_ci_visibility_test.go")
	t.Run("physical-file", func(t *testing.T) {
		content := []byte("package same_test\n")
		if err := os.WriteFile(logical, content, 0644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Remove(logical); err != nil {
				t.Error(err)
			}
		})
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "./alpha", "./beta")
		if code != 2 || !strings.Contains(stderr, "generated file already exists: "+logical) {
			t.Fatalf("physical collision: %d %s\n%s", code, out, stderr)
		}
		if data, err := os.ReadFile(logical); err != nil || string(data) != string(content) {
			t.Fatalf("existing file changed: %q %v", data, err)
		}
	})
	for _, fromEnvironment := range []bool{false, true} {
		t.Run(map[bool]string{false: "overlay-flag", true: "overlay-GOFLAGS"}[fromEnvironment], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "replacement.go")
			if err := os.WriteFile(path, []byte("package same_test\n"), 0644); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(runner.Overlay{Replace: map[string]string{logical: path}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(t.TempDir(), "overlay.json")
			if err := os.WriteFile(overlay, data, 0644); err != nil {
				t.Fatal(err)
			}
			args := []string{"test", "./alpha", "./beta"}
			env := testEnv("DD_CIVISIBILITY_ENABLED=false")
			if fromEnvironment {
				env = append(env, "GOFLAGS=-overlay="+overlay)
			} else {
				args = append(args, "-overlay="+overlay)
			}
			out, stderr, code := command(t, dir, env, driver, args...)
			if code != 2 || !strings.Contains(stderr, "generated file conflicts with user overlay: "+logical) {
				t.Fatalf("overlay collision: %d %s\n%s", code, out, stderr)
			}
		})
	}
}
