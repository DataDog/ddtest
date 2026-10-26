// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

type WorkflowOptions struct {
	Resume          string
	Apply           bool
	AllowDirty      bool
	ExplicitVersion bool
	FactsFile       string
	Site            string
	Format          string // agent: compact state + rendered facts; json: protocol only; verbose: runner logs too
}

func (t *Testdrive) Workflow(ctx context.Context, output io.Writer, options WorkflowOptions) error {
	name := t.framework.Name()
	if !supportsValidationFramework(name) && options.Format == "verbose" && !options.Apply {
		return t.Run(ctx, output)
	}
	result := onboard.AgentResult{Status: "running", Phase: "discovery", NextAction: "run_command", NextCommand: onboard.OnboardingCommand(name)}
	if len(result.NextCommand) == 0 {
		result.Status, result.Code, result.Reason, result.NextAction = "needs_input", "transient_executable", onboard.TransientExecutableReason, "supply_input"
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, options.Format))
	}
	if !options.Apply {
		result.NextCommand = []string{result.NextCommand[0], "testdrive", "--framework", name, "--agent", "--yes"}
		if t.allConfigurations {
			result.NextCommand = append(result.NextCommand, "--all")
		}
		if override := strings.TrimSpace(settings.GetCommand()); !t.allConfigurations && override != "" {
			result.NextCommand = append(result.NextCommand, "--command", override)
		}
	}
	if options.Format == "json" {
		result.NextCommand = append(result.NextCommand, "--format", "json")
	} else if options.Apply && options.Format == "agent" {
		result.NextCommand = append(result.NextCommand, "--agent")
	}

	if t.commandTimeout > 0 {
		result.NextCommand = append(result.NextCommand, "--timeout", t.commandTimeout.String())
	}
	if t.allowReviewedBuilds {
		result.NextCommand = append(result.NextCommand, "--allow-reviewed-builds")
	}
	if options.ExplicitVersion {
		result.NextCommand = append(result.NextCommand, "--tracer-version", t.tracerVersion)
	}
	if options.FactsFile != "" {
		result.NextCommand = append(result.NextCommand, "--facts", options.FactsFile)
	}
	if selectedPlatform := settings.GetPlatform(); selectedPlatform != "" {
		result.NextCommand = append(result.NextCommand, "--platform", selectedPlatform)
	}
	if options.Apply && settings.GetCommand() != "" {
		result.NextCommand = append(result.NextCommand, "--command", settings.GetCommand())
	}
	if options.AllowDirty {
		result.NextCommand = append(result.NextCommand, "--allow-dirty")
	}
	if options.Site != "" {
		result.NextCommand = append(result.NextCommand, "--site", options.Site)
	}
	recoveryCommand := append([]string{}, result.NextCommand...)
	var checkpoint *workflowCheckpoint
	var factsHash string
	ownedLock, executed := false, false
	fail := func(status, code, reason, needs string) error {
		result.Status, result.Code, result.Reason, result.Needs = status, code, reportText(reason), needs
		if options.Format == "verbose" && result.UndoInstructions != "" {
			_, _ = io.WriteString(output, result.UndoInstructions)
		}
		if checkpoint != nil {
			if checkpoint.FactsSHA256 == "" || (code != "facts_invalid" && code != "facts_refresh_required") {
				checkpoint.Plan = result.Plan
			}
			if err := checkpoint.recordFailure(result.Fingerprint, factsHash, code); err != nil {
				result.Reason += "; checkpoint: " + err.Error()
			}
			result.RunID = checkpoint.RunID
			if result.ResumeCommand == nil {
				result.ResumeCommand = append(append([]string{}, recoveryCommand...), "--resume", checkpoint.RunID)
			} else {
				for i := 0; i+1 < len(result.ResumeCommand); i++ {
					if result.ResumeCommand[i] == "--resume" {
						result.ResumeCommand[i+1] = checkpoint.RunID
					}
				}
			}
		}
		switch status {
		case "blocked_environment":
			if code == "workflow_busy" {
				result.NextAction = "wait"
			} else {
				result.NextAction = "request_capability"
			}
		case "needs_input":
			switch code {
			case "scope_unresolved":
				result.NextAction = "run_command"
				result.NextCommand = []string{recoveryCommand[0], "onboard", "--framework", name, "--instructions"}
				result.ResumeCommand, result.RequiredInput = nil, nil
				result.Needs = "Run next_command for the unsupported CI integration, then validate the discovered commands with testdrive. Keep unresolved CI explicitly unvalidated. Setup facts cannot resolve CI parsing or shrink scope; do not guess a facts schema, inspect the binary, or retry unchanged --apply."
			case "setup_dependencies_missing":
				result.NextAction = "run_command"
			default:
				result.NextAction = "supply_input"
			}
		case "unsupported":
			if code == "ci_edit_unsupported" {
				result.NextAction = "apply_source_edit"
			} else {
				result.NextAction = "report_result"
			}
		default:
			result.NextAction = "report_result"
		}
		if ownedLock && !executed && !t.checkOnly && !workflowInputFailure(code) {
			invalidatePriorWorkflow(t.repositoryRoot, name, result)
		}
		switch result.NextAction {
		case "report_result", "supply_input", "apply_source_edit", "wait":
			result.NextCommand = nil
		case "request_capability":
			// Without a checkpoint there is nothing to resume; rerun from the start.
			if len(result.ResumeCommand) > 0 {
				result.NextCommand = append([]string{}, result.ResumeCommand...)
			} else {
				result.NextCommand = append([]string{}, recoveryCommand...)
			}
		}
		if checkpoint != nil && checkpoint.Attempts >= maxWorkflowAttempts {
			result.NextAction = "report_result"
			result.NextCommand, result.ResumeCommand = nil, nil
			result.Needs += " Three identical attempts made no progress; stop and report this blocker."
		}
		return errors.Join(fmt.Errorf("%s: %s", code, result.Reason), onboard.RenderAgentResult(output, result, options.Format))
	}
	if !supportsValidationFramework(name) || t.language != "javascript" {
		result.NextCommand = nil
		return fail("unsupported", "framework_unsupported", "Controlled validation is not available for this framework.", "Preserve its unsupported status; do not substitute a different framework.")
	}
	t.repositoryRoot = canonicalWorkflowRoot(t.repositoryRoot)
	lock, err := acquireWorkflowLock(t.repositoryRoot)
	if err != nil {
		if errors.Is(err, errWorkflowBusy) {
			return fail("blocked_environment", "workflow_busy", err.Error(), "Wait for the running validation; do not start another one or delete lock files.")
		}
		result.Capabilities = []string{"writable private temporary coordination storage"}
		return fail("blocked_environment", "coordination_unavailable", err.Error(), "Restore access/free space in the named temporary storage, then rerun next_command.")
	}
	ownedLock = true
	ctx = context.WithValue(ctx, workflowLockKey{}, t.repositoryRoot)
	defer func() { _ = lock.Close() }()
	if err := cleanupExpiredCheckpoints(); err != nil {
		return fail("blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Restore private checkpoint storage.")
	}
	if err := recoverSessions(t.repositoryRoot, io.Discard); err != nil {
		return fail("blocked_environment", "session_recovery_failed", err.Error(), "Review the named owned storage; customer files are preserved.")
	}
	if err := recoverProcessOwners(t.repositoryRoot, io.Discard); err != nil {
		return fail("blocked_environment", "process_recovery_failed", err.Error(), "Restore access to the named owned process.")
	}
	if err := recoverProbes(t.repositoryRoot); err != nil {
		return fail("needs_input", "probe_recovery_failed", err.Error(), "Review only the named owned probe; modified files are preserved. Then retry the same command.")
	}
	checkpoint, err = openCheckpoint(options.Resume, t.repositoryRoot, name, settings.GetCommand(), options.Apply, t.allConfigurations)
	if err != nil {
		if errors.Is(err, errCheckpointNotFound) {
			return fail("needs_input", "resume_not_found", err.Error(), "Start a fresh workflow without --resume; this run already finished or its checkpoint expired.")
		}
		return fail("needs_input", "resume_invalid", err.Error(), "Use the supplied resume command with its original repository/framework/scope, or start a fresh workflow.")
	}
	result.RunID = checkpoint.RunID
	progress := func(phase string) {
		result.Phase = phase
		if options.Format == "verbose" {
			_, _ = fmt.Fprintf(output, "==> %s\n", phase)
		}
	}
	if t.checkOnly && options.FactsFile != "" {
		return fail("needs_input", "invalid_options", "--facts cannot be combined with --check-only.", "Omit --check-only to validate the supplied facts, or omit --facts for configuration-only checks.")
	}
	if t.checkOnly {
		if options.Format == "verbose" {
			return t.Run(ctx, output)
		}
		runErr := t.Run(ctx, io.Discard)
		result.Status = "ready"
		result.Phase = "configuration_check"
		result.Reason = "Configuration-only check; no paired execution or feature controls were performed."
		result.NextAction = "run_command"
		if runErr != nil {
			return fail("needs_input", "configuration_check_failed", runErr.Error(), "Resolve the named preflight gap before full execution.")
		}
		return onboard.RenderAgentResult(output, result, options.Format)
	}
	progress("discovery")
	fingerprint, err := InputFingerprint(ctx, t.repositoryRoot)
	if err != nil {
		return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore access to the named source input; no tests or edits were attempted.")
	}
	result.Fingerprint = fingerprint
	scope, err := onboard.DiscoverWorkflowScope(t.repositoryRoot, name)
	if err != nil {
		return fail("needs_input", "scope_unresolved", err.Error(), "Correct only the named CI/package parsing gap; preserve every selected framework configuration.")
	}
	onboard.AssignConfigurationIDs(&scope)
	runtimeVersion := ""
	if t.nodeVersion != nil {
		runtimeVersion = t.workflowNodeVersion()
	}
	plan := &onboard.ExecutionPlan{SchemaVersion: 1, Fingerprint: fingerprint, Framework: name, Site: options.Site, Runtime: runtimeVersion, Initialization: workflowInitialization(name), Scope: scope}
	for _, policy := range featurePolicies(name) {
		switch policy.Status {
		case "required":
			plan.Features = append(plan.Features, policy.Name)
		case "deferred":
			if plan.DeferredFeatures == nil {
				plan.DeferredFeatures = map[string]string{}
			}
			plan.DeferredFeatures[policy.Name] = policy.Reason
		case "not supported":
			if plan.UnsupportedFeatures == nil {
				plan.UnsupportedFeatures = map[string]string{}
			}
			plan.UnsupportedFeatures[policy.Name] = policy.Reason
		}
	}

	if plan.Site == "" {
		plan.Site = "datadoghq.com"
	}
	plan.PackageManager = workflowPackageManager(t.repositoryRoot)
	result.Plan = plan
	if options.FactsFile != "" {
		factsFingerprint := fingerprint
		// Built before facts touch the scope: rejected facts are rewritten from it.
		template := scopeFactsTemplate(fingerprint, scope)
		data, readErr := readSetupFacts(t.repositoryRoot, options.FactsFile)
		factsHash = fmt.Sprintf("%x", sha256.Sum256(data))
		if readErr == nil && options.Resume == "" {
			checkpoint.restoreFactsBinding(fingerprint, factsHash)
		}
		if readErr == nil && checkpoint.Fingerprint == fingerprint && checkpoint.FactsSHA256 == factsHash && checkpoint.Plan != nil && checkpoint.Engine == engineIdentity() {
			// These exact facts already passed validation for the pre-edit inputs.
			// Reuse only their additions, bound to the unchanged owned edit result,
			// whose plan may still hold the pre-edit commands.
			err = reattachSetupFacts(&scope, checkpoint.Plan.Scope, true)
			factsFingerprint = checkpoint.FactsFingerprint
		} else {
			if readErr != nil {
				err = readErr
			} else {
				_, err = applySetupFactsData(t.repositoryRoot, data, fingerprint, &scope)
			}
		}

		if err != nil && checkpoint.FactsDependencyHandoff && readErr == nil && checkpoint.FactsSHA256 == factsHash && checkpoint.Engine == engineIdentity() {
			var refresh onboard.SetupFacts
			if json.Unmarshal(data, &refresh) == nil {
				refresh.Fingerprint = fingerprint
				result.InputTemplate = &refresh
				result.Location = options.FactsFile
				result.RequiredInput = []string{"Review the unchanged setup entries against the post-install checkout, then replace the facts file with input_template and run resume_command."}
				return fail("needs_input", "facts_refresh_required", "The declared dependency-install handoff changed repository inputs; the old facts cannot certify the new snapshot.", "Explicitly refresh facts for the returned current fingerprint; do not retry unchanged facts or bypass their binding.")
			}
		}
		if err != nil {
			result.Location = options.FactsFile
			result.InputTemplate = template
			result.RequiredInput = []string{"schema_version=2, current input_fingerprint, configurations with configuration_id and build/service setup"}
			return fail("needs_input", "facts_invalid", err.Error(), "Rewrite the facts file outside the checkout from input_template for the current input_fingerprint, supplying only the missing prerequisite facts, then run resume_command. No discovery or validation assertion can be overridden.")
		}
		if checkpoint.FactsSHA256 != factsHash || checkpoint.Fingerprint != fingerprint || checkpoint.Engine != engineIdentity() {
			checkpoint.Tracer = "" // Replacement setup invalidates cached preparation.
		}
		checkpoint.FactsSHA256 = factsHash
		checkpoint.Fingerprint = fingerprint
		checkpoint.Engine = engineIdentity()
		checkpoint.FactsFingerprint = factsFingerprint
	}
	plan.Scope = scope
	if options.Apply || t.allConfigurations {
		if len(scope.Unresolved) > 0 || len(scope.Commands) == 0 {
			result.Location = strings.Join(scope.Unresolved[:min(1, len(scope.Unresolved))], "")
			result.RequiredInput = []string{"literal CI test entry point and any unresolved setup relationship"}
			reason := fmt.Sprintf("Discovered %d commands. %s", len(scope.Commands), strings.Join(scope.Unresolved, "; "))
			if len(scope.Commands) == 0 && len(scope.Unresolved) == 0 {
				reason = "No " + displayName(name) + " test command was found in GitHub Actions workflows or package scripts."
			}
			return fail("needs_input", "scope_unresolved", reason, "Resolve the named CI entry point. Facts can add prerequisites to discovered commands; they cannot erase unresolved CI or shrink scope. Use --instructions only for this unsupported integration.")
		}
		for _, entry := range scope.Commands {
			if len(entry.UnvalidatedSetup) > 0 || missingPrerequisiteFacts(entry) {
				result.Location = strings.Join(entry.Locations, ", ")
				result.RequiredInput = []string{"repository-local working directory and supported setup for this exact CI command"}
				result.InputTemplate = setupFactsTemplate(fingerprint, entry)
				return fail("needs_input", "setup_unresolved", entry.Command+": "+strings.Join(pendingSetup(entry), "; ")+"; declare the expected outputs of each pending prerequisite", "Save input_template as a JSON file outside the checkout, fill expected_files for each prerequisite, then run resume_command with --facts <path>. Preserve this configuration; do not replace it with an easier suite.")
			}
		}
	}
	ctx = onboard.WithWorkflowSetupScope(ctx, t.repositoryRoot, scope)
	t.workflowPlan = plan
	t.workflowRoot = t.repositoryRoot
	if t.allConfigurations {
		t.workflowScope = &scope
	}
	if !options.Apply && !options.ExplicitVersion && t.projectTracer == "" {
		ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, name)
		pin := ""
		for _, job := range ci.Jobs {
			if job.Tracer != "" {
				candidate := strings.TrimPrefix(job.Tracer, "dd-trace@")
				if pin != "" && pin != candidate {
					result.Location = job.Workflow + " / " + job.Job
					result.RequiredInput = []string{"one intended CI tracer release, or separate explicitly scoped runs"}
					return fail("needs_input", "multiple_ci_tracers", "CI selects different tracer releases.", "Preserve every configuration and align the intended tracer selection explicitly.")
				}
				pin = candidate
			}
		}
		if pin != "" {
			prepared, err := Prepare(PrepareOptions{TracerVersion: pin, All: t.allConfigurations, Timeout: t.commandTimeout, AllowReviewedBuilds: t.allowReviewedBuilds})
			if err != nil {
				return fail("needs_input", "validation_preparation", err.Error(), "Resolve the named local prerequisite.")
			}
			prepared.workflowScope = t.workflowScope
			prepared.workflowPlan = t.workflowPlan
			prepared.workflowRoot = t.workflowRoot
			prepared.workflowRun = t.workflowRun
			t = prepared
		}
	}
	if options.Apply {

		progress("preflight")
		selected := checkpoint.Tracer
		var problem error
		reused := checkpoint.reusable(fingerprint, t.workflowNodeVersion(), factsHash, t.tracerVersion, options.ExplicitVersion)
		if !reused {
			selectTracer := t.selectWorkflowTracer
			if t.workflowSelect != nil {
				selectTracer = t.workflowSelect
			}
			selected, problem = selectTracer(ctx, scope, options.ExplicitVersion)
		} else if checkpoint.Plan != nil {
			plan.CIRuntime = checkpoint.Plan.CIRuntime
			plan.TracerNode = checkpoint.Plan.TracerNode
		}
		if problem != nil {
			var metadata *platform.MetadataError
			if errors.As(problem, &metadata) {
				result.Capabilities = []string{workflowMetadataCapability(metadata)}
				return fail("blocked_environment", metadata.Code, problem.Error(), "Grant/fix only the named capability and rerun next_command. No tracer version was guessed and no source edits were made.")
			}
			var transport *url.Error
			if errors.As(problem, &transport) {
				address, _ := url.Parse(transport.URL)
				capability := "CI metadata network access"
				if address != nil {
					capability = "network access to " + address.Hostname()
				}
				result.Capabilities = []string{capability}
				return fail("blocked_environment", "metadata_network", problem.Error(), "Restore only the named metadata network capability, then run resume_command. Do not change runtime, tracer or test scope to work around a network failure.")
			}
			result.RequiredInput = []string{"the named runtime/configuration incompatibility"}
			return fail("needs_input", "preflight_unresolved", problem.Error(), "Resolve this exact mismatch without excluding CI entries or weakening feature checks.")
		}
		result.Checks = append(result.Checks, onboard.WorkflowCheck{Name: "preflight_preparation", Status: "passed", Reason: fmt.Sprintf("Verified preflight reused: %t. Test execution is always fresh.", reused)})
		plan.Tracer = selected
		if !onboard.SupportsAutomaticSetup(name) {
			result.Location = "CI initialization"
			result.RequiredInput = []string{"framework-specific CI loader/hook integration using the selected tracer release " + selected}
			result.ResumeCommand = t.workflowValidationResume(recoveryCommand[0], name, selected, options)
			checkpoint.Apply = false
			checkpoint.All = true
			result.ResumeCommand = append(result.ResumeCommand, "--resume", checkpoint.RunID)
			result.NextCommand = nil
			return fail("unsupported", "ci_edit_unsupported", "The selected tracer and runtime are resolved; this framework still needs its custom CI hooks.", "Apply only plan_summary.initialization using plan_summary.tracer and plan_summary.site at the named CI hook; run resume_command. Inspect details_command only if the missing source integration needs more context.")
		}
		progress("apply")
		var sourcePlan *onboard.SetupPlan
		sourcePlan, err = onboard.PlanJavaScriptSetup(t.repositoryRoot, name, selected, options.Site, plan.TracerNode)
		if err == nil {
			result.SetupDiff, err = sourcePlan.Diff()
			if err == nil && options.Format == "verbose" {
				_, err = io.WriteString(output, result.SetupDiff)
			}
			if err == nil {
				result.Changes, err = sourcePlan.Apply(options.AllowDirty)
			}
			if len(result.Changes) > 0 {
				names := make([]string, 0, len(result.Changes))
				for _, change := range result.Changes {
					names = append(names, change.Path)
				}
				result.UndoInstructions = "Source edits remain applied: " + strings.Join(names, ", ") + ". To undo only these edits from the repository root, preserving earlier changes:\ngit apply -R --whitespace=nowarn <<'DDTEST_SETUP_REVERT'\n" + result.SetupDiff + "DDTEST_SETUP_REVERT\n"
			}
		}
		if err != nil {
			var dependencies *onboard.SetupDependencyGap
			if errors.As(err, &dependencies) {
				checkpoint.FactsDependencyHandoff = true
				checkpoint.Engine = engineIdentity()
				checkpoint.FactsSHA256 = factsHash
				checkpoint.Fingerprint = fingerprint
				result.Location = "package.json / devDependencies"
				result.NextCommand = dependencies.Command
				result.ResumeCommand = append(append([]string{}, recoveryCommand...), "--resume", checkpoint.RunID)
				result.RequiredInput = []string{"Run next_command once to update the manifest and lockfile, then resume_command. Preserve the selected framework and CI test commands."}
				return fail("needs_input", "setup_dependencies_missing", err.Error(), "The recognized coverage recipe needs only these dependencies; no CI edits have been made.")
			}
			var dirty *onboard.DirtyWorkflowGap
			if errors.As(err, &dirty) {
				result.Location = strings.Join(dirty.Files, ", ")
				result.RequiredInput = []string{"Review setup_diff against the existing target-file edits. With approval to preserve and extend them, run resume_command with --allow-dirty; do not switch to testdrive or change scope."}
				result.ResumeCommand = append(append([]string{}, recoveryCommand...), "--allow-dirty", "--resume", checkpoint.RunID)
				return fail("needs_input", "dirty_workflow", err.Error(), "Commit or stash the listed target files, then rerun; add --allow-dirty only when the user approves extending those uncommitted edits.")
			}
			var runtimeGap *onboard.NodeRuntimeGap
			if errors.As(err, &runtimeGap) {
				result.Location = runtimeGap.Location
				result.RequiredInput = []string{"A CI Node version that satisfies " + runtimeGap.Requirement + ", or an older tracer selected with --tracer-version"}
				return fail("needs_input", "ci_runtime_incompatible", err.Error(), "Upgrade the CI Node version, or select an older tracer with --tracer-version, then rerun. No CI hook needs to be written by hand.")
			}
			var gap *onboard.SetupGap
			if errors.As(err, &gap) {
				result.Location = gap.Location
				result.RequiredInput = []string{"CI tracer initialization at " + gap.Location}
				result.ResumeCommand = t.workflowValidationResume(recoveryCommand[0], name, selected, options)
				checkpoint.Apply = false
				checkpoint.All = true
				result.ResumeCommand = append(result.ResumeCommand, "--resume", checkpoint.RunID)
				result.NextCommand = nil
				return fail("unsupported", "ci_edit_unsupported", err.Error(), "Integrate only this CI hook, then run resume_command. Do not repeatedly retry the automatic editor or drop configurations.")
			}
			return fail("blocked_environment", "ci_edit_unavailable", err.Error(), "Restore filesystem access. Completed source edits are listed in changes; do not overwrite customer edits.")
		}
		// Bind the accepted plan and facts to the snapshot this owned edit produced
		// at once, so a later failure resumes with them instead of a dead end.
		applied, err := InputFingerprint(ctx, t.repositoryRoot)
		if err != nil {
			return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore source input access. Completed source edits are listed in changes.")
		}
		result.Fingerprint = applied
		checkpoint.Engine, checkpoint.Fingerprint, checkpoint.FactsSHA256, checkpoint.Plan = engineIdentity(), applied, factsHash, plan
		checkpoint.Tracer, checkpoint.Node = selected, t.workflowNodeVersion()
		if err := errors.Join(checkpoint.save(), checkpoint.saveFactsBinding()); err != nil {
			return fail("blocked_environment", "checkpoint_unavailable", err.Error(), "Restore private temporary storage. Completed source edits are listed in changes.")
		}
		// Refresh discovery after YAML insertion. Facts remain the exact additions
		// already validated against the pre-edit snapshot; reattach them by command.
		updated, err := onboard.DiscoverWorkflowScope(t.repositoryRoot, name)
		if err != nil {
			return fail("needs_input", "scope_after_edit", err.Error(), "Review the named source edit before running validation.")
		}
		onboard.AssignConfigurationIDs(&updated)
		if err := reattachSetupFacts(&updated, scope, true); err != nil {
			result.Location = options.FactsFile
			result.InputTemplate = scopeFactsTemplate(applied, updated)
			return fail("needs_input", "facts_invalid", err.Error(), "Accepted setup could not be attached after the owned edit; rewrite the facts file from input_template for the current input_fingerprint, preserving each configuration, then run resume_command.")
		}
		onboard.AssignConfigurationIDs(&updated)
		scope = updated
		ctx = onboard.WithWorkflowSetupScope(ctx, t.repositoryRoot, scope)
		plan.Scope, plan.Tracer = scope, selected
		t.workflowScope = &scope
		t.tracerVersion = selected
		// Prepare once more to bind installation commands to the exact release.
		prepared, err := Prepare(PrepareOptions{TracerVersion: selected, All: true, Timeout: t.commandTimeout, AllowReviewedBuilds: t.allowReviewedBuilds})
		if err != nil {
			return fail("needs_input", "validation_preparation", err.Error(), "Resolve the named installed dependency or command prerequisite.")
		}
		prepared.workflowScope = t.workflowScope
		prepared.workflowPlan = t.workflowPlan
		prepared.workflowRoot = t.workflowRoot
		prepared.workflowRun = t.workflowRun
		t = prepared
	}
	plan.Tracer = t.tracerVersion
	checkpoint.Engine = engineIdentity()
	checkpoint.Fingerprint, checkpoint.Node, checkpoint.FactsSHA256, checkpoint.Tracer, checkpoint.Plan = plan.Fingerprint, t.workflowNodeVersion(), factsHash, plan.Tracer, plan
	// Cache only successful selection, not failed preflight or a test verdict.
	if !options.Apply {
		checkpoint.Tracer = ""
	}
	progress("validation")
	before := fingerprint
	if options.Apply {
		before, err = InputFingerprint(ctx, t.repositoryRoot)
	}
	if err != nil {
		return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore source input access.")
	}
	plan.Fingerprint = before
	checkpoint.Fingerprint = before
	if err := errors.Join(checkpoint.save(), checkpoint.saveFactsBinding()); err != nil {
		return fail("blocked_environment", "checkpoint_unavailable", err.Error(), "Restore private temporary storage.")
	}
	started := time.Now()
	logs := io.Discard
	if options.Format == "verbose" {
		logs = output
	}
	run := t.Run
	if t.workflowRun != nil {
		run = t.workflowRun
	}
	runErr := run(ctx, logs)
	validation, err := readCurrentOnboardingResult(t.repositoryRoot, started)
	if err != nil {
		return fail("validation_failed", "validation_report_unavailable", fmt.Sprint(errors.Join(runErr, err)), "No current full report exists. Resolve this execution failure; old/check-only evidence cannot certify completion.")
	}
	executed = true
	plan.CIRuntime = validation.CIRuntime
	if validation.Selection != nil {
		plan.Tracer = validation.Selection.Version
	}
	var runtimeMetadata *platform.MetadataError
	if errors.As(runErr, &runtimeMetadata) {
		result.Capabilities = []string{workflowMetadataCapability(runtimeMetadata)}
		result.JSONReport = validationPath(t.repositoryRoot)
		return fail("blocked_environment", runtimeMetadata.Code, runErr.Error(), "Resolve only the named registry/storage capability, then rerun next_command. The failed attempt remains incomplete in json_report.")
	}
	var supervision *processSupervisionError
	if errors.As(runErr, &supervision) {
		result.JSONReport = validationPath(t.repositoryRoot)
		if supervision.Code == "unsupported_detached_spawn" {
			return fail("unsupported", supervision.Code, supervision.Error(), "Preserve the original test command/configuration. This detached spawn cannot be supervised safely; it is not a pre-existing test failure or tracer comparison.")
		}
		result.Capabilities = []string{"local owned-process socket connections"}
		return fail("blocked_environment", supervision.Code, supervision.Error(), "Restore the named local process capability, then rerun next_command. The test comparison is unverified.")
	}
	if runErr != nil && validation.Success {
		return fail("validation_failed", "execution_result_conflict", runErr.Error(), "Resolve the execution/report discrepancy before claiming completion.")
	}
	progress("verification")
	workflow := &WorkflowEvidence{ValidationCommand: recoveryCommand, Plan: plan, Fingerprint: before, Node: t.workflowNodeVersion(), FactsSHA256: factsHash, Changes: result.Changes}
	workflow.Checks = append(result.Checks, t.verifyReportExclusions(ctx)...)
	after, snapshotErr := InputFingerprint(ctx, t.repositoryRoot)
	sourceCheck := onboard.WorkflowCheck{Name: "source_preservation", Status: "passed", Reason: "Original checkout inputs unchanged by validation; builds/probes ran in owned storage."}
	if snapshotErr != nil || after != before {
		sourceCheck.Status = "failed"
		sourceCheck.Reason = "Repository inputs changed during validation; a fresh full run is required."
	}
	workflow.Checks = append(workflow.Checks, sourceCheck)
	cleanup := onboard.WorkflowCheck{Name: "validation_cleanup", Status: "passed", Reason: "The framework engine reported removal of owned sessions/configuration copies. Adapter-owned process cleanup is preserved; this receipt does not audit unrelated/native application profiles or shared caches."}
	if validation.Cleanup == nil || validation.Cleanup.Status != "passed" {
		cleanup.Status = "failed"
		cleanup.Reason = "Framework-owned cleanup did not pass; inspect cleanup in the report."
	}
	workflow.Checks = append(workflow.Checks, cleanup)
	validation.Workflow = workflow
	finalizeErr := finishValidation(io.Discard, t.repositoryRoot, validation)
	validation, err = readCurrentOnboardingResult(t.repositoryRoot, started)
	if err != nil {
		return fail("blocked_environment", "report_invalid", fmt.Sprint(errors.Join(finalizeErr, err)), "Restore report access; preserve execution evidence.")
	}
	if finalizeErr != nil && validation.Summary.Status == "COMPLETE LOCALLY" {
		return fail("blocked_environment", "report_write_failed", finalizeErr.Error(), "Restore report output access before claiming completion.")
	}
	result.Fingerprint = before
	result.Checks = workflow.Checks
	result.JSONReport = validationPath(t.repositoryRoot)
	if validation.HTMLReportCurrent {
		result.HTMLReport = htmlReportPath(t.repositoryRoot)
	}
	result.Instruction = "Validation finished for this source snapshot. Finish any source edits, then run next_command as the final check and report its factual response. Further source edits invalidate completion. ddtest has already checked its owned cleanup and report exclusions; do not repeat cache or package audits."
	result.NextAction = "run_command"
	resume := result.NextCommand
	result.NextCommand = []string{resume[0], "testdrive", "--report"}
	if options.Format == "json" {
		result.NextCommand = append(result.NextCommand, "--format", "json")
	}
	result.Phase = "complete"
	if workflowVerdict(validation) == "selected_passed" {
		if err := checkpoint.remove(); err != nil {
			return fail("blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Remove only the named owned checkpoint after restoring access.")
		}
		result.Status = "selected_passed"
		return onboard.RenderAgentResult(output, result, options.Format)
	}
	if workflowVerdict(validation) != "complete_locally" {
		result.Instruction = ""
		result.NextCommand = nil
		result.ResumeCommand = append(append([]string{}, resume...), "--resume", checkpoint.RunID)
		result.Phase = "validation"
		if !workflowChecksPassed(workflow) {
			result.Phase = "verification"
		}
		var reviewCommands []string
		for _, configuration := range validation.Configurations {
			for _, build := range configuration.Preparation {
				if build.Code == "preparation_review_required" {
					reviewCommands = append(reviewCommands, build.Command)
				}
			}
		}
		if len(reviewCommands) > 0 {
			result.Location = strings.Join(reviewCommands, "; ")
			result.RequiredInput = []string{"Review these existing scripts/plugins and their network access, then run resume_command. No new setup facts are required."}
			if !slices.Contains(result.ResumeCommand, "--allow-reviewed-builds") {
				result.ResumeCommand = append(result.ResumeCommand, "--allow-reviewed-builds")
			}
			return fail("needs_input", "preparation_review_required", "Preparation was not executed pending review: "+result.Location, "After reviewing the named commands, opt in using resume_command. Preserve the CI commands, scope and assertions; do not invent build facts.")
		}
		if entry, cause := workflowSetupFailure(validation); entry != nil {
			if entry.Cypress != nil && len(entry.Cypress.Start) > 0 && strings.Contains(validation.Error, "cypress startup port") {
				result.Location = entry.ID
				result.InputTemplate = cypressPortFactsTemplate(t.repositoryRoot, before, *entry)
				result.RequiredInput = []string{"supported application/test port override for the existing Cypress startup; keep startup, baseUrl and action wait-on consistent"}
				return fail("needs_input", "service_setup_unresolved", strings.Join(validation.Summary.BlockingChecks, "; "), "Use input_template.configurations[0].port_override for an existing application port variable. If the app has none, add a default-preserving variable in its startup source, rerun resume_command once for the new fingerprint, then fill the template outside the checkout and append --facts to resume_command. ddtest applies the local startup/baseUrl/readiness override; do not change CI port defaults or duplicate the service.")
			}
			result.InputTemplate = setupFactsTemplate(before, *entry)
			result.RequiredInput = []string{"correct the named prerequisite or service; supply configuration_id setup facts and run resume_command"}
			reason := entry.Command + ": setup did not complete"
			if cause != "" {
				reason += ": " + cause
			}
			return fail("needs_input", "configuration_setup_failed", reason, "Tests were not compared for this configuration. Correct only its declared setup; preserve scope and assertions.")
		}
		result.RequiredInput = validation.Summary.BlockingChecks[:min(3, len(validation.Summary.BlockingChecks))]
		return fail("validation_failed", "validation_incomplete", strings.Join(result.RequiredInput, "; "), "Address only these current blockers, then rerun full validation. A failed feature or exclusion check remains failed.")
	}
	if err := checkpoint.remove(); err != nil {
		return fail("blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Restore access to private checkpoint storage.")
	}
	result.Status = "complete_locally"
	return onboard.RenderAgentResult(output, result, options.Format)
}

