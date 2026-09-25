// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
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
		probe, err := os.CreateTemp(filepath.Dir(source), "ddtest-*-"+filepath.Base(source))
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

func (t *Testdrive) runJestFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probePath, err := createJestProbe(t.repositoryRoot, baseline)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probePath)) }()
	// Verify the probe in the actual project configuration before claiming anything
	// about feature behavior. Never fall back to a clean, unrelated Jest config.
	passControl, err := t.runJest(ctx, output, session, preload, "probe-pass-control", true, intake.Scenario{}, probePath, "pass")
	result.Runs = append(result.Runs, passControl.summary())
	if err != nil {
		return err
	}
	failControl, err := t.runJest(ctx, output, session, preload, "probe-fail-control", true, intake.Scenario{}, probePath, "fail")
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
		mode := "pass"
		switch feature {
		case "auto-retries":
			mode = "fail-once"
		case "skipping", "quarantine", "disabled":
			mode = "fail"
		}
		scenario := intake.Scenario{Feature: feature, Module: identity.Module, Suite: identity.Suite, Test: identity.Name}
		run, err := t.runJest(ctx, output, session, preload, feature, true, scenario, probePath, mode)
		result.Runs = append(result.Runs, run.summary())
		if err != nil {
			return err
		}
		result.Features = append(result.Features, evaluateFeature(feature, run, identity))
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
	if run.ResultError != "" || len(run.SuiteErrors) > 0 || len(run.Facts.ConfigurationErrors) > 0 {
		result.Status = "inconclusive"
		result.Reason = "The scenario could not collect usable runner results or reported setup/configuration errors."
		return result
	}
	if run.ExitCode != 0 {
		return result
	}
	passed, failed, retries := 0, 0, 0
	quarantined, disabled, attemptToFix, skipped := false, false, false, false
	for _, event := range run.Facts.Events {
		tags := event.Tags
		if tags["test.suite"] != identity.Suite {
			continue
		}
		if event.Type == "test_suite_end" && tags["test.status"] == "skip" && tags["test.skipped_by_itr"] == "true" {
			skipped = true
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
		success = skipped && executedTests(run) == 0
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
	}
	return result
}
