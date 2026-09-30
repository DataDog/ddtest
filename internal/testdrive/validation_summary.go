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
	LocalNode               string          `json:"local_node,omitempty"`
	CIScope                 *ciRuntimeScope `json:"ci_runtime_scope,omitempty"`
	Status                  string          `json:"status"`
	LocalValidation         string          `json:"local_validation"`
	CIConfiguration         string          `json:"ci_configuration"`
	CIExecution             string          `json:"ci_execution"`
	RealCredentialsRequired bool            `json:"real_credentials_required"`
	BlockingChecks          []string        `json:"blocking_checks,omitempty"`
	FinalResponse           string          `json:"final_response,omitempty"`
	OnboardingResponse      string          `json:"onboarding_response"`
	Facts                   []string        `json:"facts,omitempty"`
}

// One copyable response, built from the same facts as the compact report. Only
// link retained files, and never present an earlier HTML report as a new run.
func validationHandoff(result validationResult) string {
	var block strings.Builder
	summary := result.Summary
	fmt.Fprintf(&block, "Onboarding validation: %s. Local validation: %s. CI configuration: %s (static analysis). CI execution and Datadog backend processing: not exercised.\n", summary.Status, summary.LocalValidation, summary.CIConfiguration)
	if summary.LocalNode != "" {
		fmt.Fprintf(&block, "Local runtime: Node %s.\n", summary.LocalNode)
	}
	if len(result.Configurations) > 0 {
		compared, passed := 0, 0
		for _, configuration := range result.Configurations {
			if configuration.Compatibility.Status == "compatible" {
				compared++
			}
			if configuration.Status == "passed" {
				passed++
			}
		}
		fmt.Fprintf(&block, "\nConfigurations: %d/%d paired comparisons compatible; %d/%d fully validated.\n", compared, len(result.Configurations), passed, len(result.Configurations))
	}
	if len(result.Configurations) > 3 {
		for _, name := range []string{"baseline", "reporting-only"} {
			var passed, failed, skipped, count int
			for _, run := range result.Runs {
				if strings.HasPrefix(run.Name, name) {
					count++
					passed += run.TestCounts.Passed
					failed += run.TestCounts.Failed
					skipped += run.TestCounts.Skipped
				}
			}
			if count > 0 {
				fmt.Fprintf(&block, "\n- %s: %d passed, %d failed, %d skipped across %d runs.", name, passed, failed, skipped, count)
			}
		}
	}
	for _, fact := range summary.Facts {
		if strings.HasPrefix(fact, "Features:") || len(result.Configurations) <= 3 && (strings.HasPrefix(fact, "baseline") || strings.HasPrefix(fact, "reporting-only")) {
			block.WriteString("\n- " + fact)
		}
	}
	fmt.Fprintf(&block, "\n- Compatibility: %s.", result.Compatibility.Status)
	seenReasons := map[string]bool{}
	for _, feature := range result.Features {
		if feature.Status == "passed" || seenReasons[feature.Reason] {
			continue
		}
		seenReasons[feature.Reason] = true
		if len(seenReasons) > 2 {
			break
		}
		fmt.Fprintf(&block, "\n- %s %s: %s", feature.Name, feature.Status, briefValidationText(feature.Reason))
	}
	for _, reason := range summary.BlockingChecks[:min(2, len(summary.BlockingChecks))] {
		block.WriteString("\n- Blocker: " + briefValidationText(reason))
	}
	if len(summary.BlockingChecks) > 2 {
		block.WriteString("\n- Additional blockers and per-configuration results are in Results JSON.")
	}
	link := func(label, path string) string {
		// Angle brackets preserve spaces; escape characters that can end a link.
		path = strings.NewReplacer("%", "%25", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(filepath.ToSlash(path))
		return fmt.Sprintf("[%s](<%s>)", label, path)
	}
	if info, err := os.Stat(htmlReportPath(result.WorkingDirectory)); err == nil && info.Mode().IsRegular() {
		label := "Open report"
		if !result.HTMLReportCurrent {
			label = "Previous HTML report (not regenerated)"
		}
		block.WriteString("\n\n" + link(label, htmlReportPath(result.WorkingDirectory)))
	}
	block.WriteString("\n\n" + link("Results JSON", validationPath(result.WorkingDirectory)))
	if result.Cleanup != nil {
		fmt.Fprintf(&block, "\n\nCleanup: %s — %s", result.Cleanup.Status, result.Cleanup.Reason)
	} else {
		block.WriteString("\n\nCleanup: not recorded.")
	}
	block.WriteString(" Verify agent-created outputs and Git/package report exclusions separately; preserve customer files and both reports.")
	return block.String()
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
		if result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if feature.Status != "passed" {
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
		summary.OnboardingResponse += " Blocking checks: " + strings.Join(summary.BlockingChecks, "; ") + "."
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
			facts = append(facts, fmt.Sprintf("%s%s: %d passed, %d failed, %d skipped; %d suite errors; exit %s.", label+configurationLabel(run.Configuration), run.Name, run.TestCounts.Passed, run.TestCounts.Failed, run.TestCounts.Skipped, run.SuiteErrorCount, exit))
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
	passed, total := 0, 0
	for _, feature := range result.Features {
		if feature.Name == "all" {
			continue
		}
		total++
		if feature.Status == "passed" {
			passed++
		}
	}
	if total == 0 {
		facts = append(facts, "Features: not exercised.")
	} else {
		facts = append(facts, fmt.Sprintf("Features: %d/%d project-feature checks passed.", passed, total))
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
