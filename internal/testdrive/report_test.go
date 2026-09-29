// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
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
		`class="page-item">src/one.js · missing source`,
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

func TestReportBuilderReusesSourceAndFileChecksWithinOneReport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.test.js")
	if err := os.WriteFile(path, []byte("test('one', () => {});\ntest('two', () => {});\n"), 0644); err != nil {
		t.Fatal(err)
	}
	builder := newReportBuilder(root)
	firstSource := builder.readSource("example.test.js", 1, 0)
	firstFiles := builder.reportCoveredFiles([]string{"example.test.js"})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	secondSource := builder.readSource("example.test.js", 2, 2)
	repeatedSource := builder.readSource("example.test.js", 1, 0)
	secondFiles := builder.reportCoveredFiles([]string{"example.test.js"})
	if len(firstSource.Lines) != 2 || len(secondSource.Lines) != 1 || secondSource.Lines[0].Number != 2 || len(repeatedSource.Lines) != 2 || firstSource.Lines[0].Code != repeatedSource.Lines[0].Code || firstFiles[0].Missing || secondFiles[0].Missing {
		t.Fatalf("report builder changed its source snapshot: first=%+v/%+v second=%+v/%+v repeated=%+v", firstSource, firstFiles, secondSource, secondFiles, repeatedSource)
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
	if len(model.Suites[0].CoveredFiles) != 1 || model.Suites[0].CoveredFiles[0].Name != "first.js" || len(model.Suites[1].CoveredFiles) != 1 || model.Suites[1].CoveredFiles[0].Name != "second.js" {
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

func TestReportShowsMissingCoveredFiles(t *testing.T) {
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
		model := buildReport(root, facts, false)
		var covered []reportCoveredFile
		if level == "test" {
			covered = model.Tests[0].CoveredFiles
		} else {
			covered = model.Suites[0].CoveredFiles
		}
		missing := make(map[string]bool)
		for _, file := range covered {
			missing[file.Name] = file.Missing
		}
		if len(covered) != 2 || missing["present.js"] || !missing["deleted.js"] || len(model.Cards[0].Coverages) != 1 || !model.Cards[0].Coverages[0].Files[0].Missing {
			t.Fatalf("missing source is hidden at %s level: files=%+v card=%+v", level, covered, model.Cards[0])
		}
		report := renderTestReport(t, root, facts)
		if !strings.Contains(report, "deleted.js · missing source") || !strings.Contains(report, "present.js</li>") || strings.Contains(report, "present.js · missing source") {
			t.Fatalf("covered file status is incorrect in %s report", level)
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
	if model.Headline != "No test events received." || model.Summary != "Check the instrumentation setup." {
		t.Fatalf("headline = %q, summary = %q", model.Headline, model.Summary)
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
	if model.Headline != "Test events received." {
		t.Fatalf("headline overstates verification: %s", model.Headline)
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
		{"failing with isolated tracer", 1, true, "dd-trace@6.15.0 · isolated", "Failed"},
		{"successful command without events", 0, false, "dd-trace@6.15.0 · reused", "No test results received"},
		{"failed command without events", 0, true, "dd-trace@6.15.0 · isolated", "No test results received"},
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
			if tc.events == 0 && (model.Headline != "No test events received." || strings.Contains(model.Summary, "No findings.")) {
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
