// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestRunSharesConsoleTroubleshootingAndCompleteDiagnostics(t *testing.T) {
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
	}{
		{name: "missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true},
		{name: "Mocha missing coverage", facts: intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true, Tests: []intake.Test{passed}}, wantAdvice: true, mocha: true},
		{name: "covered tests", facts: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1, CoverageLevel: "test", Tests: []intake.Test{passed}}},
		{name: "covered suite", facts: intake.Facts{TestEventCount: 1, TestCount: 1, CoveredTestCount: 1, CoverageLevel: "suite", Tests: []intake.Test{passed}, SuiteCoverages: []intake.SuiteCoverage{{Suite: passed.Suite, CoveredTests: 1, Files: []string{"src/greet.js"}}}}},
		{name: "failed command", facts: intake.Facts{TestEventCount: 1, TestCount: 1, Tests: []intake.Test{failed}, FailedTests: []intake.Test{failed}}, commandError: errors.New("exit status 1"), wantError: "failed after sending"},
		{name: "no events", wantError: "sent no test events", wantAdvice: true},
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
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			const commandOutput = "Complete command log\nwith additional details\n"
			drive.executor = &fakeTestdriveExecutor{output: []byte(commandOutput), err: tc.commandError}
			drive.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: tc.facts}, nil
			}
			var output bytes.Buffer
			err := drive.Run(t.Context(), &output)
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("Run() error = %v, want %q", err, tc.wantError)
			}
			data, err := os.ReadFile(filepath.Join(installer.sessionDirectory, diagnosticsFilename))
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
			if diagnostics.RepositoryRoot != drive.repositoryRoot || diagnostics.Command != "npx "+drive.args[0] || diagnostics.Framework != frameworkLabel || diagnostics.DurationUnit != "nanoseconds" || diagnostics.CommandFailed != (tc.commandError != nil) {
				t.Fatalf("incorrect diagnostic context: %+v", diagnostics)
			}
			if tc.commandError != nil && diagnostics.CommandError != tc.commandError.Error() {
				t.Fatalf("missing command error: %+v", diagnostics)
			}
			if tc.facts.CoverageLevel == "suite" && (!strings.Contains(output.String(), "Suites with coverage: 1 / 1") || strings.Contains(output.String(), "Tests with coverage:")) {
				t.Fatal("console must report coverage at the active suite level")
			}
			report, err := os.ReadFile(filepath.Join(installer.sessionDirectory, reportFilename))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(report), `href="findings.json"`) {
				t.Fatal("HTML report must link to structured findings")
			}
			if tc.wantAdvice {
				if len(diagnostics.Troubleshooting) != 1 {
					t.Fatalf("missing troubleshooting: %+v", diagnostics)
				}
				advice := diagnostics.Troubleshooting[0]
				for _, text := range []string{advice.Title, advice.Text, advice.URL} {
					if !strings.Contains(output.String(), text) || !strings.Contains(string(report), text) {
						t.Fatalf("console and report must share advice %q", text)
					}
				}
			} else if len(diagnostics.Troubleshooting) != 0 || strings.Contains(output.String(), "\nTroubleshooting:") {
				t.Fatal("must not invent troubleshooting without supporting evidence")
			}
			for _, filename := range []string{diagnosticsFilename, testOutputFilename, "intake"} {
				label, err := filepath.Rel(drive.repositoryRoot, filepath.Join(installer.sessionDirectory, filename))
				if err != nil || !strings.Contains(output.String(), label) {
					t.Fatalf("console does not identify %q: %s", filename, output.String())
				}
			}
			for _, text := range []string{drive.repositoryRoot, "Findings and troubleshooting (JSON):", "JSON durations: nanoseconds", "Source paths: repository-relative", "intake/*.json"} {
				if !strings.Contains(output.String(), text) {
					t.Fatalf("missing diagnostic label %q", text)
				}
			}
			for _, text := range []string{"For coding agents:", "Read findings.json", "Read test-output.txt", "After addressing the findings"} {
				if strings.Contains(output.String(), text) {
					t.Fatalf("diagnostics must use compact labels instead of prose: %q", text)
				}
			}
			if strings.Count(output.String(), "Full test output:") != 1 {
				t.Fatal("full command output path must be shown once on success and failure")
			}
			log, err := os.ReadFile(filepath.Join(installer.sessionDirectory, testOutputFilename))
			if err != nil || string(log) != commandOutput {
				t.Fatalf("complete command output was not preserved: %v", err)
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

func TestRunKeepsDiagnosticPathsWhenAnalysisOrReportFails(t *testing.T) {
	for _, tc := range []struct {
		name, wantError string
		analysisError   error
		blockReport     bool
		wantFindings    bool
	}{
		{name: "analysis", wantError: "invalid events", analysisError: errors.New("invalid events")},
		{name: "report", wantError: "create testdrive report", blockReport: true, wantFindings: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			drive.executor = &fakeTestdriveExecutor{output: []byte("saved output")}
			drive.startIntake = func(directory string) (localIntake, error) {
				if tc.blockReport {
					if err := os.Mkdir(filepath.Join(directory, reportFilename), 0755); err != nil {
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
			for _, filename := range []string{testOutputFilename, "intake"} {
				label, _ := filepath.Rel(drive.repositoryRoot, filepath.Join(installer.sessionDirectory, filename))
				if !strings.Contains(output.String(), label) {
					t.Fatalf("diagnostic failure hides %q: %s", filename, output.String())
				}
			}
			if strings.Contains(output.String(), "Findings and troubleshooting (JSON):") != tc.wantFindings {
				t.Fatal("console must link only to successfully saved findings")
			}
			if !tc.wantFindings && !strings.Contains(output.String(), "Structured findings: unavailable (collection error)") {
				t.Fatal("console must explain that analysis is incomplete")
			}
		})
	}
}
