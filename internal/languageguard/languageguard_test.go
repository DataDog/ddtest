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
