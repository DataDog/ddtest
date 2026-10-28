// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package languageguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recorder struct {
	testing.TB
	errors []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestCheckReportsLanguagesOutsideTheirFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// Shared code names Ruby: rejected.
		"jest.go": "package p\n\nconst gemfile = \"Gemfile\"\n",
		// Comments are documentation, not code.
		"shared.go": "package p\n\n// Python and Ruby keep their own files.\nvar shared = 1\n",
		// A language file names its own language: allowed.
		"ruby_language.go": "package p\n\nconst rubyopt = \"RUBYOPT\"\n",
		// A language file names another one: rejected.
		"python.go": "package p\n\nfunc pythonRun() { _ = \"rspec\"; _ = \"jest\" }\n",
		// The registry names every language.
		"languages.go": "package p\n\nvar all = []string{\"javascript\", \"python\", \"ruby\"}\n",
		// Tests are free to name any language.
		"jest_test.go": "package p\n\nconst fixture = \"Gemfile\"\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var report recorder
	report.TB = t
	Check(&report, dir)
	if len(report.errors) != 4 {
		t.Fatalf("want 4 violations (jest.go: gemfile, Gemfile; python.go: rspec, jest), got %d: %s", len(report.errors), strings.Join(report.errors, "; "))
	}
}

func TestJavaScriptBundlesAreNotRuby(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "vitest.go"), []byte("package p\n\nconst tool = \"bundler\"\nconst webpack = \"bundled\"\nconst served = \"Serve the bundle\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Check(t, dir)
}

// "java" is a prefix of "javascript": neither the JavaScript language file nor
// JavaScript tokens may be attributed to Java.
func TestJavaScriptIsNotJava(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"javascript_language.go": "package p\n\nconst name = \"javascript\"\nconst label = \"JavaScript\"\n",
		"shared.go":              "package p\n\nfunc javascriptRun() { _ = \"javascript\" }\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	Check(t, dir)
}

func TestCheckReportsJavaOutsideItsFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// Shared code names Maven: rejected.
		"jest.go": "package p\n\nconst build = \"mvn\"\n",
		// Java's own file names Java: allowed.
		"java_language.go": "package p\n\nconst agent = \"dd-java-agent\"\nconst language = \"java\"\n",
		// Java's file names JavaScript: rejected.
		"java_validation.go": "package p\n\nconst runner = \"jest\"\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var report recorder
	report.TB = t
	Check(&report, dir)
	if len(report.errors) != 2 {
		t.Fatalf("want 2 violations (jest.go: mvn; java_validation.go: jest), got %d: %s", len(report.errors), strings.Join(report.errors, "; "))
	}
}

func TestCheckNeutralReportsAnyLanguage(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// Provider files name no language, JavaScript included.
		"ci_provider_x.go": "package p\n\nconst install = \"npm install dd-trace\"\nconst gem = \"Gemfile\"\n",
		// Comments and neutral facts are fine.
		"ci_provider_y.go": "package p\n\n// Ruby and JavaScript steps are only commands here.\nconst image = \"ubuntu-latest\"\n",
		// Files outside the patterns and tests are not checked.
		"jest.go":               "package p\n\nconst jest = \"jest\"\n",
		"ci_provider_x_test.go": "package p\n\nconst fixture = \"pytest\"\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var report recorder
	report.TB = t
	CheckNeutral(&report, dir, "ci_provider*.go")
	if len(report.errors) != 2 {
		t.Fatalf("want 2 violations (dd-trace, Gemfile), got %d: %s", len(report.errors), strings.Join(report.errors, "; "))
	}
}

func TestJavaNamesCucumberJVMButNotJavaScript(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// Cucumber JVM is Java's too: allowed in its files.
		"java_cucumber.go": "package p\n\nconst glue = \"io.cucumber.junit.platform.engine\"\nconst javaCucumberProbe = 1\n",
		// Other JavaScript terms stay out, even next to a shared one.
		"java_steps.go": "package p\n\nconst runner = \"cucumber with dd-trace\"\n",
		// Shared code keeps naming Cucumber.js.
		"cucumber.go": "package p\n\nconst runner = \"cucumber-js\"\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var report recorder
	report.TB = t
	Check(&report, dir)
	if len(report.errors) != 1 || !strings.Contains(report.errors[0], "java_steps.go") {
		t.Fatalf("want 1 violation (java_steps.go: dd-trace), got %d: %s", len(report.errors), strings.Join(report.errors, "; "))
	}
}
