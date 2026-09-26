// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

const probeName = "ddtest validation probe"
const probeSource = `let ddtestAttempts = 0;
test('ddtest validation probe', () => {
  ddtestAttempts++;
  if (process.env.DDTEST_PROBE_MODE === 'fail' ||
      (process.env.DDTEST_PROBE_MODE === 'fail-once' && ddtestAttempts === 1)) {
    throw new Error('ddtest intentional failure');
  }
});
`

var jestFeatures = []string{"auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"}

func unavailableFeatures(result *validationResult, reason string) {
	for _, feature := range jestFeatures {
		result.Features = append(result.Features, featureResult{Name: feature, Status: "inconclusive", Reason: reason})
	}
}

func createJestProbe(root string, baseline validationRun) (string, error) {
	if executedTests(baseline) == 0 {
		return "", errors.New("no executed Jest test is available to locate a probe under the existing configuration")
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
		probe, err := os.OpenFile(filepath.Join(filepath.Dir(source), "ddtest"+string(letters)+filepath.Base(source)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		_, writeErr := probe.WriteString(probeSource)
		closeErr := probe.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return "", errors.Join(err, os.Remove(probe.Name()))
		}
		return probe.Name(), nil
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
		if len(result.Preflight.Projects) > 1 && (project.DisplayName.Name == "" || names[project.DisplayName.Name] != 1) {
			unavailableFeatures(result, "Cannot unambiguously select this Jest project: a unique displayName is required for per-project feature validation. The original configuration was preserved.")
		} else {
			if len(result.Preflight.Projects) > 1 {
				var selected bool
				runner.args, selected = projectArgs(t.command, t.args, project.DisplayName.Name)
				if !selected {
					result.ProjectChecks = append(result.ProjectChecks, projectProbeCheck{Project: label, Root: project.Root, Excluded: true})
					continue
				}
			}
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
	probePath, err := createJestProbe(t.repositoryRoot, baseline)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probePath)) }()
	check.Probe, _ = filepath.Rel(t.repositoryRoot, probePath)
	files, check.ProbeDiscoveryCommand, err = t.discoverJestTests(ctx, probePath)
	if err != nil || len(files) != 1 || filepath.Clean(files[0]) != filepath.Clean(probePath) {
		unavailableFeatures(result, "The temporary probe was not discovered by this project's original configuration; its features remain unvalidated.")
		return nil
	}
	check.Discovered = true
	run := func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		value, err := t.runJest(ctx, output, session, preload, prefix+"/"+name, true, scenario, probePath, mode)
		value.Name = name
		if d := value.Skipping; d != nil {
			repoPath, repoErr := filepath.Rel(t.repositoryRoot, probePath)
			projectPath, projectErr := filepath.Rel(check.Root, probePath)
			if repoErr == nil && projectErr == nil {
				d.RepositoryPath, d.ProjectPath = filepath.ToSlash(repoPath), filepath.ToSlash(projectPath)
				d.PathMismatch = scenario.SourceFile != d.RepositoryPath && scenario.SourceFile != d.ProjectPath
			}
		}
		return value, err
	}
	// Verify the probe in the actual project configuration before claiming anything
	// about feature behavior. Never fall back to a clean, unrelated Jest config.
	passControl, err := run("probe-pass-control", intake.Scenario{}, "pass")
	result.Runs = append(result.Runs, passControl.summary())
	if err != nil {
		return err
	}
	failControl, err := run("probe-fail-control", intake.Scenario{}, "fail")
	result.Runs = append(result.Runs, failControl.summary())
	if err != nil {
		return err
	}
	if !validProbeControl(passControl, "passed") || !validProbeControl(failControl, "failed") {
		unavailableFeatures(result, "The temporary probe did not produce its expected passing and failing control results under this Jest configuration. See the recorded probe commands, exit codes, and bounded failure diagnostics.")
		return nil
	}
	identity := passControl.Facts.Tests[0]
	for _, feature := range jestFeatures {
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
		result.Runs = append(result.Runs, value.summary())
		if err != nil {
			return err
		}
		result.Features = append(result.Features, evaluateFeature(feature, value, identity))
	}
	return nil
}

