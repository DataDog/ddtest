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
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
)

// standaloneWorkflow is testdrive --agent for a language whose validation is
// standalone, without the coordinator (Python): it asks the user the CI
// questions the coordinator asks, at the same point and with the same
// results, then runs the standalone validation and returns its verdict with
// the report's final_response. It keeps no checkpoint, so it takes neither
// --resume nor --facts.
func (t *Testdrive) standaloneWorkflow(ctx context.Context, attempt *workflowAttempt, fail func(status, code, reason, needs string) error) error {
	output, options := attempt.output, attempt.options
	result, recoveryCommand := attempt.result, attempt.recoveryCommand
	name := t.framework.Name()
	if options.Resume != "" || options.FactsFile != "" {
		result.NextCommand = nil
		return fail("needs_input", "invalid_options", displayName(name)+" validation is standalone: it keeps no checkpoint to resume and takes no setup facts.", "Run the same testdrive command without --resume and --facts.")
	}
	t.repositoryRoot = workspace.CanonicalRoot(t.repositoryRoot)
	t.selectedSite = options.Site
	if t.selectedSite == "" {
		t.selectedSite = os.Getenv("DD_SITE")
	}
	run := t.Run
	if t.workflowRun != nil {
		run = t.workflowRun
	}
	if t.checkOnly {
		runErr := run(ctx, io.Discard)
		result.Status, result.Phase, result.NextAction = "ready", "configuration_check", "run_command"
		result.Reason = "Configuration-only check; no paired execution or feature controls were performed."
		if runErr != nil {
			return fail("needs_input", "configuration_check_failed", runErr.Error(), "Resolve the named preflight gap before full execution.")
		}
		return onboard.RenderAgentResult(output, *result, options.Format)
	}
	// Only the user can tell these; nothing runs before they answer.
	if err := attempt.checkCIStatements(ctx, t); err != nil {
		return err
	}
	// A test command that drops what the setup sets runs untraced in CI:
	// the exact edit comes first, then this same command.
	if required := onboard.RequiredCIEdits(ctx, t.repositoryRoot, t.language, name, false); required != nil {
		edit := onboard.CIEditRequiredResult(required, recoveryCommand)
		result.Location, result.RequiredInput, result.Instruction, result.ResumeCommand = edit.Location, edit.RequiredInput, edit.Instruction, edit.ResumeCommand
		return fail("needs_input", edit.Code, edit.Reason, edit.Instruction)
	}
	if !options.ExplicitVersion && t.projectTracer == "" {
		// The handoff cannot name the release the CI setup pins; validate it.
		pin, err := attempt.ciPinnedTracer(ctx, t)
		if err != nil {
			return err
		}
		if pin != "" && pin != t.tracerVersion {
			prepared, err := Prepare(PrepareOptions{TracerVersion: pin, CheckOnly: t.checkOnly, All: t.allConfigurations, Timeout: t.commandTimeout, AllowReviewedBuilds: t.allowReviewedBuilds, AcceptSubstituteRuntime: t.acceptSubstituteRuntime})
			if err != nil {
				return fail("needs_input", "validation_preparation", err.Error(), "Resolve the named local prerequisite.")
			}
			prepared.selectedSite, prepared.workflowRun = t.selectedSite, t.workflowRun
			if t.workflowRun == nil {
				run = prepared.Run
			}
			t = prepared
		}
	}
	result.Phase = "validation"
	started := time.Now()
	runErr := run(ctx, io.Discard)
	if errors.Is(runErr, workspace.ErrBusy) {
		result.NextCommand = nil
		return fail("blocked_environment", "workflow_busy", runErr.Error(), "Wait for the running validation; do not start another one or delete lock files.")
	}
	validation, err := readCurrentOnboardingResult(t.repositoryRoot, started)
	if err != nil {
		return fail("validation_failed", "validation_report_unavailable", errors.Join(runErr, err).Error(), "No current full report exists. Resolve this execution failure; old or check-only evidence cannot establish the verdict.")
	}
	result.JSONReport = validationPath(t.repositoryRoot)
	if validation.HTMLReportCurrent {
		result.HTMLReport = htmlReportPath(t.repositoryRoot)
	}
	result.Instruction = onboard.StandaloneVerdictInstruction(displayName(name))
	if t.executor != nil {
		// Standalone validation records no workflow checks; the Git exclusion
		// its handoffs prescribe is checked here, as the coordinator checks it.
		if git := t.gitReportExclusions(ctx); git.Status != "passed" {
			result.Instruction += " " + git.Reason + " That ignore-file edit changes no test input: make it without rerunning validation, and list it in your answer."
		}
	}
	if validation.Success && (validation.Summary.Status == "PASSED" || validation.Summary.SelectedValidation != nil && validation.Summary.SelectedValidation.Status == "PASSED") {
		result.Status, result.Phase, result.Code, result.NextAction, result.NextCommand = "passed", "complete", "standalone_validation", "report_result", nil
		result.Reason = displayName(name) + " validation passed locally. It is standalone, without the coordinator's checkpoint and workflow checks, so its verdict is PASSED, not COMPLETE LOCALLY; CI execution and Datadog backend processing were not exercised."
		if validation.Summary.SelectedValidation != nil {
			result.Status = "selected_passed"
		}
		result.FinalResponse = validation.Summary.FinalResponse
		return onboard.RenderAgentResult(output, *result, options.Format)
	}
	attempt.finalResponse = validation.Summary.FinalResponse
	result.ResumeCommand = append([]string{}, recoveryCommand...)
	result.RequiredInput = nil
	reportOnly, actionable := splitTracerOnlyBlockers(validation)
	for _, check := range append(actionable, reportOnly...) {
		result.RequiredInput = append(result.RequiredInput, onboard.OneLineExcerpt(check))
	}
	reason := strings.Join(result.RequiredInput, "; ")
	if reason == "" {
		reason = "Validation verdict: " + validation.Summary.Status + "."
		if runErr != nil {
			reason = runErr.Error()
		}
	}
	return fail("validation_failed", "validation_incomplete", reason, "Report these blockers to the user. Address only these current blockers, then run resume_command again; a failed feature or exclusion check remains failed.")
}
