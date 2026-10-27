package instrument

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test processes of this module's runtime join ddto's test session; a
// runtime without the function, as the released ones, reports its own.
func TestSessionRuntimeErrorMatchesMini(t *testing.T) {
	dir := filepath.Join("..", "thirdparty", "dd-trace-go", "civisibility", "utils")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			if sources[name], err = os.ReadFile(filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := SessionRuntimeError(sources); err != nil {
		t.Fatal(err)
	}
	if err := SessionRuntimeError(map[string][]byte{"utils.go": []byte("package utils\nfunc HandedCLIGitState() {}\n")}); err == nil {
		t.Fatal("a runtime without HandedCLISession accepted")
	}
}