func validProbeControl(run validationRun, status string) bool {
	expectedExit := (status == "passed" && run.ExitCode == 0) || (status == "failed" && run.ExitCode > 0)
	if !expectedExit || run.ResultError != "" || executedTests(run) != 1 || len(run.SuiteErrors) != 0 || len(run.Facts.Tests) != 1 || verifyJestTelemetry(run) != "" {
		return false
	}
	for _, test := range run.Tests {
		if test.Name == probeName && test.Status == status {
			return status != "failed" || strings.Contains(test.Failure, "ddtest intentional failure")
		}
	}
	return false
}

func evaluateFeature(feature string, run validationRun, identity intake.Test) featureResult {
	result := featureResult{Name: feature, Status: "failed", Reason: "The controlled run did not show the expected feature behavior; see the recorded scenario command, exit code, and counts."}
	if feature == "skipping" && identity.SourceFile == "" {
		result.Status = "inconclusive"
		result.Reason = "The control did not report test.source.file; a repository-relative file is required to request Jest suite skipping."
		return result
	}
	if run.ResultError != "" || len(run.SuiteErrors) > 0 || len(run.Facts.ConfigurationErrors) > 0 {
		result.Status = "inconclusive"
		result.Reason = "The scenario could not collect usable runner results or reported setup/configuration errors."
		return result
	}
	if run.ExitCode != 0 {
		if d := run.Skipping; d != nil && d.PathMismatch && d.SkippableRequests > 0 {
			result.Reason = "The mock returned the control's test.source.file, but that path differs from both repository-relative and project-relative probe paths. The probe executed instead of skipping; inspect the recorded skipping path mismatch. Preserve the original Jest configuration. Real Datadog credentials are not required."
		}
		return result
	}
	passed, failed, retries := 0, 0, 0
	quarantined, disabled, attemptToFix, skipped := false, false, false, false
	for _, event := range run.Facts.Events {
		tags := event.Tags
		if isSkippedJestProbe(event, identity.Module, identity.Suite, identity.SourceFile) {
			skipped = true
		}
		if tags["test.suite"] != identity.Suite {
			continue
		}
		if event.Type != "test" || tags["test.name"] != identity.Name || tags["test.module"] != identity.Module {
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
	success := false
	switch feature {
	case "auto-retries":
		success = passed > 0 && failed == 1 && retries > 0 && nativePass > 0 && nativeFail == 0
	case "early-flake-detection":
		success = passed >= 2 && failed == 0 && retries > 0 && nativePass >= 2 && nativeFail == 0
	case "skipping":
		success = skipped && executedTests(run) == 0 && run.Facts.TestEventCount == 0
	case "quarantine":
		success = quarantined && failed == 1 && retries == 0 && nativePass == 1 && nativeFail == 0
	case "disabled":
		success = disabled && executedTests(run) == 0 && nativeSkip == 1
	case "attempt-to-fix":
		success = attemptToFix && passed >= 2 && failed == 0 && retries > 0 && nativePass >= 2 && nativeFail == 0
	}
	if success {
		result.Status = "passed"
		result.Reason = map[string]string{
			"auto-retries":          "The fail-once probe failed, retried with auto_test_retry evidence, and recovered to a successful command.",
			"early-flake-detection": "The new passing probe ran repeatedly with early_flake_detection retry evidence.",
			"skipping":              "The normally failing probe suite was skipped by ITR; no test body executed and the command succeeded.",
			"quarantine":            "The normally failing probe still reported its failure and quarantine tag, while the command succeeded.",
			"disabled":              "The normally failing probe was reported disabled/skipped, with no executed test body and a successful command.",
			"attempt-to-fix":        "The marked passing probe ran repeatedly with attempt_to_fix retry evidence and a successful command.",
		}[feature]
		if feature == "skipping" && run.Skipping != nil && run.Skipping.SuiteNameChanged {
			result.Reason += " The tracer reported a different test.suite for the skipped suite; both names are retained in the skipping diagnostics."
		}
	}
	return result
}
