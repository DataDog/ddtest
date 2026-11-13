// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// splitTracerOnlyBlockers separates blockers only a tracer change can address
// (with their reasons) from the rest. The summary lines that merely restate a
// tracer-only feature failure are dropped.
func splitTracerOnlyBlockers(validation validationResult) (reportOnly, actionable []string) {
	tracerOnly, _ := tracerOnlyFeatures(validation)
	for _, check := range validation.Summary.BlockingChecks {
		if feature, ok := tracerOnly[check]; ok {
			reportOnly = append(reportOnly, check+" — "+feature.Reason)
			continue
		}
		if restatesTracerOnlyFailures(validation, check) {
			continue
		}
		actionable = append(actionable, check)
	}
	// A configuration's "incomplete" line only restates the other blockers that
	// caused it; keep it only when it is the sole explanation.
	derived := func(check string) bool { return configurationStatusLine(validation, check) }
	if slices.ContainsFunc(validation.Summary.BlockingChecks, func(check string) bool { return !derived(check) }) {
		actionable = slices.DeleteFunc(actionable, derived)
	}
	return reportOnly, actionable
}

// tracerOnlyFeatures maps each unsatisfied feature's blocker line to the
// feature when only a tracer change can address it, and reports whether any
// other feature failed.
func tracerOnlyFeatures(validation validationResult) (map[string]featureResult, bool) {
	tracerOnly := map[string]featureResult{}
	others := false
	for _, feature := range validation.Features {
		if featureSatisfied(validation.Framework, feature) {
			continue
		}
		if feature.tracerOnly() {
			tracerOnly[fmt.Sprintf("%s/%s: %s", feature.Project, feature.Name, feature.Status)] = feature
		} else {
			others = true
		}
	}
	return tracerOnly, others
}

// restatesTracerOnlyFailures reports whether check only restates feature
// failures that are all tracer-only: the generic feature-coverage line and a
// compatible configuration's "incomplete" line.
func restatesTracerOnlyFailures(validation validationResult, check string) bool {
	tracerOnly, others := tracerOnlyFeatures(validation)
	return len(tracerOnly) > 0 && !others && featureResultsComplete(validation) &&
		(check == featureCoverageBlocker || pairedConfigurationBlocker(validation, check))
}

// featureResultsComplete reports whether every project has a result for every
// required feature and every paired configuration produced one, so a missing
// result is never hidden behind tracer-only failures.
func featureResultsComplete(validation validationResult) bool {
	projects := map[string]map[string]bool{}
	for _, feature := range validation.Features {
		if projects[feature.Project] == nil {
			projects[feature.Project] = map[string]bool{}
		}
		projects[feature.Project][feature.Name] = true
	}
	paired := 0
	for _, configuration := range validation.Configurations {
		if configuration.PairedExecution {
			paired++
		}
	}
	if len(projects) == 0 || len(projects) < paired {
		return false
	}
	for _, names := range projects {
		for _, policy := range featurePolicies(validation.Framework) {
			if !names[policy.Name] {
				return false
			}
		}
	}
	return true
}

// configurationStatusLine reports whether check is the summary's derived
// "configuration <command>: <status>" line for one of the configurations.
func configurationStatusLine(validation validationResult, check string) bool {
	return slices.ContainsFunc(validation.Configurations, func(c configurationResult) bool {
		return check == "configuration "+c.Command+": "+c.Status
	})
}

// pairedConfigurationBlocker reports whether check only says that a compatible,
// paired configuration is incomplete, which its feature results explain.
func pairedConfigurationBlocker(validation validationResult, check string) bool {
	return slices.ContainsFunc(validation.Configurations, func(c configurationResult) bool {
		return c.PairedExecution && c.Compatibility.Status == "compatible" && check == "configuration "+c.Command+": "+c.Status
	})
}

func readCurrentOnboardingResult(root string, started time.Time) (validationResult, error) {
	var result validationResult
	// A run of one command after an --all run writes beside that report.
	if data, err := os.ReadFile(validationResult{KeptReport: validationPath("")}.jsonReportPath(root)); err == nil {
		if json.Unmarshal(data, &result) == nil && !result.CheckOnly && !result.CompletedAt.Before(started) {
			return result, nil
		}
		result = validationResult{}
	}
	data, err := os.ReadFile(validationPath(root))
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return validationResult{}, err
	}
	if result.CheckOnly || result.CompletedAt.Before(started) {
		return validationResult{}, fmt.Errorf("the report does not describe a full execution from this operation; historical or check-only evidence cannot establish completion")
	}
	return result, nil
}

