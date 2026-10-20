package testdrive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestVitestWorkerResultsRequireExactCompleteAssignments(t *testing.T) {
	for _, tc := range []struct{ name, problem, want string }{
		{"valid parallel execution", "", ""},
		{"duplicate across workers", "duplicate", "multiple workers"},
		{"substring expansion", "selection", "exact assignment"},
		{"missing worker", "missing", "did not execute"},
		{"native omission", "native", "native results do not match"},
		{"missing native results", "json", "one native result per worker"},
		{"not planned", "plan", "absent from the plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			directory := t.TempDir()
			for _, file := range []string{"one.test.ts", "nested/one.test.ts"} {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755))
				requireWriteFile(t, filepath.Join(root, file), "")
			}
			require.NoError(t, os.MkdirAll(filepath.Join(root, constants.RunnerDirectory), 0755))
			plan := "one.test.ts\nnested/one.test.ts\n"
			if tc.problem == "plan" {
				plan = "one.test.ts\n"
			}
			requireWriteFile(t, filepath.Join(root, constants.TestFilesOutputPath), plan)
			writeJSON := func(path string, value any) {
				data, err := json.Marshal(value)
				require.NoError(t, err)
				requireWriteFile(t, filepath.Join(directory, path), string(data))
			}
			for i, file := range []string{"one.test.ts", "nested/one.test.ts"} {
				if i == 1 && tc.problem == "missing" {
					continue
				}
				if tc.problem == "duplicate" {
					file = "one.test.ts"
				}
				name := []string{"100", "200"}[i]
				assigned := []string{filepath.Join(root, file)}
				selected := assigned
				if i == 0 && tc.problem == "selection" {
					selected = append([]string{}, filepath.Join(root, "nested/one.test.ts"), assigned[0])
				}
				writeJSON(name+".assignment.json", map[string]any{"assigned": assigned, "selected": selected})
				if i == 1 && tc.problem == "json" {
					continue
				}
				suites := []any{map[string]any{"name": assigned[0], "assertionResults": []any{map[string]string{"fullName": "test", "status": "passed"}}}}
				if i == 1 && tc.problem == "native" {
					suites = nil
				}
				writeJSON(name+".results.json", map[string]any{"testResults": suites})
			}
			var run validationRun
			readVitestWorkerResults(directory, root, &run)
			if tc.want != "" {
				require.Contains(t, run.ResultError, tc.want)
				return
			}
			require.Empty(t, run.ResultError)
			require.Equal(t, 2, run.Workers)
			require.Len(t, run.Tests, 2)
		})
	}
}

func TestVitestRunnerCannotRemoveCustomerPlan(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, constants.RunnerDirectory), 0755))
	path := filepath.Join(root, constants.ParallelRunnersOutputPath)
	requireWriteFile(t, path, "existing customer plan")
	drive := &Testdrive{repositoryRoot: root, command: "ddtest", args: []string{"run"}}
	_, err := drive.vitestRunnerArgs(t.TempDir(), "/trace/ci/init.js", "", false, map[string]string{})
	require.ErrorContains(t, err, "disposable")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "existing customer plan", string(data))
}

func TestPlannerSkippingRequiresPositiveEvidence(t *testing.T) {
	root, directory := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, constants.RunnerDirectory), 0755))
	probe := filepath.Join(root, "probe.test.ts")
	scenario := intake.Scenario{Feature: "skipping", SourceFile: "probe.test.ts"}
	run := validationRun{root: root, Facts: intake.Facts{SkippableRequests: 1}}
	require.False(t, vitestPlannerSkippedProbe(directory, root, probe, scenario, run), "no output is not evidence of skipping")
	requireWriteFile(t, filepath.Join(root, constants.TestFilesOutputPath), "")
	requireWriteFile(t, filepath.Join(root, constants.SkippablePercentageOutputPath), "100.00")
	require.True(t, vitestPlannerSkippedProbe(directory, root, probe, scenario, run))
	run.Facts.TestEventCount = 1
	require.False(t, vitestPlannerSkippedProbe(directory, root, probe, scenario, run), "executed tests invalidate the skip")
	run.Facts.TestEventCount = 0
	run.Facts.SkippableRequests = 0
	require.False(t, vitestPlannerSkippedProbe(directory, root, probe, scenario, run), "backend negotiation is required")
	run.Facts.SkippableRequests = 1
	requireWriteFile(t, filepath.Join(root, constants.TestFilesOutputPath), "probe.test.ts")
	require.False(t, vitestPlannerSkippedProbe(directory, root, probe, scenario, run), "a runnable plan is not planner skipping")
}

func TestVitestRunnerResetPreservesProbeOwnership(t *testing.T) {
	root := t.TempDir()
	ownership := filepath.Join(root, constants.PlanDirectory, ".probes", "owned.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(ownership), 0700))
	requireWriteFile(t, ownership, "probe ownership")
	for _, directory := range []string{constants.RunnerDirectory, constants.HTTPCacheDir} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0700))
		requireWriteFile(t, filepath.Join(root, directory, "old.json"), "stale scenario")
	}
	drive := &Testdrive{repositoryRoot: root, isolated: true, command: "ddtest", args: []string{"run"}}
	_, err := drive.vitestRunnerArgs(t.TempDir(), "/trace/ci/init.js", "", true, map[string]string{})
	require.NoError(t, err)
	require.FileExists(t, ownership)
	require.NoDirExists(t, filepath.Join(root, constants.RunnerDirectory))
	require.NoDirExists(t, filepath.Join(root, constants.HTTPCacheDir))
}
