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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/kballard/go-shellquote"
)

type WorkflowOptions struct {
	Resume          string
	Apply           bool
	ExplicitVersion bool
	FactsFile       string
	Site            string
	Format          string // agent: compact state + rendered facts; json: protocol only; verbose: runner logs too
}

// Onboard and standalone testdrive share the same coordinator and engine.
func (t *Testdrive) Onboard(ctx context.Context, output io.Writer, explicitVersion bool) error {
	return t.Workflow(ctx, output, WorkflowOptions{Apply: true, ExplicitVersion: explicitVersion, Format: "json"})
}

func (t *Testdrive) Workflow(ctx context.Context, output io.Writer, options WorkflowOptions) error {
	name := t.framework.Name()
	if (t.language != "javascript" || !supportsValidationFramework(name)) && options.Format == "verbose" && !options.Apply {
		return t.Run(ctx, output)
	}
	result := onboard.AgentResult{Status: "running", Phase: "discovery", NextAction: "run_command", NextCommand: onboard.OnboardingCommand(name)}
	if !options.Apply {
		result.NextCommand = []string{result.NextCommand[0], "testdrive", "--framework", name, "--agent", "--yes"}
		if t.allConfigurations {
			result.NextCommand = append(result.NextCommand, "--all")
		}
		if override := strings.TrimSpace(settings.GetCommand()); !t.allConfigurations && override != "" {
			result.NextCommand = append(result.NextCommand, "--command", override)
		}
	}
	if options.ExplicitVersion {
		result.NextCommand = append(result.NextCommand, "--tracer-version", t.tracerVersion)
	}
	if options.FactsFile != "" {
		result.NextCommand = append(result.NextCommand, "--facts", options.FactsFile)
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
		if checkpoint != nil {
			key := result.Fingerprint + factsHash + code + result.Reason
			if checkpoint.Failure == key {
				checkpoint.Attempts++
			} else {
				checkpoint.Failure, checkpoint.Attempts = key, 1
			}
			checkpoint.Plan = result.Plan
			if err := checkpoint.save(); err != nil {
				result.Reason += "; checkpoint: " + err.Error()
			}
			if result.ResumeCommand == nil {
				result.ResumeCommand = append(append([]string{}, recoveryCommand...), "--resume", checkpoint.RunID)
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
			if code == "setup_dependencies_missing" {
				result.NextAction = "run_command"
			} else {
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
		if ownedLock && !executed {
			invalidatePriorWorkflow(t.repositoryRoot, name, result)
		}
		if checkpoint != nil && checkpoint.Attempts >= 3 {
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
	defer func() { _ = lock.Close() }()
	if err := recoverProbes(t.repositoryRoot); err != nil {
		return fail("needs_input", "probe_recovery_failed", err.Error(), "Review only the named owned probe; modified files are preserved. Then retry the same command.")
	}
	checkpoint, err = openCheckpoint(options.Resume, t.repositoryRoot, name, settings.GetCommand(), options.Apply, t.allConfigurations)
	if err != nil {
		return fail("needs_input", "resume_invalid", err.Error(), "Use the supplied resume command with its original repository/framework/scope, or start a fresh workflow.")
	}
	result.RunID = checkpoint.RunID
	progress := func(phase string) {
		result.Phase = phase
		if options.Format != "json" {
			_ = onboard.WriteAgentResult(output, onboard.AgentResult{Status: "running", Phase: phase})
		}
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
	scope, err := onboard.DiscoverValidationScope(t.repositoryRoot, name)
	if err != nil {
		return fail("needs_input", "scope_unresolved", err.Error(), "Correct only the named CI/package parsing gap; preserve every selected framework configuration.")
	}
	onboard.AssignConfigurationIDs(&scope)
	runtimeVersion := ""
	if t.nodeVersion != nil {
		runtimeVersion = t.nodeVersion()
	}
	plan := &onboard.ExecutionPlan{SchemaVersion: 1, Fingerprint: fingerprint, Framework: name, Site: options.Site, Runtime: runtimeVersion, Initialization: workflowInitialization(name), Features: append([]string{}, jestFeatures...), Scope: scope}
	if name == "cypress" {
		plan.Features = nil
		for _, feature := range jestFeatures {
			if feature != "skipping" {
				plan.Features = append(plan.Features, feature)
			}
		}
		plan.DeferredFeatures = map[string]string{"skipping": cypressTIADeferredReason}
	}
	if plan.Site == "" {
		plan.Site = "datadoghq.com"
	}
	plan.PackageManager = workflowPackageManager(t.repositoryRoot)
	result.Plan = plan
	if options.FactsFile != "" {
		factsHash, err = applySetupFacts(t.repositoryRoot, options.FactsFile, fingerprint, &scope)
		if err != nil {
			result.Location = options.FactsFile
			result.RequiredInput = []string{"schema_version=2, current input_fingerprint, configurations with configuration_id and build/service setup"}
			return fail("needs_input", "facts_invalid", err.Error(), "Supply only the missing prerequisite facts in a JSON file outside the checkout; no discovery or validation assertion can be overridden.")
		}
	}
	plan.Scope = scope
	if options.Apply || t.allConfigurations {
		if len(scope.Unresolved) > 0 || len(scope.Commands) == 0 {
			result.Location = strings.Join(scope.Unresolved[:min(1, len(scope.Unresolved))], "")
			result.RequiredInput = []string{"literal CI test entry point and any unresolved setup relationship"}
			return fail("needs_input", "scope_unresolved", fmt.Sprintf("Discovered %d commands. %s", len(scope.Commands), strings.Join(scope.Unresolved, "; ")), "Resolve the named CI entry point. Facts can add prerequisites to discovered commands; they cannot erase unresolved CI or shrink scope. Use --instructions only for this unsupported integration.")
		}
		for _, entry := range scope.Commands {
			if len(entry.UnvalidatedSetup) > 0 {
				result.Location = strings.Join(entry.Locations, ", ")
				result.RequiredInput = []string{"repository-local working directory and supported setup for this exact CI command"}
				result.InputTemplate = setupFactsTemplate(fingerprint, entry)
				return fail("needs_input", "setup_unresolved", entry.Command+": "+strings.Join(entry.UnvalidatedSetup, "; "), "Preserve this configuration. Integrate its local setup explicitly; do not replace it with an easier suite.")
			}
		}
	}
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
			prepared, err := Prepare(pin, false, t.allConfigurations)
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
		reused := checkpoint.reusable(fingerprint, t.nodeVersion(), factsHash, t.tracerVersion, options.ExplicitVersion)
		if !reused {
			selectTracer := t.selectWorkflowTracer
			if t.workflowSelect != nil {
				selectTracer = t.workflowSelect
			}
			selected, problem = selectTracer(ctx, scope, options.ExplicitVersion)
		} else if checkpoint.Plan != nil {
			plan.CIRuntime = checkpoint.Plan.CIRuntime
		}
		if problem != nil {
			var metadata *platform.MetadataError
			if errors.As(problem, &metadata) {
				result.Capabilities = []string{metadata.Capability}
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
			result.ResumeCommand = []string{result.NextCommand[0], "testdrive", "--framework", name, "--all", "--agent", "--tracer-version", selected, "--yes"}
			if options.FactsFile != "" {
				result.ResumeCommand = append(result.ResumeCommand, "--facts", options.FactsFile)
			}
			checkpoint.Apply = false
			checkpoint.All = true
			result.ResumeCommand = append(result.ResumeCommand, "--resume", checkpoint.RunID)
			result.NextCommand = nil
			return fail("unsupported", "ci_edit_unsupported", "The selected tracer and runtime are resolved; this framework still needs its custom CI hooks.", "Apply only plan_summary.initialization using plan_summary.tracer and plan_summary.site at the named CI hook; run resume_command. Inspect details_command only if the missing source integration needs more context.")
		}
		progress("apply")
		result.Changes, err = onboard.ApplyJavaScriptSetup(t.repositoryRoot, name, selected, options.Site, plan.TracerNode)
		if err != nil {
			var dependencies *onboard.SetupDependencyGap
			if errors.As(err, &dependencies) {
				result.Location = "package.json / devDependencies"
				result.NextCommand = dependencies.Command
				result.ResumeCommand = append(append([]string{}, recoveryCommand...), "--resume", checkpoint.RunID)
				result.RequiredInput = []string{"Run next_command once to update the manifest and lockfile, then resume_command. Preserve the selected framework and CI test commands."}
				return fail("needs_input", "setup_dependencies_missing", err.Error(), "The recognized coverage recipe needs only these dependencies; no CI edits have been made.")
			}
			var gap *onboard.SetupGap
			if errors.As(err, &gap) {
				result.Location = gap.Location
				result.RequiredInput = []string{"CI tracer initialization at " + gap.Location}
				result.ResumeCommand = []string{result.NextCommand[0], "testdrive", "--framework", name, "--all", "--agent", "--tracer-version", selected, "--yes"}
				if options.FactsFile != "" {
					result.ResumeCommand = append(result.ResumeCommand, "--facts", options.FactsFile)
				}
				checkpoint.Apply = false
				checkpoint.All = true
				result.ResumeCommand = append(result.ResumeCommand, "--resume", checkpoint.RunID)
				result.NextCommand = nil
				return fail("unsupported", "ci_edit_unsupported", err.Error(), "Integrate only this CI hook, then run resume_command. Do not repeatedly retry the automatic editor or drop configurations.")
			}
			return fail("blocked_environment", "ci_edit_unavailable", err.Error(), "Restore filesystem access. Completed source edits are listed in changes; do not overwrite customer edits.")
		}
		// Refresh discovery after YAML insertion. Facts remain the exact additions
		// already validated against the pre-edit snapshot; reattach them by command.
		updated, err := onboard.DiscoverValidationScope(t.repositoryRoot, name)
		if err != nil {
			return fail("needs_input", "scope_after_edit", err.Error(), "Review the named source edit before running validation.")
		}
		onboard.AssignConfigurationIDs(&updated)
		for i := range updated.Commands {
			for _, entry := range scope.Commands {
				if entry.ID == updated.Commands[i].ID {
					updated.Commands[i].Services = entry.Services
					updated.Commands[i].LocalOverrides = entry.LocalOverrides
					if len(entry.LocalOverrides) > 0 {
						updated.Commands[i].Cypress = entry.Cypress
						updated.Commands[i].Environment = entry.Environment
					}
					for _, build := range entry.Prerequisites {
						if build.FactSource != "" {
							updated.Commands[i].Prerequisites = append(updated.Commands[i].Prerequisites, build)
						}
					}
				}
			}
		}
		onboard.AssignConfigurationIDs(&updated)
		scope = updated
		plan.Scope, plan.Tracer = scope, selected
		t.workflowScope = &scope
		t.tracerVersion = selected
		// Prepare once more to bind installation commands to the exact release.
		prepared, err := Prepare(selected, false, true)
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
	checkpoint.Fingerprint, checkpoint.Node, checkpoint.FactsSHA256, checkpoint.Tracer, checkpoint.Plan = plan.Fingerprint, t.nodeVersion(), factsHash, plan.Tracer, plan
	// Cache only successful selection, not failed preflight or a test verdict.
	if !options.Apply {
		checkpoint.Tracer = ""
	}
	progress("validation")
	before, err := InputFingerprint(ctx, t.repositoryRoot)
	if err != nil {
		return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore source input access.")
	}
	plan.Fingerprint = before
	checkpoint.Fingerprint = before
	if err := checkpoint.save(); err != nil {
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
		result.Capabilities = []string{runtimeMetadata.Capability}
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
	if runErr != nil && validation.Summary.Status == "COMPLETE LOCALLY" {
		return fail("validation_failed", "execution_result_conflict", runErr.Error(), "Resolve the execution/report discrepancy before claiming completion.")
	}
	progress("verification")
	workflow := &WorkflowEvidence{Plan: plan, Fingerprint: before, Node: t.nodeVersion(), FactsSHA256: factsHash, Changes: result.Changes}
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
	result.Phase = "complete"
	if validation.Summary.SelectedValidation != nil && validation.Summary.SelectedValidation.Status == "PASSED" && workflowChecksPassed(workflow) {
		if err := checkpoint.remove(); err != nil {
			return fail("blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Remove only the named owned checkpoint after restoring access.")
		}
		result.Status = "selected_passed"
		return onboard.RenderAgentResult(output, result, options.Format)
	}
	if validation.Summary.Status != "COMPLETE LOCALLY" {
		result.Instruction = ""
		result.NextCommand = nil
		result.ResumeCommand = append(append([]string{}, resume...), "--resume", checkpoint.RunID)
		result.Phase = "validation"
		if !workflowChecksPassed(workflow) {
			result.Phase = "verification"
		}
		if entry := workflowSetupFailure(validation); entry != nil {
			if entry.Cypress != nil && len(entry.Cypress.Start) > 0 && strings.Contains(validation.Error, "cypress startup port") {
				result.Location = entry.ID
				result.InputTemplate = cypressPortFactsTemplate(t.repositoryRoot, before, *entry)
				result.RequiredInput = []string{"supported application/test port override for the existing Cypress startup; keep startup, baseUrl and action wait-on consistent"}
				return fail("needs_input", "service_setup_unresolved", strings.Join(validation.Summary.BlockingChecks, "; "), "Use input_template.configurations[0].port_override for an existing application port variable. If the app has none, add a default-preserving variable in its startup source, rerun resume_command once for the new fingerprint, then fill the template outside the checkout and append --facts to resume_command. ddtest applies the local startup/baseUrl/readiness override; do not change CI port defaults or duplicate the service.")
			}
			result.InputTemplate = setupFactsTemplate(before, *entry)
			result.RequiredInput = []string{"correct the named prerequisite or service; supply configuration_id setup facts and run resume_command"}
			return fail("needs_input", "configuration_setup_failed", entry.Command+": setup did not complete", "Tests were not compared for this configuration. Correct only its declared setup; preserve scope and assertions.")
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

func (t *Testdrive) selectWorkflowTracer(ctx context.Context, scope onboard.ValidationScope, explicit bool) (string, error) {
	ci := onboard.CheckCIRuntimes(ctx, t.repositoryRoot, t.framework.Name())
	if t.workflowPlan != nil {
		t.workflowPlan.CIRuntime = &ci
	}
	if len(ci.Jobs) == 0 {
		return "", fmt.Errorf("CI test runtime is unresolved: %s", ci.Reason)
	}
	pin := ""
	for _, job := range ci.Jobs {
		if job.Tracer != "" {
			version := strings.TrimPrefix(job.Tracer, "dd-trace@")
			if pin != "" && pin != version {
				return "", fmt.Errorf("CI selects multiple tracer versions; align them explicitly")
			}
			pin = version
		}
	}
	version := t.tracerVersion
	if pin != "" && !explicit && t.projectTracer == "" {
		version = pin
	}
	selected := ""
	for _, entry := range scope.Commands {
		words, err := shellquote.Split(entry.Command)
		if entry.Cypress != nil {
			words, err = []string{"node"}, nil
		}
		if err != nil || len(words) == 0 {
			return "", fmt.Errorf("unresolved command: %s", entry.Command)
		}
		probe := *t
		probe.cypress, probe.cypressCommand = entry.Cypress, entry.Command
		directory, directoryErr := localSetupDirectory(t.repositoryRoot, entry.Directory)
		if directoryErr != nil {
			return "", directoryErr
		}
		probe.repositoryRoot = directory
		probe.command, probe.args = t.configurationExecutable(words[0]), words[1:]
		probe.tracerVersion = version
		probe.executor = configurationExecutor{t.executor, entry.Environment}
		probe.bindPreflight()
		check := validationResult{CIRuntime: &ci}
		err = workflowPreflight(ctx, func() error {
			check = validationResult{CIRuntime: &ci}
			return probe.preflightInDirectory(ctx, directory, &check)
		})
		if check.Selection == nil || check.Selection.Version == "" || check.Preflight == nil {
			if err == nil {
				err = fmt.Errorf("preflight did not resolve tracer metadata")
			}
			return "", err
		}
		selection := *check.Selection
		if pin == "" && t.projectTracer == "" && !explicit && selected == "" {
			if rec := check.Preflight.Recommendation; rec != nil && len(rec.Unresolved) == 0 && len(rec.Compatible) > 0 {
				selection = rec.Selection
			}
		}
		support := map[string]func(jestPreflight, platform.JSSelection) verdict{"jest": checkJestSupport, "vitest": checkVitestSupport, "mocha": checkMochaSupport, "playwright": checkPlaywrightSupport, "cypress": checkCypressSupport, "cucumber": checkCucumberSupport, "webdriverio": checkWebdriverIOSupport}[t.framework.Name()]
		if verdict := support(*check.Preflight, selection); verdict.Status != "compatible" {
			return "", fmt.Errorf("%s: %s", entry.Command, verdict.Reason)
		}
		for _, job := range ci.Jobs {
			node := job.Node
			if job.NodeResolution != nil {
				node = job.NodeResolution.Version
			}
			status, reason := onboard.CompareNodeRequirement(node, selection.Node)
			if job.Code == "excluded_runtime" {
				continue
			}
			if status == "incompatible" && job.Code == "missing_instrumentation" {
				continue
			}
			if status != "compatible" || job.Code != "missing_instrumentation" && job.Status != "compatible" {
				return "", fmt.Errorf("%s / %s / step %d: %s %s", job.Workflow, job.Job, job.Step, job.Reason, reason)
			}
		}
		if selected != "" && selected != selection.Version {
			return "", fmt.Errorf("tracer selection changed across configurations")
		}
		selected = selection.Version
		if t.workflowPlan != nil {
			t.workflowPlan.TracerNode = selection.Node
			t.workflowPlan.Tracer = selected
		}
		version = selected
	}
	return selected, nil
}

// Only idempotent metadata preflight may retry, and only one recognized
// transient transport failure. Tests, auth and permission failures never retry.
func workflowPreflight(ctx context.Context, run func() error) error {
	err := run()
	var metadata *platform.MetadataError
	if errors.As(err, &metadata) && metadata.Retryable && ctx.Err() == nil {
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
		return err
	}
	lock, err := acquireWorkflowLock(root)
	if err != nil {
		result := onboard.AgentResult{Status: "blocked_environment", Phase: "report", Code: "workflow_busy_or_unavailable", Reason: "A validation is active or coordination storage is unavailable; wait before rendering saved evidence.", NextAction: "wait"}
		return errors.Join(err, onboard.RenderAgentResult(output, result, format))
	}
	defer func() { _ = lock.Close() }()
	validation, err := readPreviousValidation(root)
	if err != nil {
		return err
	}
	if validation == nil {
		return fmt.Errorf("no saved validation report")
	}
	if !supportsValidationFramework(validation.Framework) {
		result := onboard.AgentResult{Status: "unsupported", Phase: "report", Code: "framework_unsupported", Reason: "Controlled validation is not available for this framework.", NextAction: "report_result"}
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
	}
	result := onboard.AgentResult{Status: "validation_failed", Phase: "report", NextAction: "report_result", JSONReport: validationPath(root)}
	current, err := InputFingerprint(ctx, root)
	if err != nil {
		return err
	}
	if validation.CheckOnly || validation.Workflow == nil || validation.Workflow.Fingerprint != current || validation.Workflow.Node != currentNodeVersion() {
		result.Code = "stale_report"
		result.Reason = "Saved evidence does not certify the current source/runtime. Run full testdrive; do not report historical success."
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, format))
	}
	result.Fingerprint = current
	result.Checks = validation.Workflow.Checks
	result.FinalResponse = validation.Summary.FinalResponse
	if validation.HTMLReportCurrent {
		result.HTMLReport = htmlReportPath(root)
	}
	if validation.Summary.Status == "COMPLETE LOCALLY" {
		result.Status = "complete_locally"
	} else if validation.Summary.SelectedValidation != nil && validation.Summary.SelectedValidation.Status == "PASSED" {
		result.Status = "selected_passed"
	}
	return onboard.RenderAgentResult(output, result, format)
}

// Keep the root canonical for lock/fact path comparisons even on platforms
// where the shell enters a repository through a symlink.
func canonicalWorkflowRoot(root string) string {
	if value, err := filepath.EvalSymlinks(root); err == nil {
		return value
	}
	return root
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
