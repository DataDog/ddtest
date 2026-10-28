package testdrive

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func pairedReport(session, featureStatus string) validationResult {
	result := validationResult{Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}, HTMLReportCurrent: true, Cleanup: &verdict{Status: "passed"}, Session: session, Framework: "jest", Tracer: "dd-trace@6.17.0",
		Compatibility: verdict{Status: "compatible"},
		ProjectChecks: []projectProbeCheck{{Project: "Web", Discovered: true, Probe: "src/probe.test.js"}},
		Features:      []featureResult{{Project: "Web", Name: "skipping", Status: featureStatus}},
		Runs: []runSummary{
			(validationRun{Role: roleBaseline, Name: "baseline", Command: "pnpm exec jest --config original.js", ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
			(validationRun{Role: roleReporting, Name: "reporting-only", Command: "pnpm exec jest --config original.js", Instrumented: true, ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
			(validationRun{Project: "Web", Name: "skipping", Command: "pnpm exec jest --config original.js --selectProjects Web --runTestsByPath src/probe.test.js", Instrumented: true, ExitCode: 1}).summary(),
		}}
	for _, name := range jestFeatures {
		if name != "skipping" {
			result.Features = append(result.Features, featureResult{Project: "Web", Name: name, Status: "passed"})
		}
	}
	return result
}
func TestCheckOnlyKeepsPairedCommandsAsHistoricalEvidence(t *testing.T) {
	for _, status := range []string{"passed", "failed"} {
		t.Run(status, func(t *testing.T) {
			run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
			root := run.repositoryRoot
			err := finishValidation(&bytes.Buffer{}, root, pairedReport("paired", status))
			require.Equal(t, status == "passed", err == nil)
			original := readValidationReport(t, root)
			run.checkOnly = true
			for range 3 {
				require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
				report := readValidationReport(t, root)
				require.True(t, report.ChecksPassed)
				require.False(t, report.Success)
				require.False(t, report.LocalSuccess)
				require.Empty(t, report.Runs)
				require.NotNil(t, report.Retained)
				require.Equal(t, original.Runs, report.Retained.Result.Runs)
				require.Nil(t, report.Retained.Result.Retained)
				require.NotNil(t, report.LastExecution)
				require.False(t, report.LastExecution.Current)
				require.Equal(t, "paired", report.LastExecution.Session)
			}
		})
	}
}
func TestUnreadableHistoricalReportDoesNotBlockCurrentResults(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
		requireWriteFile(t, validationPath(root), "not valid JSON")
		if oversized {
			require.NoError(t, os.Truncate(validationPath(root), (8<<20)+1))
		}
		var output bytes.Buffer
		require.Error(t, finishValidation(&output, root, validationResult{Framework: "jest", CheckOnly: true}))
		data, err := os.ReadFile(validationPath(root))
		require.NoError(t, err)
		require.True(t, json.Valid(data))
		require.Contains(t, output.String(), "Writing current results")
		require.NotContains(t, output.String(), "left unchanged")
	}
}

func TestFailedFullRunNeverReusesEarlierPass(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, pairedReport("earlier pass", "passed")))
	requireWriteFile(t, htmlReportPath(root), "earlier successful HTML")
	result := validationResult{Framework: "jest", Error: "intake failed before baseline"}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
	got := readValidationReport(t, root)
	require.NoFileExists(t, htmlReportPath(root))
	require.NotNil(t, got.Retained)
	require.NotNil(t, got.LastExecution)
	require.False(t, got.LastExecution.Current)
	require.False(t, got.Success)
	require.Equal(t, "INCOMPLETE", got.Summary.LocalValidation)
}

func TestStoppedWorkflowAttemptKeepsEarlierExecutionAndFindings(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, pairedReport("earlier pass", "passed")))
	requireWriteFile(t, htmlReportPath(root), "earlier findings")
	invalidatePriorWorkflow(root, "jest", onboard.AgentResult{Code: "scope_unresolved", Reason: "Cannot statically resolve command: sudo apt-get -y install lcov"})
	got := readValidationReport(t, root)
	html, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Equal(t, "earlier findings", string(html))
	require.True(t, got.StoppedBeforeExecution)
	require.False(t, got.Success)
	require.NotNil(t, got.Retained)
	require.Equal(t, "earlier pass", got.Retained.Result.Session)
	require.NotNil(t, got.LastExecution)
	require.False(t, got.LastExecution.Current)
	require.Equal(t, "INCOMPLETE", got.Summary.Status)
	require.Equal(t, "PASSED EARLIER; NOT RERUN", got.Summary.LocalValidation)
	require.Contains(t, got.Summary.FinalResponse, "this attempt stopped before running tests; the earlier full execution (passed) is kept as history")
	require.Contains(t, got.Summary.FinalResponse, "Previous HTML report (not regenerated)")
	require.NotContains(t, got.Summary.FinalResponse, "stale")
}

func TestReportsCanReplaceExistingDestinationTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	require.NoError(t, writeValidation(path, []byte("first")))
	require.NoError(t, writeValidation(path, []byte("second")))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second", string(data))
}

func TestBlockedDiscoveryRetainsPartialExecutionDiagnostics(t *testing.T) {
	root := t.TempDir()
	code := 1
	attempt := validationResult{Framework: "cypress", Session: "timed-out", Error: "browser process resource limit", Runs: []runSummary{{Role: roleBaseline, Name: "baseline", Command: "npm run test:ci", ExitCode: &code, Diagnostic: "Chrome failed to start"}}}
	require.Error(t, finishValidation(&bytes.Buffer{}, root, attempt))
	for range 3 {
		require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "cypress", Error: "CI discovery blocked"}))
		got := readValidationReport(t, root)
		require.False(t, got.Success)
		require.Empty(t, got.Runs)
		require.Equal(t, "CI discovery blocked", got.Error)
		require.NotNil(t, got.Retained)
		require.Nil(t, got.Retained.Result.Retained)
		require.Equal(t, "npm run test:ci", got.Retained.Result.Runs[0].Command)
		require.Equal(t, "Chrome failed to start", got.Retained.Result.Runs[0].Diagnostic)
	}
}
