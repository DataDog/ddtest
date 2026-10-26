package testdrive

import (
	"fmt"
	"github.com/DataDog/ddtest/internal/onboard"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type validationSummary struct {
	LocalPython           string                 `json:"local_python,omitempty"`
	FinalResponse         string                 `json:"final_response,omitempty"`
	Facts                 []string               `json:"facts,omitempty"`
	ConfigurationCoverage *configurationCoverage `json:"configuration_coverage,omitempty"`
	ExecutedValidation    *executedValidation    `json:"executed_validation,omitempty"`
	SelectedValidation    *verdict               `json:"selected_validation,omitempty"`
	Status                string                 `json:"status"`
	LocalValidation       string                 `json:"local_validation"`
	CIConfiguration       string                 `json:"ci_configuration"`
	BlockingChecks        []string               `json:"blocking_checks"`
	CIAdvisories          []string               `json:"ci_advisories,omitempty"`
	// Disclosed limits of the local evidence; they do not block success.
	NotExercised            []string        `json:"not_exercised,omitempty"`
	LocalNode               string          `json:"local_node,omitempty"`
	CIScope                 *ciRuntimeScope `json:"ci_runtime_scope,omitempty"`
	OnboardingResponse      string          `json:"onboarding_response"`
	CIExecution             string          `json:"ci_execution"`
	RealCredentialsRequired bool            `json:"real_credentials_required"`
	// Steps only the repository owner can take; local validation cannot do them.
	HumanActions []string `json:"human_actions,omitempty"`
	// Caveats name CI values this run could not reproduce exactly; they never
	// block completion.
	Caveats []string `json:"caveats,omitempty"`
}

// apiKeyAction is the one step CI needs that no local run can perform. What
// happens until it exists depends on how the workflow guards the Datadog step.
const (
	apiKeyAction        = "Add the DD_API_KEY repository secret (GitHub Settings > Secrets and variables > Actions). Until it exists, the Datadog step fails without failing the job and tests run untraced."
	apiKeySkippedAction = "Add the DD_API_KEY repository secret (GitHub Settings > Secrets and variables > Actions). Until it exists, the Datadog step is skipped and tests run untraced."
)

// apiKeyHumanAction picks the wording for a step skipped by an
// env.DD_API_KEY guard (the Cypress setup) or kept with continue-on-error.
func apiKeyHumanAction(check *onboard.RuntimeCheck) string {
	if check != nil {
		for _, job := range check.Jobs {
			if job.Reason == onboard.GuardedActionBranch {
				return apiKeySkippedAction
			}
		}
	}
	return apiKeyAction
}

// featureCoverageBlocker is the summary line for incomplete feature results.
const featureCoverageBlocker = "not every project has every required feature result"

func summarizeValidation(result validationResult) validationSummary {
	summary := validationSummary{Status: "INCOMPLETE", LocalValidation: "INCOMPLETE", CIConfiguration: "not exercised", CIExecution: "not exercised", BlockingChecks: []string{}}
	if result.LocalSuccess {
		summary.LocalValidation = "PASSED"
	}
	if result.CheckOnly {
		summary.LocalValidation = "NOT EXERCISED"
	}
	if (result.CheckOnly || result.StoppedBeforeExecution) && result.Retained != nil && result.Retained.Result != nil {
		summary.LocalValidation = strings.ToUpper(executionStatus(result.Retained.Result)) + " EARLIER; NOT RERUN"
	}
	if result.Compatibility.Status == "suspected regression" && !result.CheckOnly {
		summary.Status, summary.LocalValidation = "FAILED", "FAILED"
	}
	if result.CIRuntime != nil {
		summary.CIConfiguration = result.CIRuntime.Status
	}
	if result.Success {
		summary.Status = "PASSED"
	}
	if result.Error != "" {
		summary.BlockingChecks = append(summary.BlockingChecks, result.Error)
	}
	for _, run := range result.Runs {
		for _, reason := range run.NotExercised {
			if !slices.Contains(summary.NotExercised, reason) {
				summary.NotExercised = append(summary.NotExercised, reason)
			}
		}
	}
	if result.CheckOnly {
		summary.BlockingChecks = append(summary.BlockingChecks, "configuration checks only; paired execution and features were not rerun")
	} else {
		if supportsValidationFramework(result.Framework) && !hasValidationPair(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, "current baseline and reporting-only execution evidence is missing")
		}
		// A hang explains the missing results, coverage and probes; say it once.
		timeouts := timedOutRuns(result)
		summary.BlockingChecks = append(summary.BlockingChecks, timeouts...)
		if len(timeouts) == 0 && missingRealCoverage(result) && (result.Compatibility.Status == "" || result.Compatibility.Status == "compatible") {
			summary.BlockingChecks = append(summary.BlockingChecks, displayName(result.Framework)+" real-suite coverage was not observed: "+missingRealCoverageReason(result)+" Synthetic skipping probes do not validate coverage of customer source; preserve coverage thresholds.")
		}
		if result.Compatibility.Status != "" && result.Compatibility.Status != "compatible" {
			summary.BlockingChecks = append(summary.BlockingChecks, "compatibility: "+result.Compatibility.Status)
		}
		for _, feature := range result.Features {
			if !featureSatisfied(result.Framework, feature) && !slices.ContainsFunc(timeouts, func(timeout string) bool { return strings.HasPrefix(feature.Reason, timeout) }) {
				summary.BlockingChecks = append(summary.BlockingChecks, fmt.Sprintf("%s/%s: %s", feature.Project, feature.Name, feature.Status))
			}
		}
		if !featureChecksPassed(result.Framework, result.Features) {
			summary.BlockingChecks = append(summary.BlockingChecks, featureCoverageBlocker)
		}
		if result.Cleanup == nil {
			summary.BlockingChecks = append(summary.BlockingChecks, "owned cleanup was not exercised")
		} else if result.Cleanup.Status != "passed" {
			summary.BlockingChecks = append(summary.BlockingChecks, "owned cleanup did not pass")
		}
		// A run that stopped before any test command has no report to publish.
		if !result.HTMLReportCurrent && hasValidationExecution(result) {
			summary.BlockingChecks = append(summary.BlockingChecks, "no current HTML findings report")
		}
	}
	for _, sub := range result.substitutions() {
		if !sub.Accepted && !result.CheckOnly {
			summary.BlockingChecks = append(summary.BlockingChecks, sub.blocker())
		}
	}
	var ciFindings []string
	if result.CISelection != nil && result.CISelection.Status != "compatible" {
		ciFindings = append(ciFindings, "CI tracer agreement: "+result.CISelection.Status)
	}
	if result.CIRuntime != nil && result.CIRuntime.Status != "compatible" && result.CIRuntime.Status != "not applicable" {
		ciFindings = append(ciFindings, "CI runtime: "+result.CIRuntime.Status)
	}
	if result.CIPreservation != nil && result.CIPreservation.Status != "passed" {
		finding := "CI preservation: " + result.CIPreservation.Status + " — " + result.CIPreservation.Reason
		if result.ciPreservationBlocking() {
			summary.BlockingChecks = append(summary.BlockingChecks, finding)
		} else {
			ciFindings = append(ciFindings, finding)
		}
	}
	caveats, fidelityBlockers := ciFidelity(&result)
	summary.Caveats = caveats
	if result.Scope != nil {
		ciFindings = append(ciFindings, scopeBlockers(append(slices.Clone(result.Scope.Unresolved), fidelityBlockers...))...)
		for _, configuration := range result.Configurations {
			if configuration.RemoteGrid == "" && !configurationSettled(result, configuration) && !alreadyExplained(ciFindings, configuration.Error) {
				ciFindings = append(ciFindings, "configuration "+configuration.Command+": "+configuration.Status)
			}
		}
	}
	if result.focusedLocalCommand() {
		summary.CIAdvisories = ciFindings
	} else {
		summary.BlockingChecks = append(summary.BlockingChecks, ciFindings...)
	}
	// Lines that only restate tracer-only feature failures add nothing to the
	// failures themselves.
	summary.BlockingChecks = slices.DeleteFunc(summary.BlockingChecks, func(check string) bool { return restatesTracerOnlyFailures(result, check) })
	// Success is scoped to an explicitly selected command; the overall summary
	// continues to expose every unvalidated repository configuration.
	if !result.focusedLocalCommand() && result.Scope != nil && (len(result.Scope.Unresolved)+len(fidelityBlockers) > 0 || slices.ContainsFunc(result.Configurations, func(c configurationResult) bool { return !configurationSettled(result, c) })) && summary.Status != "FAILED" {
		summary.Status = "INCOMPLETE"
	}
	summary.ConfigurationCoverage, summary.ExecutedValidation = summarizeConfigurationCoverage(result)
	if result.FocusedCommand {
		check := selectedValidation(result)
		summary.SelectedValidation = &check
	}
	if result.Workflow != nil {
		for _, check := range result.Workflow.Checks {
			// The scope blockers already list each unresolved entry.
			if check.Name == "ci_scope" && result.Scope != nil && len(result.Scope.Unresolved)+len(fidelityBlockers) > 0 {
				continue
			}
			if check.Status != "passed" && check.Status != "not applicable" {
				summary.BlockingChecks = append(summary.BlockingChecks, "workflow "+check.Name+": "+check.Status+" — "+reportText(check.Reason))
			}
		}
		if result.Success && len(summary.BlockingChecks) == 0 {
			summary.Status = "COMPLETE LOCALLY"
		} else if summary.Status == "PASSED" {
			summary.Status = "INCOMPLETE"
		}
	}
	if result.Preflight != nil {
		summary.LocalNode = result.Preflight.Node
		summary.LocalPython = result.Preflight.Python
	}
	summary.CIScope = summarizeCIScope(result.CIRuntime)
	if summary.CIScope != nil && len(summary.CIScope.Instrumented) > 0 {
		summary.HumanActions = append(summary.HumanActions, apiKeyHumanAction(result.CIRuntime))
	}
	summary.OnboardingResponse = fmt.Sprintf("Onboarding validation: %s. Local validation: %s. CI configuration: %s (static analysis). CI execution and Datadog backend processing: not exercised.", summary.Status, summary.LocalValidation, summary.CIConfiguration)
	if result.focusedLocalCommand() {
		summary.OnboardingResponse = fmt.Sprintf("Selected command validation: %s. Local validation: %s. CI configuration: %s (advisory). Other repository configurations, CI execution and Datadog backend processing remain unvalidated.", summary.Status, summary.LocalValidation, summary.CIConfiguration)
	}
	if summary.LocalNode != "" {
		summary.OnboardingResponse += " Local execution runtime: Node " + summary.LocalNode + "."
	}
	if summary.CIScope != nil {
		summary.OnboardingResponse += fmt.Sprintf(" CI Node scope (static): configured for instrumentation: %s; excluded from instrumentation: %s; unresolved or blocked: %s.", scopeList(summary.CIScope.Instrumented), scopeList(summary.CIScope.Excluded), scopeList(summary.CIScope.Unverified))
	}
	if len(summary.BlockingChecks) > 0 {
		summary.OnboardingResponse += " Blocking checks: " + strings.Join(summary.BlockingChecks[:min(3, len(summary.BlockingChecks))], "; ") + "."
	}
	return summary
}

