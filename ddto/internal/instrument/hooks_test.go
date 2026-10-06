package instrument

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeclaresParallelStop(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"package testing\nimport \"sync/atomic\"\nvar (\n\tparallelStart atomic.Int64\n\tparallelStop  atomic.Int64 // stopped\n)\n", true},
		{"package testing\nvar parallelStop int64\n", false},
		{"package testing\n// parallelStop atomic.Int64\n", false},
		{"package testing\n", false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "testing.go", tc.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		if got := declaresParallelStop(file); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.src, got, tc.want)
		}
	}
}

// Supported toolchains keep the counter. If one stops, Mini builds without the
// hook and its in-process retries of parallel tests break AllocsPerRun again.
func TestToolchainTestingDeclaresParallelStop(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(strings.TrimSpace(string(out)), "src", "testing")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if files[name], err = os.ReadFile(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	rewritten, err := Transform(files)
	if err != nil {
		t.Fatal(err)
	}
	if !rewritten.ParallelStop {
		t.Fatal("testing no longer declares parallelStop as an atomic.Int64; review ParallelStopHook")
	}
}
