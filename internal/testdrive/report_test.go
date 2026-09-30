// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func TestReportCountsIndividualFindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts intake.Facts
		want  string
	}{
		{"single", intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{{Name: "failed"}}}, "1 finding."},
		{"missing coverage", intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true}, "1 finding."},
		{"multiple in one card", intake.Facts{TestEventCount: 2, FailedTests: []intake.Test{{Name: "one"}, {Name: "two"}}}, "2 findings."},
		{"empty coverage without events", intake.Facts{EmptyCoverageEntryCount: 2}, "2 findings."},
		{"configuration error without events", intake.Facts{ConfigurationErrors: []string{"skippable_tests"}}, "1 finding."},
		{"combined", intake.Facts{
			TestEventCount: 2, EmptyCoverageEntryCount: 2, ConfigurationErrors: []string{"skippable_tests"},
			FailedTests: []intake.Test{{Name: "failed"}}, FlakyTests: []intake.Test{{Name: "flaky"}},
			SlowTests: []intake.Test{{Name: "slow"}}, BroadCoverage: []intake.CoverageFact{{Name: "broad"}},
		}, "7 findings."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := buildReport(t.TempDir(), tc.facts, false)
			if !strings.HasPrefix(model.Summary, tc.want) {
				t.Fatalf("summary = %q, want prefix %q", model.Summary, tc.want)
			}
			var console bytes.Buffer
			writeFindings(&console, tc.facts)
			if !strings.Contains(console.String(), tc.want+"\n") {
				t.Fatalf("report and terminal counts differ: report %q, terminal %q", model.Summary, console.String())
			}
			if len(tc.facts.ConfigurationErrors) > 0 && !strings.Contains(model.Summary, "Tracer configuration errors: skippable_tests.") {
				t.Fatalf("summary hides configuration errors: %q", model.Summary)
			}
		})
	}
}

func TestReportShowsCucumberCoverageTroubleshootingWithFinding(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"@cucumber/cucumber":"13.2.1"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	facts := intake.Facts{TestEventCount: 2, TestCount: 2, MissingCoverage: true}
	model := buildReport(root, facts, false, reportRuntime{Framework: "Cucumber"})
	if model.Summary != "1 finding." || len(model.Cards) != 1 || len(model.Cards[0].Advices) != 1 {
		t.Fatalf("missing coverage advice: summary=%q cards=%+v", model.Summary, model.Cards)
	}
	path, err := writeReport(root, t.TempDir(), facts, false, reportRuntime{Framework: "Cucumber"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Coverage not reported", "Cucumber needs nyc for coverage", "npm install --save-dev nyc"} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("report does not show %q", expected)
		}
	}
}

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
	flaky := intake.Test{
		Name: "sometimes works", Suite: "flaky.test.js", SourceFile: "flaky.test.js", SourceStart: 1, Duration: 2 * time.Second,
		Attempts: []intake.TestRun{
			{Status: "fail", Duration: 20 * time.Millisecond, ErrorType: "AssertionError", ErrorMessage: "expected true", ErrorStack: "full stack"},
			{Status: "pass", Duration: 2 * time.Second, Retry: true, RetryReason: "automatic_test_retry"},
		},
	}
	findings := intake.Facts{
		TestCount:        1,
		TestEventCount:   2,
		CoveredTestCount: 1,
		Tests:            []intake.Test{flaky},
		FlakyTests:       []intake.Test{flaky},
	}

	report := renderTestReport(t, repositoryRoot, findings)
	for _, expected := range []string{
		"Flaky tests",
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
	findings := intake.Facts{
		TestCount:          2,
		TestEventCount:     2,
		CoveredTestCount:   2,
		CoverageLevel:      "test",
		CoveredFilesMedian: 2,
		BroadCoverage: []intake.CoverageFact{{
			Name: "broad.test.js › broad", Level: "test", SourceFile: "broad.test.js", SourceStart: 1, FileCount: 12,
			Files: []string{"src/one.js", "src/two.js"},
		}},
	}

	report := renderTestReport(t, repositoryRoot, findings)
	for _, expected := range []string{
		"Broad coverage",
		"12 files · test level",
		"Median covered files · 2",
		"src/one.js",
		"src/two.js",
		`data-page-size="50"`,
		`class="page-item">src/one.js</li>`,
		`class="token-string">&#39;broad&#39;`,
	} {
		if !strings.Contains(report, expected) {
			t.Errorf("report does not contain %q", expected)
		}
	}
}

