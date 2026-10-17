// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

type validationSummary struct {
	ExecutedValidation      *executedValidation    `json:"executed_validation,omitempty"`
	ConfigurationCoverage   *configurationCoverage `json:"configuration_coverage,omitempty"`
	SelectedValidation      *verdict               `json:"selected_validation,omitempty"`
	LocalNode               string                 `json:"local_node,omitempty"`
	CIScope                 *ciRuntimeScope        `json:"ci_runtime_scope,omitempty"`
	Status                  string                 `json:"status"`
	LocalValidation         string                 `json:"local_validation"`
	CIConfiguration         string                 `json:"ci_configuration"`
	CIExecution             string                 `json:"ci_execution"`
	RealCredentialsRequired bool                   `json:"real_credentials_required"`
	BlockingChecks          []string               `json:"blocking_checks,omitempty"`
	FinalResponse           string                 `json:"final_response,omitempty"`
	OnboardingResponse      string                 `json:"onboarding_response"`
	Facts                   []string               `json:"facts,omitempty"`
}

// One copyable response, built from the same facts as the compact report. Only
// link retained files, and never present an earlier HTML report as a new run.
func validationHandoff(result validationResult) string {
	var block strings.Builder
	summary := result.Summary
	if summary.SelectedValidation != nil {
		fmt.Fprintf(&block, "Selected %s testdrive: %s.\n", displayName(result.Framework), summary.SelectedValidation.Status)
	}
	if executed := summary.ExecutedValidation; executed != nil {
		fmt.Fprintf(&block, "Executed %s validations: %s (%d/%d).\n", displayName(result.Framework), executed.Status, executed.Passed, executed.Configurations)
	}
	fmt.Fprintf(&block, "Onboarding validation: %s. Local validation: %s.\n", summary.Status, summary.LocalValidation)
	if coverage := summary.ConfigurationCoverage; coverage != nil {
		fmt.Fprintf(&block, "Configurations: %d/%d validated; %d executed, %d paired; %d blocked, %d not exercised, %d incomplete.\n", coverage.Validated, coverage.Discovered, coverage.Executed, coverage.Paired, coverage.Blocked, coverage.NotExercised, coverage.Incomplete)
	}
	current := !result.CheckOnly && (result.LastExecution == nil || result.LastExecution.Current)
	if counts := result.ReportingCounts; counts != nil {
		label := "Reporting"
		if !current {
			label = "Historical reporting (not revalidated)"
		}
		fmt.Fprintf(&block, "%s: %d test executions; ", label, counts.TestExecutions)
		if counts.SourceTestIdentities == nil {
			block.WriteString("source-test identities unavailable.\n")
		} else {
			fmt.Fprintf(&block, "%d source-test identities.\n", *counts.SourceTestIdentities)
		}
	} else {
		block.WriteString("Reporting: test executions and source-test identities unavailable.\n")
	}
	compatibility := result.Compatibility.Status
	if compatibility == "" {
		compatibility = "not checked"
	}
	fmt.Fprintf(&block, "Compatibility: %s. ", compatibility)
	passed, total, unsupported := validationFeatureCounts(result.Framework, result.Features)
	if total == 0 {
		block.WriteString("Features: not exercised.")
	} else if !current {
		fmt.Fprintf(&block, "Historical features (not revalidated): %d/%d supported checks passed.", passed, total)
	} else {
		fmt.Fprintf(&block, "Features: %d/%d supported checks passed.", passed, total)
	}
	if len(unsupported) > 0 {
		fmt.Fprintf(&block, " %s not supported.", strings.Join(unsupported, ", "))
	}
	block.WriteByte('\n')
	coverageRuns, coveredRuns, coveredTests, totalTests := 0, 0, 0, 0
	var levels []string
	for _, run := range result.Runs {
		if run.Coverage == nil || !run.Instrumented || run.ProbeMode != "" || !strings.HasPrefix(run.Name, "reporting-only") {
			continue
		}
		coverageRuns++
		if run.Coverage.Status == "observed" {
			coveredRuns++
		}
		coveredTests += run.Coverage.CoveredTests
		totalTests += run.Coverage.TotalTests
		if run.Coverage.Level != "" {
			levels = appendUnique(levels, run.Coverage.Level)
		}
	}
	if coverageRuns > 0 {
		label := "Real-suite coverage"
		if !current {
			label = "Historical real-suite coverage (not revalidated)"
		}
		fmt.Fprintf(&block, "%s: observed in %d/%d reporting runs; %d/%d results linked to source", label, coveredRuns, coverageRuns, coveredTests, totalTests)
		if len(levels) == 1 && levels[0] == "suite" {
			block.WriteString(" (suite-level, not independent per-test coverage)")
		}
		block.WriteString(".\n")
	}
	fmt.Fprintf(&block, "CI configuration: %s (static analysis). CI execution and Datadog backend processing: not exercised.\n", summary.CIConfiguration)
	if scope := summary.CIScope; scope != nil {
		fmt.Fprintf(&block, "CI runtime scope (static): %d instrumented, %d excluded, %d unverified; versions in Results JSON.\n", len(scope.Instrumented), len(scope.Excluded), len(scope.Unverified))
	}
	if supportsValidationFramework(result.Framework) && summary.CIConfiguration != "compatible" && summary.CIConfiguration != "not applicable" {
		block.WriteString("CI edits require a full local rerun; an actual CI run does not update this local report.\n")
	}
	if summary.LocalNode != "" {
		fmt.Fprintf(&block, "Local runtime: Node %s.\n", summary.LocalNode)
	}
	if result.CheckOnly {
		block.WriteString("Evidence: check-only; paired execution and feature controls were not rerun.\n")
	}
	if result.LastExecution != nil && !result.LastExecution.Current {
		block.WriteString("Evidence: earlier execution is stale; rerun full validation after configuration checks or edits.\n")
	}
	if len(summary.BlockingChecks) > 0 {
		var headlines []string
		for _, reason := range summary.BlockingChecks {
			if !strings.HasPrefix(reason, "configuration ") && len(headlines) < 2 {
				headlines = append(headlines, briefValidationText(reason))
			}
		}
		fmt.Fprintf(&block, "Blockers: %d", len(summary.BlockingChecks))
		if len(headlines) > 0 {
			block.WriteString(" — " + strings.Join(headlines, "; "))
		}
		block.WriteString(". Full blockers and commands are in Results JSON.\n")
	}
	link := func(label, path string) string {
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		// Angle brackets preserve spaces; escape characters that can end a link.
		path = strings.NewReplacer("%", "%25", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(filepath.ToSlash(path))
		return fmt.Sprintf("[%s](<%s>)", label, path)
	}
	if info, err := os.Stat(htmlReportPath(result.WorkingDirectory)); err == nil && info.Mode().IsRegular() {
		label := "Open report"
		if !result.HTMLReportCurrent {
			label = "Previous HTML report (not regenerated)"
		}
		block.WriteString("\n" + link(label, htmlReportPath(result.WorkingDirectory)) + "\n")
	} else {
		block.WriteString("\nHTML report unavailable.\n")
	}
	block.WriteString(link("Results JSON", validationPath(result.WorkingDirectory)))
	if result.Cleanup != nil {
		fmt.Fprintf(&block, "\nCleanup: %s (ddtest sessions only)", result.Cleanup.Status)
		if result.Cleanup.Status != "passed" {
			block.WriteString(" — " + briefValidationText(result.Cleanup.Reason))
		}
		block.WriteString(".")
	} else {
		block.WriteString("\nCleanup: not recorded.")
	}
	block.WriteString(" Verify your own scratch and Git/package report exclusions; preserve customer files and both reports.")
	return block.String()
}

func validationFeatureCounts(framework string, features []featureResult) (passed, total int, unsupported []string) {
	for _, feature := range features {
		if feature.Name == "all" {
			continue
		}
		if feature.Status == "not supported" && featureSatisfied(framework, feature) {
			unsupported = appendUnique(unsupported, feature.Name)
			continue
		}
		total++
		if feature.Status == "passed" {
			passed++
		}
	}
	return
}

func briefValidationText(value string) string {
	text := []rune(value)
	if len(text) > 240 {
		return string(text[:240]) + "… (see Results JSON)"
	}
	return value
}

func summarizeValidation(result validationResult) validationSummary {
	summary := validationSummary{Status: "INCOMPLETE", LocalValidation: "INCOMPLETE", CIConfiguration: "not checked", CIExecution: "not exercised"}
	summary.ConfigurationCoverage, summary.ExecutedValidation = summarizeConfigurationCoverage(result)
	if supportsValidationFramework(result.Framework) && result.FocusedCommand {
		selected := selectedValidation(result)
		summary.SelectedValidation = &selected
	}
	if result.Success {
		summary.Status = "COMPLETE LOCALLY"
	}
	if result.LocalSuccess {
		summary.LocalValidation = "PASSED"
	}
	if result.CheckOnly {
		summary.LocalValidation = "NOT EXERCISED"
		summary.BlockingChecks = append(summary.BlockingChecks, "paired execution and features not exercised (--check-only)")
	}
	if result.LastExecution != nil && !result.LastExecution.Current {
		summary.LocalValidation = strings.ToUpper(result.LastExecution.Status) + " EARLIER; NOT RERUN"
		if result.CheckOnly {
			summary.BlockingChecks = nil
		}
		summary.BlockingChecks = append(summary.BlockingChecks, "final full validation required after configuration checks or edits; earlier execution has not been revalidated")
	}
	if result.Error != "" {
		summary.BlockingChecks = append(summary.BlockingChecks, "execution error: "+reportText(result.Error))
	}
	if result.Preflight != nil && result.Preflight.Verdict.Status != "compatible" {
		summary.BlockingChecks = append(summary.BlockingChecks, "preflight "+result.Preflight.Verdict.Status)
	}
	if !result.CheckOnly {
		if missingRealCoverage(result) && result.Framework == "cypress" {
			summary.BlockingChecks = append(summary.BlockingChecks, "Cypress real-application coverage is missing or invalid. Instrument application source with Istanbul, preserving repository source filenames, and enable that startup/build path in local validation and CI. API-only tests may have no browser coverage. Synthetic probes and test-file coverage do not prove application coverage.")
		}
		if missingRealCoverage(result) && result.Framework != "cypress" {
			summary.BlockingChecks = append(summary.BlockingChecks, "Mocha real-suite coverage was not observed. Synthetic skipping probes do not validate coverage of the customer's source. Check NYC instrument/include settings and ESM instrumentation; preserve existing coverage thresholds.")
		}
		if result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if !featureSatisfied(result.Framework, feature) {
				label := feature.Name
				if feature.Project != "" {
					label = feature.Project + "/" + label
				}
				summary.BlockingChecks = append(summary.BlockingChecks, label+" "+feature.Status)
			}
		}
	}
	if result.CIRuntime != nil {
		summary.CIConfiguration = result.CIRuntime.Status
		if result.CIRuntime.Status != "compatible" && result.CIRuntime.Status != "not applicable" {
			summary.BlockingChecks = append(summary.BlockingChecks, "CI runtime "+result.CIRuntime.Status)
		}
	}
	if result.CIPreservation != nil && result.CIPreservation.Status != "passed" {
		summary.CIConfiguration = result.CIPreservation.Status
		summary.BlockingChecks = append(summary.BlockingChecks, "CI matrix preservation "+result.CIPreservation.Status+": "+result.CIPreservation.Reason)
	}
	if result.CISelection != nil && result.CISelection.Status != "compatible" {
		summary.CIConfiguration = result.CISelection.Status
		summary.BlockingChecks = append(summary.BlockingChecks, "CI tracer agreement "+result.CISelection.Status)
	}
	if result.Scope != nil {
		for _, reason := range result.Scope.Unresolved {
			summary.BlockingChecks = append(summary.BlockingChecks, "unresolved validation scope: "+reportText(reason))
		}
		for _, configuration := range result.Configurations {
			if configuration.Status != "passed" {
				summary.BlockingChecks = append(summary.BlockingChecks, "configuration "+configuration.Command+": "+configuration.Status)
			}
		}
	}
	if !result.CheckOnly && supportsValidationFramework(result.Framework) {
		if !hasValidationPair(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, "current baseline/reporting pair is incomplete")
		}
		if !featureChecksPassed(result.Framework, result.Features) {
			summary.BlockingChecks = append(summary.BlockingChecks, "required feature checks are incomplete for at least one project")
		}
		if hasValidationPair(result) && !result.HTMLReportCurrent {
			summary.BlockingChecks = append(summary.BlockingChecks, "current HTML report was not written")
		}
		if result.Cleanup == nil || result.Cleanup.Status != "passed" {
			summary.BlockingChecks = append(summary.BlockingChecks, "temporary-session cleanup did not pass")
		}
	}
	if result.Preflight != nil {
		summary.LocalNode = result.Preflight.Node
	}
	summary.CIScope = summarizeCIScope(result.CIRuntime)
	summary.OnboardingResponse = fmt.Sprintf("Onboarding validation: %s. Local validation: %s. CI configuration: %s (static analysis). CI execution and Datadog backend processing: not exercised.", summary.Status, summary.LocalValidation, summary.CIConfiguration)
	if summary.LocalNode != "" {
		summary.OnboardingResponse += " Local execution runtime: Node " + summary.LocalNode + "."
	}
	if summary.CIScope != nil {
		summary.OnboardingResponse += fmt.Sprintf(" CI Node scope (static): configured for instrumentation: %s; excluded from instrumentation: %s; unresolved or blocked: %s.", scopeList(summary.CIScope.Instrumented), scopeList(summary.CIScope.Excluded), scopeList(summary.CIScope.Unverified))
	}
	if len(summary.BlockingChecks) > 0 {
		summary.OnboardingResponse += " Blocking checks: " + strings.Join(summary.BlockingChecks[:min(3, len(summary.BlockingChecks))], "; ") + "."
		if len(summary.BlockingChecks) > 3 {
			summary.OnboardingResponse += fmt.Sprintf(" %d additional blockers are in blocking_checks in Results JSON.", len(summary.BlockingChecks)-3)
		}
	}
	return summary
}

