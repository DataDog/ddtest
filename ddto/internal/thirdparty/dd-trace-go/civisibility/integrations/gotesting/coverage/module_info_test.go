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
	startedIn(t, module)
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

// A test process that ddto describes takes its module from ddto's state and
// starts no go list; a directory that the state does not describe still asks
// go list.
func TestModuleInfoFromCLIState(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)
	dir := t.TempDir()
	startedIn(t, dir)
	calls := 0
	previous, previousCLI := listModuleInfo, cliModuleInfo
	listModuleInfo = func(string, []string, string) (string, string) {
		calls++
		return "example.com/listed", "/listed"
	}
	described := map[string][2]string{dir: {"example.com/described", "/described"}}
	cliModuleInfo = func(d string) (string, string, bool) {
		m, ok := described[d]
		return m[0], m[1], ok
	}
	t.Cleanup(func() { listModuleInfo, cliModuleInfo = previous, previousCLI })

	deferModuleInfo()
	resolveModuleInfo()
	if calls != 0 || modulePath != "example.com/described" || moduleDir != "/described" {
		t.Fatalf("calls=%d module=%q dir=%q", calls, modulePath, moduleDir)
	}

	delete(described, dir)
	deferModuleInfo()
	resolveModuleInfo()
	if calls != 1 || modulePath != "example.com/listed" || moduleDir != "/listed" {
		t.Fatalf("calls=%d module=%q dir=%q", calls, modulePath, moduleDir)
	}
}

// startedIn makes dir the directory that the test process started in.
func startedIn(t *testing.T, dir string) {
	t.Helper()
	previous := testPackageDirectory
	testPackageDirectory = dir
	t.Cleanup(func() { testPackageDirectory = previous })
}

// Only the module path's prefix names the module directory: module app's
// app/app.go is <dir>/app.go, and another module's file keeps its name.
func TestModuleFilePath(t *testing.T) {
	savedPath, savedDir := modulePath, moduleDir
	t.Cleanup(func() { modulePath, moduleDir = savedPath, savedDir })
	modulePath, moduleDir = "app", "/work/repo"
	for file, want := range map[string]string{
		"app/app.go":            "/work/repo/app.go",
		"app/api/app/app.go":    "/work/repo/api/app/app.go",
		"example.com/app/x.go":  "example.com/app/x.go",
		"application/server.go": "application/server.go",
	} {
		if got := moduleFilePath(file); got != want {
			t.Errorf("%s: %s, want %s", file, got, want)
		}
	}
	modulePath = ""
	if got := moduleFilePath("app/app.go"); got != "app/app.go" {
		t.Errorf("without a module path: %s", got)
	}
}
