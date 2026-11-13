// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
)

// acceptSetupFacts binds only validated additions to this checkpoint's inputs.
func (a *workflowAttempt) acceptSetupFacts(t *Testdrive, fingerprint string, scope *onboard.ValidationScope) error {
	if a.options.FactsFile == "" {
		return nil
	}
	result := a.result
	var err error

	factsFingerprint := fingerprint
	// Built before facts touch the scope: rejected facts are rewritten from it.
	template := scopeFactsTemplate(fingerprint, *scope)
	data, readErr := readSetupFacts(t.repositoryRoot, a.options.FactsFile)
	a.factsHash = fmt.Sprintf("%x", sha256.Sum256(data))
	if readErr == nil && a.options.Resume == "" {
		a.checkpoint.restoreFactsBinding(fingerprint, a.factsHash)
	}
	if readErr == nil && a.checkpoint.Fingerprint == fingerprint && a.checkpoint.FactsSHA256 == a.factsHash && a.checkpoint.Plan != nil && a.checkpoint.Engine == engineIdentity() {
		// These exact facts already passed validation for the pre-edit inputs.
		// Reuse only their additions, bound to the unchanged owned edit result,
		// whose plan may still hold the pre-edit commands.
		err = reattachSetupFacts(scope, a.checkpoint.Plan.Scope, true)
		factsFingerprint = a.checkpoint.FactsFingerprint
	} else {
		if readErr != nil {
			err = readErr
		} else {
			_, err = applySetupFactsData(t.repositoryRoot, data, fingerprint, scope)
		}
	}

	if err != nil && a.checkpoint.FactsDependencyHandoff && readErr == nil && a.checkpoint.FactsSHA256 == a.factsHash && a.checkpoint.Engine == engineIdentity() {
		var refresh onboard.SetupFacts
		if json.Unmarshal(data, &refresh) == nil {
			refresh.Fingerprint = fingerprint
			result.InputTemplate = &refresh
			result.Location = a.options.FactsFile
			result.RequiredInput = []string{"Review the unchanged setup entries against the post-install checkout, then replace the facts file with input_template and run resume_command."}
			return a.fail(t, "needs_input", "facts_refresh_required", "The declared dependency-install handoff changed repository inputs; the old facts cannot certify the new snapshot.", "Explicitly refresh facts for the returned current fingerprint; do not retry unchanged facts or bypass their binding.")
		}
	}
	if err != nil {
		result.Location = a.options.FactsFile
		result.InputTemplate = template
		result.RequiredInput = []string{"schema_version=2, current input_fingerprint, configurations with configuration_id and build/service setup"}
		return a.fail(t, "needs_input", "facts_invalid", err.Error(), "Rewrite the facts file outside the checkout from input_template for the current input_fingerprint, supplying only the missing prerequisite facts, then run resume_command. No discovery or validation assertion can be overridden.")
	}
	if a.checkpoint.FactsSHA256 != a.factsHash || a.checkpoint.Fingerprint != fingerprint || a.checkpoint.Engine != engineIdentity() {
		a.checkpoint.Tracer = "" // Replacement setup invalidates cached preparation.
	}
	a.checkpoint.FactsSHA256 = a.factsHash
	a.checkpoint.Fingerprint = fingerprint
	a.checkpoint.Engine = engineIdentity()
	a.checkpoint.FactsFingerprint = factsFingerprint

	return nil
}

func (a *workflowAttempt) failTracerSelection(t *Testdrive, problem error) error {
	result := a.result

	var metadata *platform.MetadataError
	if errors.As(problem, &metadata) {
		result.Capabilities = []string{workflowMetadataCapability(metadata)}
		return a.fail(t, "blocked_environment", metadata.Code, problem.Error(), "Grant/fix only the named capability and rerun next_command. No tracer version was guessed and no source edits were made.")
	}
	var transport *url.Error
	if errors.As(problem, &transport) {
		address, _ := url.Parse(transport.URL)
		capability := "CI metadata network access"
		if address != nil {
			capability = "network access to " + address.Hostname()
		}
		result.Capabilities = []string{capability}
		return a.fail(t, "blocked_environment", "metadata_network", problem.Error(), "Restore only the named metadata network capability, then run resume_command. Do not change runtime, tracer or test scope to work around a network failure.")
	}
	var majors *ciTracerMajors
	if errors.As(problem, &majors) {
		// Resume with the release ddtest recommends rather than the default.
		if majors.Recommended != "" && !a.options.ExplicitVersion && a.checkpoint != nil {
			result.ResumeCommand = append(append([]string{}, a.recoveryCommand...), "--tracer-version", majors.Recommended, "--resume", a.checkpoint.RunID)
		}
		if len(majors.Exclusions) > 0 {
			result.RequiredInput = []string{"the user's choice: one --tracer-version every instrumented CI row supports, or approval to opt these rows out: " + strings.Join(majors.Exclusions, "; ")}
			return a.fail(t, "needs_input", "preflight_unresolved", problem.Error(), "Ask the user before excluding any CI row. If they approve, make exactly the listed DD_CIVISIBILITY_ENABLED edits and run resume_command; otherwise rerun with one --tracer-version every instrumented row supports. Never weaken feature checks.")
		}
		result.RequiredInput = []string{"one --tracer-version every instrumented CI row supports"}
		needs := "No opt-out edit applies to these CI rows; do not exclude or rewrite them. Report them as unvalidated blockers unless one --tracer-version supports every instrumented row."
		if majors.Recommended != "" {
			needs = "Run resume_command, which selects dd-trace@" + majors.Recommended + ". Rows it cannot run stay unvalidated blockers to report; do not exclude or rewrite CI rows."
		}
		return a.fail(t, "needs_input", "preflight_unresolved", problem.Error(), needs)
	}
	result.RequiredInput = []string{"the named runtime/configuration incompatibility"}
	return a.fail(t, "needs_input", "preflight_unresolved", problem.Error(), "Resolve this exact mismatch without weakening feature checks. Do not exclude or rewrite CI rows to get past it; if it can't be resolved, report it as a blocker.")
}