// RenderReport is read-only. It cannot certify modified source or a new Node
// runtime, and exposes the same structured result to hosts without an LLM.
func RenderReport(ctx context.Context, output io.Writer, format string) error {
	root, err := os.Getwd()
	if err != nil {
		return renderReportFailure(output, format, err)
	}
	root = workspace.CanonicalRoot(root)
	lock, err := lockReports(root)
	if err != nil {
		result := onboard.AgentResult{Status: "blocked_environment", Phase: "report", Code: "coordination_unavailable", Reason: "A validation is active or coordination storage is unavailable; wait before rendering saved evidence.", NextAction: "wait"}
		if errors.Is(err, workspace.ErrBusy) {
			result.Code = "workflow_busy"
		} else {
			result.NextAction = "request_capability"
		}
		return errors.Join(err, onboard.RenderAgentResult(output, result, format))
	}
	defer func() { _ = lock.Close() }()
	validation, err := readPreviousValidation(root)
	if err != nil {
		return renderReportFailure(output, format, err)
	}
	if validation == nil {
		return renderReportFailure(output, format, fmt.Errorf("no saved validation report"))
	}
	if result, handled := languageOrUnsupported(validation.Framework).standaloneReport(root, validation); handled {
		return onboard.RenderAgentResult(output, result, format)
	}
	if !supportsValidationFramework(validation.Framework) || !languageOrUnsupported(validation.Framework).coordinated() {
		result := onboard.AgentResult{Status: "unsupported", Phase: "report", Code: "framework_unsupported", Reason: "Structured onboarding reports are not available for this framework. Inspect the saved standalone HTML/JSON as historical evidence, or rerun standalone testdrive.", NextAction: "report_result"}
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
	}
	if validation.CheckOnly && validation.Retained != nil && validation.Retained.Result != nil {
		validation = validation.Retained.Result
	}
	result := onboard.AgentResult{Status: "validation_failed", Phase: "report", NextAction: "report_result", JSONReport: validationPath(root)}
	// A focused run in a package below the root bound its evidence to the
	// package: check the inputs it validated.
	runRoot := reportRunRoot(root, *validation)
	current, err := workflowFingerprint(ctx, runRoot, validation.Framework)
	if err != nil {
		return renderReportFailure(output, format, err)
	}
	receiptValid := verifyWorkflowReceipt(root, *validation) == nil
	if validation.CheckOnly || validation.Workflow == nil || validation.Workflow.Fingerprint != current || !languageOrUnsupported(validation.Framework).reportRuntimeCurrent(runRoot, *validation.Workflow) || !receiptValid {
		result.Code = "stale_report"
		plain := !validation.CheckOnly && validation.Workflow == nil
		result.NextAction = "run_command"
		result.NextCommand = onboard.DDTestCommand("testdrive", "--framework", validation.Framework, "--all", "--agent", "--yes")
		if receiptValid && validation.Workflow != nil && len(validation.Workflow.ValidationCommand) > 0 {
			result.NextCommand = append([]string{}, validation.Workflow.ValidationCommand...)
		}
		if len(result.NextCommand) == 0 {
			result.NextAction = "supply_input"
			result.Code = "transient_executable"
			result.Reason = onboard.TransientExecutableReason
			return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
		}
		if format == "json" && !slices.Contains(result.NextCommand, "json") {
			result.NextCommand = append(result.NextCommand, "--format", "json")
		}
		if receiptValid && validation.Selection != nil && validation.Selection.Version != "" {
			result.NextCommand = append(result.NextCommand, "--tracer-version", validation.Selection.Version)
		}
		result.Reason = "Saved evidence does not certify the current source/runtime. Run full testdrive; do not report historical success."
		if plain {
			// Plain runs replace the report without the workflow's checks or binding.
			result.Code = "plain_report"
			result.Reason = "The saved report comes from a plain testdrive run, which the agent workflow neither produced nor certifies. Run next_command to validate through the workflow."
		}
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
	}
	result.Fingerprint = current
	result.Checks = validation.Workflow.Checks
	result.FinalResponse = validation.Summary.FinalResponse
	if validation.HTMLReportCurrent {
		result.HTMLReport = htmlReportPath(root)
	}
	result.Status = workflowVerdict(*validation)
	if result.Status == "validation_failed" {
		return errors.Join(fmt.Errorf("saved validation remains incomplete"), onboard.RenderAgentResult(output, result, format))
	}
	return onboard.RenderAgentResult(output, result, format)
}

// A stopped attempt invalidates the current handoff while keeping one historical
// execution. Never overwrite a different framework or an unreadable report.
func invalidatePriorWorkflow(root, framework string, state onboard.AgentResult) {
	previous, err := readPreviousValidation(root)
	if err != nil || previous == nil || previous.Framework != framework {
		return
	}
	result := validationResult{Session: workspace.PlanSession().ID(), Framework: framework, Error: state.Code + ": " + state.Reason, StoppedBeforeExecution: true,
		Compatibility: verdict{Status: "not exercised", Reason: "This workflow attempt stopped before a current full execution."},
		Workflow:      &WorkflowEvidence{Fingerprint: state.Fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: state.Code, Status: "failed", Reason: state.Reason}}}}
	_ = finishValidation(io.Discard, root, result) // A successfully written INCOMPLETE report also returns an error.
}

func renderReportFailure(output io.Writer, format string, err error) error {
	result := onboard.AgentResult{Status: "needs_input", Phase: "report", Code: "report_unavailable", Reason: reportText(err.Error()), NextAction: "report_result"}
	if errors.Is(err, errReportUnreadable) {
		result.Code, result.Reason = "report_unreadable", errReportUnreadable.Error()
	}
	return errors.Join(err, onboard.RenderAgentResult(output, result, format))
}