func TestReportShowsCoverageOnlyAtActiveSkippingLevel(t *testing.T) {
	repositoryRoot := t.TempDir()
	test := intake.Test{
		Name: "works", Suite: "one.test.js", Status: "pass",
	}
	suiteFacts := intake.Facts{CoverageLevel: "suite", Tests: []intake.Test{test}, SuiteCoverages: []intake.SuiteCoverage{{Suite: "one.test.js", Files: []string{"src/suite.js"}, CoveredTests: 1}}}
	suiteModel := buildReport(repositoryRoot, suiteFacts, false)
	requireReportCoverage(t, suiteModel, 0, 1)
	suiteSection, testSection, found := strings.Cut(renderTestReport(t, repositoryRoot, suiteFacts), `<section id="tests"`)
	if !found || !strings.Contains(suiteSection, "src/suite.js") || strings.Contains(testSection, "src/suite.js") {
		t.Fatal("suite coverage must appear only in the Suites section")
	}

	test.CoverageLevel = "test"
	test.CoveredFiles = []string{"src/test.js"}
	testFacts := intake.Facts{CoverageLevel: "test", Tests: []intake.Test{test}}
	testModel := buildReport(repositoryRoot, testFacts, false)
	requireReportCoverage(t, testModel, 1, 0)
	suiteSection, testSection, found = strings.Cut(renderTestReport(t, repositoryRoot, testFacts), `<section id="tests"`)
	if !found || strings.Contains(suiteSection, "src/test.js") || !strings.Contains(testSection, "src/test.js") {
		t.Fatal("test coverage must appear only in the Tests section")
	}
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

func TestSourceWithoutEndUsesFiveLines(t *testing.T) {
	repositoryRoot := t.TempDir()
	source := "test(\"one\", () => {\n  expect(\")\").toMatch(/\\)/);\n});\n\ntest(\"two\", () => {});\nconst outside = true;\n"
	if err := os.WriteFile(filepath.Join(repositoryRoot, "fallback.test.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name       string
		start, end int
		wantEnd    int
	}{
		{name: "five lines regardless of syntax", start: 1, wantEnd: 5},
		{name: "truncated at EOF", start: 4, wantEnd: 6},
		{name: "explicit end", start: 1, end: 3, wantEnd: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			excerpt := readSource(repositoryRoot, "fallback.test.js", tc.start, tc.end)
			if excerpt.Start != tc.start || excerpt.End != tc.wantEnd || len(excerpt.Lines) != tc.wantEnd-tc.start+1 {
				t.Fatalf("source excerpt = lines %d-%d (%d lines), want %d-%d", excerpt.Start, excerpt.End, len(excerpt.Lines), tc.start, tc.wantEnd)
			}
		})
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
	if excerpt.Start != 1 || excerpt.End != 1 || len(excerpt.Lines) != 1 {
		t.Fatalf("absolute source excerpt = lines %d-%d (%d lines), want 1-1", excerpt.Start, excerpt.End, len(excerpt.Lines))
	}
	requireSourceError(t, readSource(repositoryRoot, sourcePath, 2, 0), "outside")
}

func TestReportBuilderReusesSourceWithinOneReport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.test.js")
	if err := os.WriteFile(path, []byte("test('one', () => {});\ntest('two', () => {});\n"), 0644); err != nil {
		t.Fatal(err)
	}
	builder := newReportBuilder(root)
	firstSource := builder.readSource("example.test.js", 1, 0)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	secondSource := builder.readSource("example.test.js", 2, 2)
	repeatedSource := builder.readSource("example.test.js", 1, 0)
	if len(firstSource.Lines) != 2 || len(secondSource.Lines) != 1 || secondSource.Lines[0].Number != 2 || len(repeatedSource.Lines) != 2 || firstSource.Lines[0].Code != repeatedSource.Lines[0].Code {
		t.Fatalf("report builder changed its source snapshot: first=%+v second=%+v repeated=%+v", firstSource, secondSource, repeatedSource)
	}
	if source := newReportBuilder(root).readSource("example.test.js", 1, 0); source.Error == "" {
		t.Fatalf("a new report should observe the deleted source: %+v", source)
	}
}

func requireSourceError(t *testing.T, source reportSource, expected string) {
	t.Helper()
	if !strings.Contains(source.Error, expected) {
		t.Fatalf("source error = %q, want %q", source.Error, expected)
	}
}

func TestReportSuitesPreservesAllSkippedStatus(t *testing.T) {
	suites := newReportBuilder(t.TempDir()).reportSuites([]intake.Test{{Name: "one", Suite: "suite", Status: "skip"}, {Name: "two", Suite: "suite", Status: "skip"}}, nil, false)
	if len(suites) != 1 || suites[0].Status != "Skip" {
		t.Fatalf("reportSuites() = %+v, want one skipped suite", suites)
	}
}

func TestReportPreservesModuleAndParameters(t *testing.T) {
	tests := []intake.Test{
		{Module: "first", Suite: "shared", Name: "same", Parameters: `{"case":1}`, Status: "pass"},
		{Module: "first", Suite: "shared", Name: "same", Parameters: `{"case":2}`, Status: "skip"},
		{Module: "second", Suite: "shared", Name: "same", Parameters: `{"case":1}`, Status: "fail"},
	}
	model := buildReport(t.TempDir(), intake.Facts{
		CoverageLevel: "suite", Tests: tests,
		SuiteCoverages: []intake.SuiteCoverage{
			{Module: "first", Suite: "shared", Files: []string{"first.js"}, CoveredTests: 1},
			{Module: "second", Suite: "shared", Files: []string{"second.js"}, CoveredTests: 1},
		},
	}, false)
	if len(model.Tests) != 3 || model.Tests[0].Label == model.Tests[1].Label || model.Tests[0].Label == model.Tests[2].Label {
		t.Fatalf("test labels are indistinguishable: %+v", model.Tests)
	}
	if len(model.Suites) != 2 || model.Suites[0].Name != "first › shared" || model.Suites[0].TestCount != 2 || model.Suites[0].CoveredCount != 1 || model.Suites[1].Name != "second › shared" || model.Suites[1].TestCount != 1 || model.Suites[1].CoveredCount != 1 || model.Suites[1].Status != "Failed" {
		t.Fatalf("suite identities merged: %+v", model.Suites)
	}
	if len(model.Suites[0].CoveredFiles) != 1 || model.Suites[0].CoveredFiles[0] != "first.js" || len(model.Suites[1].CoveredFiles) != 1 || model.Suites[1].CoveredFiles[0] != "second.js" {
		t.Fatalf("suite coverage crossed module boundaries: %+v", model.Suites)
	}
	if model.Suites[0].Tests[0].Name == model.Suites[0].Tests[1].Name {
		t.Fatalf("parameterized tests are indistinguishable: %+v", model.Suites[0].Tests)
	}
}

func TestReportSkippedOutcomesStayDistinctFromFailures(t *testing.T) {
	test := intake.Test{Name: "skipped", Suite: "suite", Status: "skip", Attempts: []intake.TestRun{{Status: "skip"}}}
	model := buildReport(t.TempDir(), intake.Facts{Tests: []intake.Test{test}}, false)
	for _, status := range []string{model.Tests[0].Status, model.Tests[0].Attempts[0].Status, model.Suites[0].Status, model.Suites[0].Tests[0].Status} {
		if reportStatus(status) != "Skipped" {
			t.Fatalf("skipped result is displayed as %q", status)
		}
	}
}

func TestReportListsCoveredPathsWithoutCheckingTheirAvailability(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "present.js"), []byte("export const value = 1;"), 0644); err != nil {
		t.Fatal(err)
	}
	files := []string{"present.js", "deleted.js"}
	for _, level := range []string{"test", "suite"} {
		facts := intake.Facts{
			CoverageLevel: level,
			Tests:         []intake.Test{{Name: "covered", Suite: "suite", Status: "pass"}},
			BroadCoverage: []intake.CoverageFact{{Name: "broad", Level: level, Files: files}},
		}
		if level == "test" {
			facts.Tests[0].CoverageLevel = "test"
			facts.Tests[0].CoveredFiles = files
		} else {
			facts.SuiteCoverages = []intake.SuiteCoverage{{Suite: "suite", Files: files, CoveredTests: 1}}
		}
		report := renderTestReport(t, root, facts)
		if !strings.Contains(report, "deleted.js</li>") || !strings.Contains(report, "present.js</li>") || strings.Contains(report, "missing source") {
			t.Fatalf("covered paths must be displayed without availability labels in %s report", level)
		}
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
	tests := []intake.Test{
		{Name: "flaky", Status: "pass", Duration: time.Millisecond, Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass"}}},
		{Name: "failed", Status: "fail", Duration: 2 * time.Millisecond},
	}
	model := buildReport(t.TempDir(), intake.Facts{Tests: tests}, true)
	if !model.NoTestEvents || model.Summary != "Check the instrumentation setup." {
		t.Fatalf("no events = %v, summary = %q", model.NoTestEvents, model.Summary)
	}
	if len(model.Suites) != 1 || model.Suites[0].Name != "Unknown suite" || model.Suites[0].Status != "Failed" {
		t.Fatalf("suite rollup = %#v", model.Suites)
	}
}

func renderTestReport(t *testing.T, repositoryRoot string, findings intake.Facts) string {
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

func TestReportSurfacesConfigurationErrorsDespiteReceivedTests(t *testing.T) {
	model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{{Name: "fails"}}, ConfigurationErrors: []string{"skippable_tests"}}, false)
	if model.NoTestEvents {
		t.Fatal("received events must not show the missing-events warning")
	}
	if !strings.Contains(model.Summary, "Tracer configuration errors: skippable_tests.") {
		t.Fatalf("missing configuration error: %s", model.Summary)
	}
}

