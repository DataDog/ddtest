// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const probeName = "ddtest validation probe"
const probeSource = `// @ts-nocheck
(() => {
if (globalThis.process.env.DD_CIVISIBILITY_FLAKY_RETRY_ENABLED !== 'true' &&
    typeof ddtestJest.retryTimes === 'function') ddtestJest.retryTimes(0);
let ddtestAttempts = 0;
test('ddtest validation probe', () => {
  ddtestAttempts++;
  if (globalThis.process.env.DDTEST_PROBE_MODE === 'fail' ||
      (globalThis.process.env.DDTEST_PROBE_MODE === 'fail-once' && ddtestAttempts === 1)) {
    throw new Error('ddtest intentional failure');
  }
});
})();
`

func unavailableFeatures(result *validationResult, reason string) {
	for _, policy := range featurePolicies(result.Framework) {
		status, explanation := "inconclusive", reason
		if policy.Status != "required" {
			status, explanation = policy.Status, policy.Reason
		}
		result.Features = append(result.Features, featureResult{Name: policy.Name, Status: status, Reason: explanation})
	}
}

func createJestProbe(root string, baseline validationRun) (string, error) {
	return createJestProjectProbe(root, baseline, jestProject{})
}

func createJestProjectProbe(root string, baseline validationRun, project jestProject) (string, error) {
	// Pick the same eligible source as createProbe before choosing module syntax.
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, candidate := range baseline.Tests {
		if candidate.Status != "passed" && candidate.Status != "failed" {
			continue
		}
		path, err := filepath.EvalSymlinks(candidate.File)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(canonical, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		baseline.Tests = []jestTest{candidate}
		break
	}
	source := probeSource
	{
		if len(baseline.Tests) == 0 {
			return "", errors.New("no executed test is available to locate a probe")
		}
		path := baseline.Tests[0].File
		esm := filepath.Ext(path) == ".mjs" || slices.Contains(project.ExtensionsToTreatAsEsm, filepath.Ext(path))
		if filepath.Ext(path) == ".js" {
			for directory := filepath.Dir(path); ; directory = filepath.Dir(directory) {
				data, err := os.ReadFile(filepath.Join(directory, "package.json"))
				if err == nil {
					var manifest struct {
						Type string `json:"type"`
					}
					if err := json.Unmarshal(data, &manifest); err != nil {
						return "", err
					}
					esm = manifest.Type == "module"
					break
				}
				if !os.IsNotExist(err) {
					return "", err
				}
				if directory == canonical || filepath.Dir(directory) == directory {
					break
				}
			}
		}
		prefix := "const ddtestJest = jest;\n"
		disabled := project.InjectGlobals != nil && !*project.InjectGlobals
		if esm {
			prefix = "import {jest as ddtestJest} from '@jest/globals';\n"
		}
		if disabled {
			prefix = "const {test, jest: ddtestJest} = require('@jest/globals');\n"
			if esm {
				prefix = "import {test, jest as ddtestJest} from '@jest/globals';\n"
			}
		}

		source = "// @ts-nocheck\n" + prefix + strings.TrimPrefix(source, "// @ts-nocheck\n")
	}
	return createProbe(root, baseline, source)
}

func createProbe(root string, baseline validationRun, sourceText string) (string, error) {
	if executedTests(baseline) == 0 {
		return "", errors.New("no executed test is available to locate a probe under the existing configuration")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, test := range baseline.Tests {
		if test.Status != "passed" && test.Status != "failed" {
			continue
		}
		source, err := filepath.EvalSymlinks(test.File)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, source)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		// Letters preserve patterns such as +([a-zA-Z]).server.test.tsx.
		// Discovery still verifies the actual project accepts this candidate.
		letters := []byte(rand.Text())
		for i, value := range letters {
			letters[i] = 'a' + value%26
		}
		name := filepath.Join(filepath.Dir(source), "ddtest"+string(letters)+filepath.Base(source))
		if err := createOwnedProbe(root, name, []byte(sourceText)); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", errors.New("no writable test location inside this repository was found for the probe")
}

func (t *Testdrive) runJestFeatures(ctx context.Context, output io.Writer, session *Session, preload string, result *validationResult) (runErr error) {
	if result.Preflight == nil || len(result.Preflight.Projects) == 0 {
		unavailableFeatures(result, "Effective Jest projects were not resolved; feature coverage is unvalidated.")
		return nil
	}
	names := map[string]int{}
	for _, project := range result.Preflight.Projects {
		names[project.DisplayName.Name]++
	}
	for i, project := range result.Preflight.Projects {
		label := project.DisplayName.Name
		if label == "" || names[label] > 1 {
			label = fmt.Sprintf("project %d", i+1)
		}
		firstFeature, firstRun := len(result.Features), len(result.Runs)
		runner := *t
		runner.jestProject = project
		if len(result.Preflight.Projects) > 1 {
			var selected bool
			selectionArgs := t.args
			if result.Preflight.Arguments != nil {
				selectionArgs = result.Preflight.Arguments
			}
			_, selected = projectArgs("jest", selectionArgs, project.DisplayName.Name)
			if selected {
				runner.args, selected = projectArgs(t.command, t.args, project.DisplayName.Name)
			}
			if !selected {
				result.ProjectChecks = append(result.ProjectChecks, projectProbeCheck{Project: label, Root: project.Root, Excluded: true})
				continue
			}
		}
		if len(result.Preflight.Projects) > 1 && (project.DisplayName.Name == "" || names[project.DisplayName.Name] != 1) {
			unavailableFeatures(result, "Cannot unambiguously select this Jest project: a unique displayName is required for per-project feature validation. The original configuration was preserved.")
		} else {

			check := projectProbeCheck{Project: label, Root: project.Root}
			err := runner.runJestProjectFeatures(ctx, output, session, preload, fmt.Sprintf("project-%d", i+1), &check, result)
			result.ProjectChecks = append(result.ProjectChecks, check)
			if err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		for j := firstFeature; j < len(result.Features); j++ {
			result.Features[j].Project = label
		}
		for j := firstRun; j < len(result.Runs); j++ {
			result.Runs[j].Project = label
		}
		if runErr != nil {
			return runErr
		}
	}
	return nil
}

func (t *Testdrive) runJestProjectFeatures(ctx context.Context, output io.Writer, session *Session, preload, prefix string, check *projectProbeCheck, result *validationResult) (runErr error) {
	files, command, err := t.discoverJestTests(ctx, "")
	check.DiscoveryCommand = command
	if err != nil || len(files) == 0 {
		unavailableFeatures(result, fmt.Sprintf("Could not discover tests in this Jest project: %v", err))
		return nil
	}
	baseline := validationRun{}
	for _, path := range files {
		baseline.Tests = append(baseline.Tests, jestTest{File: path, Status: "passed"})
	}
	probePath, err := createJestProjectProbe(t.repositoryRoot, baseline, t.jestProject)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbeWithOutput(t.repositoryRoot, probePath, output)) }()
	check.Probe, _ = filepath.Rel(t.repositoryRoot, probePath)
	files, check.ProbeDiscoveryCommand, err = t.discoverJestTests(ctx, probePath)
	if err != nil || len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(probePath) {
		check.Diagnostic = fmt.Sprintf("Probe discovery returned %d paths instead of the requested probe.", len(files))
		if err != nil {
			check.Diagnostic = reportText(err.Error())
		}
		unavailableFeatures(result, "The temporary probe was not discovered by this project's original configuration; its features remain unvalidated. "+check.Diagnostic)
		return nil
	}
	check.Discovered = true
	run := func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		value, err := t.runJest(ctx, output, session, preload, runSpec{Name: prefix + "/" + name, Role: roleProbeFeature, Instrumented: true, Scenario: scenario, ProbePath: probePath, ProbeMode: mode})
		value.Name = name
		if scenario.Feature == "" || scenario.Feature == "reporting-only" {
			value.Role = roleProbeControl
		}
		if d := value.Skipping; d != nil {
			repoPath, repoErr := filepath.Rel(t.checkoutRoot(), probePath)
			projectPath, projectErr := filepath.Rel(check.Root, probePath)
			if repoErr == nil && projectErr == nil {
				d.RepositoryPath, d.ProjectPath = filepath.ToSlash(repoPath), filepath.ToSlash(projectPath)
				d.PathMismatch = scenario.SourceFile != d.RepositoryPath && scenario.SourceFile != d.ProjectPath
			}
		}
		return value, err
	}
	first := len(result.Features)
	err = runProbeFeatures(run, result)
	explainInjectedGlobalFailures(result.Features[first:], t.jestProject)
	return err
}

