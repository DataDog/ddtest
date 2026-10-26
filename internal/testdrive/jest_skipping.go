// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import "github.com/DataDog/ddtest/internal/testdrive/intake"

// Retain only controlled identities and counts, never full requests or events.
type skippingDiagnostic struct {
	SettingsRequests   int    `json:"settings_requests"`
	SkippableRequests  int    `json:"skippable_requests"`
	RequestedSuite     string `json:"requested_probe_suite"`
	SourceFile         string `json:"source_file"`
	ReturnedSuite      string `json:"returned_suite,omitempty"`
	ObservedSuite      string `json:"observed_probe_suite,omitempty"`
	ObservedSourceFile string `json:"observed_source_file,omitempty"`
	SuiteNameChanged   bool   `json:"suite_name_changed"`
	IdentityMatched    bool   `json:"observed_identity_matched"`
	RepositoryPath     string `json:"repository_relative_probe,omitempty"`
	ProjectPath        string `json:"project_relative_probe,omitempty"`
	PathMismatch       bool   `json:"suite_path_mismatch"`
	SkippedByITR       bool   `json:"skipped_by_itr"`
	ExecutedTests      int    `json:"executed_tests"`
	Expected           string `json:"expected"`
}

func diagnoseSkipping(run validationRun, scenario intake.Scenario) *skippingDiagnostic {
	d := &skippingDiagnostic{SettingsRequests: run.Facts.SettingsRequests, SkippableRequests: run.Facts.SkippableRequests,
		RequestedSuite: reportText(scenario.Suite), SourceFile: reportText(scenario.SourceFile), ExecutedTests: executedTests(run),
		Expected: "Mock returns the control's test.source.file; ITR skips that file, no test body executes, and exit code is 0. Suite naming differences are retained separately. Real Datadog credentials are not required."}
	if d.SkippableRequests > 0 {
		d.ReturnedSuite = reportText(scenario.SourceFile)
		if scenario.SuiteSelector != "" {
			d.ReturnedSuite = reportText(scenario.SuiteSelector)
			d.Expected = "Mock returns the control's framework suite identity; ITR skips it, no test body executes, and exit code is 0. Source-file evidence is retained separately. Real Datadog credentials are not required."
		}
	}
	if run.framework == "cypress" {
		// The Cypress tracer requests test-level skipping and skips the test at
		// runtime, so the test itself carries the ITR evidence.
		if d.SkippableRequests > 0 {
			d.ReturnedSuite = reportText(scenario.Suite)
		}
		d.Expected = "Mock returns the control's test (spec suite and full title) at test level; ITR skips it, no test body executes, and exit code is 0. Real Datadog credentials are not required."
	}
	probe := intake.Test{Module: scenario.Module, Suite: scenario.Suite, Name: scenario.Test, SourceFile: scenario.SourceFile}
	for _, event := range run.Facts.Events {
		tags := event.Tags
		if isSkippedJestProbe(event, scenario.Module, scenario.Suite, scenario.SourceFile) || isSkippedProbeTest(event, probe) {
			d.SkippedByITR = true
			d.ObservedSuite = reportText(tags["test.suite"])
			d.ObservedSourceFile = reportText(tags["test.source.file"])
			d.IdentityMatched = tags["test.suite"] == scenario.Suite
			d.SuiteNameChanged = !d.IdentityMatched
		}
	}
	return d
}

func isSkippedJestProbe(event intake.Event, module, suite, sourceFile string) bool {
	tags := event.Tags
	if sourceFile == "" || event.Type != "test_suite_end" || tags["test.module"] != module || tags["test.status"] != "skip" || tags["test.skipped_by_itr"] != "true" {
		return false
	}
	if observedSource := tags["test.source.file"]; observedSource != "" && observedSource != sourceFile {
		return false
	}
	// Current dd-trace uses the returned file path as the skipped suite name.
	// Also accept tracers that preserve the executed suite's reported identity.
	return tags["test.suite"] == sourceFile || tags["test.suite"] == suite
}

// isSkippedProbeTest matches test-level ITR skip evidence: the probe test
// itself is reported skipped by ITR, as Cypress's tracer does.
func isSkippedProbeTest(event intake.Event, probe intake.Test) bool {
	tags := event.Tags
	return probe.SourceFile != "" && event.Type == "test" && tags["test.module"] == probe.Module && tags["test.suite"] == probe.Suite &&
		tags["test.name"] == probe.Name && tags["test.status"] == "skip" && tags["test.skipped_by_itr"] == "true"
}
