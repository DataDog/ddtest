// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package utils

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/constants"
)

func TestMirrorNestedModuleSourceAndOwnership(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(root, "services", "api")
	packageDir := filepath.Join(moduleDir, "pkg")
	if err := os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	moduleFile := filepath.Join(moduleDir, "go.mod")
	const modulePath = "example.com/declared/api/v2"
	if err := os.WriteFile(moduleFile, []byte("module \""+modulePath+"\" // nested module\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path, dir := discoverSourceModule(packageDir)
	if path != modulePath || dir != moduleDir {
		t.Fatalf("module=%q directory=%q", path, dir)
	}
	previous := sourceModuleInfo
	calls := 0
	sourceModuleInfo = sync.OnceValues(func() (string, string) {
		calls++
		return discoverSourceModule(packageDir)
	})
	t.Cleanup(func() { sourceModuleInfo = previous })
	resetCodeOwnersTestState(t, root)
	ResetServiceFromCodeOwnersForTesting()
	t.Cleanup(ResetServiceFromCodeOwnersForTesting)
	const mirror = "https://mirror.example.com/transport/repo.git"
	AddCITagsMap(map[string]string{constants.GitCheckoutRoot: root, constants.GitRepositoryURL: mirror, constants.GitBranch: "feature"})
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/services/api/ @example/api\n")
	t.Setenv("DD_SERVICE", "")
	t.Setenv(ServiceFromCodeOwnersEnv, "true")
	t.Setenv(ServiceFromCodeOwnersFormatEnv, "service-$(owner)")
	t.Chdir(packageDir)
	runtimePath := modulePath + "/pkg/foo_test.go"
	RegisterTestPackageSource(runtimePath)
	source := ResolveSourceFilePathFromCITags(runtimePath)
	if source.RelativePath != "services/api/pkg/foo_test.go" || source.FilesystemPath != filepath.Join(packageDir, "foo_test.go") || !source.FilesystemKnown {
		t.Fatalf("nested module source=%+v", source)
	}
	if err := os.Remove(moduleFile); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if again := ResolveSourceFilePathFromCITags(runtimePath); again != source || calls != 1 {
		t.Fatalf("module context changed after chdir: %+v calls=%d", again, calls)
	}
	if ServiceFromCodeOwners() != "service-api" || ServiceFromCodeOwnersForDirectory(packageDir) != "service-api" {
		t.Fatal("worker and test process did not use nested-module ownership")
	}
	if GetCITags()[constants.GitRepositoryURL] != mirror {
		t.Fatal("source resolution changed the reported repository")
	}
	unrelated := ResolveSourceFilePathFromCITags("example.com/declared/api/v20/pkg/foo_test.go")
	if unrelated.FilesystemKnown {
		t.Fatalf("unrelated module mapped into checkout: %+v", unrelated)
	}
	if allocations := testing.AllocsPerRun(100, func() { _, _ = sourceModuleInfo() }); allocations != 0 {
		t.Fatalf("cached module lookup allocated %g times", allocations)
	}
}

func TestSourceModuleUsesCLIContext(t *testing.T) {
	ResetHandedCLIGitStateForTesting()
	t.Cleanup(ResetHandedCLIGitStateForTesting)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cliTestsOnce.Do(func() {
		cliTests = &CLITests{Packages: []CLITestPackage{{Dir: dir, ModulePath: "example.com/overlay", ModuleDir: dir}}}
	})
	if module, moduleDir := discoverSourceModule(dir); module != "example.com/overlay" || moduleDir != dir {
		t.Fatalf("CLI module context lost: %q %q", module, moduleDir)
	}
}

func TestMirrorSymlinkModuleDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module example.com/api\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "module")
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	modulePath, moduleDir := discoverSourceModule(alias)
	if moduleDir != directory {
		t.Fatalf("module directory=%q, want %q", moduleDir, directory)
	}
	source := resolveSourceFilePathForModule("example.com/api/generated_test.go", map[string]string{
		constants.GitCheckoutRoot: root, constants.GitRepositoryURL: "https://mirror.example.com/transport.git",
	}, modulePath, moduleDir)
	if source.RelativePath != "services/api/generated_test.go" || source.FilesystemPath != filepath.Join(directory, "generated_test.go") || !source.FilesystemKnown {
		t.Fatalf("symlink module source=%+v", source)
	}
}
