// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func TestReportShowsEveryFlakyAttemptErrorAndSource(t *testing.T) {
	repositoryRoot := t.TempDir()
	source := "test('sometimes works', () => expect(true).toBe(true));\n"
	if err := os.WriteFile(filepath.Join(repositoryRoot, "flaky.test.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	findings := intake.Findings{
		TestCount:        1,
		TestEventCount:   2,
		CoveredTestCount: 1,
		PassedOnRetry: []intake.TestFinding{{
			Name: "sometimes works", Suite: "flaky.test.js", SourceFile: "flaky.test.js", Duration: 2 * time.Second,
			Attempts: []intake.TestAttempt{
				{Status: "fail", Duration: 20 * time.Millisecond, ErrorType: "AssertionError", ErrorMessage: "expected true", ErrorStack: "full stack"},
				{Status: "pass", Duration: 2 * time.Second, Retry: true, RetryReason: "automatic_test_retry"},
			},
		}},
	}

	report := renderTestReport(t, repositoryRoot, findings)
	for _, expected := range []string{
		"Any flaky tests?",
		"Run 1 · Initial run",
		"Run 2 · Retry · automatic test retry",
		"20ms",
		"2s",
		"AssertionError: expected true",
		"full stack",
		"flaky.test.js",
		"test(&#39;sometimes works&#39;",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}

func TestReportShowsBroadCoverageFilesAndSource(t *testing.T) {
	repositoryRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repositoryRoot, "broad.test.js"), []byte("test('broad', () => {});\n"), 0644); err != nil {
		t.Fatal(err)
	}
	findings := intake.Findings{
		TestCount:        2,
		TestEventCount:   2,
		CoveredTestCount: 2,
		BroadCoverage: []intake.CoverageFinding{{
			Name: "broad.test.js", Level: "suite", SourceFile: "broad.test.js", FileCount: 12,
			Files: []string{"src/one.js", "src/two.js"},
		}},
	}

	report := renderTestReport(t, repositoryRoot, findings)
	for _, expected := range []string{
		"Any unusually broad test coverage?",
		"12 covered files · suite-level coverage · broad.test.js",
		"src/one.js",
		"src/two.js",
		"test(&#39;broad&#39;",
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}

func renderTestReport(t *testing.T, repositoryRoot string, findings intake.Findings) string {
	t.Helper()
	sessionDirectory := t.TempDir()
	path, err := writeReport(repositoryRoot, sessionDirectory, findings, false)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
