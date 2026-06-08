// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStripCwdSubdirPrefix_SubdirPrefixMatch_StripsPrefix(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("core/spec/models/order_spec.rb")
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_NestedSubdirPrefixMatch_StripsFullPrefix(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	nestedDir := filepath.Join(repoRoot, "packages", "core")
	_ = os.MkdirAll(nestedDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(nestedDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("packages/core/spec/user_spec.rb")
	expected := "spec/user_spec.rb"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_AlreadyRelative_NoChange(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("spec/models/order_spec.rb")
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q unchanged, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_PrefixMismatch_NoChange(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	apiDir := filepath.Join(repoRoot, "api")
	_ = os.MkdirAll(apiDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(apiDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("core/spec/models/order_spec.rb")
	expected := "core/spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q unchanged, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_AtRepoRoot_NoChange(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(repoRoot)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("spec/models/order_spec.rb")
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q unchanged, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_GitRootUnavailable_NoChange(t *testing.T) {
	tempDir := t.TempDir()

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("core/spec/models/order_spec.rb")
	expected := "core/spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q unchanged when git root unavailable, got %q", expected, result)
	}
}

func TestStripCwdSubdirPrefix_AbsolutePath_NoChange(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	resetCwdSubdirPrefixCache(t)

	absPath := "/absolute/path/to/spec.rb"
	result := StripCwdSubdirPrefix(absPath)
	if result != absPath {
		t.Errorf("Expected %q unchanged, got %q", absPath, result)
	}
}

func TestStripCwdSubdirPrefix_EmptyPath_NoChange(t *testing.T) {
	result := stripSubdirPrefix("", "core")
	if result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}
}

func resetCwdSubdirPrefixCache(t *testing.T) {
	t.Helper()
	ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ResetCwdSubdirPrefixForTesting)
}

func initGitRepoInDir(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	cmd.Env = gitTestEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo in %s: %v\n%s", dir, err, string(out))
	}
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(gitTestEnv(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit in %s: %v\n%s", dir, err, string(out))
	}
}

func gitTestEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
}