func TestReportSurfacesEmptyCoverageAsTracerError(t *testing.T) {
	path, err := writeReport(t.TempDir(), t.TempDir(), intake.Facts{TestEventCount: 1, TestCount: 1, EmptyCoverageEntryCount: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Tracer error: empty coverage entries", "2 coverage entries had an empty files list", "Affected payloads were excluded", "Inspect the captured traffic"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("missing %q in report", expected)
		}
	}
	if strings.Contains(string(data), "No findings.") {
		t.Fatal("report hides tracer error as no findings")
	}
}

func TestReportRuntimeFacts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		events        int
		commandFailed bool
		tracer        string
		wantStatus    string
	}{
		{"passing with reused tracer", 1, false, "dd-trace@6.15.0 · reused", "Passed"},
		{"failing with installed tracer", 1, true, "dd-trace@6.15.0", "Failed"},
		{"successful command without events", 0, false, "dd-trace@6.15.0 · reused", "No test results received"},
		{"failed command without events", 0, true, "dd-trace@6.15.0", "No test results received"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := buildReport(t.TempDir(), intake.Facts{TestEventCount: tc.events}, tc.commandFailed, reportRuntime{Framework: "Jest", Tracer: tc.tracer})
			facts := make(map[string]string)
			for _, fact := range model.Facts {
				facts[fact.Label] = fact.Value
			}
			if facts["Jest"] != tc.wantStatus || facts["Datadog library"] != tc.tracer {
				t.Fatalf("incorrect runtime facts: %v", facts)
			}
			if tc.events == 0 && (!model.NoTestEvents || strings.Contains(model.Summary, "No findings.")) {
				t.Fatalf("report implies successful instrumentation without events: %+v", model)
			}
		})
	}
}

