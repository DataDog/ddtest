// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package utils

import (
	"path/filepath"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/goenv"
)

// ddto's state gives a test process the module of its package, so that the
// process starts no go list for it. Without the description, an unknown
// directory or a module without a directory, the process asks go list.
func TestCLITestPackageModule(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "--initial-branch=main")
	git(t, repo, "commit", "--allow-empty", "-m", "state")
	t.Chdir(repo)
	t.Cleanup(ResetHandedCLIGitStateForTesting)
	t.Cleanup(goenv.Receive)
	state := t.TempDir()
	if err := WriteCLIGitStateFile(state, CLIGitRecordFile, CollectCLIGitRecord()); err != nil {
		t.Fatal(err)
	}
	hand := func(dir string) {
		t.Helper()
		t.Setenv(goenv.GitStateHandoff, dir)
		goenv.Receive()
		ResetHandedCLIGitStateForTesting()
	}
	pkg := filepath.Join(repo, "pkg")

	hand(state)
	if _, _, ok := CLITestPackageModule(pkg); ok {
		t.Fatal("a state without a description gave a module")
	}

	tests := CLITests{GOARCH: "amd64", Packages: []CLITestPackage{
		{Dir: repo, ModulePath: "example.com/m", ModuleDir: repo},
		{Dir: pkg, ModulePath: "example.com/m", ModuleDir: repo},
		{Dir: filepath.Join(repo, "vendored"), ModulePath: "example.com/v"},
	}}
	if err := WriteCLIGitStateFile(state, CLITestsFile, tests); err != nil {
		t.Fatal(err)
	}
	hand(state)
	if path, dir, ok := CLITestPackageModule(pkg + string(filepath.Separator)); !ok || path != "example.com/m" || dir != repo {
		t.Fatalf("module %q %q %t", path, dir, ok)
	}
	if HandedCLITests().GOARCH != "amd64" {
		t.Fatalf("description %+v", HandedCLITests())
	}
	for _, dir := range []string{filepath.Join(repo, "other"), filepath.Join(repo, "vendored"), ""} {
		if _, _, ok := CLITestPackageModule(dir); ok {
			t.Fatalf("%q gave a module", dir)
		}
	}

	hand("")
	if _, _, ok := CLITestPackageModule(pkg); ok || HandedCLITests() != nil {
		t.Fatal("a process without ddto's state gave a module")
	}
}