func reattachSetupFacts(updated *onboard.ValidationScope, previous onboard.ValidationScope, afterEdit bool) error {
	matched := map[string]bool{}
	identity := func(entry onboard.TestCommand) string {
		// The owned editor may partition instrumentation while preserving the
		// original command/setup. Each resulting row retains its own new ID.
		entry.CIInstrumentationExcluded = false
		entry.Environment = maps.Clone(entry.Environment)
		if entry.Environment["NODE_OPTIONS"] == "" {
			delete(entry.Environment, "NODE_OPTIONS")
		}
		if len(entry.Environment) == 0 {
			entry.Environment = nil
		}
		scope := onboard.ValidationScope{Commands: []onboard.TestCommand{entry}}
		onboard.AssignConfigurationIDs(&scope)
		return scope.Commands[0].ID
	}
	for i := range updated.Commands {
		current := updated.Commands[i]
		match := slices.IndexFunc(previous.Commands, func(entry onboard.TestCommand) bool { return entry.ID == current.ID })
		if match < 0 && afterEdit {
			match = slices.IndexFunc(previous.Commands, func(entry onboard.TestCommand) bool { return identity(entry) == identity(current) })
			if match < 0 && strings.HasPrefix(current.Command, "nyc -- ") {
				// The bounded editor may add this exact coverage wrapper. Match an
				// existing wrapped command first, before considering its old leaf.
				current.Command = strings.TrimPrefix(current.Command, "nyc -- ")
				match = slices.IndexFunc(previous.Commands, func(entry onboard.TestCommand) bool { return identity(entry) == identity(current) })
			}
		}
		if match < 0 {
			continue
		}
		entry := previous.Commands[match]
		matched[entry.ID] = true
		updated.Commands[i].Services = entry.Services
		updated.Commands[i].PortOverride = entry.PortOverride

		for _, build := range entry.Prerequisites {
			if build.FactSource != "" {
				pending := slices.IndexFunc(updated.Commands[i].Prerequisites, func(candidate onboard.BuildCommand) bool {
					return candidate.Kind == onboard.PreparationNeedsFacts && candidate.Command == build.Command && candidate.Directory == build.Directory
				})
				if pending >= 0 {
					updated.Commands[i].Prerequisites[pending] = build
				} else {
					updated.Commands[i].Prerequisites = append(updated.Commands[i].Prerequisites, build)
				}
			}
		}
	}
	for _, entry := range previous.Commands {
		hasFacts := len(entry.Services) > 0 || entry.PortOverride != nil || len(entry.LocalOverrides) > 0
		for _, build := range entry.Prerequisites {
			hasFacts = hasFacts || build.FactSource != ""
		}
		if hasFacts && !matched[entry.ID] {
			return fmt.Errorf("accepted facts for %s could not be reattached", entry.ID)
		}
	}
	return nil
}