// A focused command can execute and pass without matching any discovered CI
// configuration exactly. Say so next to the zero configuration counts.
func uncreditedExecution(result validationResult) string {
	command := ""
	for _, run := range result.Runs {
		if run.Role == roleBaseline {
			command = run.Command
			break
		}
	}
	text := "This run's command executed and was paired"
	if command != "" {
		text = fmt.Sprintf("This run's command (%s) executed and was paired", command)
	}
	text += ", but matches no discovered CI configuration exactly, so no CI configuration is credited"
	for _, item := range result.Configurations {
		if len(item.EnvironmentDifferences) > 0 {
			text += " (" + strings.TrimSuffix(item.Compatibility.Reason, ".") + ")"
			break
		}
	}
	return text + ".\n"
}

// One copyable response, built from the same facts as the compact report. Only
// link retained files, and never present an earlier HTML report as a new run.
func validationHandoff(result validationResult) string {
	var block strings.Builder
	summary := result.Summary
	if summary.Status != "COMPLETE LOCALLY" && summary.Status != "PASSED" {
		// A headline that cannot be paraphrased into success, even when a
		// selected command or the local comparison passed.
		fmt.Fprintf(&block, "NOT COMPLETE: onboarding validation is %s. Report the blockers below; do not describe onboarding as complete or successful.\n", summary.Status)
	}
	if summary.SelectedValidation != nil {
		fmt.Fprintf(&block, "Selected %s testdrive: %s.\n", displayName(result.Framework), summary.SelectedValidation.Status)
	}
	if executed := summary.ExecutedValidation; executed != nil {
		fmt.Fprintf(&block, "Executed %s validations: %s (%d/%d).\n", displayName(result.Framework), executed.Status, executed.Passed, executed.Configurations)
	}
	if result.focusedLocalCommand() {
		fmt.Fprintf(&block, "Local testdrive: %s (selected command). Other repository configurations remain unvalidated.\n", summary.Status)
	} else {
		fmt.Fprintf(&block, "Onboarding validation: %s. Local validation: %s.\n", summary.Status, summary.LocalValidation)
	}
	if coverage := summary.ConfigurationCoverage; coverage != nil && coverage.Discovered > 0 {
		fmt.Fprintf(&block, "Configurations: %d/%d validated; %d executed, %d paired; %d blocked, %d not exercised, %d incomplete%s.\n", coverage.Validated, coverage.Discovered, coverage.Executed, coverage.Paired, coverage.Blocked, coverage.NotExercised, coverage.Incomplete, remoteOnlyCount(coverage))
		windows := 0
		for _, configuration := range result.Configurations {
			if configurationExcluded(configuration) && configuration.WindowsRunner {
				windows++
			}
		}
		if optOuts := coverage.Excluded - windows; optOuts > 0 {
			fmt.Fprintf(&block, "CI opt-outs: %d excluded from instrumentation; not executed or validated locally. Original CI tests are preserved.\n", optOuts)
		}
		if windows > 0 {
			fmt.Fprintf(&block, "Windows runners: %d not instrumented; ddtest doesn't set up or validate Windows runners locally. Add manual initialization to instrument them. Original CI tests are preserved.\n", windows)
		}
		if coverage.Executed == 0 && !result.CheckOnly && hasValidationPair(result) {
			block.WriteString(uncreditedExecution(result))
		}
	}
	current := !result.CheckOnly && (result.LastExecution == nil || result.LastExecution.Current)
	if counts := result.ReportingCounts; counts != nil {
		label := "Reporting"
		if !current {
			label = "Historical reporting (not revalidated)"
		}
		fmt.Fprintf(&block, "%s: %d test executions; ", label, counts.TestExecutions)
		if counts.SourceTestIdentities == nil {
			block.WriteString("source-test identities unavailable")
			if reason := strings.TrimSuffix(strings.TrimPrefix(counts.Reason, "Source-test identities are unavailable because "), "."); reason != "" && reason != counts.Reason {
				block.WriteString(" (" + reason + ")")
			}
			block.WriteString("; this count does not affect coverage or feature results.\n")
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
	for _, hint := range result.Compatibility.Hints {
		block.WriteString(hint + "\n")
	}
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
	for _, action := range summary.HumanActions {
		block.WriteString("Human action: " + action + "\n")
	}
	block.WriteString(repositoryChangesHandoff(result.RepositoryChanges))
	if scope := summary.CIScope; scope != nil {
		// The scope groups CI jobs by runtime version, not by job.
		runtime := "Node"
		if result.Framework == "pytest" {
			runtime = "Python"
		}
		instrumented, excluded, unverified := scope.counts()
		fmt.Fprintf(&block, "CI runtime scope (static): %s versions %d instrumented, %d excluded, %d unverified (each version counted once; per-job findings in Results JSON).\n", runtime, instrumented, excluded, unverified)
		for _, job := range scope.NotTried {
			fmt.Fprintf(&block, "Not tried: %s (below Node 18, the oldest dd-trace major ddtest supports; ddtest skipped them on its own, no approval needed).\n", job)
		}
	}
	for _, caveat := range summary.Caveats {
		block.WriteString("Caveat (not a blocker): " + caveat + "\n")
	}
	if result.Workflow != nil && len(result.Workflow.EarlierAttempts) > 0 {
		fmt.Fprintf(&block, "Earlier attempts for this workflow stopped before this run: %s. This run's results stand on their own; the earlier stops are listed so retries stay visible.\n", strings.Join(result.Workflow.EarlierAttempts, ", "))
	}
	for _, disabled := range disabledInstrumentations(result) {
		block.WriteString("Disabled instrumentation: " + disabled + "\n")
	}
	if !result.focusedLocalCommand() && supportsValidationFramework(result.Framework) && summary.CIConfiguration != "compatible" && summary.CIConfiguration != "not applicable" {
		block.WriteString("CI edits require a full local rerun; an actual CI run does not update this local report.\n")
	}
	if summary.LocalNode != "" {
		fmt.Fprintf(&block, "Local runtime: Node %s.\n", summary.LocalNode)
	}
	if len(result.AmbientDatadogEnvironment) > 0 {
		var settings []string
		for _, key := range slices.Sorted(maps.Keys(result.AmbientDatadogEnvironment)) {
			settings = append(settings, key+"="+result.AmbientDatadogEnvironment[key])
		}
		fmt.Fprintf(&block, "Ambient Datadog settings (not from CI) reached local runs: %s. CI runs without them unless the workflow sets them.\n", strings.Join(settings, ", "))
	}
	if summary.LocalPython != "" {
		fmt.Fprintf(&block, "Local runtime: Python %s.\n", summary.LocalPython)
	}
	if result.CheckOnly {
		block.WriteString("Evidence: check-only; paired execution and feature controls were not rerun.\n")
	}
	if result.LastExecution != nil && !result.LastExecution.Current {
		if result.CheckOnly {
			// Check-only keeps the last full run as history; it isn't stale, only not rerun.
			fmt.Fprintf(&block, "Evidence: check-only kept the earlier full execution (%s) as history without rerunning it; testdrive --report reports that execution.\n", result.LastExecution.Status)
		} else if result.StoppedBeforeExecution {
			fmt.Fprintf(&block, "Evidence: this attempt stopped before running tests; the earlier full execution (%s) is kept as history, not rerun. Resolve the blocker, then rerun full validation.\n", result.LastExecution.Status)
		} else {
			block.WriteString("Evidence: earlier execution is stale; rerun full validation after configuration checks or edits.\n")
		}
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
			if more := len(summary.BlockingChecks) - len(headlines); more > 0 {
				fmt.Fprintf(&block, "; and %d more", more)
			}
		}
		block.WriteString(". Full blockers and commands are in Results JSON.\n")
	}
	if len(summary.CIAdvisories) > 0 {
		fmt.Fprintf(&block, "Repository CI advisories: %d (outside the selected command). See ci_advisories and configurations in Results JSON.\n", len(summary.CIAdvisories))
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
		if len(result.Cleanup.Leftovers) > 0 {
			fmt.Fprintf(&block, " Left in the checkout: %s; remove them unless they belong there.", strings.Join(result.Cleanup.Leftovers, ", "))
		}
	} else {
		block.WriteString("\nCleanup: not recorded.")
	}
	if result.Workflow != nil {
		fmt.Fprintf(&block, "\nWorkflow checks: %d recorded; Git/package exclusions and owned cleanup: %s. Preserve both reports. No further cache or package audit is required for unchanged inputs.", len(result.Workflow.Checks), map[bool]string{true: "passed", false: "incomplete"}[exclusionAndCleanupChecksPassed(result.Workflow)])
	} else {
		block.WriteString(" Verify your own scratch and Git/package report exclusions; preserve customer files and both reports.")
	}
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
			if strings.HasPrefix(run.Name, "baseline") && run.ExecutionContext != "" {
				facts = append(facts, run.ExecutionContext)
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
		facts = append(facts, "Earlier execution attempt (historical; not revalidated): "+previous.Session+". Local status: "+executionStatus(previous))
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
	// NotTried names CI jobs on a Node no dd-trace release supports. ddtest
	// skips them on its own; they count as excluded and never block.
	NotTried []string `json:"not_tried,omitempty"`
}

func summarizeCIScope(check *onboard.RuntimeCheck) *ciRuntimeScope {
	if check == nil || len(check.Jobs) == 0 {
		return nil
	}
	scope := &ciRuntimeScope{}
	// A step whose guarded Datadog action is skipped on some branch (for
	// example, forks without a key) is still instrumented on that runtime; the
	// skipped branch is not a separately excluded configuration.
	type stepRuntime struct {
		workflow, job, node string
		step                int
	}
	instrumented := map[stepRuntime]bool{}
	for _, job := range check.Jobs {
		if job.Status == "compatible" {
			instrumented[stepRuntime{job.Workflow, job.Job, job.Node, job.Step}] = true
		}
	}
	type location struct{ workflow, job string }
	var skipped []location
	nodes := map[location][]string{}
	for _, job := range check.Jobs {
		if job.Code != onboard.CodeUnsupportedNode {
			continue
		}
		key := location{job.Workflow, job.Job}
		if nodes[key] == nil {
			skipped = append(skipped, key)
		}
		nodes[key] = appendUnique(nodes[key], nodeScopeLabel(job.Node))
	}
	for _, key := range skipped {
		scope.NotTried = append(scope.NotTried, key.workflow+" / "+key.job+" on Node "+strings.Join(nodes[key], ", "))
	}
	for _, job := range check.Jobs {
		label := nodeScopeLabel(job.Node)
		if job.Python != "" {
			label = "Python " + job.Python
		}
		switch job.Status {
		case "compatible":
			scope.Instrumented = appendUnique(scope.Instrumented, label)
		case "excluded":
			if job.Reason != onboard.GuardedActionBranch || !instrumented[stepRuntime{job.Workflow, job.Job, job.Node, job.Step}] {
				scope.Excluded = appendUnique(scope.Excluded, label)
			}
		default:
			scope.Unverified = appendUnique(scope.Unverified, label)
		}
	}
	return scope
}

// counts returns each Node version once: instrumented on any job wins over
// excluded, and excluded over unverified. The lists keep every finding; only
// the summary line must not add a version to two totals.
func (scope *ciRuntimeScope) counts() (instrumented, excluded, unverified int) {
	seen := map[string]bool{}
	count := func(labels []string) int {
		n := 0
		for _, label := range labels {
			if !seen[label] {
				seen[label] = true
				n++
			}
		}
		return n
	}
	instrumented = count(scope.Instrumented)
	excluded = count(scope.Excluded)
	unverified = count(scope.Unverified)
	return instrumented, excluded, unverified
}

func configurationLabel(command string) string {
	if command == "" {
		return ""
	}
	return command + " / "
}

// scopeBlockers reports unresolved CI scope once per cause: steps that share
// a cause (for example one unsupported shell) are listed on one line.
func scopeBlockers(unresolved []string) []string {
	var causes []string
	locations := map[string][]string{}
	for _, reason := range unresolved {
		location, cause, found := strings.Cut(reason, "): ")
		if !found {
			location, cause = "", reason
		} else {
			location += ")"
		}
		if _, seen := locations[cause]; !seen {
			causes = append(causes, cause)
		}
		locations[cause] = append(locations[cause], location)
	}
	var blockers []string
	for _, cause := range causes {
		switch places := locations[cause]; {
		case len(places) == 1 && places[0] == "":
			blockers = append(blockers, "unresolved CI scope: "+cause)
		case len(places) == 1:
			blockers = append(blockers, "unresolved CI scope: "+places[0]+": "+cause)
		default:
			blockers = append(blockers, fmt.Sprintf("unresolved CI scope (%d steps: %s): %s", len(places), strings.Join(places, "; "), cause))
		}
	}
	return blockers
}

// alreadyExplained reports whether a configuration's error is already listed,
// so its derived "configuration ...: blocked" line would only repeat it.
func alreadyExplained(blockers []string, err string) bool {
	err = strings.TrimSpace(err)
	return err != "" && slices.ContainsFunc(blockers, func(blocker string) bool { return strings.Contains(blocker, err) })
}

// disabledInstrumentations names the CI step settings that turn tracer
// integrations off, with the configuration they apply to: the coverage CI
// loses must be visible in the handoff, not only in the JSON environment.
func disabledInstrumentations(result validationResult) []string {
	var disabled []string
	for _, configuration := range result.Configurations {
		for _, key := range slices.Sorted(maps.Keys(configuration.Environment)) {
			value := configuration.Environment[key]
			if !onboard.TracerPluginSetting(key) || value == "" {
				continue
			}
			if strings.HasSuffix(key, "_ENABLED") && value != "false" && value != "0" {
				continue
			}
			line := fmt.Sprintf("%s=%s on the CI test step of %s (spans from it are not reported in CI)", key, value, configuration.Command)
			if !slices.Contains(disabled, line) {
				disabled = append(disabled, line)
			}
		}
	}
	return disabled
}