func TestAbsoluteFileURL(t *testing.T) {
	for path, want := range map[string]string{
		`C:\repo\report.html`:                "file:///C:/repo/report.html",
		`C:\project space\report.html`:       "file:///C:/project%20space/report.html",
		`\\server\share\report.html`:         "file://server/share/report.html",
		`\\server\share space\report#1.html`: "file://server/share%20space/report%231.html",
		`/repo space/report#1.html`:          "file:///repo%20space/report%231.html",
		`/repo\name/report.html`:             "file:///repo%5Cname/report.html",
	} {
		if got := absoluteFileURL(path); got != want {
			t.Errorf("absoluteFileURL(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestReportExplorerKeepsIdentitiesEscapedAndDetailsDistinct(t *testing.T) {
	tests := []intake.Test{
		{Name: `same <script>alert(1)</script>`, Module: "one", Suite: "shared", Parameters: `{"case":1}`, Status: "pass"},
		{Name: `same <script>alert(1)</script>`, Module: "two", Suite: "shared", Parameters: `{"case":2}`, Status: "fail"},
	}
	model := buildReport(t.TempDir(), intake.Facts{Tests: tests}, false)
	if model.Tests[0].Name == model.Tests[1].Name || model.Tests[0].Suite == model.Tests[1].Suite {
		t.Fatal("table loses module or parameter identity")
	}
	report := renderTestReport(t, t.TempDir(), intake.Facts{Tests: tests})
	for _, want := range []string{`data-status="Passed"`, `data-status="Failed"`, `aria-controls="test-detail-0"`, `aria-controls="test-detail-1"`, `id="test-detail-0"`, `id="test-detail-1"`, `&lt;script&gt;alert(1)&lt;/script&gt;`} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if strings.Contains(report, `same <script>`) {
		t.Fatal("test name can execute HTML")
	}
}

func TestReportStatusFiltersDistinguishFlakySkippedAndUnknown(t *testing.T) {
	for _, tc := range []struct{ status, want string }{
		{"Pass", "Passed"}, {"Passed", "Passed"}, {"Fail", "Failed"}, {"Failed", "Failed"}, {"Skip", "Skipped"}, {"Flaky", "Flaky"}, {"Unknown", "Unknown"}, {"", "Unknown"},
	} {
		if got := reportStatus(tc.status); got != tc.want {
			t.Errorf("reportStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
	model := buildReport(t.TempDir(), intake.Facts{Tests: []intake.Test{{Name: "retried", Status: "pass", Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true}}}}}, false)
	if reportStatus(model.Tests[0].Status) != "Flaky" || reportStatus(model.Suites[0].Status) != "Flaky" {
		t.Fatal("flaky result must not be counted as a clean pass")
	}
}

func TestReportTotalsAppearOnlyInRunDetails(t *testing.T) {
	for _, events := range []int{0, 1, 1363} {
		report := renderTestReport(t, t.TempDir(), intake.Facts{TestEventCount: events, Tests: []intake.Test{{Name: "one test"}}})
		_, header, _ := strings.Cut(report, `<header class="page-header">`)
		header, _, _ = strings.Cut(header, "</header>")
		if strings.Contains(header, "run-totals") || strings.Contains(header, "received") {
			t.Fatal("header must not show totals or event messages")
		}
		_, details, _ := strings.Cut(report, `id="run-details"`)
		details, _, _ = strings.Cut(details, "</section>")
		if !strings.Contains(details, `<p class="run-totals">1 test · 1 suite</p>`) {
			t.Fatal("run details missing test and suite totals")
		}
		if strings.Contains(report, "1363") || strings.Contains(report, "test event received") || strings.Contains(report, "test events received") && events > 0 {
			t.Fatal("report still displays received event counts")
		}
		if strings.Contains(details, `<p class="no-test-events">No test events received.</p>`) != (events == 0) {
			t.Fatal("missing-events warning must match event availability")
		}
	}
}

func TestReportRunDetailsAlwaysVisibleAfterFindings(t *testing.T) {
	for _, hasFindings := range []bool{false, true} {
		facts := intake.Facts{TestEventCount: 1}
		if hasFindings {
			facts.FailedTests = []intake.Test{{Name: "failed", Status: "fail"}}
		}
		report := renderTestReport(t, t.TempDir(), facts)
		start := strings.Index(report, `id="run-details"`)
		if start < 0 {
			t.Fatal("run details missing")
		}
		opening, _, _ := strings.Cut(report[start:], ">")
		if strings.Contains(opening, "hidden") || strings.Contains(report, "run-toggle") {
			t.Fatal("run details must be visible without a toggle")
		}
		if hasFindings && start < strings.Index(report, `id="finding-0"`) {
			t.Fatal("run details must follow finding cards and their details")
		}
		if start > strings.Index(report, `<nav class="tabs"`) {
			t.Fatal("run details must precede results")
		}
	}
}

func TestReportCoverageColumnsMatchReportedLevel(t *testing.T) {
	for _, level := range []string{"test", "suite", ""} {
		t.Run("level_"+level, func(t *testing.T) {
			report := renderTestReport(t, t.TempDir(), intake.Facts{
				CoverageLevel: level,
				Tests:         []intake.Test{{Name: "one", Suite: "suite", Status: "pass"}},
			})
			for _, view := range []struct {
				id, level string
				columns   int
			}{{"tests", "test", 7}, {"suites", "suite", 6}} {
				_, section, found := strings.Cut(report, `<section id="`+view.id+`"`)
				if !found {
					t.Fatalf("missing %s view", view.id)
				}
				section, _, _ = strings.Cut(section, "</section>")
				showCoverage := level == view.level
				if strings.Contains(section, `class="coverage-column"`) != showCoverage {
					t.Fatalf("%s coverage column does not match level %q", view.id, level)
				}
				columns := view.columns
				if showCoverage {
					columns++
				}
				_, row, _ := strings.Cut(section, `<tr class="result-main">`)
				row, _, _ = strings.Cut(row, "</tr>")
				if strings.Count(row, "<td") != columns || !strings.Contains(section, fmt.Sprintf(`colspan="%d" class="detail-cell"`, columns)) {
					t.Fatalf("%s cells and expanded detail span must match %d columns", view.id, columns)
				}
				if !showCoverage && strings.Contains(row, "Not reported") {
					t.Fatalf("%s shows an inapplicable coverage warning", view.id)
				}
			}
		})
	}
}

func TestReportCoverageSummaryCountsTheReportedLevel(t *testing.T) {
	tests := []intake.Test{
		{Module: "one", Suite: "shared", Name: "first"},
		{Module: "one", Suite: "shared", Name: "second"},
		{Module: "two", Suite: "shared", Name: "third"},
	}
	for _, tc := range []struct {
		name, level, label, value, tone string
		coverages                       []intake.SuiteCoverage
	}{
		{"test", "test", "Tests with coverage", "2 / 3", "attention", nil},
		{"partial suites", "suite", "Suites with coverage", "1 / 2", "attention", []intake.SuiteCoverage{{Module: "one", Suite: "shared", CoveredTests: 2}}},
		{"all suites", "suite", "Suites with coverage", "2 / 2", "good", []intake.SuiteCoverage{{Module: "one", Suite: "shared", CoveredTests: 2}, {Module: "two", Suite: "shared", CoveredTests: 1}}},
		{"no suite coverage", "suite", "Suites with coverage", "Not reported", "attention", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := intake.Facts{CoverageLevel: tc.level, Tests: tests, TestCount: 3, CoveredTestCount: 2, SuiteCoverages: tc.coverages}
			model := buildReport(t.TempDir(), facts, false)
			want := reportFact{Label: tc.label, Value: tc.value, Tone: tc.tone}
			if model.Facts[len(model.Facts)-1] != want {
				t.Fatalf("coverage summary = %+v, want %+v", model.Facts[len(model.Facts)-1], want)
			}
		})
	}
}

func TestSlowSuiteFindingShowsSuiteDetailsAndCounts(t *testing.T) {
	facts := intake.Facts{
		TestEventCount: 2, CoverageLevel: "suite", SuiteDurationMedian: time.Second,
		Tests:          []intake.Test{{Module: "module", Suite: "slow suite", Name: "first", Duration: 4 * time.Second}, {Module: "module", Suite: "slow suite", Name: "second", Duration: 2 * time.Second}},
		SlowSuites:     []intake.SlowSuite{{Module: "module", Suite: "slow suite", Duration: 6 * time.Second}},
		SuiteCoverages: []intake.SuiteCoverage{{Module: "module", Suite: "slow suite", Files: []string{"covered.go"}, CoveredTests: 2}},
	}
	report := renderTestReport(t, t.TempDir(), facts)
	_, panel, _ := strings.Cut(report, `id="finding-0"`)
	panel, _, _ = strings.Cut(panel, "</section>")
	for _, want := range []string{"Slow suites", "module › slow suite", "6s", "first", "second", "Suite coverage", "covered.go"} {
		if !strings.Contains(panel, want) {
			t.Errorf("suite finding missing %q", want)
		}
	}
	for _, want := range []string{"1 finding.", "Median suite time · 1s"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q", want)
		}
	}
	var console bytes.Buffer
	writeFindings(&console, facts)
	for _, want := range []string{"1 finding.", "Suites slower than the others (1)", "Median suite time: 1s", "module › slow suite · 6s"} {
		if !strings.Contains(console.String(), want) {
			t.Errorf("console missing %q", want)
		}
	}
}

func TestSuiteDetailsSeparateCoverageAndCollapsibleTests(t *testing.T) {
	for _, count := range []int{1, 2} {
		var tests []intake.Test
		for i := 0; i < count; i++ {
			tests = append(tests, intake.Test{Name: fmt.Sprintf("test %d", i), Suite: "suite"})
		}
		report := renderTestReport(t, t.TempDir(), intake.Facts{
			CoverageLevel: "suite", Tests: tests,
			SuiteCoverages: []intake.SuiteCoverage{{Suite: "suite", CoveredTests: count, Files: []string{"source.go"}}},
		})
		if strings.Contains(report, "tests with coverage") {
			t.Fatal("suite coverage summary contains test coverage count")
		}
		_, list, found := strings.Cut(report, `<details class="suite-test-list">`)
		if !found {
			t.Fatal("suite test list is not initially collapsed")
		}
		list, _, _ = strings.Cut(list, "</details>")
		summary, _, _ := strings.Cut(list, "</summary>")
		want := fmt.Sprintf(`<summary>Tests · %d %s`, count, plural(count, "test", "tests"))
		if !strings.Contains(summary, want) {
			t.Fatalf("missing count in test list header: %s", want)
		}
	}
}

func TestReportSortingUsesExactDurationsAndAvailableColumns(t *testing.T) {
	for _, level := range []string{"test", "suite", ""} {
		facts := intake.Facts{CoverageLevel: level, Tests: []intake.Test{
			{Name: "short", Suite: "one", Duration: 900 * time.Millisecond},
			{Name: "long", Suite: "one", Attempts: []intake.TestRun{{Duration: 2 * time.Second}}},
		}}
		model := buildReport(t.TempDir(), facts, false)
		if model.Tests[0].DurationNanos != int64(900*time.Millisecond) || model.Tests[1].DurationNanos != int64(2*time.Second) || model.Suites[0].DurationNanos != int64(2900*time.Millisecond) {
			t.Fatal("sorting must use exact durations, including attempt fallback and suite totals")
		}
		report := renderTestReport(t, t.TempDir(), facts)
		for _, view := range []struct {
			id, level string
			keys      []string
		}{
			{"tests", "test", []string{"index", "status", "name", "file", "duration", "attempts"}},
			{"suites", "suite", []string{"index", "status", "name", "count", "duration"}},
		} {
			_, section, _ := strings.Cut(report, `<section id="`+view.id+`"`)
			section, _, _ = strings.Cut(section, "</section>")
			for _, key := range view.keys {
				if !strings.Contains(section, `data-sort="`+key+`"`) {
					t.Errorf("%s missing sort control for %s", view.id, key)
				}
			}
			if strings.Contains(section, `data-sort="coverage"`) != (level == view.level) {
				t.Fatal("coverage sorting must match the visible coverage column")
			}
			want := `data-sort-value="900000000"`
			if view.id == "suites" {
				want = `data-sort-value="2900000000"`
			}
			if !strings.Contains(section, want) {
				t.Fatalf("missing numeric sort value %s", want)
			}
		}
	}
}

func TestSuiteTestLinksTargetExactTest(t *testing.T) {
	facts := intake.Facts{Tests: []intake.Test{
		{Module: "z", Suite: "shared", Name: "same", Parameters: "first"},
		{Module: "a", Suite: "shared", Name: "same"},
		{Module: "z", Suite: "shared", Name: "same", Parameters: "second"},
		{Module: "z", Suite: "other", Name: "same"},
	}, SlowSuites: []intake.SlowSuite{{Module: "z", Suite: "shared"}}}
	model := buildReport(t.TempDir(), facts, false)
	seen := make(map[int]bool)
	for _, suite := range model.Suites {
		for _, test := range suite.Tests {
			original := facts.Tests[test.TestIndex]
			if suite.Key != original.Module+"\x00"+original.Suite || test.Name != model.Tests[test.TestIndex].Name {
				t.Fatalf("suite test targets a different test: %+v", test)
			}
			if seen[test.TestIndex] {
				t.Fatalf("duplicate target: %d", test.TestIndex)
			}
			seen[test.TestIndex] = true
		}
	}
	if len(seen) != len(facts.Tests) {
		t.Fatal("not all tests have navigation targets")
	}
	report := renderTestReport(t, t.TempDir(), facts)
	for index := range facts.Tests {
		want := 1
		if index == 0 || index == 2 {
			want++ // The slow-suite finding uses the same destination.
		}
		if got := strings.Count(report, fmt.Sprintf(`data-open-test="test-detail-%d"`, index)); got != want {
			t.Errorf("test %d has %d links, want %d", index, got, want)
		}
		if got := strings.Count(report, fmt.Sprintf(`id="test-detail-%d"`, index)); got != 1 {
			t.Errorf("test %d has %d destinations, want 1", index, got)
		}
	}
}

func TestSuiteCoverageDistinguishesUnreportedFromEmpty(t *testing.T) {
	facts := intake.Facts{
		CoverageLevel: "suite",
		Tests: []intake.Test{
			{Module: "a", Suite: "shared", Name: "covered"},
			{Module: "b", Suite: "shared", Name: "unreported"},
			{Module: "c", Suite: "shared", Name: "empty"},
		},
		SuiteCoverages: []intake.SuiteCoverage{
			{Module: "a", Suite: "shared", Files: []string{"source.js"}, CoveredTests: 1},
			{Module: "c", Suite: "shared", CoveredTests: 1},
		},
		SlowSuites: []intake.SlowSuite{{Module: "b", Suite: "shared"}},
	}
	model := buildReport(t.TempDir(), facts, false)
	for index, want := range []bool{true, false, true} {
		if model.Suites[index].ShowCoverage != want {
			t.Errorf("suite %d coverage availability = %v, want %v", index, model.Suites[index].ShowCoverage, want)
		}
	}
	report := renderTestReport(t, t.TempDir(), facts)
	_, section, _ := strings.Cut(report, `<section id="suites"`)
	section, _, _ = strings.Cut(section, "</section>")
	rows := strings.Split(section, `<tbody data-result`)[1:]
	for index, want := range []struct{ label, sortValue string }{
		{"1 files", "1"}, {"Not reported", ""}, {"0 files", "0"},
	} {
		row, _, _ := strings.Cut(rows[index], "</tr>")
		if !strings.Contains(row, want.label) || !strings.Contains(row, `class="duration" data-sort-value="`+want.sortValue+`"`) {
			t.Errorf("suite %d missing coverage label %q or sort value %q", index, want.label, want.sortValue)
		}
		if strings.Contains(rows[index], `<details class="coverage">`) != (index != 1) {
			t.Errorf("suite %d coverage details do not match reported data", index)
		}
	}
	_, finding, _ := strings.Cut(report, `id="finding-0"`)
	finding, _, _ = strings.Cut(finding, `id="run-details"`)
	if strings.Contains(finding, `<details class="coverage">`) {
		t.Fatal("slow-suite finding must not invent coverage for an uncovered suite")
	}
}
