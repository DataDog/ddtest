package testdrive

import (
	"bytes"
	"encoding/json"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// The B06 shape: a run of one command after an --all run is written beside
// the --all report, so a focused probe never erases it; a later --all run,
// or a run after a focused report, replaces the saved report as before.
func TestFocusedRunKeepsTheAllReport(t *testing.T) {
	root := t.TempDir()
	all := pairedReport("full", "passed")
	all.All = true
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, all))
	requireWriteFile(t, htmlReportPath(root), "full findings")

	focused := pairedReport("probe", "passed")
	focused.FocusedCommand = true
	var output bytes.Buffer
	require.NoError(t, finishValidation(&output, root, focused))
	kept := readValidationReport(t, root)
	require.Equal(t, "full", kept.Session, "the --all report stays where --report reads it")
	html, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Equal(t, "full findings", string(html))
	data, err := os.ReadFile(filepath.Join(root, ".testoptimization", focusedValidationFilename))
	require.NoError(t, err)
	var beside validationResult
	require.NoError(t, json.Unmarshal(data, &beside))
	require.Equal(t, "probe", beside.Session)
	require.Equal(t, ".testoptimization/testdrive.json", beside.KeptReport)
	require.Contains(t, beside.Summary.FinalResponse, "did not replace the --all report")
	require.Contains(t, beside.Summary.FinalResponse, focusedValidationFilename)
	require.Contains(t, beside.Summary.Facts, "Report: .testoptimization/"+focusedValidationFilename)
	require.Contains(t, output.String(), "Results JSON: .testoptimization/"+focusedValidationFilename)
	require.Contains(t, output.String(), "Kept the --all report .testoptimization/testdrive.json")

	// A repeated probe replaces only its own report.
	again := pairedReport("probe again", "passed")
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, again))
	require.Equal(t, "full", readValidationReport(t, root).Session)
	data, err = os.ReadFile(filepath.Join(root, ".testoptimization", focusedValidationFilename))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &beside))
	require.Equal(t, "probe again", beside.Session)

	// An explicit --all rerun replaces the --all report.
	rerun := pairedReport("full again", "passed")
	rerun.All = true
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, rerun))
	require.Equal(t, "full again", readValidationReport(t, root).Session)

	// Without an --all report, a focused run replaces the saved report.
	plain := t.TempDir()
	require.NoError(t, finishValidation(&bytes.Buffer{}, plain, pairedReport("first", "passed")))
	require.NoError(t, finishValidation(&bytes.Buffer{}, plain, pairedReport("second", "passed")))
	require.Equal(t, "second", readValidationReport(t, plain).Session)
	require.NoFileExists(t, filepath.Join(plain, ".testoptimization", focusedValidationFilename))
}

// Reports written before All existed are --all reports when their workflow
// command says so; a coordinated run of one command is not.
func TestKeptReportRecognizesEarlierAllReports(t *testing.T) {
	for command, keeps := range map[string]bool{"--all": true, "--command": false} {
		root := t.TempDir()
		saved := pairedReport("saved", "passed")
		saved.Workflow = &WorkflowEvidence{ValidationCommand: []string{"ddtest", "testdrive", command}}
		data, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
		requireWriteFile(t, validationPath(root), string(data))
		require.Equal(t, keeps, keepsSavedReport(root, "jest", pairedReport("probe", "passed")), command)
		require.False(t, keepsSavedReport(root, "vitest", pairedReport("probe", "passed")), "another framework's report is not this one's")
		checkOnly := pairedReport("check", "passed")
		checkOnly.CheckOnly = true
		require.False(t, keepsSavedReport(root, "jest", checkOnly))
	}
}

// The coordinator reads back the report this operation wrote, beside the
// --all report or in its place.
func TestCoordinatorReadsTheReportBesideTheAllReport(t *testing.T) {
	root := t.TempDir()
	all := pairedReport("full", "passed")
	all.All = true
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, all))
	started := time.Now()
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, pairedReport("probe", "passed")))
	current, err := readCurrentOnboardingResult(root, started)
	require.NoError(t, err)
	require.Equal(t, "probe", current.Session)
	require.Equal(t, filepath.Join(root, ".testoptimization", focusedValidationFilename), current.jsonReportPath(root))
	rerun := pairedReport("full again", "passed")
	rerun.All = true
	started = time.Now()
	require.NoError(t, finishValidation(&bytes.Buffer{}, root, rerun))
	current, err = readCurrentOnboardingResult(root, started)
	require.NoError(t, err)
	require.Equal(t, "full again", current.Session)
}
