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

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "empty", path: "", want: ""},
		{name: "plain path", path: "spec/models/user_spec.rb", want: "spec/models/user_spec.rb"},
		{name: "other user", path: "~other/spec.rb", want: "~other/spec.rb"},
		{name: "home only", path: "~", want: home},
		{name: "home path", path: "~/spec/models/user_spec.rb", want: filepath.Join(home, "spec/models/user_spec.rb")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandPath(tt.path); got != tt.want {
				t.Fatalf("ExpandPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "", want: ""},
		{path: ".", want: ""},
		{path: "./spec/../spec/models/user_spec.rb", want: "spec/models/user_spec.rb"},
		{path: filepath.Join("spec", "models", "user_spec.rb"), want: "spec/models/user_spec.rb"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := NormalizePath(tt.path); got != tt.want {
				t.Fatalf("NormalizePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestNormalizePattern(t *testing.T) {
	if got := NormalizePattern("  ./spec/**/*_spec.rb  "); got != "spec/**/*_spec.rb" {
		t.Fatalf("NormalizePattern() = %q", got)
	}
}

func TestParseGlobPatterns(t *testing.T) {
	for _, tt := range []struct {
		name     string
		patterns []string
		want     string
		matches  []string
		excludes []string
	}{
		{name: "none"},
		{name: "exact file", patterns: []string{"spec/a_spec.rb"}, want: "spec/a_spec.rb", matches: []string{"spec/a_spec.rb"}, excludes: []string{"spec/b_spec.rb"}},
		{name: "normalize", patterns: []string{" ./spec/**/*_spec.rb ", " ./tests/**/test_*.py "}, want: "{spec/**/*_spec.rb,tests/**/test_*.py}", matches: []string{"spec/a_spec.rb", "spec/models/a_spec.rb", "tests/test_a.py"}, excludes: []string{"spec/spec_helper.rb", "tests/conftest.py"}},
		{name: "nested alternatives", patterns: []string{"{spec,other}/**/*_spec.rb", "tests/**/{test_*,*_test}.py"}, want: "{{spec,other}/**/*_spec.rb,tests/**/{test_*,*_test}.py}", matches: []string{"other/a_spec.rb", "tests/a_test.py"}, excludes: []string{"spec/fixtures/a.json"}},
		{name: "character class", patterns: []string{"spec/[ab]?_spec.rb"}, want: "spec/[ab]?_spec.rb", matches: []string{"spec/a1_spec.rb"}, excludes: []string{"spec/c1_spec.rb"}},
		{name: "literal bracket", patterns: []string{"spec/a[[]1]_spec.rb"}, want: "spec/a[[]1]_spec.rb", matches: []string{"spec/a[1]_spec.rb"}, excludes: []string{"spec/a1_spec.rb"}},
		{name: "directory unchanged", patterns: []string{"spec/"}, want: "spec/", excludes: []string{"spec/a_spec.rb"}},
		{name: "absolute unchanged", patterns: []string{"/project/spec/*.rb"}, want: "/project/spec/*.rb", matches: []string{"/project/spec/a.rb"}},
		{name: "parent unchanged", patterns: []string{"../spec/*.rb"}, want: "../spec/*.rb", matches: []string{"../spec/a.rb"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGlobPatterns(tt.patterns)
			if err != nil || got != tt.want {
				t.Fatalf("ParseGlobPatterns() = %q, %v; want %q", got, err, tt.want)
			}
			matcher, err := NewPathMatcher(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range tt.matches {
				if !matcher.Match(path) {
					t.Errorf("pattern %q does not match %q", got, path)
				}
			}
			for _, path := range tt.excludes {
				if matcher.Match(path) {
					t.Errorf("pattern %q unexpectedly matches %q", got, path)
				}
			}
		})
	}
}

func TestParseGlobPatternsRejectsInvalidInput(t *testing.T) {
	for _, patterns := range [][]string{{""}, {"   "}, {"./"}, {"spec/["}, {"{spec", "other}"}, {"spec/*.rb", "["}} {
		if _, err := ParseGlobPatterns(patterns); err == nil {
			t.Errorf("ParseGlobPatterns(%q) accepted invalid input", patterns)
		}
	}
}

func TestPathMatcher(t *testing.T) {
	matcher, err := NewPathMatcher(" ./spec/**/*_spec.rb ")
	if err != nil {
		t.Fatalf("NewPathMatcher() returned error: %v", err)
	}
	if matcher.Empty() {
		t.Fatal("NewPathMatcher() returned empty matcher")
	}
	if !matcher.Match("./spec/models/user_spec.rb") {
		t.Fatal("expected matcher to match normalized path")
	}
	if !matcher.MatchNormalizedPath("spec/models/user_spec.rb") {
		t.Fatal("expected matcher to match already-normalized path")
	}
	if matcher.MatchNormalizedPath("test/models/user_test.rb") {
		t.Fatal("expected matcher not to match outside path")
	}
}

func TestPathMatcherEmptyPattern(t *testing.T) {
	matcher, err := NewPathMatcher(" ./ ")
	if err != nil {
		t.Fatalf("NewPathMatcher() returned error: %v", err)
	}
	if !matcher.Empty() {
		t.Fatal("expected empty matcher")
	}
	if matcher.MatchNormalizedPath("spec/models/user_spec.rb") {
		t.Fatal("empty matcher should not match paths")
	}
}

func TestPathMatcherInvalidPattern(t *testing.T) {
	if _, err := NewPathMatcher("["); err == nil {
		t.Fatal("expected invalid pattern error")
	}
}

func TestNewNormalizedPathMatcher(t *testing.T) {
	matcher, err := NewNormalizedPathMatcher("spec/**/*_spec.rb")
	if err != nil {
		t.Fatalf("NewNormalizedPathMatcher() returned error: %v", err)
	}
	if !matcher.MatchNormalizedPath("spec/models/user_spec.rb") {
		t.Fatal("expected matcher to match already-normalized path")
	}
}

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

func TestStripCwdSubdirPrefix_LeadingCurrentDir_StripsPrefix(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepoInDir(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(coreDir, 0755)

	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)
	resetCwdSubdirPrefixCache(t)

	result := StripCwdSubdirPrefix("./core/spec/models/order_spec.rb")
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

func TestParseTestSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, file := range []string{"spec/a_spec.rb", "spec/nested/b_spec.rb", "spec/a[1],x_spec.rb", "spec/[models]/c_spec.rb", "other/d_spec.rb"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	absolute, err := filepath.Abs("spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		args     []string
		matches  []string
		excludes []string
	}{
		{name: "directory", args: []string{"./spec/"}, matches: []string{"spec/a_spec.rb", "spec/nested/b_spec.rb"}, excludes: []string{"specs/a_spec.rb", "other/d_spec.rb"}},
		{name: "current directory", args: []string{"."}, matches: []string{"spec/a_spec.rb", "other/d_spec.rb"}},
		{name: "absolute directory", args: []string{absolute}, matches: []string{"spec/a_spec.rb"}, excludes: []string{"other/d_spec.rb"}},
		{name: "absolute glob", args: []string{absolute + "/**/*_spec.rb"}, matches: []string{"spec/nested/b_spec.rb"}},
		{name: "literal metacharacters", args: []string{"spec/a[1],x_spec.rb", "other/d_spec.rb"}, matches: []string{"spec/a[1],x_spec.rb", "other/d_spec.rb"}, excludes: []string{"spec/a1,x_spec.rb", "x_spec.rb"}},
		{name: "directory metacharacters", args: []string{"spec/[models]"}, matches: []string{"spec/[models]/c_spec.rb"}, excludes: []string{"spec/m/c_spec.rb"}},
		{name: "unmatched glob", args: []string{"missing/**/*_spec.rb"}, excludes: []string{"spec/a_spec.rb"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pattern, err := ParseTestSelection(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			matcher, err := NewPathMatcher(pattern)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range tt.matches {
				if !matcher.Match(file) {
					t.Errorf("%q should match %q", pattern, file)
				}
			}
			for _, file := range tt.excludes {
				if matcher.Match(file) {
					t.Errorf("%q should not match %q", pattern, file)
				}
			}
		})
	}
	for _, arg := range []string{"", " ", "missing.rb", "missing_dir/", "spec/["} {
		if _, err := ParseTestSelection([]string{arg}); err == nil {
			t.Errorf("accepted invalid argument %q", arg)
		}
	}
}