func (t *Testdrive) selectWorkflowTracer(ctx context.Context, scope onboard.ValidationScope, explicit bool) (string, error) {
	ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
	if t.workflowPlan != nil {
		t.workflowPlan.CIRuntime = &ci
	}
	var selected platform.JSSelection
	for _, entry := range scope.Commands {
		if ciInstrumentationDisabled(entry) {
			continue
		}
		var candidate platform.JSSelection
		err := workflowPreflight(ctx, func() (err error) {
			candidate, err = t.selectSetupTracer(ctx, onboard.ValidationScope{Commands: []onboard.TestCommand{entry}}, explicit)
			return err
		})
		if err != nil {
			return selected.Version, err
		}
		if selected.Version != "" && selected.Version != candidate.Version {
			return selected.Version, fmt.Errorf("tracer selection changed across configurations")
		}
		selected = candidate
		if t.workflowPlan != nil {
			t.workflowPlan.TracerNode = selected.Node
			t.workflowPlan.Tracer = selected.Version
		}
	}
	if selected.Version == "" {
		return "", fmt.Errorf("no configurations enabled for instrumentation; explicit CI opt-outs are not validations")
	}
	return selected.Version, nil
}
func workflowMetadataCapability(metadata *platform.MetadataError) string {
	switch metadata.Code {
	case "EACCES", "EPERM", "EROFS", "ENOSPC":
		return "private temporary storage"
	}
	return "package metadata"
}
func workflowMetadataRetryable(code string) bool {
	switch code {
	case "EAI_AGAIN", "ENETUNREACH", "ECONNREFUSED", "ECONNRESET", "ETIMEDOUT", "FETCH_ERROR":
		return true
	}
	return false
}

