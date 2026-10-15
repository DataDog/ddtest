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
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestRunFailureFindingsAcrossReportConsoleAndJSON(t *testing.T) {
	failed := intake.Test{Name: "assertion", Status: "fail"}
	for _, tc := range []struct {
		name, command, log, kind, advice, framework string
		facts                                       intake.Facts
		err                                         error
	}{
		{name: "UFO bare node regression", command: "node", kind: "no-test-events", advice: "No test runner was launched"},
		{name: "successful runner without instrumentation", command: "vitest", log: "Tests passed", kind: "no-test-events", advice: "Verify test execution and instrumentation"},
		{name: "startup failure", command: "vitest", log: "Error: cannot load config <config.ts>", err: errors.New("exit status 1"), kind: "command-failed", advice: "Check the command error"},
		{name: "failure after passing events", command: "vitest", log: "Error: teardown failed", err: errors.New("exit status 1"), facts: intake.Facts{TestEventCount: 1}, kind: "command-failed", advice: "Check the command error"},
		{name: "reported assertion needs no extra finding", command: "vitest", err: errors.New("exit status 1"), facts: intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{failed}}, kind: "failed"},
		{name: "pytest plugin not loaded", framework: "pytest", command: "pytest", log: "ERROR: usage: pytest [options]\npytest: error: unrecognized arguments: --ddtrace -q\n", err: errors.New("exit status 4"), kind: "pytest-plugin-not-loaded", advice: "Load the ddtrace pytest plugin in CI"},
		{name: "unrelated pytest option", framework: "pytest", command: "pytest", log: "pytest: error: unrecognized arguments: --other\n", err: errors.New("exit status 4"), kind: "command-failed", advice: "Check the command error"},
		{name: "different ddtrace option", framework: "pytest", command: "pytest", log: "pytest: error: unrecognized arguments: --ddtrace-patch-all\n", err: errors.New("exit status 4"), kind: "command-failed", advice: "Check the command error"},
		{name: "pytest instrumented before later error", framework: "pytest", command: "pytest", log: "pytest: error: unrecognized arguments: --ddtrace\n", err: errors.New("exit status 4"), facts: intake.Facts{TestEventCount: 1}, kind: "command-failed", advice: "Check the command error"},
		{name: "explicit pytest loading with autoload disabled", framework: "pytest", command: "pytest", log: "1 passed", facts: intake.Facts{TestEventCount: 1, TestCount: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			drive.framework = framework.NewVitest(platform.NewJavaScript())
			if tc.framework == "pytest" {
				drive.framework = framework.NewPytest(platform.NewPython())
				drive.language = "python"
				t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
				if tc.kind == "" {
					t.Setenv("PYTEST_PLUGINS", "ddtrace.testing.internal.pytest.entry_point")
				}
			}
			drive.command, drive.args = tc.command, nil
			installer := &fakeTracer{preloadPath: "/tmp/dd-trace/ci/init.js"}
			drive.platform = installer
			drive.executor = &fakeTestdriveExecutor{output: []byte(tc.log), err: tc.err}
			drive.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: tc.facts}, nil
			}
			var console bytes.Buffer
			runErr := drive.Run(t.Context(), &console)
			if tc.kind == "" {
				require.NoError(t, runErr)
			} else {
				require.Error(t, runErr)
			}
			data, err := os.ReadFile(filepath.Join(drive.session.Directory(), diagnosticsFilename))
			require.NoError(t, err)
			var diagnostics runDiagnostics
			require.NoError(t, json.Unmarshal(data, &diagnostics))
			require.Equal(t, tc.kind != "", diagnostics.RunFailed)
			require.Equal(t, tc.err != nil, diagnostics.CommandFailed)
			if tc.kind == "" {
				require.Empty(t, diagnostics.Findings)
				require.Empty(t, diagnostics.Troubleshooting)
				require.Contains(t, console.String(), "No findings.")
				return
			}
			require.Len(t, diagnostics.Findings, 1)
			require.Equal(t, tc.kind, diagnostics.Findings[0].Kind)
			require.Equal(t, "error", diagnostics.Findings[0].Severity)
			require.Equal(t, "1 finding.", diagnostics.Summary)
			require.NotContains(t, console.String(), "1 finding.")
			page, err := os.ReadFile(filepath.Join(drive.session.Directory(), reportFilename))
			require.NoError(t, err)
			report := string(page)
			require.Contains(t, report, `<article class="problem-card" data-kind="`+tc.kind+`" data-severity="error">`)
			require.Contains(t, report, `<details class="command-output" open>`)
			require.Contains(t, html.UnescapeString(report), tc.log)
			require.NotContains(t, report, "Check the instrumentation setup.")
			if tc.advice != "" {
				require.Len(t, diagnostics.Troubleshooting, 1)
				require.Equal(t, tc.advice, diagnostics.Troubleshooting[0].Title)
				for _, text := range []string{diagnostics.Findings[0].Context, tc.advice, diagnostics.Troubleshooting[0].Text} {
					require.Contains(t, console.String(), text)
					require.Contains(t, html.UnescapeString(report), text)
				}
			} else {
				require.Empty(t, diagnostics.Troubleshooting)
				require.Equal(t, 1, strings.Count(console.String(), "Failed tests"))
			}
			if strings.Contains(tc.log, "<config.ts>") {
				require.Contains(t, report, "&lt;config.ts&gt;")
			}
		})
	}
}

func TestRunFindingPresentationDoesNotDependOnKind(t *testing.T) {
	root := t.TempDir()
	facts := intake.Facts{}
	model := buildReport(root, facts, true, reportRuntime{Framework: "pytest", Command: "pytest", Error: "exit status 4"})
	require.Len(t, model.Cards, 1)
	card := &model.Cards[0]
	card.Kind = "new-run-diagnostic"
	card.Advices[0].Finding = card.Kind

	var console bytes.Buffer
	writeFindings(&console, facts, model.Cards...)
	require.Contains(t, console.String(), card.Title+": "+card.Context)
	require.Contains(t, console.String(), card.Advices[0].Title)

	path, err := writeReportModel(root, model)
	require.NoError(t, err)
	page, err := os.ReadFile(path)
	require.NoError(t, err)
	_, article, found := strings.Cut(string(page), `<article class="problem-card"`)
	require.True(t, found)
	article, _, _ = strings.Cut(article, "</article>")
	require.Contains(t, article, `data-kind="new-run-diagnostic" data-severity="error"`)
	require.Contains(t, article, "bi-exclamation-circle")
	require.Contains(t, article, card.Title)
	require.Contains(t, article, card.Advices[0].Title)

	path, err = writeDiagnostics(root, root, facts, model, true)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var diagnostics runDiagnostics
	require.NoError(t, json.Unmarshal(data, &diagnostics))
	require.Len(t, diagnostics.Findings, 1)
	require.Equal(t, card.Kind, diagnostics.Findings[0].Kind)
	require.Equal(t, card.Severity, diagnostics.Findings[0].Severity)
	require.Equal(t, card.Advices, diagnostics.Troubleshooting)
}
