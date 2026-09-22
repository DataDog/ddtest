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

func TestReportKeepsPythonAndRubySourceEscaped(t *testing.T) {
	for _, extension := range []string{".py", ".rb"} {
		t.Run(extension, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "test"+extension), []byte("value = '<script>alert(1)</script>'\nassert value\n"), 0644); err != nil {
				t.Fatal(err)
			}
			source := readSource(root, "test"+extension, 1, 0)
			if source.Error != "" || len(source.Lines) < 2 {
				t.Fatalf("source not rendered: %+v", source)
			}
			if !strings.Contains(string(source.Lines[0].Code), "&lt;script&gt;") || strings.Contains(string(source.Lines[0].Code), "<script>") {
				t.Fatalf("unescaped source: %s", source.Lines[0].Code)
			}
		})
	}
}

func TestReportShowsEveryFlakyAttemptErrorAndSource(t *testing.T) {
	repositoryRoot := t.TempDir()
	source := "test('sometimes works', () => {\n  expect(true).toBe(true);\n});\n"
	if err := os.WriteFile(filepath.Join(repositoryRoot, "flaky.test.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	flaky := intake.TestFinding{
		Name: "sometimes works", Suite: "flaky.test.js", SourceFile: "flaky.test.js", SourceStart: 1, Duration: 2 * time.Second,
		Attempts: []intake.TestAttempt{
			{Status: "fail", Duration: 20 * time.Millisecond, ErrorType: "AssertionError", ErrorMessage: "expected true", ErrorStack: "full stack"},
			{Status: "pass", Duration: 2 * time.Second, Retry: true, RetryReason: "automatic_test_retry"},
		},
	}
	findings := intake.Findings{
		TestCount:        1,
		TestEventCount:   2,
		CoveredTestCount: 1,
		Tests:            []intake.TestFinding{flaky},
		FlakyTests:       []intake.TestFinding{flaky},
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
		"Source · lines 1–3",
		`class="token-string">&#39;sometimes works&#39;`,
		`data-tab="suites"`,
		`data-tab="tests"`,
		`data-paginated`,
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
		TestCount:          2,
		TestEventCount:     2,
		CoveredTestCount:   2,
		CoverageLevel:      "test",
		CoveredFilesMedian: 2,
		BroadCoverage: []intake.CoverageFinding{{
			Name: "broad.test.js › broad", Level: "test", SourceFile: "broad.test.js", SourceStart: 1, FileCount: 12,
			Files: []string{"src/one.js", "src/two.js"},
		}},
	}

	report := renderTestReport(t, repositoryRoot, findings)
	for _, expected := range []string{
		"Any unusually broad test coverage?",
		"12 files · test level",
		"Median covered files · 2",
		"src/one.js",
		"src/two.js",
		`data-page-size="50"`,
		`class="page-item">src/one.js`,
		`class="token-string">&#39;broad&#39;`,
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}

func TestReportShowsCoverageOnlyAtActiveSkippingLevel(t *testing.T) {
	test := intake.TestFinding{
		Name: "works", Suite: "one.test.js", Status: "pass",
		CoverageLevel: "suite", CoveredFiles: []string{"src/suite.js"},
	}
	suiteModel := buildReport(t.TempDir(), intake.Findings{CoverageLevel: "suite", Tests: []intake.TestFinding{test}}, false)
	requireReportCoverage(t, suiteModel, 0, 1)

	test.CoverageLevel = "test"
	test.CoveredFiles = []string{"src/test.js"}
	testModel := buildReport(t.TempDir(), intake.Findings{CoverageLevel: "test", Tests: []intake.TestFinding{test}}, false)
	requireReportCoverage(t, testModel, 1, 0)
}

func requireReportCoverage(t *testing.T, model reportModel, testFiles, suiteFiles int) {
	t.Helper()
	if len(model.Tests) != 1 || len(model.Tests[0].CoveredFiles) != testFiles {
		t.Fatalf("test covered files = %v, want %d", model.Tests, testFiles)
	}
	if len(model.Suites) != 1 || len(model.Suites[0].CoveredFiles) != suiteFiles {
		t.Fatalf("suite covered files = %v, want %d", model.Suites, suiteFiles)
	}
}

func TestSourceUsesReportedRangeAndHighlightsJavaScript(t *testing.T) {
	repositoryRoot := t.TempDir()
	source := "const outside = true;\ntest(\"one\", () => {\n  expect(1).toBe(1);\n});\ntest(\"two\", () => {});\n"
	if err := os.WriteFile(filepath.Join(repositoryRoot, "range.test.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	excerpt := readSource(repositoryRoot, "range.test.js", 2, 4)
	if excerpt.Start != 2 || excerpt.End != 4 || len(excerpt.Lines) != 3 {
		t.Fatalf("source excerpt = lines %d-%d (%d lines), want 2-4", excerpt.Start, excerpt.End, len(excerpt.Lines))
	}
	if !strings.Contains(string(excerpt.Lines[0].Code), `class="token-string">&#34;one&#34;`) {
		t.Fatalf("source is not highlighted: %s", excerpt.Lines[0].Code)
	}
}

func TestSourceInfersJestTestEnd(t *testing.T) {
	repositoryRoot := t.TempDir()
	source := "test(\"one\", () => {\n  expect(true).toBe(true);\n});\n\ntest(\"two\", () => {});\n"
	if err := os.WriteFile(filepath.Join(repositoryRoot, "inferred.test.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	excerpt := readSource(repositoryRoot, "inferred.test.js", 1, 0)
	if excerpt.End != 3 {
		t.Fatalf("inferred source end = %d, want 3", excerpt.End)
	}
}

func TestSourceReportsUsefulErrors(t *testing.T) {
	repositoryRoot := t.TempDir()

	requireSourceError(t, readSource(repositoryRoot, "", 1, 1), "Source file not reported")
	requireSourceError(t, readSource(repositoryRoot, "missing.test.js", 0, 1), "Source line not reported")
	requireSourceError(t, readSource(repositoryRoot, "missing.test.js", 1, 1), "Source could not be read")

	sourcePath := filepath.Join(repositoryRoot, "short.test.js")
	if err := os.WriteFile(sourcePath, []byte("test('one', () => {});\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	requireSourceError(t, readSource(repositoryRoot, "short.test.js", 3, 3), "outside short.test.js")

	excerpt := readSource(repositoryRoot, sourcePath, 1, 99)
	if excerpt.Start != 1 || excerpt.End != 2 || len(excerpt.Lines) != 2 {
		t.Fatalf("absolute source excerpt = lines %d-%d (%d lines), want 1-2", excerpt.Start, excerpt.End, len(excerpt.Lines))
	}
}

func requireSourceError(t *testing.T, source reportSource, expected string) {
	t.Helper()
	if !strings.Contains(source.Error, expected) {
		t.Fatalf("source error = %q, want %q", source.Error, expected)
	}
}

func TestInferJavaScriptTestEndIgnoresCommentsAndQuotedParentheses(t *testing.T) {
	lines := []string{
		`test(")", () => {`,
		`  /* ) */`,
		`  const value = "escaped \")";`,
		`  // )`,
		`}); // done`,
	}
	if end := inferJavaScriptTestEnd(lines, 1); end != 5 {
		t.Fatalf("inferJavaScriptTestEnd() = %d, want 5", end)
	}

	fallbackLines := make([]string, 30)
	for index := range fallbackLines {
		fallbackLines[index] = "const value = 1;"
	}
	if end := inferJavaScriptTestEnd(fallbackLines, 3); end != 22 {
		t.Fatalf("fallback end = %d, want 22", end)
	}
}

func TestHighlightJavaScriptLineHandlesCommentsTokensAndEscapes(t *testing.T) {
	inBlockComment := false
	first := string(highlightJavaScriptLine("/* open <tag>", &inBlockComment))
	if !inBlockComment || !strings.Contains(first, `token-comment`) || !strings.Contains(first, `&lt;tag&gt;`) {
		t.Fatalf("opening block comment = %q, inBlockComment = %v", first, inBlockComment)
	}

	second := string(highlightJavaScriptLine("continued */ const answer_2 = 42; // tail", &inBlockComment))
	for _, expected := range []string{`token-comment`, `token-keyword`, `token-number`, `answer_2`, `tail`} {
		if !strings.Contains(second, expected) {
			t.Fatalf("continued line does not contain %q: %s", expected, second)
		}
	}
	if inBlockComment {
		t.Fatal("block comment did not close")
	}

	third := string(highlightJavaScriptLine(`let value = "<tag>\""; const ok = true;`, &inBlockComment))
	for _, expected := range []string{`token-string`, `&lt;tag&gt;`, `token-literal`, `token-keyword`} {
		if !strings.Contains(third, expected) {
			t.Fatalf("highlighted line does not contain %q: %s", expected, third)
		}
	}

	unclosed := string(highlightJavaScriptLine("const value = `unfinished", &inBlockComment))
	if !strings.Contains(unclosed, `token-string`) || !strings.Contains(unclosed, "unfinished") {
		t.Fatalf("unclosed string was not highlighted: %s", unclosed)
	}
}

func TestBuildReportShowsNoEventsAndRollsUpUnknownSuite(t *testing.T) {
	tests := []intake.TestFinding{
		{Name: "flaky", Status: "pass", Duration: time.Millisecond, Attempts: []intake.TestAttempt{{Status: "fail"}, {Status: "pass"}}},
		{Name: "failed", Status: "fail", Duration: 2 * time.Millisecond},
	}
	model := buildReport(t.TempDir(), intake.Findings{Tests: tests}, true)
	if model.Headline != "No test events received." || model.Summary != "Check the instrumentation setup." {
		t.Fatalf("headline = %q, summary = %q", model.Headline, model.Summary)
	}
	if len(model.Suites) != 1 || model.Suites[0].Name != "Unknown suite" || model.Suites[0].Status != "Failed" {
		t.Fatalf("suite rollup = %#v", model.Suites)
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
