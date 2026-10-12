package gotesting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This package's tests measure allocations with allocsPerRun. Their fresh
// retry attempts leave testing's parallel-test accounting unbalanced without
// ddto's overlay, so testing.AllocsPerRun panics in every later test: with
// -count above one, or with -shuffle.
func TestPackageTestsMeasureAllocationsWithHelper(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("test files %v: %v", files, err)
	}
	call := "testing." + "AllocsPerRun("
	for _, file := range files {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), call) {
			t.Errorf("%s calls testing.AllocsPerRun; use allocsPerRun", file)
		}
	}
}
