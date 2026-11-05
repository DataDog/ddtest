// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// complete turns the verified current report into a final handoff or a bounded
// recovery command. It never starts tests or recomputes a validation verdict.
func (a *workflowAttempt) complete(t *Testdrive, validation validationResult, workflow *WorkflowEvidence, before string) error {
	result := a.result

	result.Fingerprint = before
	result.Checks = workflow.Checks
	result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
	if validation.HTMLReportCurrent {
		result.HTMLReport = validation.htmlReportPath(t.repositoryRoot)
	}
	result.Instruction = "Validation finished for this source snapshot. Finish any source edits, then run next_command as the final check and report its factual response. Further source edits invalidate completion. ddtest has already checked its owned cleanup and report exclusions; do not repeat cache or package audits."
	result.NextAction = "run_command"
	resume := result.NextCommand
	result.NextCommand = onboard.DDTestCommand("testdrive", "--report")
	if a.options.Format == "json" {
		result.NextCommand = append(result.NextCommand, "--format", "json")
	}
	result.Phase = "complete"
	if workflowVerdict(validation) == "selected_passed" {
		if err := a.checkpoint.remove(); err != nil {
			return a.fail(t, "blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Remove only the named owned checkpoint after restoring access.")
		}
		result.Status = "selected_passed"
		if validation.KeptReport != "" {
			// testdrive --report renders the kept --all report, not this run.
			result.NextAction, result.NextCommand = "report_result", nil
			result.FinalResponse = validation.Summary.FinalResponse
			result.Instruction = "This run of one command passed. It was written beside the --all report " + validation.KeptReport + ", which it does not replace and which alone can certify onboarding. Report final_response unchanged; do not present this run as certification."
		}
		return onboard.RenderAgentResult(a.output, *result, a.options.Format)
	}
	if workflowVerdict(validation) != "complete_locally" {
		result.Instruction = ""
		result.NextCommand = nil
		// Results that end the agent's turn carry the current report's canonical
		// text, so the agent can relay it verbatim (see fail).
		a.finalResponse = validation.Summary.FinalResponse
		result.ResumeCommand = append(append([]string{}, resume...), "--resume", a.checkpoint.RunID)
		result.Phase = "validation"
		if !workflowChecksPassed(workflow) {
			result.Phase = "verification"
		}
		var reviewCommands, reviewItems []string
		for _, configuration := range validation.Configurations {
			for _, build := range configuration.Preparation {
				if build.Code == "preparation_review_required" && !slices.Contains(reviewCommands, build.Command) {
					reviewCommands = append(reviewCommands, build.Command)
					item := build.Command
					if build.Diagnostic != "" {
						item += " (review " + build.Diagnostic + ")"
					}
					reviewItems = append(reviewItems, item)
				}
			}
		}
		if len(reviewCommands) > 0 {
			result.Location = strings.Join(reviewCommands, "; ")
			result.RequiredInput = []string{"Read " + strings.Join(reviewItems, "; ") + ". The build runs in an isolated copy, never in the checkout. You may run resume_command without asking only when every named step runs repository-local code (package scripts resolving to files in the repository, or bundler configuration with well-known plugins) and nothing downloads or installs (no npx of uncached packages, curl, wget or network installs) or writes outside the repository copy; otherwise ask the user first. The final response reports the opt-in. No new setup facts are required."}
			if !slices.Contains(result.ResumeCommand, "--allow-reviewed-builds") {
				result.ResumeCommand = append(result.ResumeCommand, "--allow-reviewed-builds")
			}
			return a.fail(t, "needs_input", "preparation_review_required", "Preparation was not executed pending review: "+result.Location, "After reviewing the named commands, opt in using resume_command. Preserve the CI commands, scope and assertions; do not invent build facts.")
		}
		if entry, cause := workflowSetupFailure(validation); entry != nil {
			if entry.Cypress != nil && len(entry.Cypress.Start) > 0 && strings.Contains(validation.Error, "cypress startup port") {
				result.Location = entry.ID
				result.InputTemplate = cypressPortFactsTemplate(t.repositoryRoot, before, *entry)
				result.RequiredInput = []string{"supported application/test port override for the existing Cypress startup; keep startup, baseUrl and action wait-on consistent"}
				return a.fail(t, "needs_input", "service_setup_unresolved", strings.Join(validation.Summary.BlockingChecks, "; "), "Use input_template.configurations[0].port_override for an existing application port variable. If the app has none, add a default-preserving variable in its startup source, rerun resume_command once for the new fingerprint, then fill the template outside the checkout and append --facts to resume_command. ddtest applies the local startup/baseUrl/readiness override; do not change CI port defaults or duplicate the service.")
			}
			result.InputTemplate = setupFactsTemplate(before, *entry)
			result.RequiredInput = []string{"correct the named prerequisite or service; supply configuration_id setup facts and run resume_command"}
			reason := entry.Command + ": setup did not complete"
			if cause != "" {
				reason += ": " + cause
			}
			return a.fail(t, "needs_input", "configuration_setup_failed", reason, "Tests were not compared for this configuration. Correct only its declared setup; preserve scope and assertions.")
		}
		// Evidence that passed on a compatible substitute runtime is the user's to accept.
		var subBlockers []string
		var unaccepted []runtimeSubstitution
		for _, sub := range validation.substitutions() {
			if !sub.Accepted {
				subBlockers = append(subBlockers, sub.blocker())
				unaccepted = append(unaccepted, sub)
			}
		}
		subLocal, subRemedy := substituteChoice(unaccepted)
		if len(subBlockers) > 0 && slices.Equal(validation.Summary.BlockingChecks, subBlockers) {
			if !slices.Contains(result.ResumeCommand, AcceptSubstituteRuntimeFlag) {
				result.ResumeCommand = append(result.ResumeCommand, AcceptSubstituteRuntimeFlag)
			}
			result.RequiredInput = []string{"User decision: accept the evidence from " + subLocal + " by running resume_command, or " + subRemedy + " and rerun resume_command without " + AcceptSubstituteRuntimeFlag + "."}
			return a.fail(t, "needs_input", "substitute_runtime_unaccepted", strings.Join(subBlockers, "; "), "Every check passed on a compatible substitute runtime. Ask the user which option to take; do not accept it yourself, download a runtime, or change CI, the tracer or the test scope.")
		}
		reportOnly, actionable := splitTracerOnlyBlockers(validation)
		decision := ""
		if len(subBlockers) > 0 {
			// The substitute is still the user's decision when other blockers remain;
			// it must not be lost among them or accepted by the agent.
			actionable = slices.DeleteFunc(actionable, func(check string) bool { return slices.Contains(subBlockers, check) })
			a.acceptFlag = AcceptSubstituteRuntimeFlag
			decision = " Separately, ask the user whether to accept the evidence from substitute " + subLocal + ": once they approve, run accept_resume_command instead of resume_command; otherwise " + subRemedy + ". Do not accept it yourself or download a runtime."
		}
		if len(actionable) == 0 && len(reportOnly) > 0 {
			result.RequiredInput = reportOnly
			return a.fail(t, "validation_failed", "validation_incomplete", strings.Join(reportOnly, "; "), "Report these blockers to the user as they are: the tracer did not apply these controls for this framework setup, and no customer change is expected to fix them. Do not change the suite, CI, configuration or tracer to work around them, and do not rerun."+decision)
		}
		// Every actionable blocker is listed: a capped list hid real checks behind
		// lines that only restate them. Multi-line CI scripts are shortened to one
		// line; the report keeps them whole.
		result.RequiredInput = nil
		for _, check := range actionable {
			result.RequiredInput = append(result.RequiredInput, onboard.OneLineExcerpt(check))
		}
		needs := "Address only these current blockers, then rerun full validation. A failed feature or exclusion check remains failed."
		if slices.ContainsFunc(actionable, isScopeBlocker) {
			needs = "Address only these current blockers, then rerun full validation; unresolved CI scope blockers are not yours to address. A failed feature or exclusion check remains failed. " + onboard.ScopeBlockerInstruction
			if !slices.ContainsFunc(actionable, func(check string) bool { return !isScopeBlocker(check) }) {
				needs = "Report these blockers to the user with their options; there is nothing to rerun until the user decides. " + onboard.ScopeBlockerInstruction
			}
		}
		if len(reportOnly) > 0 {
			needs += " Report, without trying to fix, these tracer limitations: " + strings.Join(reportOnly, "; ") + "."
		}
		return a.fail(t, "validation_failed", "validation_incomplete", strings.Join(result.RequiredInput, "; "), needs+decision)
	}
	if err := a.checkpoint.remove(); err != nil {
		return a.fail(t, "blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Restore access to private checkpoint storage.")
	}
	result.Status = "complete_locally"
	return onboard.RenderAgentResult(a.output, *result, a.options.Format)
}
