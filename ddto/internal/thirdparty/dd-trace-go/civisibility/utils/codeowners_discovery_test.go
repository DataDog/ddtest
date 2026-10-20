// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.
package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
)

func resetCodeOwnersTestState(t *testing.T, workspace string) {
	t.Helper()
	ResetCodeOwnersForTesting()
	ResetCITags()
	originalCiTags = map[string]string{constants.CIWorkspacePath: workspace}
	t.Cleanup(func() { ResetCodeOwnersForTesting(); ResetCITags() })
}
func writeCodeOwnersFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestGetCodeOwnersCachesMissingDiscovery(t *testing.T) {
	root := t.TempDir()
	resetCodeOwnersTestState(t, root)
	if c, complete := GetCodeOwnersWithStatus(); c != nil || !complete {
		t.Fatalf("%v %t", c, complete)
	}
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "/cached/miss @owner")
	if GetCodeOwners() != nil {
		t.Fatal("missing-file decision changed")
	}
	ResetCodeOwnersForTesting()
	c := GetCodeOwners()
	if c == nil {
		t.Fatal("missing rules")
	}
	got, ok := c.Match("/cached/miss")
	if !ok || got.FirstOwner() != "@owner" {
		t.Fatal(got.Tag())
	}
}
func TestGetCodeOwnersCachesSuccessfulDiscovery(t *testing.T) {
	root := t.TempDir()
	resetCodeOwnersTestState(t, root)
	filename := filepath.Join(root, "CODEOWNERS")
	writeCodeOwnersFile(t, filename, "* @first")
	c := GetCodeOwners()
	if c == nil {
		t.Fatal("missing rules")
	}
	writeCodeOwnersFile(t, filename, "* @second")
	originalCiTags[constants.CIWorkspacePath] = t.TempDir()
	if GetCodeOwners() != c {
		t.Fatal("identity changed")
	}
	got, _ := c.Match("/a.go")
	if got.FirstOwner() != "@first" {
		t.Fatal(got.Tag())
	}
}
func TestGetCodeOwnersRetriesIOFailure(t *testing.T) {
	root := t.TempDir()
	resetCodeOwnersTestState(t, root)
	file := filepath.Join(root, "CODEOWNERS")
	if err := os.Symlink("CODEOWNERS", file); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if c, complete := GetCodeOwnersWithStatus(); c != nil || complete {
		t.Fatalf("I/O failure cached: %v %t", c, complete)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	writeCodeOwnersFile(t, file, "* @fixed")
	c, complete := GetCodeOwnersWithStatus()
	if c == nil || !complete {
		t.Fatalf("%v %t", c, complete)
	}
	got, _ := c.Match("/a.go")
	if got.FirstOwner() != "@fixed" {
		t.Fatal(got.Tag())
	}
}

// An unreadable workspace is retried; it never falls back to the current
// directory's file.
func TestGetCodeOwnersRetriesWorkspaceIOFailure(t *testing.T) {
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.Symlink("workspace", workspace); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, workspace)
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "* @wrong")
	if c, complete := GetCodeOwnersWithStatus(); c != nil || complete {
		t.Fatalf("I/O failure cached or fell back: %v %t", c, complete)
	}
	if err := os.Remove(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	writeCodeOwnersFile(t, filepath.Join(workspace, "CODEOWNERS"), "* @fixed")
	c, complete := GetCodeOwnersWithStatus()
	if c == nil || !complete {
		t.Fatalf("%v %t", c, complete)
	}
	if got, _ := c.Match("/a.go"); got.FirstOwner() != "@fixed" {
		t.Fatal(got.Tag())
	}
}

func TestGetCodeOwnersUsesProviderAndOfficialLocation(t *testing.T) {
	root := t.TempDir()
	resetCodeOwnersTestState(t, root)
	for _, folder := range []string{".github", ".gitlab", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeCodeOwnersFile(t, filepath.Join(root, "CODEOWNERS"), "* @root")
	writeCodeOwnersFile(t, filepath.Join(root, ".github", "CODEOWNERS"), "* @github")
	writeCodeOwnersFile(t, filepath.Join(root, ".gitlab", "CODEOWNERS"), "* @gitlab")
	writeCodeOwnersFile(t, filepath.Join(root, "docs", "CODEOWNERS"), "* @docs")
	c := GetCodeOwners()
	got, _ := c.Match("/a.go")
	if got.FirstOwner() != "@github" {
		t.Fatal(got.Tag())
	}
	ResetCodeOwnersForTesting()
	AddCITags(constants.CIProviderName, "gitlab")
	c = GetCodeOwners()
	got, _ = c.Match("/a.go")
	if got.FirstOwner() != "@root" {
		t.Fatal(got.Tag())
	}
}

// Source paths are relative to the CI workspace, so a workspace inside a Git
// checkout reads its own CODEOWNERS locations and not the enclosing root's.
func TestGetCodeOwnersReadsOnlyTheWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, folder := range []string{".git", ".github", "internal"} {
		if err := os.Mkdir(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeCodeOwnersFile(t, filepath.Join(root, ".github", "CODEOWNERS"), "* @root")
	workspace := filepath.Join(root, "internal")
	t.Chdir(t.TempDir())
	resetCodeOwnersTestState(t, workspace)
	if c, complete := GetCodeOwnersWithStatus(); c != nil || !complete {
		t.Fatalf("enclosing root selected: %v %t", c, complete)
	}
	writeCodeOwnersFile(t, filepath.Join(workspace, "CODEOWNERS"), "/config/ @workspace")
	ResetCodeOwnersForTesting()
	got, ok := GetCodeOwners().Match("/config/config.go")
	if !ok || got.FirstOwner() != "@workspace" {
		t.Fatal(got.Tag())
	}
}

// An existing workspace without CODEOWNERS has no rules, even when the test's
// directory has a file: the Git worker, which runs elsewhere, decides the same.
func TestGetCodeOwnersDoesNotFallBackFromAnExistingWorkspace(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, t.TempDir())
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "* @package")
	if c, complete := GetCodeOwnersWithStatus(); c != nil || !complete {
		t.Fatalf("fell back to the working directory: %v %t", c, complete)
	}
}

// A binary that runs where its CI workspace does not exist still finds the
// CODEOWNERS file in its current directory.
func TestGetCodeOwnersFallsBackToWorkingDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, filepath.Join(t.TempDir(), "missing-workspace"))
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "/fallback @owner")
	c, complete := GetCodeOwnersWithStatus()
	if c == nil || !complete {
		t.Fatalf("%v %t", c, complete)
	}
	if got, ok := c.Match("/fallback"); !ok || got.FirstOwner() != "@owner" {
		t.Fatal(got.Tag())
	}
}

// The last-resort file keeps the callers' repository-relative paths, even when
// the current directory is inside a Git checkout.
func TestGetCodeOwnersFallbackKeepsPaths(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "pkg")
	for _, dir := range []string{filepath.Join(root, ".git"), workingDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(workingDir)
	resetCodeOwnersTestState(t, filepath.Join(t.TempDir(), "missing-workspace"))
	writeCodeOwnersFile(t, filepath.Join(workingDir, "CODEOWNERS"), "* @global\n/pkg/a_test.go @specific")
	c := GetCodeOwners()
	if c == nil {
		t.Fatal("missing rules")
	}
	if got, ok := c.Match("/pkg/a_test.go"); !ok || got.FirstOwner() != "@specific" {
		t.Fatal(got.Tag())
	}
}
