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
	"github.com/DataDog/ddtest/internal/onboard"
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
				require.Equal(t, strings.ToUpper(executionStatus(&original))+" EARLIER; NOT RERUN", report.Summary.LocalValidation)
				require.Equal(t, original.Session, report.LastExecution.Session)
				require.Equal(t, original.CompletedAt, report.LastExecution.CompletedAt)
				require.False(t, report.LastExecution.Current)
				require.Equal(t, executionStatus(&original), report.LastExecution.Status)
				require.Equal(t, report.Session, report.ConfigurationCheck.Session)
				require.True(t, report.ConfigurationCheck.Current)
				require.Equal(t, "passed", report.ConfigurationCheck.Status)
				require.Contains(t, report.Summary.OnboardingResponse, "final full validation required")
				require.Equal(t, "dd-trace@6.16.0", report.Tracer)
				require.NotNil(t, report.Retained)
				require.Equal(t, "historical", report.Retained.Status)
				require.Equal(t, original, *report.Retained.Result)
				require.Contains(t, output.String(), "Last full execution: original / ")
				require.Contains(t, output.String(), "not revalidated by this invocation")
				if featureStatus == "failed" {
					require.Contains(t, output.String(), "Earlier feature check: Web/skipping failed")
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
	require.ErrorContains(t, run.Run(t.Context(), &bytes.Buffer{}), "preflight currently supports Jest and Vitest only")
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
	require.True(t, replacement.LastExecution.Current)
	require.Equal(t, "replacement", replacement.LastExecution.Session)
	require.Equal(t, replacement.CompletedAt, replacement.LastExecution.CompletedAt)
	require.Equal(t, "incomplete", replacement.LastExecution.Status)
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

func TestConfigurationChangeCannotReuseEarlierLocalPass(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	root := run.repositoryRoot
	original := pairedReport("before-ci-edit", "passed")
	original.CIRuntime = &onboard.RuntimeCheck{Status: "incompatible"}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, original))
	before := readValidationReport(t, root)
	require.Equal(t, "passed", before.LastExecution.Status)
	require.Equal(t, "incomplete", before.ConfigurationCheck.Status)
	// A successful static recheck does not certify the changed repository.
	run.checkOnly = true
	require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
	after := readValidationReport(t, root)
	require.Equal(t, "passed", after.ConfigurationCheck.Status)
	require.Equal(t, before.CompletedAt, after.LastExecution.CompletedAt)
	require.False(t, after.LastExecution.Current)
	require.Equal(t, "INCOMPLETE", after.Summary.Status)
	require.NotContains(t, after.Summary.OnboardingResponse, "CI runtime incompatible")
	require.Contains(t, after.Summary.OnboardingResponse, "final full validation required")
	// Only a new full run replaces the earlier evidence and completes locally.
	final := pairedReport("after-ci-edit", "passed")
	final.Preflight = &jestPreflight{Verdict: verdict{Status: "compatible"}}
	final.CIRuntime = &onboard.RuntimeCheck{Status: "compatible"}
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, final))
	report := readValidationReport(t, root)
	require.Nil(t, report.Retained)
	require.True(t, report.LastExecution.Current)
	require.Equal(t, "after-ci-edit", report.LastExecution.Session)
	require.Equal(t, "passed", report.ConfigurationCheck.Status)
	require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status)
}
