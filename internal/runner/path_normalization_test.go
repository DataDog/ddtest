package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizePath_SubdirPrefixMatch_StripsPrefix(t *testing.T) {
	// Simulates: git root = /repo, CWD = /repo/core
	// Path "core/spec/models/order_spec.rb" -> "spec/models/order_spec.rb"
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)

	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("core/spec/models/order_spec.rb", prefix)
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestNormalizePath_NestedSubdirPrefixMatch_StripsFullPrefix(t *testing.T) {
	// Simulates: git root = /repo, CWD = /repo/packages/core
	// Path "packages/core/spec/user_spec.rb" -> "spec/user_spec.rb"
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	nestedDir := filepath.Join(repoRoot, "packages", "core")
	_ = os.MkdirAll(nestedDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(nestedDir)

	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("packages/core/spec/user_spec.rb", prefix)
	expected := "spec/user_spec.rb"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestNormalizePath_AlreadyRelative_NoChange(t *testing.T) {
	// When path doesn't start with subdir prefix, it's already CWD-relative
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)

	// This path is already CWD-relative (doesn't start with "core/")
	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("spec/models/order_spec.rb", prefix)
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q (unchanged), got %q", expected, result)
	}
}

func TestNormalizePath_PrefixMismatch_NoChange(t *testing.T) {
	// CWD is "api/" but path has "core/" prefix - should NOT be stripped
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	apiDir := filepath.Join(repoRoot, "api")
	_ = os.MkdirAll(apiDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(apiDir)

	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("core/spec/models/order_spec.rb", prefix)
	expected := "core/spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q (unchanged), got %q", expected, result)
	}
}

func TestNormalizePath_AtRepoRoot_NoChange(t *testing.T) {
	// When CWD == git root, no prefix to strip
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(repoRoot)

	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("spec/models/order_spec.rb", prefix)
	expected := "spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q (unchanged), got %q", expected, result)
	}
}

func TestNormalizePath_GitRootUnavailable_NoChange(t *testing.T) {
	// When not in a git repo, normalization should be a no-op (fail-safe)
	tempDir := t.TempDir()

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix("core/spec/models/order_spec.rb", prefix)
	expected := "core/spec/models/order_spec.rb"
	if result != expected {
		t.Errorf("Expected %q (unchanged when git root unavailable), got %q", expected, result)
	}
}

func TestNormalizePath_AbsolutePath_NoChange(t *testing.T) {
	// Absolute paths should not be modified
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)

	absPath := "/absolute/path/to/spec.rb"
	prefix := getCwdSubdirPrefix()
	result := normalizeTestFilePathWithPrefix(absPath, prefix)
	if result != absPath {
		t.Errorf("Expected %q (absolute path unchanged), got %q", absPath, result)
	}
}

func TestNormalizePath_EmptyPath_NoChange(t *testing.T) {
	result := normalizeTestFilePathWithPrefix("", "core")
	if result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}
}

// initGitRepoInDir initializes a git repo in the specified directory.
func initGitRepoInDir(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo in %s: %v\n%s", dir, err, string(out))
	}
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit in %s: %v\n%s", dir, err, string(out))
	}
}
