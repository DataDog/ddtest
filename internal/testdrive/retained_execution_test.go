package testdrive

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func pairedReport(session, featureStatus string) validationResult {
	result := validationResult{HTMLReportCurrent: true, Cleanup: &verdict{Status: "passed"}, Session: session, Framework: "jest", Tracer: "dd-trace@6.17.0",
		Compatibility: verdict{Status: "compatible"},
		ProjectChecks: []projectProbeCheck{{Project: "Web", Discovered: true, Probe: "src/probe.test.js"}},
		Features:      []featureResult{{Project: "Web", Name: "skipping", Status: featureStatus}},
		Runs: []runSummary{
			(validationRun{Name: "baseline", Command: "pnpm exec jest --config original.js", ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
			(validationRun{Name: "reporting-only", Command: "pnpm exec jest --config original.js", Instrumented: true, ExitCode: 0, Tests: []jestTest{{Status: "passed"}}}).summary(),
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
func TestUnreadableHistoricalReportIsLeftUnchanged(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
	requireWriteFile(t, validationPath(root), "not valid JSON")
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "jest", CheckOnly: true}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Equal(t, "not valid JSON", string(data))
}
