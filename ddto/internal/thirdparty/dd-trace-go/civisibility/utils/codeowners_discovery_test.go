// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.
package utils

import (
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"os"
	"path/filepath"
	"testing"
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
