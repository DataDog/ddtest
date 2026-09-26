// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/stretchr/testify/require"
)

func pairedReport(session, featureStatus string) validationResult {
	return validationResult{Session: session, Framework: "jest", Tracer: "dd-trace@6.17.0",
		Compatibility: verdict{Status: "compatible"},
		ProjectChecks: []projectProbeCheck{{Project: "Web", Discovered: true, Probe: "src/probe.test.js"}},
		Features:      []featureResult{{Project: "Web", Name: "skipping", Status: featureStatus}},
		Runs: []runSummary{
			(validationRun{Name: "baseline", Command: "pnpm exec jest --config original.js", ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
			(validationRun{Name: "reporting-only", Command: "pnpm exec jest --config original.js", Instrumented: true, ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
			(validationRun{Project: "Web", Name: "skipping", Command: "pnpm exec jest --config original.js --selectProjects Web --runTestsByPath src/probe.test.js", Instrumented: true, ExitCode: 1}).summary(),
		}}
}

func readValidationReport(t *testing.T, root string) validationResult {
	t.Helper()
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	return report
}

func TestCheckOnlyRetainsPairedExecutionWithoutReusingItsVerdict(t *testing.T) {
	for _, featureStatus := range []string{"passed", "failed"} {
		t.Run(featureStatus, func(t *testing.T) {
			run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
			root := run.repositoryRoot
			err := finishValidation(&bytes.Buffer{}, root, pairedReport("original", featureStatus))
			require.Equal(t, featureStatus == "passed", err == nil)
			original := readValidationReport(t, root)
			run.checkOnly = true
			for range 3 {
				var output bytes.Buffer
				require.NoError(t, run.Run(t.Context(), &output))
				report := readValidationReport(t, root)
				require.True(t, report.ChecksPassed)
				require.False(t, report.Success)
				require.False(t, report.LocalSuccess)
				require.Empty(t, report.Runs)
				require.Equal(t, "NOT EXERCISED", report.Summary.LocalValidation)
				require.Equal(t, "dd-trace@6.16.0", report.Tracer)
				require.NotNil(t, report.Retained)
				require.Equal(t, "historical", report.Retained.Status)
				require.Equal(t, original, *report.Retained.Result)
				require.Contains(t, output.String(), "Retained Jest execution: original / ")
				require.Contains(t, output.String(), "not revalidated by this invocation")
				if featureStatus == "failed" {
					require.Contains(t, output.String(), "Earlier blocking checks: Web/skipping failed")
				}
			}
			require.Equal(t, 3, run.executor.(*preflightExecutor).calls, "check-only must not run tests")
			files, err := os.ReadDir(filepath.Dir(validationPath(root)))
			require.NoError(t, err)
			require.Len(t, files, 1)
		})
	}
}

func TestPairedEvidenceSurvivesOtherFrameworkAndFailedPreflight(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := run.repositoryRoot
	require.Error(t, finishValidation(&bytes.Buffer{}, root, pairedReport("original", "failed")))
	original := readValidationReport(t, root)
	// Reproduce Claude's full Jest -> unsupported Playwright check ->
	// reporting-only Playwright -> final Jest check sequence.
	run.framework = &framework.Playwright{}
	run.checkOnly = true
	require.ErrorContains(t, run.Run(t.Context(), &bytes.Buffer{}), "preflight currently supports Jest only")
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "playwright",
		Compatibility: verdict{Status: "unvalidated"}, Runs: []runSummary{
			(validationRun{Name: "reporting-only", Command: "pnpm e2e", Instrumented: true}).summary(),
		}}))
	run.framework = &framework.Jest{}
	require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
	require.Equal(t, original, *readValidationReport(t, root).Retained.Result)
	// A new setup failure also keeps earlier evidence without hiding the error.
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "jest", Error: "setup failed"}))
	report := readValidationReport(t, root)
	require.Equal(t, "setup failed", report.Error)
	require.Equal(t, original, *report.Retained.Result)
	// Only a new paired execution supersedes the old one; no history accumulates.
	require.Error(t, finishValidation(&bytes.Buffer{}, root, pairedReport("replacement", "failed")))
	replacement := readValidationReport(t, root)
	require.Nil(t, replacement.Retained)
	require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
	report = readValidationReport(t, root)
	require.Equal(t, replacement, *report.Retained.Result)
	require.Nil(t, report.Retained.Result.Retained)
}

func TestUnreadableHistoryIsNotOverwrittenByCheckOnly(t *testing.T) {
	for _, contents := range []string{"broken report", strings.Repeat("x", (8<<20)+1)} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
		requireWriteFile(t, validationPath(root), contents)
		err := finishValidation(&bytes.Buffer{}, root, validationResult{CheckOnly: true})
		require.ErrorContains(t, err, "left unchanged")
		data, err := os.ReadFile(validationPath(root))
		require.NoError(t, err)
		require.Equal(t, contents, string(data))
	}
}
