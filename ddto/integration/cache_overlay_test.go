package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResultCache(t *testing.T) {
	dir, driver := prepareFixture(t, false)
	env := testEnv("DD_CIVISIBILITY_ENABLED=false")
	for _, tool := range []string{"go", driver} {
		t.Run(filepath.Base(tool), func(t *testing.T) {
			for i := 0; i < 2; i++ {
				out, stderr, code := command(t, dir, env, tool, "test", "-run=^TestPass$", ".")
				if code != 0 {
					t.Fatalf("run %d: %s\n%s", i, out, stderr)
				}
				if i == 1 && !strings.Contains(out, "(cached)") {
					t.Fatalf("second run did not reuse test result: %s\n%s", out, stderr)
				}
			}
			out, stderr, code := command(t, dir, env, tool, "test", "-count=1", "-run=^TestPass$", ".")
			if code != 0 || strings.Contains(out, "(cached)") {
				t.Fatalf("count=1 must execute tests: %d %s\n%s", code, out, stderr)
			}
		})
	}
}

func TestExistingOverlay(t *testing.T) {
	dir, driver := prepareFixture(t, false)
	replacement := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(replacement, []byte("package fixture\nfunc Add(a,b int) int { return a+b+1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]any{"Replace": map[string]string{filepath.Join(dir, "sample.go"): replacement}})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for _, fromEnvironment := range []bool{false, true} {
		t.Run(map[bool]string{false: "flag", true: "GOFLAGS"}[fromEnvironment], func(t *testing.T) {
			env := testEnv("DD_CIVISIBILITY_ENABLED=false")
			args := []string{"test", "-count=1", "-run=^TestPass$", "."}
			if fromEnvironment {
				env = append(env, "GOFLAGS=-overlay="+overlay)
			} else {
				args = append(args, "-overlay="+overlay)
			}
			want, stderr, code := command(t, dir, env, "go", args...)
			if code == 0 || !strings.Contains(want, "addition") {
				t.Fatalf("overlay did not change native test: %d %s\n%s", code, want, stderr)
			}
			got, stderr, gotCode := command(t, dir, env, driver, args...)
			if gotCode != code || normalizedOutput(got) != normalizedOutput(want) {
				t.Fatalf("overlay mismatch: %d/%d\n%s\n%s\n%s", gotCode, code, got, want, stderr)
			}
		})
	}
}
