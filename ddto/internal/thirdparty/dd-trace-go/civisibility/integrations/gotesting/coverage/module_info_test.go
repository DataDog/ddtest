package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

// Coverage initialization records where it ran instead of starting go list:
// most runs never read the module. The first reader resolves it from that
// directory and environment, even after TestMain or a test changes them.
func TestModuleInfoResolvesOnFirstUseFromInitialization(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	module, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"go.mod": "module example.com/lazycoverage\n\ngo 1.25\n",
		"lib.go": "package lazycoverage\n",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Chdir(module)
	calls := 0
	previous := listModuleInfo
	listModuleInfo = func(dir string, env []string, goPath string) (string, string) {
		calls++
		return previous(dir, env, goPath)
	}
	t.Cleanup(func() { listModuleInfo = previous })

	deferModuleInfo()
	if calls != 0 {
		t.Fatal("initialization started go list")
	}
	t.Chdir(t.TempDir())
	t.Setenv("GOFLAGS", "-mod=invalid")

	if root := repositoryRootFromModuleInfo(); calls != 1 || modulePath != "example.com/lazycoverage" || moduleDir != module || root != module {
		t.Fatalf("calls=%d module=%q dir=%q root=%q, want %q", calls, modulePath, moduleDir, root, module)
	}
	_ = coveragePathCandidates(filepath.Join(module, "lib.go"))
	if calls != 1 {
		t.Fatalf("module resolved %d times", calls)
	}
}
