// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

import "io"

// ObserveExit classifies the top-level result before diagnostics are sent.
// It preserves typed blocked/unsupported/cancelled results.
// Finish calls it too, so standalone Attempt users keep the same semantics.
func (a *Attempt) ObserveExit(exitCode int, cancelled bool) {
	if a == nil || a.finished {
		return
	}
	if cancelled {
		a.event.Outcome = "cancelled"
	} else if a.event.Outcome == "" || (exitCode != 0 && (a.event.Outcome == "succeeded" || a.event.Outcome == "handoff")) {
		a.event.Outcome = "failed"
		if exitCode == 0 {
			a.event.Outcome = "succeeded"
			if a.event.Mode == "discovery" || a.event.Mode == "apply" {
				a.event.Outcome = "handoff"
			}
		}
	}
	if exitCode == 0 && a.event.Mode == "configuration_check" && a.event.Outcome == "handoff" {
		a.event.Outcome = "succeeded"
	}
}

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
	case "options", "preparation", "discovery", "onboarding", "apply", "consent", "preflight",
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
		"report_unavailable", "report_unreadable", "stale_report", "dirty_workflow", "transient_executable",
		"checkpoint_cleanup_failed", "checkpoint_unavailable", "ci_edit_unavailable", "ci_runtime_incompatible",
		"dependency_readiness_unavailable", "execution_result_conflict", "input_snapshot_unavailable",
		"metadata_network", "multiple_ci_tracers", "plain_report", "preflight_unresolved",
		"probe_recovery_failed", "process_recovery_failed", "scope_after_edit", "service_setup_unresolved",
		"session_recovery_failed", "validation_report_unavailable", "standalone_validation",
		"file_descriptors_exhausted", "process_unavailable", "ownership_connection_failed", "unsupported_detached_spawn",
		"registry_metadata_failed", "ENOTFOUND", "EAI_AGAIN", "ENETUNREACH", "ECONNREFUSED", "ECONNRESET",
		"ETIMEDOUT", "FETCH_ERROR", "EACCES", "EPERM", "EROFS", "ENOSPC":
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
	if a.troubleshootingError == nil && (a.event.Outcome == "failed" || a.event.Outcome == "blocked" || a.event.Outcome == "unsupported") {
		a.troubleshootingError = captureTroubleshootingError("result", nil)
	}
}
