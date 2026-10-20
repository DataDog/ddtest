package testdrive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func (t *Testdrive) vitestExecutionPath() string {
	if t.usesVitestRunner() {
		return "ddtest-run-node-api"
	}
	return "vitest-cli"
}

func (t *Testdrive) usesVitestRunner() bool {
	return t.vitestRunner || onboard.IsDDTestCommand(t.command)
}

func (t *Testdrive) vitestRunnerArgs(directory, preload, probe string, instrumented bool, env map[string]string) ([]string, error) {
	if !t.isolated {
		return nil, fmt.Errorf("ddtest run validation requires a disposable configuration workspace")
	}
	parsed, err := onboard.ParseVitestRunnerCommand(t.args)
	if err != nil {
		return nil, err
	}
	// Never reuse a previous scenario's backend cache or plan. This directory
	// belongs to the disposable copy, not the customer's repository.
	for _, directory := range []string{constants.RunnerDirectory, constants.HTTPCacheDir} {
		if err := os.RemoveAll(filepath.Join(t.repositoryRoot, directory)); err != nil {
			return nil, err
		}
	}
	args := append([]string{"run"}, parsed.Options...)
	files := parsed.Files
	validation := map[string]string{"directory": directory}
	if probe != "" {
		files = []string{probe}
		validation["probe"] = "^" + probeName + "$"
	}
	encoded, err := json.Marshal(validation)
	if err != nil {
		return nil, err
	}
	env["DDTEST_VITEST_VALIDATION"] = string(encoded)
	env["DD_TRACE_PACKAGE"] = preload
	env["DD_TRACE_ESM_IMPORT"] = filepath.Join(filepath.Dir(filepath.Dir(preload)), "register.js")
	if !instrumented {
		// RunEnv normally inserts the tracer. Its explicit worker override lets
		// the baseline run the same adapter without loading dd-trace at all.
		options := env["NODE_OPTIONS"]
		if strings.Contains(options, ";") {
			return nil, fmt.Errorf("baseline NODE_OPTIONS cannot be represented by --worker-env")
		}
		args = append(args, "--worker-env", "NODE_OPTIONS="+options)
	}
	args = append(args, "--")
	return append(args, files...), nil
}

// Check the persisted plan, each worker's assigned/selected files, and the
// native reporter output. Equal baseline/instrumented counts alone could hide
// the same duplicate or missing files in both executions.
func readVitestWorkerResults(directory, root string, run *validationRun) {
	err := collectVitestWorkerResults(directory, root, run)
	if err != nil {
		run.ResultError = "Vitest Node API results: " + err.Error()
	}
}

// A fully skippable probe is removed by ddtest's planner before a Node worker
// starts. Require the successful plan artifacts and backend negotiation; an
// absent JSON report alone never proves skipping.
func vitestPlannerSkippedProbe(directory, root, probe string, scenario intake.Scenario, run validationRun) bool {
	if scenario.Feature != "skipping" || probe == "" || run.ExitCode != 0 || run.Facts.SkippableRequests == 0 || run.Facts.TestEventCount != 0 || len(run.Tests) != 0 {
		return false
	}
	relative, err := filepath.Rel(root, probe)
	if err != nil || filepath.ToSlash(relative) != scenario.SourceFile {
		return false
	}
	files, err := os.ReadFile(filepath.Join(root, constants.TestFilesOutputPath))
	if err != nil || strings.TrimSpace(string(files)) != "" {
		return false
	}
	percentage, err := os.ReadFile(filepath.Join(root, constants.SkippablePercentageOutputPath))
	if err != nil || strings.TrimSpace(string(percentage)) != "100.00" {
		return false
	}
	assignments, err := filepath.Glob(filepath.Join(directory, "*.assignment.json"))
	if err != nil || len(assignments) != 0 {
		return false
	}
	results, err := filepath.Glob(filepath.Join(directory, "*.results.json"))
	return err == nil && len(results) == 0
}

func collectVitestWorkerResults(directory, root string, run *validationRun) error {
	planned, err := os.ReadFile(filepath.Join(root, constants.TestFilesOutputPath))
	if err != nil {
		return fmt.Errorf("the runner did not produce a test-file plan: %w", err)
	}
	assignments, err := filepath.Glob(filepath.Join(directory, "*.assignment.json"))
	if err != nil {
		return err
	}
	results, err := filepath.Glob(filepath.Join(directory, "*.results.json"))
	if err != nil {
		return err
	}
	if len(assignments) == 0 || len(assignments) != len(results) {
		return fmt.Errorf("expected one native result per worker; received %d assignments and %d results", len(assignments), len(results))
	}
	seen := map[string]bool{}
	for _, path := range assignments {
		var assignment struct{ Assigned, Selected []string }
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &assignment); err != nil {
			return err
		}
		slices.Sort(assignment.Assigned)
		slices.Sort(assignment.Selected)
		if len(assignment.Assigned) == 0 || !slices.Equal(assignment.Assigned, assignment.Selected) {
			return fmt.Errorf("a worker selected files outside its exact assignment, or omitted assigned files")
		}
		for _, file := range assignment.Assigned {
			if seen[file] {
				return fmt.Errorf("a file was assigned to multiple workers: %s", file)
			}
			seen[file] = true
		}
		resultPath := strings.TrimSuffix(path, ".assignment.json") + ".results.json"
		var native jestResults
		data, err = os.ReadFile(resultPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &native); err != nil {
			return err
		}
		var reported []string
		for _, suite := range native.TestResults {
			canonical, err := filepath.EvalSymlinks(suite.Name)
			if err != nil {
				return err
			}
			reported = append(reported, canonical)
		}
		slices.Sort(reported)
		if len(reported) == 0 && len(assignment.Selected) == 1 && run.Name == "skipping" && run.ExitCode == 0 && run.Facts.TestEventCount == 0 && run.Skipping != nil && run.Skipping.SkippedByITR {
			// Vitest omits ITR-skipped suites from its native JSON. Accept only
			// the isolated probe whose matching suite-skip event was received.
			file, err := filepath.EvalSymlinks(filepath.Join(root, run.Skipping.SourceFile))
			if err == nil && file == assignment.Selected[0] {
				reported = []string{file}
			}
		}
		if !slices.Equal(assignment.Selected, reported) {
			return fmt.Errorf("native results do not match the worker's selected files")
		}
		readRunnerResults(resultPath, "Vitest", run)
		if run.ResultError != "" {
			return fmt.Errorf("%s", run.ResultError)
		}
	}
	for _, file := range strings.Split(strings.TrimSpace(string(planned)), "\n") {
		if file == "" {
			continue
		}
		canonical, err := filepath.EvalSymlinks(filepath.Join(root, file))
		if err != nil {
			return err
		}
		if !seen[canonical] {
			return fmt.Errorf("planned file did not execute: %s", file)
		}
		delete(seen, canonical)
	}
	if len(seen) != 0 {
		return fmt.Errorf("workers executed files absent from the plan")
	}
	run.Workers = len(assignments)
	return nil
}
