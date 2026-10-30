// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

type verdict struct {
	Status          string   `json:"status"`
	Code            string   `json:"code,omitempty"`
	Reason          string   `json:"reason"`
	DifferenceCount int      `json:"difference_count,omitempty"`
	Differences     []string `json:"differences,omitempty"`
	// Untracked or ignored checkout entries a run in the checkout left behind.
	Leftovers []string `json:"leftovers,omitempty"`
	// Known causes worth checking first; they never change the status.
	Hints []string `json:"hints,omitempty"`
}

type featureResult struct {
	Project string `json:"project,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	// Code marks failures no customer change is expected to fix.
	Code string `json:"code,omitempty"`
	// ObservedCode keeps the evaluated failure code when Code reports a
	// known limitation instead, such as tracer_control_not_applied.
	ObservedCode string `json:"observed_code,omitempty"`
	Reason       string `json:"reason"`
}

const (
	// The tracer reported the probe but applied none of the requested control.
	tracerControlNotApplied = "tracer_control_not_applied"
	// A required feature the tracer is known not to apply for this setup.
	tracerLimitation = "tracer_limitation"
)

// tracerOnly reports whether the feature failed in a way only a tracer change
// can address.
func (f featureResult) tracerOnly() bool {
	return f.Code == tracerControlNotApplied || f.Code == tracerLimitation
}

type runRole string

const (
	roleBaseline     runRole = "baseline"
	roleReporting    runRole = "reporting-only"
	roleRepeat       runRole = "repeat"
	roleProbeControl runRole = "probe-control"
	roleProbeFeature runRole = "probe-feature"
)

type runSpec struct {
	Name                 string
	Role                 runRole
	Instrumented         bool
	Scenario             intake.Scenario
	ProbePath, ProbeMode string
}

// Detailed results feed validation and the HTML findings report, not compact JSON.
type validationRun struct {
	webdriverIOAttempts []jestTest
	missingSourceLoader string // Explains a .js import that only exists as TypeScript.
	ReplacedReporters   []string
	Role                runRole
	cucumberParallel    bool
	cucumberSupport     *cucumberSupport
	FrameworkNode       string
	NativeCommand       string
	ProbeExecutions     *int
	Coverage            *coverageSummary
	ExecutionContext    string `json:"execution_context,omitempty"`
	temporaryRoot       string
	commandOutput       string
	cypressCode         cypressTestCode
	commandError        error
	Project             string
	Skipping            *skippingDiagnostic
	root                string
	checkoutRoot        string
	// Why a real-suite Mocha run may report no per-test coverage.
	coverageCause                   string
	framework                       string
	Name                            string
	Command                         string
	ExecutionPath                   string
	Workers                         int
	PlannerSkipped                  bool
	Instrumented                    bool
	ProbeMode                       string
	ProbeCoverageThresholdsDisabled bool
	ProbeLintSkipped                bool
	Diagnostic                      string
	ExitCode                        int
	ResultError                     string
	// Set when testdrive's --timeout stopped the command; replaces ResultError.
	Timeout       string
	Tests         []jestTest
	SuiteErrors   []string
	SuiteFailures []string
	Facts         intake.Facts
	// Parts of the customer's suite that this run could not exercise.
	NotExercised []string
}

type testCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Other   int `json:"other"`
}

type coverageSummary struct {
	Reason       string  `json:"reason,omitempty"`
	Ratio        float64 `json:"covered_test_ratio"`
	Status       string  `json:"status"`
	CoveredTests int     `json:"covered_tests"`
	TotalTests   int     `json:"total_tests"`
	Level        string  `json:"level,omitempty"`
	// Edit is an exact, approval-gated instrumentation change for a recognized
	// application build when no application coverage was observed.
	Edit *coverageEdit `json:"edit,omitempty"`
}

type runSummary struct {
	ReplacedReporters               []string            `json:"replaced_reporters,omitempty"`
	Role                            runRole             `json:"role"`
	NativeResults                   string              `json:"native_results,omitempty"`
	FrameworkNode                   string              `json:"framework_node,omitempty"`
	NativeCommand                   string              `json:"native_command,omitempty"`
	ProbeExecutions                 *int                `json:"probe_executions,omitempty"`
	ExecutionContext                string              `json:"execution_context,omitempty"`
	Coverage                        *coverageSummary    `json:"coverage,omitempty"`
	ExecutionPath                   string              `json:"execution_path,omitempty"`
	Workers                         int                 `json:"workers,omitempty"`
	PlannerSkipped                  bool                `json:"probe_skipped_by_planner,omitempty"`
	Configuration                   string              `json:"configuration,omitempty"`
	NativeCounts                    *executionCounts    `json:"native_counts,omitempty"`
	ProbeLintSkipped                bool                `json:"probe_lint_skipped,omitempty"`
	Project                         string              `json:"project,omitempty"`
	Skipping                        *skippingDiagnostic `json:"skipping,omitempty"`
	Name                            string              `json:"name"`
	Command                         string              `json:"command,omitempty"`
	Instrumented                    bool                `json:"instrumented"`
	ProbeMode                       string              `json:"probe_mode,omitempty"`
	ProbeCoverageThresholdsDisabled bool                `json:"probe_coverage_thresholds_disabled,omitempty"`
	Diagnostic                      string              `json:"diagnostic,omitempty"`
	NotExercised                    []string            `json:"not_exercised,omitempty"`
	ExitCode                        *int                `json:"exit_code"`
	ResultError                     string              `json:"result_error,omitempty"`
	Timeout                         string              `json:"timeout,omitempty"`
	TestCounts                      testCounts          `json:"test_counts"`
	SuiteErrorCount                 int                 `json:"suite_error_count"`
	TestEventCount                  int                 `json:"test_event_count"`
	// Runtimes are the runtime versions the tracer reported running on.
	Runtimes []string `json:"runtimes,omitempty"`
}

func (r validationRun) summary() runSummary {
	summary := runSummary{Coverage: r.Coverage, FrameworkNode: r.FrameworkNode, ProbeExecutions: r.ProbeExecutions, NativeCommand: r.NativeCommand, ExecutionContext: r.ExecutionContext, ReplacedReporters: r.ReplacedReporters, Role: r.Role, ExecutionPath: r.ExecutionPath, Workers: r.Workers, PlannerSkipped: r.PlannerSkipped, Name: r.Name, Project: r.Project, Skipping: r.Skipping, Command: r.Command, Instrumented: r.Instrumented,
		ProbeLintSkipped: r.ProbeLintSkipped, ProbeMode: r.ProbeMode, ResultError: reportText(r.ResultError), Timeout: r.Timeout, ProbeCoverageThresholdsDisabled: r.ProbeCoverageThresholdsDisabled, Diagnostic: reportText(r.Diagnostic), NotExercised: r.NotExercised, SuiteErrorCount: len(r.SuiteErrors), TestEventCount: r.Facts.TestEventCount, Runtimes: r.Facts.RuntimeVersions}
	if r.framework == "cucumber" || r.framework == "webdriverio" {
		summary.NativeResults = "observed"
		if r.ResultError != "" {
			summary.NativeResults = "unavailable"
		}
	}
	if r.Role == roleReporting {
		summary.NativeCounts = nativeExecutionCounts(r)
	}
	if r.framework == "mocha" && r.Instrumented && r.ProbeMode == "" && strings.HasPrefix(r.Name, "reporting-only") && r.Facts.TestCount > 0 {
		status := "observed"
		if r.Facts.CoveredTestCount == 0 {
			status = "not observed"
		}
		summary.Coverage = &coverageSummary{Status: status, CoveredTests: r.Facts.CoveredTestCount, TotalTests: r.Facts.TestCount, Level: r.Facts.CoverageLevel, Ratio: float64(r.Facts.CoveredTestCount) / float64(r.Facts.TestCount)}
		summary.Coverage.Reason = fmt.Sprintf("Coverage reported for %d/%d real tests; synthetic probes are excluded.", r.Facts.CoveredTestCount, r.Facts.TestCount)
		cause := r.coverageCause
		if cause == "" && slices.ContainsFunc(r.Tests, func(test jestTest) bool {
			return filepath.Ext(test.File) == ".mjs" || filepath.Ext(test.File) == ".mts"
		}) {
			cause = mochaESMCoverageCause
		}
		if status != "observed" && cause != "" {
			summary.Coverage.Reason += " " + cause
		}
	}
	// A setup failure before execution must not look like a successful command.
	if r.Command != "" {
		exitCode := r.ExitCode
		summary.ExitCode = &exitCode
	}
	for _, test := range r.Tests {
		switch test.Status {
		case "passed":
			summary.TestCounts.Passed++
		case "failed":
			summary.TestCounts.Failed++
		case "pending", "skipped", "todo", "disabled":
			summary.TestCounts.Skipped++
		default:
			summary.TestCounts.Other++
		}
	}
	return summary
}

type validationResult struct {
	// Set when a compatible local runtime stood in for the CI runtime.
	RuntimeSubstitution *runtimeSubstitution `json:"runtime_substitution,omitempty"`
	// RuntimeSubstitutions lists every distinct substitution when --all
	// validated configurations whose CI paths use different Node majors.
	RuntimeSubstitutions []runtimeSubstitution `json:"runtime_substitutions,omitempty"`

	TelemetryOnly  bool `json:"telemetry_only,omitempty"`
	SchemaVersion  int  `json:"schema_version"`
	FocusedCommand bool `json:"focused_command,omitempty"`
	// A workflow gate stopped this attempt before any test command. The earlier
	// execution and its HTML findings stay as history; the handoff is still invalidated.
	StoppedBeforeExecution bool `json:"stopped_before_execution,omitempty"`
	// DD_* values inherited from ddtest's own environment that reached the test
	// commands without coming from CI or ddtest, with secret-like values masked.
	AmbientDatadogEnvironment map[string]string `json:"ambient_datadog_environment,omitempty"`
	// Uncommitted changes outside CI workflows and ignore files, such as the
	// dependencies and coverage config ddtest prescribes for validation.
	RepositoryChanges []repositoryChange `json:"repository_changes,omitempty"`
	// Leading VAR=value assignments of --command, with secret-like values masked.
	CommandEnvironment map[string]string        `json:"command_environment,omitempty"`
	CIPreservation     *verdict                 `json:"ci_preservation,omitempty"`
	CIBaseline         []string                 `json:"ci_baseline,omitempty"`
	Workflow           *WorkflowEvidence        `json:"workflow,omitempty"`
	Configurations     []configurationResult    `json:"configurations,omitempty"`
	ReportingCounts    *executionCounts         `json:"reporting_counts,omitempty"`
	Scope              *onboard.ValidationScope `json:"scope,omitempty"`
	Cleanup            *verdict                 `json:"cleanup,omitempty"`
	HTMLReportCurrent  bool                     `json:"html_report_current"`
	Session            string                   `json:"session"`
	CompletedAt        time.Time                `json:"completed_at"`
	CheckOnly          bool                     `json:"check_only"`
	ChecksPassed       bool                     `json:"checks_passed"`
	LocalSuccess       bool                     `json:"local_success"`
	Preflight          *jestPreflight           `json:"preflight,omitempty"`
	ProjectChecks      []projectProbeCheck      `json:"project_checks,omitempty"`
	Selection          *platform.JSSelection    `json:"tracer_selection,omitempty"`
	TracerSource       string                   `json:"tracer_source,omitempty"`
	CISelection        *verdict                 `json:"ci_tracer_selection,omitempty"`
	CIExecution        verdict                  `json:"ci_execution"`
	Success            bool                     `json:"success"`
	WorkingDirectory   string                   `json:"working_directory"`
	Error              string                   `json:"error,omitempty"`
	Framework          string                   `json:"framework"`
	Tracer             string                   `json:"tracer"`
	CIRuntime          *onboard.RuntimeCheck    `json:"ci_runtime,omitempty"`
	Compatibility      verdict                  `json:"compatibility"`
	Features           []featureResult          `json:"features"`
	Runs               []runSummary             `json:"runs"`
	Summary            validationSummary        `json:"summary,omitzero"`
	LastExecution      *validationEvidence      `json:"last_execution,omitempty"`
	ConfigurationCheck validationEvidence       `json:"configuration_check"`
	Retained           *retainedExecution       `json:"retained_execution,omitempty"`
}

func marshalValidation(result validationResult) ([]byte, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if !result.CheckOnly {
		return json.MarshalIndent(result, "", "  ")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for _, name := range []string{"success", "local_success", "runs"} {
		delete(fields, name)
	}
	return json.MarshalIndent(fields, "", "  ")
}

const maxReportDifferences = 10
const diagnosticLimit = 1024

// Bound diagnostics too: install errors can contain entire package-manager logs.
func reportText(value string) string {
	const limit = diagnosticLimit
	text := []rune(value)
	if len(text) > limit {
		return string(text[:limit]) + "… [truncated]"
	}
	return value
}

const validationFilename = "testdrive.json"

func validationPath(repositoryRoot string) string {
	return filepath.Join(repositoryRoot, constants.PlanDirectory, validationFilename)
}

func finishValidation(output io.Writer, repositoryRoot string, result validationResult) error {
	result.SchemaVersion = 1
	if !result.CheckOnly && !result.StoppedBeforeExecution && !result.HTMLReportCurrent {
		if info, err := os.Lstat(htmlReportPath(repositoryRoot)); err == nil && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			if err := os.Remove(htmlReportPath(repositoryRoot)); err != nil {
				return err
			}
		}
	}
	if result.Framework != "" && !supportsValidationFramework(result.Framework) {
		result.Error = reportText(result.Error)
		result.CompletedAt = time.Now().UTC()
		result.WorkingDirectory = repositoryRoot
		result.Success = result.Error == ""
		result.TelemetryOnly = true
		result.Summary = validationSummary{Status: "UNVALIDATED (TELEMETRY ONLY)", LocalValidation: "NOT EXERCISED", CIConfiguration: "not exercised"}
		data, err := marshalValidation(result)
		if err != nil {
			return fmt.Errorf("encode testdrive results: %w", err)
		}
		if err := writeValidation(validationPath(repositoryRoot), append(data, '\n')); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "\nResults JSON: %s\n", validationPath(""))
		return nil
	}
	if err := checkCIPreservation(repositoryRoot, &result); err != nil {
		return err
	}
	if !result.CheckOnly && result.ReportingCounts == nil {
		result.ReportingCounts = reportingExecutionCounts(result.Runs)
	}
	if result.Compatibility.Status == "suspected regression" {
		result.Compatibility.Hints = knownConflictHints(repositoryRoot)
	}
	prepareValidationResult(repositoryRoot, &result)
	result.RepositoryChanges = nonCIChanges(repositoryRoot)
	if err := retainExecution(repositoryRoot, &result); err != nil {
		_, _ = fmt.Fprintf(output, "Warning: earlier execution evidence could not be retained: %s. Writing current results.\n", reportText(err.Error()))
	}
	setValidationEvidence(&result)
	result.Summary = summarizeValidation(result)
	result.Summary.Facts = validationFacts(result)
	result.Summary.FinalResponse = validationHandoff(result)
	data, err := marshalValidation(result)
	if err != nil {
		return fmt.Errorf("encode validation results: %w", err)
	}
	path := validationPath(repositoryRoot)
	if err := writeValidation(path, append(data, '\n')); err != nil {
		return err
	}

	if !result.CheckOnly && result.Workflow != nil {
		if err := saveWorkflowReceipt(repositoryRoot, result); err != nil {
			return fmt.Errorf("save private validation receipt: %w", err)
		}
	}

	if result.CIRuntime != nil {
		_, _ = fmt.Fprintf(output, "\nCI runtime compatibility: %s\n%s\n", result.CIRuntime.Status, result.CIRuntime.Reason)
		for _, job := range result.CIRuntime.Jobs {
			label := job.Workflow + " / " + job.Job
			if job.Step != 0 {
				label += fmt.Sprintf(" / step %d", job.Step)
			}
			if job.Source != "" {
				label += " (" + job.Source + ")"
			}
			if job.Node != "" {
				label += " / Node " + job.Node
			}
			if job.Runtime != "" {
				label += " / " + frameworkCIRuntimeName(result.Framework) + " " + job.Runtime
			}
			if job.NodeResolution != nil {
				label += " => " + job.NodeResolution.Version + " (" + job.NodeResolution.Source + ")"
			}
			_, _ = fmt.Fprintf(output, "  - %s: %s — %s", label, job.Status, job.Reason)
			if job.Tracer != "" {
				_, _ = fmt.Fprintf(output, " (%s; requires %s)", job.Tracer, job.Requirement)
			}
			_, _ = fmt.Fprintln(output)
		}
		for _, entry := range result.CIRuntime.Review {
			command := entry.Command
			if onboard.RedactsCommandKeys(result.Framework) {
				command = onboard.RedactCypressCommand(command)
			}
			_, _ = fmt.Fprintf(output, "  - Review separately: %s / %s / step %d (%s): %s\n", entry.Workflow, entry.Job, entry.Step, command, entry.Reason)
		}
	}
	if result.CISelection != nil {
		_, _ = fmt.Fprintf(output, "\nCI tracer agreement: %s — %s\n", result.CISelection.Status, result.CISelection.Reason)
	}
	if result.CheckOnly {
		_, _ = fmt.Fprintln(output, "Configuration checks only. Compatibility tests and features were not rerun by this invocation.")
	}
	_, _ = fmt.Fprintf(output, "\nCompatibility (this invocation): %s\n%s\n", result.Compatibility.Status, result.Compatibility.Reason)
	for _, difference := range result.Compatibility.Differences {
		_, _ = fmt.Fprintf(output, "  - %s\n", difference)
	}
	if result.Error != "" {
		_, _ = fmt.Fprintf(output, "Validation error: %s\n", result.Error)
	}
	for _, feature := range result.Features {
		label := feature.Name
		if feature.Project != "" {
			label = feature.Project + "/" + label
		}
		_, _ = fmt.Fprintf(output, "Feature %s: %s — %s\n", label, feature.Status, feature.Reason)
	}
	_, _ = fmt.Fprintf(output, "\nValidation verdict: %s\nLocal validation: %s\n", result.Summary.Status, result.Summary.LocalValidation)
	if execution := result.Summary.ExecutedValidation; execution != nil {
		_, _ = fmt.Fprintf(output, "Executed %s validations: %s — %s\n", displayName(result.Framework), execution.Status, execution.Reason)
	}
	if coverage := result.Summary.ConfigurationCoverage; coverage != nil {
		if coverage.Discovered > 0 {
			_, _ = fmt.Fprintf(output, "Configuration coverage: %s; %d blocked, %d not exercised, %d incomplete%s.\n", coverage.validatedText(), coverage.Blocked, coverage.NotExercised, coverage.Incomplete, remoteOnlyCount(coverage))
		} else {
			_, _ = fmt.Fprintln(output, "Configuration coverage: no matching CI configurations discovered; current command evidence is reported separately.")
		}
	}
	if selected := result.Summary.SelectedValidation; selected != nil {
		_, _ = fmt.Fprintf(output, "Selected %s testdrive: %s — %s\n", displayName(result.Framework), selected.Status, selected.Reason)
	}
	if len(result.Summary.BlockingChecks) > 0 {
		_, _ = fmt.Fprintf(output, "Blocking checks: %s\n", strings.Join(result.Summary.BlockingChecks, "; "))
	}
	for _, reason := range result.Summary.CIAdvisories {
		_, _ = fmt.Fprintf(output, "CI advisory (outside selected command): %s\n", reason)
	}
	for _, reason := range result.Summary.NotExercised {
		_, _ = fmt.Fprintf(output, "Not exercised: %s\n", reason)
	}
	_, _ = fmt.Fprintf(output, "CI configuration: %s\nCI execution: not exercised\nReal Datadog credentials required for these local checks: no\nPreserve this verdict in the onboarding response. A failed local feature check is not explained by missing real Datadog credentials.\n", result.Summary.CIConfiguration)
	if retained := result.Retained; retained != nil {
		previous := retained.Result
		_, _ = fmt.Fprintf(output, "\nEarlier execution attempt: %s / %s\nLocal execution: %s earlier; not rerun\n%s\n", previous.Session, previous.CompletedAt.Format(time.RFC3339), result.LastExecution.Status, retained.Reason)
		for _, feature := range previous.Features {
			if feature.Status != "passed" {
				_, _ = fmt.Fprintf(output, "Earlier feature check: %s/%s %s\n", feature.Project, feature.Name, feature.Status)
			}
		}
		_, _ = fmt.Fprintln(output, "Keep the earlier commands and feature results in retained_execution.result when reporting onboarding progress.")
	}
	_, _ = fmt.Fprintf(output, "Datadog library: %s\nResults JSON: %s\n", tracerDescription(result), validationPath(""))
	_, _ = fmt.Fprintln(output, "Keep this JSON report and the HTML findings report after cleanup, including when validation fails.")
	if result.CheckOnly && result.ChecksPassed {
		return nil
	}
	if !result.Success {
		return fmt.Errorf("validation is incomplete or found an unexpected difference; see %s", path)
	}
	return nil
}

// markTimeout names testdrive's own --timeout when it stopped the run, in
// place of the partial results or internal context error it leaves behind.
func markTimeout(run validationRun) validationRun {
	var timeout *ext.CommandTimeoutError
	if errors.As(run.commandError, &timeout) {
		run.Timeout = fmt.Sprintf("%s exceeded --timeout %s; raise --timeout or fix the hanging step", run.Name, timeout.Timeout)
		run.ResultError = run.Timeout
	}
	return run
}

// timedOutRuns lists the timeout of every run that testdrive's --timeout stopped.
func timedOutRuns(result validationResult) []string {
	var timeouts []string
	for _, run := range result.Runs {
		if run.Timeout != "" {
			timeouts = append(timeouts, run.Timeout)
		}
	}
	return timeouts
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// Write next to the destination and rename only a complete report. Concurrent
// runs use distinct scratch files; the last completed report replaces the old one.
func writeValidation(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create validation report directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".testdrive-report-*")
	if err != nil {
		return fmt.Errorf("create validation report: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return fmt.Errorf("write validation report: %w", err)
	}
	if err := replaceReportFile(file.Name(), path); err != nil {
		return fmt.Errorf("replace validation report: %w", err)
	}
	return nil
}

func compareCISelection(check onboard.RuntimeCheck, localVersion string) *verdict {
	if check.Status == onboard.StatusNotApplicable {
		return nil
	}
	result := &verdict{Status: onboard.StatusCompatible, Reason: "Every statically resolved instrumented CI entry selects the same exact tracer version as the local selection. Floating selections are checked only at this run's resolution time."}
	if localVersion == "" {
		return &verdict{Status: onboard.StatusInconclusive, Reason: "Local tracer version is unresolved."}
	}
	checked := false
	for _, job := range check.Jobs {
		if job.Status == onboard.StatusExcluded {
			continue
		}
		if job.Tracer == "" {
			result.Status = onboard.StatusInconclusive
			result.Reason = fmt.Sprintf("%s / %s / step %d: %s", job.Workflow, job.Job, job.Step, job.Reason)
			if job.Code == onboard.CodeMissingInstrumentation {
				result.Status = onboard.StatusIncompatible
				return result
			}
			continue
		}
		checked = true
		if strings.TrimPrefix(job.Tracer, "dd-trace@") != localVersion {
			return &verdict{Status: onboard.StatusIncompatible, Reason: fmt.Sprintf("%s / %s selects %s, but local validation selected dd-trace@%s. Align selections or validate this CI version separately.", job.Workflow, job.Job, job.Tracer, localVersion)}
		}
	}
	if !checked {
		result.Status = onboard.StatusInconclusive
		result.Reason = "No instrumented CI tracer selection could be verified."
	}
	return result
}

func prepareValidationResult(repositoryRoot string, result *validationResult) {
	result.SchemaVersion = 1
	if result.CheckOnly {
		result.Compatibility = verdict{Status: onboard.StatusNotExercised, Reason: "Configuration checks only; paired execution was not exercised."}
		result.Features = []featureResult{{Name: "all", Status: onboard.StatusNotExercised, Reason: "Configuration checks only."}}
	}
	result.CompletedAt = time.Now().UTC()
	result.WorkingDirectory = repositoryRoot
	result.CIExecution = verdict{Status: "not exercised", Reason: "Local-only validation; CI execution and Datadog backend processing were not exercised."}
	result.LocalSuccess = result.Compatibility.Status == "compatible" && result.Error == "" && featureChecksPassed(result.Framework, result.Features) && !missingRealCoverage(*result)
	if supportsValidationFramework(result.Framework) {
		result.LocalSuccess = result.LocalSuccess && hasValidationPair(*result)
	}
	result.Success = result.LocalSuccess && workflowChecksPassed(result.Workflow)
	if supportsValidationFramework(result.Framework) {
		result.Success = result.Success && result.HTMLReportCurrent && result.Cleanup != nil && result.Cleanup.Status == "passed"
	}
	result.ChecksPassed = workflowChecksPassed(result.Workflow) && result.Error == "" && result.Preflight != nil && result.Preflight.Verdict.Status == "compatible"
	if result.Scope != nil && !result.focusedLocalCommand() {
		_, fidelityBlockers := ciFidelity(result)
		result.ChecksPassed = result.ChecksPassed && len(result.Scope.Unresolved)+len(fidelityBlockers) == 0
		for _, command := range result.Scope.Commands {
			if ciInstrumentationDisabled(command) {
				continue
			}
			result.ChecksPassed = result.ChecksPassed && len(command.UnvalidatedSetup) == 0 && (!result.CheckOnly || len(command.Prerequisites) == 0 || len(result.Configurations) > 0)
		}
	}
	if result.ciPreservationBlocking() {
		result.Success, result.ChecksPassed = false, false
	}
	if result.CISelection != nil && !result.focusedLocalCommand() {
		result.Success = result.Success && result.CISelection.Status == "compatible"
		result.ChecksPassed = result.ChecksPassed && result.CISelection.Status == "compatible"
	}
	if result.CIRuntime != nil {
		ciOK := result.CIRuntime.Status == "compatible" || result.CIRuntime.Status == "not applicable"
		if !result.focusedLocalCommand() {
			result.Success = result.Success && ciOK
			result.ChecksPassed = result.ChecksPassed && ciOK
		}
		check := *result.CIRuntime
		check.Reason = reportText(check.Reason)
		check.Jobs = slices.Clone(check.Jobs)
		check.Review = slices.Clone(check.Review)
		for _, findings := range [][]onboard.RuntimeFinding{check.Jobs, check.Review} {
			for i := range findings {
				job := &findings[i]
				for _, field := range []*string{&job.Workflow, &job.Job, &job.Source, &job.Command, &job.Resolution, &job.Node, &job.Runtime, &job.NodeSource, &job.Action, &job.Tracer, &job.TracerRequested, &job.Requirement, &job.Reason} {
					*field = reportText(*field)
				}
			}
		}
		result.CIRuntime = &check
	}
	result.Error = reportText(result.Error)
	result.Compatibility.Reason = reportText(result.Compatibility.Reason)
	result.Compatibility.DifferenceCount = len(result.Compatibility.Differences)
	// An unstable-identity verdict names every example to fix.
	limit := maxReportDifferences
	if result.Compatibility.Code == "unstable_test_identities" {
		limit = maxUnstableExamples + 1
	}
	result.Compatibility.Differences = slices.Clone(result.Compatibility.Differences[:min(len(result.Compatibility.Differences), limit)])
	for i, difference := range result.Compatibility.Differences {
		result.Compatibility.Differences[i] = reportText(difference)
	}
	result.Features = slices.Clone(result.Features)
	for i, feature := range result.Features {
		result.Features[i].Reason = reportText(feature.Reason)
	}
	if result.CheckOnly {
		result.Success = false
		result.LocalSuccess = false
	}

	if !result.CheckOnly && supportsValidationFramework(result.Framework) {
		result.Success = validationPassed(*result)
	}
	applyScopeVerdict(result)
	for _, sub := range result.substitutions() {
		if !sub.Accepted {
			result.Success = false
		}
	}
}

// Runs that stop before a tracer is selected show the request, marked as such.
func tracerDescription(result validationResult) string {
	if result.TracerSource == "" {
		if result.Tracer == "" || result.Tracer == "not selected" {
			return result.Tracer
		}
		return result.Tracer + " · not resolved"
	}
	return result.Tracer + " · " + result.TracerSource
}

// The reason of the first real-suite run without coverage, or "".
func missingRealCoverageReason(result validationResult) string {
	for _, run := range result.Runs {
		if run.Coverage != nil && run.Coverage.Status != "observed" {
			return run.Coverage.Reason
		}
	}
	return ""
}

func missingRealCoverage(result validationResult) bool {
	if skippingPolicy(result.Framework).Status != "required" {
		return false
	}
	for _, run := range result.Runs {
		if run.Coverage != nil && run.Coverage.Status != "observed" {
			return true
		}
	}
	return false
}

var secretEnvironmentName = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|auth|credential)`)

// Report what a focused command set, without copying credentials into the report.
func reportedEnvironment(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return nil
	}
	reported := make(map[string]string, len(environment))
	for key, value := range environment {
		if secretEnvironmentName.MatchString(key) {
			value = "[REDACTED]"
		}
		reported[key] = value
	}
	return reported
}