// Use the current report as the source of final-answer facts, including all
// repetitions. A later successful repetition must not erase an earlier crash.
func validationFacts(result validationResult) []string {
	facts := []string{"Report: .testoptimization/testdrive.json", "Session: " + result.Session}
	appendRuns := func(label string, runs []runSummary) {
		for _, run := range runs {
			if !strings.HasPrefix(run.Name, "baseline") && !strings.HasPrefix(run.Name, "reporting-only") {
				continue
			}
			exit := "not executed"
			if run.ExitCode != nil {
				exit = fmt.Sprint(*run.ExitCode)
			}
			if run.NativeResults == "unavailable" {
				facts = append(facts, fmt.Sprintf("%s%s: native outcome counts unavailable; exit %s. %s", label+configurationLabel(run.Configuration), run.Name, exit, run.ResultError))
			} else {
				facts = append(facts, fmt.Sprintf("%s%s: %d passed, %d failed, %d skipped; %d suite errors; exit %s.", label+configurationLabel(run.Configuration), run.Name, run.TestCounts.Passed, run.TestCounts.Failed, run.TestCounts.Skipped, run.SuiteErrorCount, exit))
			}
			if run.Coverage != nil {
				facts = append(facts, fmt.Sprintf("Real-suite coverage: %s%s — %s; %d/%d reported tests linked to covered source. Synthetic feature probes are separate evidence.", label+configurationLabel(run.Configuration), run.Name, run.Coverage.Status, run.Coverage.CoveredTests, run.Coverage.TotalTests))
				if run.Coverage.Reason != "" {
					facts = append(facts, "Real-suite coverage: "+briefValidationText(run.Coverage.Reason))
				}
			}
			if run.ExecutionPath != "" {
				facts = append(facts, "Validated execution path: "+run.ExecutionPath+".")
			}
		}
	}
	appendRuns("", result.Runs)
	facts = append(facts, "Compatibility: "+result.Compatibility.Status+". "+result.Compatibility.Reason)
	// Keep only two short examples here; the bounded details live in compatibility.
	for _, difference := range result.Compatibility.Differences[:min(2, len(result.Compatibility.Differences))] {
		text := []rune(difference)
		if len(text) > 240 {
			text = append(text[:240], '…')
		}
		facts = append(facts, string(text))
	}
	passed, total, _ := validationFeatureCounts(result.Framework, result.Features)
	if total == 0 {
		facts = append(facts, "Features: not exercised.")
	} else {
		fact := fmt.Sprintf("Features: %d/%d project-feature checks passed.", passed, total)
		unexecuted := 0
		for _, configuration := range result.Configurations {
			if configuration.Compatibility.Status == "not exercised" {
				unexecuted++
			}
		}
		if unexecuted > 0 {
			fact += fmt.Sprintf(" %d configuration(s) were not exercised; these passes do not cover them.", unexecuted)
		}
		facts = append(facts, fact)
	}
	if result.CIRuntime != nil {
		seen := map[string]bool{}
		for _, job := range result.CIRuntime.Jobs {
			if job.Status == "compatible" || job.Status == "excluded" {
				continue
			}
			fact := fmt.Sprintf("CI %s / %s / step %d: %s", job.Workflow, job.Job, job.Step, job.Reason)
			if seen[fact] {
				continue
			}
			seen[fact] = true
			if len(seen) <= 8 {
				text := []rune(fact)
				if len(text) > 300 {
					fact = string(text[:300]) + "… (see ci_runtime.jobs)"
				}
				facts = append(facts, fact)
			}
		}
		if len(seen) > 8 {
			facts = append(facts, "Additional CI findings are in ci_runtime.jobs in the report.")
		}
	}
	for _, configuration := range result.Configurations {
		facts = append(facts, fmt.Sprintf("Configuration [%s] %s: %s.", configuration.Directory, configuration.Command, configuration.Status))
		for _, build := range configuration.Preparation {
			fact := fmt.Sprintf("Build prerequisite for %s: [%s] %s: %s", configuration.Command, build.Directory, build.Command, build.Status)
			if build.ExitCode != nil {
				fact += fmt.Sprintf("; exit %d", *build.ExitCode)
			}
			facts = append(facts, fact+".")
		}
	}
	if result.Retained != nil {
		previous := result.Retained.Result
		facts = append(facts, "Last full execution (historical; not revalidated): "+previous.Session+". Local status: "+executionStatus(previous))
		appendRuns("Historical ", previous.Runs)
		for _, feature := range previous.Features {
			if feature.Name != "all" {
				facts = append(facts, fmt.Sprintf("Historical feature %s/%s: %s.", feature.Project, feature.Name, feature.Status))
			}
		}
	}
	return facts
}

// Scope describes configuration entries, not execution on these Node versions.
type ciRuntimeScope struct {
	Instrumented []string `json:"instrumented,omitempty"`
	Excluded     []string `json:"excluded,omitempty"`
	Unverified   []string `json:"unverified,omitempty"`
}

func summarizeCIScope(check *onboard.RuntimeCheck) *ciRuntimeScope {
	if check == nil || len(check.Jobs) == 0 {
		return nil
	}
	scope := &ciRuntimeScope{}
	for _, job := range check.Jobs {
		label := nodeScopeLabel(job.Node)
		switch job.Status {
		case "compatible":
			scope.Instrumented = appendUnique(scope.Instrumented, label)
		case "excluded":
			scope.Excluded = appendUnique(scope.Excluded, label)
		default:
			scope.Unverified = appendUnique(scope.Unverified, label)
		}
	}
	return scope
}

func configurationLabel(command string) string {
	if command == "" {
		return ""
	}
	return command + " / "
}