// The tracer repeats early-flake-detection and attempt-to-fix probes through
// Jest's injected global test, which injectGlobals: false removes. Name that
// cause instead of only reporting the counts.
func explainInjectedGlobalFailures(features []featureResult, project jestProject) {
	if project.InjectGlobals == nil || *project.InjectGlobals {
		return
	}
	for i := range features {
		if features[i].Status == "failed" && (features[i].Name == "early-flake-detection" || features[i].Name == "attempt-to-fix") {
			features[i].Reason += " Likely cause: this Jest configuration sets injectGlobals: false, and the tracer repeats tests through Jest's injected global test; tests imported from @jest/globals are not repeated."
		}
	}
}

func runProbeFeatures(run func(string, intake.Scenario, string) (validationRun, error), result *validationResult) error {
	// Verify the probe in the actual project configuration before claiming anything
	// about feature behavior. Never fall back to a clean, unrelated Jest config.
	passControl, err := run("probe-pass-control", intake.Scenario{}, "pass")
	passControl = markTimeout(passControl)
	result.Runs = append(result.Runs, passControl.summary())
	if err != nil {
		return err
	}
	failControl, err := run("probe-fail-control", intake.Scenario{}, "fail")
	failControl = markTimeout(failControl)
	result.Runs = append(result.Runs, failControl.summary())
	if err != nil {
		return err
	}
	if reason := cmp.Or(passControl.Timeout, failControl.Timeout); reason != "" {
		unavailableFeatures(result, reason+".")
		return nil
	}
	if !validProbeControl(passControl, "passed") || !validProbeControl(failControl, "failed") {
		unavailableFeatures(result, "The temporary probe did not produce its expected passing and failing control results under this framework configuration. "+probeControlDiagnostic(passControl, failControl))
		return nil
	}
	identity := passControl.Facts.Tests[0]
	for _, policy := range featurePolicies(result.Framework) {
		feature := policy.Name
		if policy.Status != "required" {
			result.Features = append(result.Features, featureResult{Name: feature, Status: policy.Status, Reason: policy.Reason})
			continue
		}
		if feature == "skipping" && identity.SourceFile == "" {
			result.Features = append(result.Features, evaluateFeature(feature, validationRun{}, identity))
			continue
		}
		mode := "pass"
		switch feature {
		case "auto-retries":
			mode = "fail-once"
		case "skipping", "quarantine", "disabled":
			mode = "fail"
		}
		scenario := intake.Scenario{Feature: feature, Module: identity.Module, Suite: identity.Suite, Test: identity.Name, SourceFile: identity.SourceFile}
		value, err := run(feature, scenario, mode)
		value = markTimeout(value)
		result.Runs = append(result.Runs, value.summary())
		if err != nil {
			return err
		}
		result.Features = append(result.Features, withTracerLimitation(result, evaluateFeature(feature, value, identity)))
	}
	return nil
}

