// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

import "io"

// ObserveResult passes typed result metadata to the command's output observer.
// Ordinary writers are unaffected. No rendered output or full result is read.
func ObserveResult(output io.Writer, status, phase, code string) {
	if observer, ok := output.(interface{ ObserveResult(string, string, string) }); ok {
		observer.ObserveResult(status, phase, code)
	}
}

// ObserveResult retains only allowlisted result metadata. It must be called on
// the command goroutine, before Finish. Unknown strings never leave the client.
func (a *Attempt) ObserveResult(status, phase, code string) {
	if a == nil || a.finished {
		return
	}
	switch status {
	case "complete_locally", "selected_passed", "passed", "succeeded":
		a.event.Outcome = "succeeded"
	case "ready", "handoff":
		a.event.Outcome = "handoff"
	case "needs_input", "blocked_environment":
		a.event.Outcome = "blocked"
	case "unsupported", "cancelled", "failed":
		a.event.Outcome = status
	case "validation_failed":
		a.event.Outcome = "failed"
	}
	switch phase {
	case "options", "preparation", "discovery", "onboarding", "consent", "preflight",
		"configuration_check", "validation", "verification", "complete", "report":
		a.event.Phase = phase
	default:
		a.event.Phase = "unknown"
	}
	switch code {
	case "", "invalid_options", "git_unavailable", "platform_unresolved", "framework_unresolved",
		"framework_unsupported", "validation_preparation", "consent_required", "ci_discovery",
		"ci_provider_required", "ci_provider_unsupported", "ci_runtime_required", "ci_runtime_conflict",
		"ci_setup_required", "ci_setup_unsupported", "ci_setup_confirmation_required", "ci_edit_required", "ci_edit_unsupported",
		"configuration_check_failed", "configuration_setup_failed", "validation_incomplete",
		"scope_unresolved", "scope_decision_required", "setup_unresolved", "facts_invalid",
		"facts_refresh_required", "resume_invalid", "resume_not_found", "workflow_busy",
		"coordination_unavailable", "repository_unavailable", "runtime_unavailable", "local_runtime_mismatch",
		"preparation_review_required", "project_dependencies_missing", "setup_dependencies_missing",
		"substitute_runtime_unaccepted", "report_exclusions_required", "report_invalid", "report_write_failed",
		"report_unavailable", "report_unreadable", "stale_report", "dirty_workflow", "transient_executable":
		a.event.Code = code
	default:
		a.event.Code = "unknown"
	}
	if code == "invalid_options" {
		a.event.Outcome = "failed"
	}
	if code == "framework_unsupported" {
		a.event.Outcome = "unsupported"
	}
}