// Only idempotent metadata preflight may retry, and only one recognized
// transient transport failure. Tests, auth and permission failures never retry.
func workflowPreflight(ctx context.Context, run func() error) error {
	err := run()
	var metadata *platform.MetadataError
	if errors.As(err, &metadata) && workflowMetadataRetryable(metadata.Code) && ctx.Err() == nil {
		return run()
	}
	return err
}

func readCurrentOnboardingResult(root string, started time.Time) (validationResult, error) {
	var result validationResult
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
	root = canonicalWorkflowRoot(root)
	lock, err := acquireWorkflowLock(root)
	if err != nil {
		result := onboard.AgentResult{Status: "blocked_environment", Phase: "report", Code: "coordination_unavailable", Reason: "A validation is active or coordination storage is unavailable; wait before rendering saved evidence.", NextAction: "wait"}
		if errors.Is(err, errWorkflowBusy) {
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
	if !supportsValidationFramework(validation.Framework) {
		result := onboard.AgentResult{Status: "unsupported", Phase: "report", Code: "framework_unsupported", Reason: "Controlled validation is not available for this framework.", NextAction: "report_result"}
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
	}
	if validation.CheckOnly && validation.Retained != nil && validation.Retained.Result != nil {
		validation = validation.Retained.Result
	}
	result := onboard.AgentResult{Status: "validation_failed", Phase: "report", NextAction: "report_result", JSONReport: validationPath(root)}
	current, err := InputFingerprint(ctx, root)
	if err != nil {
		return renderReportFailure(output, format, err)
	}
	receiptValid := verifyWorkflowReceipt(root, *validation) == nil
	if validation.CheckOnly || validation.Workflow == nil || validation.Workflow.Fingerprint != current || validation.Workflow.Node != currentNodeVersion() || !receiptValid {
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

// Rejected options, facts or resume IDs describe this invocation, not the
// repository, so they leave an earlier report and its binding untouched.
func workflowInputFailure(code string) bool {
	switch code {
	case "resume_invalid", "resume_not_found", "facts_invalid", "facts_refresh_required":
		return true
	}
	return false
}

// A stopped attempt invalidates the current handoff while keeping one historical
// execution. Never overwrite a different framework or an unreadable report.
func invalidatePriorWorkflow(root, framework string, state onboard.AgentResult) {
	previous, err := readPreviousValidation(root)
	if err != nil || previous == nil || previous.Framework != framework {
		return
	}
	result := validationResult{Session: planSession().ID(), Framework: framework, Error: state.Code + ": " + state.Reason,
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

func (t *Testdrive) workflowNodeVersion() string {
	if t.nodeVersion == nil {
		return ""
	}
	return t.nodeVersion()
}

func (t *Testdrive) workflowValidationResume(executable, name, selected string, options WorkflowOptions) []string {
	command := []string{executable, "testdrive", "--framework", name, "--all", "--agent", "--tracer-version", selected, "--yes"}
	if options.Format == "json" {
		command = append(command, "--format", "json")
	}
	for _, flag := range []struct{ name, value string }{{"facts", options.FactsFile}, {"site", options.Site}, {"platform", settings.GetPlatform()}, {"command", settings.GetCommand()}} {
		if flag.value != "" {
			command = append(command, "--"+flag.name, flag.value)
		}
	}
	if t.commandTimeout > 0 {
		command = append(command, "--timeout", t.commandTimeout.String())
	}
	if t.allowReviewedBuilds {
		command = append(command, "--allow-reviewed-builds")
	}
	return command
}