// withTracerLimitation reports a failed feature the tracer is known not to apply
// as that limitation. The probe still runs, so a fixed tracer release passes.
func withTracerLimitation(result *validationResult, feature featureResult) featureResult {
	limitation := tracerFeatureLimitation(result, feature.Name)
	if limitation == "" || feature.Status != "failed" {
		return feature
	}
	return featureResult{Name: feature.Name, Status: "known limitation", Code: tracerLimitation, ObservedCode: feature.Code, Reason: limitation + " Observed: " + feature.Reason}
}

func validProbeControl(run validationRun, status string) bool {
	if frameworkFeaturePolicies[run.framework].ProbeBodyCounts && (run.ProbeExecutions == nil || *run.ProbeExecutions != 1) {
		return false
	}
	expectedExit := (status == "passed" && run.ExitCode == 0) || (status == "failed" && run.ExitCode > 0)
	if !expectedExit || run.ResultError != "" || executedTests(run) != 1 || len(run.SuiteErrors) != 0 || len(run.Facts.Tests) != 1 || verifyJestTelemetry(run).Status != "" {
		return false
	}
	for _, test := range run.Tests {
		if (test.Name == probeName || run.framework == "pytest" && isPytestProbe(test.Name)) && test.Status == status {
			return status != "failed" || strings.Contains(test.Failure, "ddtest intentional failure")
		}
	}
	return false
}