func (a *workflowAttempt) failSetupEdits(t *Testdrive, err error, selected, fingerprint string) error {
	result := a.result

	var dependencies *onboard.SetupDependencyGap
	if errors.As(err, &dependencies) {
		a.checkpoint.FactsDependencyHandoff = true
		a.checkpoint.DependencyPackages = dependencies.Packages
		a.checkpoint.Engine = engineIdentity()
		a.checkpoint.FactsSHA256 = a.factsHash
		a.checkpoint.Fingerprint = fingerprint
		result.Location = "package.json / devDependencies"
		result.NextCommand = dependencies.Command
		result.ResumeCommand = append(append([]string{}, a.recoveryCommand...), "--resume", a.checkpoint.RunID)
		result.RequiredInput = []string{"Run next_command once, as given and without asking, to update the manifest and lockfile, then resume_command. Preserve the selected framework and CI test commands."}
		result.Instruction = onboard.PrescribedEditInstruction
		return a.fail(t, "needs_input", "setup_dependencies_missing", err.Error(), "The recognized coverage recipe needs only these dependencies; installing them is a ddtest-prescribed edit, listed with its revert in the final report. No CI edits have been made.")
	}
	var dirty *onboard.DirtyWorkflowGap
	if errors.As(err, &dirty) {
		result.Location = strings.Join(dirty.Files, ", ")
		result.RequiredInput = []string{"Review setup_diff against the existing target-file edits. With approval to preserve and extend them, run resume_command with --allow-dirty; do not switch to testdrive or change scope."}
		result.ResumeCommand = append(append([]string{}, a.recoveryCommand...), "--allow-dirty", "--resume", a.checkpoint.RunID)
		return a.fail(t, "needs_input", "dirty_workflow", err.Error(), "Commit or stash the listed target files, then rerun; add --allow-dirty only when the user approves extending those uncommitted edits.")
	}
	var runtimeGap *onboard.NodeRuntimeGap
	if errors.As(err, &runtimeGap) {
		result.Location = runtimeGap.Location
		result.RequiredInput = []string{"A CI Node version that satisfies " + runtimeGap.Requirement + ", or an older tracer selected with --tracer-version"}
		return a.fail(t, "needs_input", "ci_runtime_incompatible", err.Error(), "Upgrade the CI Node version, or select an older tracer with --tracer-version, then rerun. No CI hook needs to be written by hand.")
	}
	var editGap *onboard.EditThenApplyGap
	if errors.As(err, &editGap) {
		// One source edit, then onboard --apply writes the setup itself:
		// validating before that would check CI without the setup.
		result.Location = editGap.Location
		result.RequiredInput = []string{"Only the source edit reason names, at " + editGap.Location + " (and a dependency it names, through its install command); then run resume_command, which adds the Datadog CI setup and validates."}
		result.ResumeCommand = append([]string{}, a.recoveryCommand...)
		if !slices.Contains(result.ResumeCommand, "--allow-dirty") {
			// It extends the uncommitted edit just made.
			result.ResumeCommand = append(result.ResumeCommand, "--allow-dirty")
		}
		result.NextCommand = nil
		return a.fail(t, "needs_input", "ci_edit_required", err.Error(), "Make only the edit reason names, then run resume_command: onboard --apply adds the Datadog CI setup and validates. Do not run testdrive first."+manualSiteSentence(a.options.Site))
	}
	var gap *onboard.SetupGap
	if errors.As(err, &gap) {
		result.Location = gap.Location
		result.RequiredInput = []string{"CI tracer initialization at " + gap.Location}
		result.ResumeCommand = t.workflowValidationResume(a.recoveryCommand[0], a.framework, selected, a.options)
		a.checkpoint.Apply = false
		a.checkpoint.All = true
		result.ResumeCommand = append(result.ResumeCommand, "--resume", a.checkpoint.RunID)
		result.NextCommand = nil
		return a.fail(t, "unsupported", "ci_edit_unsupported", err.Error(), "Integrate only this CI hook, then run resume_command. Do not repeatedly retry the automatic editor or drop configurations."+manualSiteSentence(a.options.Site))
	}
	return a.fail(t, "blocked_environment", "ci_edit_unavailable", err.Error(), "Restore filesystem access. Completed source edits are listed in changes; do not overwrite customer edits.")
}
