// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func TestFindingsShareConsoleTroubleshootingAndCompleteDiagnostics(t *testing.T) {
	failed := intake.Test{
		Name: "greets", Suite: "greeting.feature", SourceFile: "features/steps.js", SourceStart: 4, SourceEnd: 9, Status: "fail",
		Attempts: []intake.TestRun{{Status: "fail", Duration: time.Millisecond, ErrorType: "AssertionError", ErrorMessage: "expected hello", ErrorStack: "AssertionError: expected hello\n    at features/steps.js:6"}},
	}
	passed := intake.Test{Name: "greets", Suite: "greeting.feature", Status: "pass", Duration: time.Millisecond, CoverageLevel: "test", CoveredFiles: []string{"src/greet.js"}}
	for _, tc := range []struct {
		name         string
		facts        intake.Facts
		commandError error
		wantError    string
		wantAdvice   bool
		mocha        bool
		cypress      bool
		browserInfo  bool
		nycInstalled bool
		nycDeclared  bool
	}{
		{name: "missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true},
		{name: "Mocha missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true, mocha: true},
		{name: "Cypress missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true, cypress: true},
		{name: "Cypress with coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1, CoverageLevel: "test", Tests: []intake.Test{passed}}, cypress: true},
		{name: "Mocha with installed nyc missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true, mocha: true, nycInstalled: true},
		{name: "Cucumber with declared nyc missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true, nycDeclared: true},
		{name: "covered tests", facts: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1, CoverageLevel: "test", Tests: []intake.Test{passed}}},
		{name: "covered suite", facts: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1, CoverageLevel: "suite", Tests: []intake.Test{passed}, SuiteCoverages: []intake.SuiteCoverage{{Suite: passed.Suite, CoveredTests: 1, Files: []string{"src/greet.js"}}}}},
		{name: "failed command", facts: intake.Facts{TestEventCount: 1, TestCount: 1, Tests: []intake.Test{failed}, FailedTests: []intake.Test{failed}}, commandError: errors.New("exit status 1"), wantError: "failed after sending"},
		{name: "no events", wantError: "sent no test events", wantAdvice: true},
		{name: "browser coverage is information", facts: intake.Facts{TestEventCount: 1, TestCount: 1, BrowserCoverageUnavailable: true, Tests: []intake.Test{passed}}, wantAdvice: true, browserInfo: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			requireWriteFile(t, filepath.Join(drive.repositoryRoot, "package.json"), `{"devDependencies":{"@cucumber/cucumber":"13.2.1"}}`)
			drive.framework = framework.NewCucumber(platform.NewJavaScript())
			drive.command, drive.args = "npx", []string{"cucumber-js"}
			frameworkLabel := "Cucumber"
			if tc.mocha {
				requireWriteFile(t, filepath.Join(drive.repositoryRoot, "package.json"), `{"devDependencies":{"mocha":"11.7.6"}}`)
				drive.framework = framework.NewMocha(platform.NewJavaScript())
				drive.args = []string{"mocha"}
				frameworkLabel = "Mocha"
			}
			if tc.cypress {
				drive.framework = framework.NewCypress(platform.NewJavaScript())
				drive.args = []string{"cypress"}
				frameworkLabel = "Cypress"
			}
			if tc.browserInfo {
				drive.framework = framework.NewVitest(platform.NewJavaScript())
				drive.command, drive.args = "node", []string{"vitest.mjs"}
				frameworkLabel = "Vitest"
			}
			if tc.nycDeclared {
				requireWriteFile(t, filepath.Join(drive.repositoryRoot, "package.json"), `{"devDependencies":{"nyc":"17.0.0"}}`)
			}
			if tc.nycInstalled {
				bin := filepath.Join(drive.repositoryRoot, "node_modules", ".bin")
				if err := os.MkdirAll(bin, 0755); err != nil {
					t.Fatal(err)
				}
				requireWriteFile(t, filepath.Join(bin, "nyc"), "")
				if err := os.Chmod(filepath.Join(bin, "nyc"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			session := t.TempDir()
			command, args := drive.executionCommand()
			runtime := reportRuntime{Framework: frameworkLabel, Command: command + " " + strings.Join(args, " "), Tracer: "dd-trace@latest"}
			if tc.commandError != nil {
				runtime.Error = tc.commandError.Error()
			}
			model := buildReport(drive.repositoryRoot, tc.facts, tc.commandError != nil, runtime)
			var output bytes.Buffer
			writeFindings(&output, tc.facts, model.Cards...)
			path, err := writeDiagnostics(drive.repositoryRoot, session, tc.facts, model, tc.commandError != nil)
			if err != nil {
				t.Fatal(err)
			}
			model.Artifacts = append(model.Artifacts, reportArtifact{Title: "Findings JSON", Href: diagnosticsFilename})
			if _, err := writeReportModel(session, model); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var diagnostics runDiagnostics
			if err := json.Unmarshal(data, &diagnostics); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(diagnostics.Facts, tc.facts) {
				t.Fatalf("JSON did not preserve complete facts: %+v", diagnostics.Facts)
			}
			wantCommand := drive.command + " " + drive.args[0]
			if tc.mocha && tc.nycInstalled {
				wantCommand = "node_modules/.bin/nyc -- npx mocha"
			}
			if diagnostics.RepositoryRoot != drive.repositoryRoot || diagnostics.Command != wantCommand || diagnostics.Framework != frameworkLabel || diagnostics.DurationUnit != "nanoseconds" || diagnostics.CommandFailed != (tc.commandError != nil) {
				t.Fatalf("incorrect diagnostic context: %+v", diagnostics)
			}
			if tc.commandError != nil && diagnostics.CommandError != tc.commandError.Error() {
				t.Fatalf("missing command error: %+v", diagnostics)
			}
			if strings.Contains(output.String(), "1 finding.") {
				t.Fatal("console must print the finding instead of a standalone total")
			}
			if tc.facts.MissingCoverage && !strings.Contains(output.String(), intake.MissingCoverageContext+"\n  Fix:") {
				t.Fatalf("coverage guidance must immediately follow the concise finding:\n%s", output.String())
			}
			report, err := os.ReadFile(filepath.Join(session, reportFilename))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(report), `href="findings.json"`) {
				t.Fatal("HTML report must link to structured findings")
			}
			if tc.browserInfo {
				if diagnostics.RunFailed || len(diagnostics.Findings) != 1 || diagnostics.Findings[0].Severity != "info" || diagnostics.Findings[0].Kind != intake.BrowserCoverageFinding {
					t.Fatalf("browser coverage must be an informational finding: %+v", diagnostics)
				}
				for _, text := range []string{`data-kind="browser-coverage-unavailable" data-severity="info"`, `<span class="info-label">Info</span>`, `class="fact info"`, "Unavailable (browser projects)"} {
					if !strings.Contains(string(report), text) {
						t.Fatalf("report lacks informational treatment %q", text)
					}
				}
				if strings.Contains(string(report), `<article class="problem-card" data-kind="missing-coverage"`) || strings.Contains(output.String(), intake.MissingCoverageContext) {
					t.Fatal("known browser limitation must not also appear as unexpected missing coverage")
				}
				if !strings.Contains(output.String(), "Info: "+intake.BrowserCoverageTitle) {
					t.Fatal("console must identify the informational finding")
				}
			}
			if tc.wantAdvice {
				if len(diagnostics.Troubleshooting) != 1 {
					t.Fatalf("missing troubleshooting: %+v", diagnostics)
				}
				advice := diagnostics.Troubleshooting[0]
				if tc.cypress {
					for _, text := range []string{"Test Impact Analysis data", "babel-plugin-istanbul", "vite-plugin-istanbul", "window.__coverage__"} {
						if !strings.Contains(advice.Text, text) {
							t.Fatalf("Cypress advice lacks setup instruction %q: %+v", text, advice)
						}
					}
					if advice.Finding != intake.MissingCoverageFinding || !strings.HasSuffix(advice.URL, "#cypress") || strings.Contains(advice.Text, "nyc ddtest") {
						t.Fatalf("Cypress needs browser instrumentation guidance: %+v", advice)
					}
				}
				if tc.nycInstalled && (advice.Title != "Check NYC coverage configuration" || !strings.Contains(advice.Text, "include/exclude") || strings.Contains(advice.Text, "./node_modules/.bin/nyc ddtest")) {
					t.Fatalf("installed NYC still needs actionable coverage guidance: %+v", advice)
				}
				if tc.nycDeclared && advice.Title != "Install the declared nyc dependency" {
					t.Fatalf("declared NYC executable is missing: %+v", advice)
				}
				for _, text := range []string{advice.Title, advice.Text, advice.URL} {
					if !strings.Contains(output.String(), strings.ReplaceAll(text, "\n", "\n    ")) || !strings.Contains(html.UnescapeString(string(report)), text) {
						t.Fatalf("console and report must share advice %q", text)
					}
				}
			} else if len(diagnostics.Troubleshooting) != 0 || strings.Contains(output.String(), "  Fix:") {
				t.Fatal("must not invent troubleshooting without supporting evidence")
			}

		})
	}
}

func TestConsoleFindingIncludesSourceAndEveryAttemptError(t *testing.T) {
	flaky := intake.Test{
		Module: "api", Suite: "greeting.test.js", Name: "greets", Parameters: "[world]", SourceFile: "test/greeting.test.js", SourceStart: 4, SourceEnd: 9,
		Attempts: []intake.TestRun{
			{Status: "fail", Duration: time.Millisecond, ErrorType: "AssertionError", ErrorMessage: "expected hello\nreceived goodbye", ErrorStack: "full stack"},
			{Status: "pass", Duration: 2 * time.Millisecond, Retry: true, RetryReason: "automatic_test_retry"},
		},
	}
	var output bytes.Buffer
	writeFindings(&output, intake.Facts{TestEventCount: 2, FlakyTests: []intake.Test{flaky}, BroadCoverage: []intake.CoverageFact{{Name: "broad", Level: "test", SourceFile: "test/broad.test.js", SourceStart: 12, FileCount: 50}}, CoveredFilesMedian: 2})
	for _, text := range []string{
		"api › greeting.test.js › greets [world]", "Source: test/greeting.test.js:4-9",
		"Run 1 · Initial run · Fail · 1ms", "AssertionError: expected hello\n      received goodbye",
		"Run 2 · Retry · automatic test retry · Pass · 2ms", "Source: test/broad.test.js:12",
	} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("console hides important finding detail %q: %s", text, output.String())
		}
	}
}

func TestRunKeepsValidationReportWhenAnalysisOrReportFails(t *testing.T) {
	for _, tc := range []struct {
		name, wantError string
		analysisError   error
		blockReport     bool
		wantFindings    bool
	}{
		{name: "analysis", wantError: "invalid events", analysisError: errors.New("invalid events")},
		{name: "report", wantError: "save testdrive HTML report", blockReport: true, wantFindings: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			drive.framework = framework.NewMocha(platform.NewJavaScript())
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			drive.executor = &fakeTestdriveExecutor{output: []byte("saved output")}
			drive.startIntake = func(directory string, _ intake.Scenario) (localIntake, error) {
				if tc.blockReport {
					if err := os.MkdirAll(htmlReportPath(drive.repositoryRoot), 0755); err != nil {
						t.Fatal(err)
					}
				}
				return &fakeIntake{url: "http://127.0.0.1:1234", findingsErr: tc.analysisError, findings: intake.Facts{TestEventCount: 1}}, nil
			}
			var output bytes.Buffer
			err := drive.Run(t.Context(), &output)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("Run() error = %v, want %q", err, tc.wantError)
			}
			data, err := os.ReadFile(validationPath(drive.repositoryRoot))
			if err != nil {
				t.Fatal(err)
			}
			var result validationResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(result.Error, tc.wantError) {
				t.Fatal(result.Error)
			}
			if strings.Contains(output.String(), "Full test output:") {
				t.Fatal("must not link removed scratch files")
			}

		})
	}
}