func evaluateFeature(feature string, run validationRun, identity intake.Test) featureResult {
	result := featureResult{Name: feature, Status: "failed", Reason: "The controlled run did not show the expected feature behavior; see the recorded scenario command, exit code, and counts."}
	if feature == "skipping" && identity.SourceFile == "" {
		result.Status = "inconclusive"
		result.Reason = "The control did not report test.source.file; a repository-relative file is required to request suite skipping."
		return result
	}
	if run.Timeout != "" {
		result.Status, result.Reason = "inconclusive", run.Timeout+"."
		return result
	}
	if run.ResultError != "" || len(run.SuiteErrors) > 0 || len(run.Facts.ConfigurationErrors) > 0 {
		result.Status = "inconclusive"
		result.Reason = "The scenario could not collect usable runner results or reported setup/configuration errors."
		return result
	}
	if feature == "skipping" && run.ExecutionPath == vitestNodeAPIPath && run.PlannerSkipped {
		return featureResult{Name: feature, Status: "passed", Reason: "ddtest's planner received the skippable probe, reported 100% skipped, and produced an empty execution plan. The command succeeded without starting a Vitest worker or executing a test body. This validates planner skipping on the ddtest run path."}
	}
	if feature == "skipping" && run.framework == "vitest" && run.Facts.SkippableRequests == 0 {
		result.Status = "inconclusive"
		result.Reason = "The Vitest tracer did not request skippable suites. Check this tracer release's Vitest Test Impact Analysis support; a successful reporting run does not prove skipping is supported. Real Datadog credentials are not required."
		return result
	}
	passed, failed, retries := 0, 0, 0
	quarantined, disabled, attemptToFix, skipped := false, false, false, false
	omittedPlaywrightIdentity := false
	// Test-level skipping (Cypress) reports the skipped test itself.
	skippedTestEvents := 0
	for _, event := range run.Facts.Events {
		tags := event.Tags

		if isSkippedJestProbe(event, identity.Module, identity.Suite, identity.SourceFile) {
			skipped = true
		}
		if isSkippedProbeTest(event, identity) {
			skipped = true
			skippedTestEvents++
		}
		if tags["test.suite"] != identity.Suite {
			continue
		}
		moduleMatches := tags["test.module"] == identity.Module
		if feature == "disabled" && run.framework == "playwright" && !moduleMatches && playwrightDisabledProbe(tags, identity, run) {
			moduleMatches = true
			omittedPlaywrightIdentity = true
		}
		if event.Type != "test" || tags["test.name"] != identity.Name || !moduleMatches {
			continue
		}
		if tags["test.status"] == "pass" {
			passed++
		}
		if tags["test.status"] == "fail" {
			failed++
		}
		reason := map[string]string{"auto-retries": "auto_test_retry", "early-flake-detection": "early_flake_detection", "attempt-to-fix": "attempt_to_fix"}[feature]
		if reason != "" && tags["test.is_retry"] == "true" && tags["test.retry_reason"] == reason {
			retries++
		}
		quarantined = quarantined || tags["test.test_management.is_quarantined"] == "true"
		disabled = disabled || (tags["test.test_management.is_test_disabled"] == "true" && tags["test.status"] == "skip")
		attemptToFix = attemptToFix || tags["test.test_management.is_attempt_to_fix"] == "true"
	}
	nativePass, nativeFail, nativeSkip := 0, 0, 0
	for _, test := range run.Tests {
		if test.Name != identity.Name {
			result.Status = "inconclusive"
			result.Reason = "The scenario selected tests beyond the controlled probe."
			return result
		}
		switch test.Status {
		case "passed":
			nativePass++
		case "failed":
			nativeFail++
		case "pending", "skipped":
			nativeSkip++
		}
	}
	nativeRepeated := (frameworkFeaturePolicies[run.framework].ProbeBodyCounts && nativePass == 1 && run.ProbeExecutions != nil && *run.ProbeExecutions >= 2) || nativePass >= 2 || ((run.framework == "vitest" || run.framework == "playwright") && nativePass == 1)
	success := false
	switch feature {
	case "auto-retries":
		success = passed > 0 && failed == 1 && retries > 0 && nativePass > 0 && nativeFail == 0
	case "early-flake-detection":
		success = passed >= 2 && failed == 0 && retries > 0 && nativeRepeated && nativeFail == 0
	case "skipping":
		success = skipped && executedTests(run) == 0 && passed+failed == 0 && run.Facts.TestEventCount == skippedTestEvents
	case "quarantine":
		// Quarantine changes the command outcome, not necessarily the native
		// assertion status. Some Jest versions retain the failing assertion.
		success = quarantined && failed == 1 && passed == 0 && retries == 0 && len(run.Tests) == 1 && nativePass+nativeFail == 1
		if run.framework == "pytest" {
			success = success || quarantined && failed == 1 && passed == 0 && retries == 0 && len(run.Tests) == 1 && nativeSkip == 1
		}
	case "disabled":
		success = disabled && executedTests(run) == 0 && nativeSkip == 1
	case "attempt-to-fix":
		success = attemptToFix && passed >= 2 && failed == 0 && retries > 0 && nativeRepeated && nativeFail == 0
	}
	if frameworkFeaturePolicies[run.framework].ProbeBodyCounts {
		success = success && run.ProbeExecutions != nil && *run.ProbeExecutions == passed+failed
	}
	success = success && run.ExitCode == 0
	if !success {
		result.Reason = fmt.Sprintf("Controlled %s did not show the expected behavior: matching telemetry has %d passed, %d failed and %d retries with the expected reason (%d total test events); runner has %d passed, %d failed and %d skipped; exit %d.",
			feature, passed, failed, retries, run.Facts.TestEventCount, nativePass, nativeFail, nativeSkip, run.ExitCode)
		if feature == "auto-retries" {
			result.Reason += " The fail-once probe requires exactly one failed telemetry event followed by a successful retry; the runner may report only the final passing result."
			if failed != 1 {
				result.Reason = fmt.Sprintf("Retry telemetry count mismatch: expected 1 failed event, received %d. ", failed) + result.Reason
			}
		}
		if d := run.Skipping; run.ExitCode != 0 && d != nil && d.PathMismatch && d.SkippableRequests > 0 {
			result.Reason = "The mock returned the control's test.source.file, but that path differs from both repository-relative and project-relative probe paths. The probe executed instead of skipping; inspect the recorded skipping path mismatch. Preserve the original framework configuration. Real Datadog credentials are not required."
		}
		// The tracer reported the probe exactly once, as the runner ran it, with
		// none of the requested control: no retry, management tag or skip. An
		// attempt-to-fix tag alone is not its control; the control is the retries.
		applied := retries > 0 || quarantined || disabled || skipped || attemptToFix && feature != "attempt-to-fix"
		if feature != "skipping" && !applied && passed+failed == 1 && nativePass+nativeFail+nativeSkip == 1 {
			result.Code = tracerControlNotApplied
			result.Reason = fmt.Sprintf("The tracer did not apply the %s control: telemetry reported the probe once (%d passed, %d failed) with no retry or test management evidence, and the runner ran it once (%d passed, %d failed, %d skipped; exit %d). The local intake served the control, so no customer change is expected to fix this; report it.",
				feature, passed, failed, nativePass, nativeFail, nativeSkip, run.ExitCode)
		}
	}
	if success {
		result.Status = "passed"
		result.Reason = map[string]string{
			"auto-retries":          "The fail-once probe failed, retried with auto_test_retry evidence, and recovered to a successful command.",
			"early-flake-detection": "The new passing probe ran repeatedly with early_flake_detection retry evidence.",
			"skipping":              "The normally failing probe was skipped by ITR; no test body executed and the command succeeded.",
			"quarantine":            "The normally failing probe still reported its failure and quarantine tag, while the command succeeded.",
			"disabled":              "The normally failing probe was reported disabled/skipped, with no executed test body and a successful command.",
			"attempt-to-fix":        "The marked passing probe ran repeatedly with attempt_to_fix retry evidence and a successful command.",
		}[feature]
		if omittedPlaywrightIdentity {
			result.Reason += " The tracer omitted module/browser identity on the disabled event; the unique probe source, suite and name were matched to its single native project/browser."
		}
		if feature == "skipping" && run.Skipping != nil && run.Skipping.SuiteNameChanged {
			result.Reason += " The tracer reported a different test.suite for the skipped suite; both names are retained in the skipping diagnostics."
		}
	}
	return result
}

func probeControlDiagnostic(runs ...validationRun) string {
	for _, run := range runs {
		if run.missingSourceLoader != "" {
			return reportText(run.missingSourceLoader)
		}
		if len(run.SuiteErrors) > 0 {
			return reportText(run.SuiteErrors[0])
		}
		for _, test := range run.Tests {
			if test.Failure != "" && !strings.Contains(test.Failure, "ddtest intentional failure") {
				return reportText(test.Failure)
			}
		}
		if run.ResultError != "" {
			return reportText(run.ResultError)
		}
		if telemetry := verifyJestTelemetry(run); telemetry.Status != "" {
			return reportText(telemetry.Reason + " " + strings.Join(telemetry.Differences, "; "))
		}
		if run.Diagnostic != "" {
			return reportText(run.Diagnostic)
		}
	}
	return "See recorded probe commands, exit codes and bounded diagnostics."
}
