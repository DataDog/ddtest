// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

// workflowAttempt owns one invocation's handoff, checkpoint and retry state.
// Coordinated and standalone validation share its failure policy; standalone
// validation leaves the checkpoint and report-lock ownership unset.
type workflowAttempt struct {
	result              *onboard.AgentResult
	output              io.Writer
	options             WorkflowOptions
	framework           string
	recoveryCommand     []string
	checkpoint          *workflowCheckpoint
	factsHash           string
	ownedLock, executed bool
	// Added after the checkpoint rewrites --resume, so this command stays current.
	acceptFlag    string
	finalResponse string
	// Failures can read only reports written by this execution attempt.
	started time.Time
}

func (a *workflowAttempt) fail(t *Testdrive, status, code, reason, needs string) error {
	result := a.result

	result.Status, result.Code, result.Reason, result.Needs = status, code, reportText(reason), needs
	if a.options.Format == "verbose" && result.UndoInstructions != "" {
		_, _ = io.WriteString(a.output, result.UndoInstructions)
	}
	if a.checkpoint != nil {
		if a.checkpoint.FactsSHA256 == "" || (code != "facts_invalid" && code != "facts_refresh_required") {
			a.checkpoint.Plan = result.Plan
		}
		var varying []string
		if !a.started.IsZero() {
			if report, err := readCurrentOnboardingResult(t.repositoryRoot, a.started); err == nil {
				varying = varyingTests(report)
			}
		}
		if err := a.checkpoint.recordFailure(result.Fingerprint, a.factsHash, code, result.Reason, varying...); err != nil {
			result.Reason += "; checkpoint: " + err.Error()
		}
		result.RunID = a.checkpoint.RunID
		if result.ResumeCommand == nil {
			result.ResumeCommand = append(append([]string{}, a.recoveryCommand...), "--resume", a.checkpoint.RunID)
		} else {
			for i := 0; i+1 < len(result.ResumeCommand); i++ {
				if result.ResumeCommand[i] == "--resume" {
					result.ResumeCommand[i+1] = a.checkpoint.RunID
				}
			}
		}
	}
	if a.acceptFlag != "" && len(result.ResumeCommand) > 0 && !slices.Contains(result.ResumeCommand, a.acceptFlag) {
		result.AcceptResumeCommand = append(append([]string{}, result.ResumeCommand...), a.acceptFlag)
	}
	switch status {
	case "blocked_environment":
		switch code {
		case "workflow_busy":
			result.NextAction = "wait"
		case fileDescriptorsExhaustedCode:
			// A transient host limit, not a missing capability: retry as is.
			result.NextAction = "run_command"
			result.NextCommand = append([]string{}, a.recoveryCommand...)
			if len(result.ResumeCommand) > 0 {
				result.NextCommand = append([]string{}, result.ResumeCommand...)
			}
		default:
			result.NextAction = "request_capability"
		}
	case "needs_input":
		switch code {
		case "scope_unresolved":
			result.NextAction = "run_command"
			result.NextCommand = onboard.DDTestCommand("onboard", "--framework", a.framework, "--instructions")
			result.ResumeCommand, result.RequiredInput = nil, nil
			result.Needs = scopeUnresolvedNeeds(result.Needs, a.options.Site)
		case "setup_dependencies_missing", "project_dependencies_missing":
			result.NextAction = "run_command"
		case "ci_edit_required", "report_exclusions_required":
			result.NextAction = "apply_source_edit"
		case "ci_runtime_required", "ci_setup_confirmation_required":
			// Only the user's answer, added to the same command, continues.
			result.NextAction = "ask_user"
			result.ResumeCommand = nil
		default:
			result.NextAction = "supply_input"
		}
	case "unsupported":
		if code == "ci_edit_unsupported" && len(result.NextCommand) > 0 {
			// The setup to apply comes from next_command (the manual
			// instructions); resume_command validates it.
			result.NextAction = "run_command"
		} else if code == "ci_edit_unsupported" {
			result.NextAction = "apply_source_edit"
		} else {
			result.NextAction = "report_result"
		}
	default:
		result.NextAction = "report_result"
	}
	if a.ownedLock && !a.executed && !t.checkOnly && !workflowInputFailure(code) {
		invalidatePriorWorkflow(t.repositoryRoot, a.framework, *result)
	}
	switch result.NextAction {
	case "report_result", "supply_input", "apply_source_edit", "wait", "ask_user":
		result.NextCommand = nil
	case "request_capability":
		// Without a checkpoint there is nothing to resume; rerun from the start.
		if len(result.ResumeCommand) > 0 {
			result.NextCommand = append([]string{}, result.ResumeCommand...)
		} else {
			result.NextCommand = append([]string{}, a.recoveryCommand...)
		}
	}
	if a.checkpoint != nil && a.checkpoint.repeated && !transientWorkflowFailure(code) {
		// A deterministic failure with unchanged inputs repeats on every rerun.
		result.NextAction = "report_result"
		result.NextCommand, result.ResumeCommand = nil, nil
		result.Needs += " This attempt failed exactly like the previous one with unchanged inputs, so rerunning cannot progress; stop and report this blocker."
	} else if a.checkpoint != nil && a.checkpoint.Attempts >= maxWorkflowAttempts {
		result.NextAction = "report_result"
		result.NextCommand, result.ResumeCommand = nil, nil
		result.Needs += " Three attempts with unchanged inputs made no progress; stop and report this blocker."
	}
	// A setup repair continues the workflow; a report or a user decision ends
	// the turn and must carry the text to relay.
	if a.finalResponse != "" && (result.NextAction == "report_result" || code == "substitute_runtime_unaccepted") {
		result.FinalResponse = a.finalResponse
	}
	return errors.Join(fmt.Errorf("%s: %s", code, result.Reason), onboard.RenderAgentResult(a.output, *result, a.options.Format))
}

// Rejected options, facts or resume IDs describe this invocation, not the
// repository, so they leave an earlier report and its binding untouched.
func workflowInputFailure(code string) bool {
	switch code {
	case "resume_invalid", "resume_not_found", "facts_invalid", "facts_refresh_required":
		return true
	}
	return false
}
