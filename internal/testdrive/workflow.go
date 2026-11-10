// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/DataDog/ddtest/internal/usagetelemetry"
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

// quietReportOutput suppresses runner text while retaining the typed callback
// for reports actually written by this attempt. It never reads saved reports.
type quietReportOutput struct{ output io.Writer }

func (w quietReportOutput) Write(data []byte) (int, error) { return len(data), nil }
func (w quietReportOutput) SetTroubleshootingReport(path string) {
	usagetelemetry.SetTroubleshootingReport(w.output, path)
}
func (w quietReportOutput) ObserveTroubleshootingError(operation string, err error) {
	usagetelemetry.ObserveTroubleshootingError(w.output, operation, err)
}

func (t *Testdrive) Workflow(ctx context.Context, output io.Writer, options WorkflowOptions) error {
	name := t.framework.Name()
	if !t.lang().coordinated() && options.Format == "verbose" && !options.Apply && options.Resume == "" && options.FactsFile == "" {
		return t.Run(ctx, output)
	}
	result := onboard.AgentResult{Status: "running", Phase: "discovery", NextAction: "run_command", NextCommand: t.workflowCommand(options)}
	if len(result.NextCommand) == 0 {
		result.Status, result.Code, result.Reason, result.NextAction = "needs_input", "transient_executable", onboard.TransientExecutableReason, "supply_input"
		return errors.Join(fmt.Errorf("%s", result.Reason), onboard.RenderAgentResult(output, result, options.Format))
	}
	attempt := &workflowAttempt{
		result: &result, output: output, options: options, framework: name,
		recoveryCommand: append([]string{}, result.NextCommand...),
	}
	// Preparation may replace t; failures must use the currently prepared drive.
	fail := func(status, code, reason, needs string) error {
		return attempt.fail(t, status, code, reason, needs)
	}

	if supportsValidationFramework(name) && StructuredValidation(t.language) && !t.lang().coordinated() && !options.Apply {
		// Standalone validation has no automatic CI edits; --apply stays unsupported.
		return t.standaloneWorkflow(ctx, attempt, fail)
	}
	if !supportsValidationFramework(name) || !t.lang().coordinated() {
		result.NextCommand = nil
		return fail("unsupported", "framework_unsupported", "Controlled validation is not available for this framework.", "Preserve its unsupported status; do not substitute a different framework.")
	}
	t.repositoryRoot = workspace.CanonicalRoot(t.repositoryRoot)
	// Like onboard, DD_SITE is the default site the CI setup must report to.
	t.selectedSite = options.Site
	if t.selectedSite == "" {
		t.selectedSite = os.Getenv("DD_SITE")
	}
	lock, err := lockReports(t.repositoryRoot)
	if err != nil {
		if errors.Is(err, workspace.ErrBusy) {
			return fail("blocked_environment", "workflow_busy", err.Error(), "Wait for the running validation; do not start another one or delete lock files.")
		}
		result.Capabilities = []string{"writable private temporary coordination storage"}
		return fail("blocked_environment", "coordination_unavailable", err.Error(), "Restore access/free space in the named temporary storage, then rerun next_command.")
	}
	attempt.ownedLock = true
	ctx = context.WithValue(ctx, workflowLockKey{}, t.repositoryRoot)
	defer func() { _ = lock.Close() }()
	if err := cleanupExpiredCheckpoints(); err != nil {
		return fail("blocked_environment", "checkpoint_cleanup_failed", err.Error(), "Restore private checkpoint storage.")
	}
	if err := workspace.RecoverSessions(t.repositoryRoot, io.Discard); err != nil {
		return fail("blocked_environment", "session_recovery_failed", err.Error(), "Review the named owned storage; customer files are preserved.")
	}
	if err := recoverProcessOwners(t.repositoryRoot, io.Discard); err != nil {
		return fail("blocked_environment", "process_recovery_failed", err.Error(), "Restore access to the named owned process.")
	}
	if err := recoverProbes(t.repositoryRoot); err != nil {
		return fail("needs_input", "probe_recovery_failed", err.Error(), "Review only the named owned probe; modified files are preserved. Then retry the same command.")
	}
	attempt.checkpoint, err = openCheckpoint(options.Resume, t.repositoryRoot, name, settings.GetCommand(), options.Apply, t.allConfigurations)
	if err != nil {
		if errors.Is(err, errCheckpointNotFound) {
			return fail("needs_input", "resume_not_found", err.Error(), "Start a fresh workflow without --resume; this run already finished or its checkpoint expired.")
		}
		return fail("needs_input", "resume_invalid", err.Error(), "Use the supplied resume command with its original repository/framework/scope, or start a fresh workflow.")
	}
	result.RunID = attempt.checkpoint.RunID
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
		runErr := t.Run(ctx, quietReportOutput{output})
		usagetelemetry.ObserveTroubleshootingError(output, "test_execution", runErr)
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
	fingerprint, err := workflowFingerprint(ctx, t.repositoryRoot, name)
	if err != nil {
		return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore access to the named source input; no tests or edits were attempted.")
	}
	result.Fingerprint = fingerprint
	scope, err := onboard.DiscoverWorkflowScope(t.repositoryRoot, name)
	if err != nil {
		return fail("needs_input", "scope_unresolved", err.Error(), "ddtest could not read the named CI or package configuration. "+onboard.ScopeBlockerInstruction)
	}
	onboard.AssignConfigurationIDs(&scope)
	runtimeVersion := ""
	if t.nodeVersion != nil {
		runtimeVersion = t.workflowNodeVersion()
	}
	plan := &onboard.ExecutionPlan{SchemaVersion: 1, Fingerprint: fingerprint, Framework: name, Site: options.Site, Runtime: runtimeVersion, Initialization: workflowInitialization(t.repositoryRoot, name), Scope: scope}
	for _, policy := range featurePolicies(name) {
		switch policy.Status {
		case "required":
			plan.Features = append(plan.Features, policy.Name)
		case "not supported":
			if plan.UnsupportedFeatures == nil {
				plan.UnsupportedFeatures = map[string]string{}
			}
			plan.UnsupportedFeatures[policy.Name] = policy.Reason
		}
	}

	t.setPlanTracer(plan, plan.Tracer)
	if plan.Site == "" {
		plan.Site = "datadoghq.com"
	}
	plan.PackageManager = t.lang().packageManager(t.repositoryRoot)
	languageRuntime, err := t.lang().workflowRuntime(t, scope)
	if err != nil {
		return fail("blocked_environment", "runtime_unavailable", err.Error(), "Restore the runtime that runs the selected commands first on PATH, then rerun next_command. No tests ran.")
	}
	if languageRuntime != "" {
		plan.Runtime = languageRuntime
	}
	result.Plan = plan
	// A CI job's runtime that neither its configuration nor a statement
	// names is the user's answer to give; nothing is set up or run before it.
	if err := attempt.checkCIStatements(ctx, t); err != nil {
		return err
	}
	if err := attempt.acceptSetupFacts(t, fingerprint, &scope); err != nil {
		return err
	}
	plan.Scope = scope
	if options.Apply || t.allConfigurations {
		// Unresolved CI never blocks the configurations that are ready: setup
		// edits and validates those, and the unresolved entries stay report
		// blockers. With nothing ready, there is nothing to set up or validate.
		partial := len(scope.Unresolved) > 0 && slices.ContainsFunc(scope.Commands, func(entry onboard.TestCommand) bool { return t.readyConfiguration(entry) })
		if partial {
			result.Checks = append(result.Checks, scopeCheck(scope.Unresolved))
		}
		if len(scope.Commands) == 0 || len(scope.Unresolved) > 0 && !partial {
			result.Location = strings.Join(scope.Unresolved[:min(1, len(scope.Unresolved))], "")
			result.RequiredInput = []string{"literal CI test entry point and any unresolved setup relationship"}
			reason := fmt.Sprintf("Discovered %d commands. %s", len(scope.Commands), strings.Join(scope.Unresolved, "; "))
			if len(scope.Commands) == 0 && len(scope.Unresolved) == 0 {
				reason = "No " + displayName(name) + " test command was found in " + onboard.CIProviderDisplayName() + " workflows or package scripts."
			}
			needs := "Facts can add prerequisites to discovered commands; they cannot erase unresolved CI or shrink scope. " + onboard.ScopeBlockerInstruction + " Use --instructions only for this unsupported integration."
			// Manual CI edits follow; name the release CI's Node can run so agents
			// don't pin a newer major themselves.
			if !options.ExplicitVersion && t.projectTracer == "" {
				if version := t.ciRuntimeTracer(ctx); version != "" {
					t.setPlanTracer(plan, version)
					needs += manualCITracerPrefix + onboard.CIProviderTracerSetting("js", version) + " (every detected CI Node runtime supports it) and pass --tracer-version " + version + " to validation."
				}
			}
			return fail("needs_input", "scope_unresolved", reason, needs)
		}
		for _, entry := range scope.Commands {
			if t.setupStaysReported(entry, partial) {
				continue
			}
			if len(entry.UnvalidatedSetup) > 0 || missingPrerequisiteFacts(entry) {
				result.Location = strings.Join(entry.Locations, ", ")
				result.RequiredInput = []string{"repository-local working directory and supported setup for this exact CI command"}
				result.InputTemplate = setupFactsTemplate(fingerprint, entry)
				suggestPrerequisiteOutputs(ctx, t.repositoryRoot, result.InputTemplate)
				return fail("needs_input", "setup_unresolved", entry.Command+": "+strings.Join(pendingSetup(entry), "; ")+"; declare the expected outputs of each pending prerequisite", "Find each prerequisite's outputs from its configuration (tsconfig outDir, bundler config, package.json main/exports) or by running it in a disposable copy; do not run it in the checkout. Where input_template already lists expected_files, they are git-ignored package.json entry points: keep the ones this prerequisite produces and add any others it writes. Save input_template as a JSON file outside the checkout, fill expected_files for each prerequisite, then run resume_command with --facts <path>. Preserve this configuration; do not replace it with an easier suite.")
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
		pin, err := attempt.ciPinnedTracer(ctx, t)
		if err != nil {
			return err
		}
		if pin != "" {
			prepared, err := Prepare(PrepareOptions{TracerVersion: pin, All: t.allConfigurations, Timeout: t.commandTimeout, AllowReviewedBuilds: t.allowReviewedBuilds, AcceptSubstituteRuntime: t.acceptSubstituteRuntime})
			if err != nil {
				return fail("needs_input", "validation_preparation", err.Error(), "Resolve the named local prerequisite.")
			}
			t = prepared.withWorkflowState(t)
		}
	}
	if options.Apply {

		progress("preflight")
		selected := attempt.checkpoint.Tracer
		var problem error
		reused := attempt.checkpoint.reusable(fingerprint, t.workflowNodeVersion(), attempt.factsHash, t.tracerVersion, options.ExplicitVersion)
		if !reused {
			selectTracer := t.selectWorkflowTracer
			if t.workflowSelect != nil {
				selectTracer = t.workflowSelect
			}
			selected, problem = selectTracer(ctx, scope, options.ExplicitVersion)
		} else if attempt.checkpoint.Plan != nil {
			plan.CIRuntime = attempt.checkpoint.Plan.CIRuntime
			plan.TracerNode = attempt.checkpoint.Plan.TracerNode
		}
		if problem != nil {
			return attempt.failTracerSelection(t, problem)
		}
		if len(t.workflowReportOnly) > 0 {
			reason := "Not set up or validated locally; reported as blockers: " + strings.Join(t.workflowReportOnly, "; ")
			if i := slices.IndexFunc(result.Checks, func(check onboard.WorkflowCheck) bool { return check.Name == "ci_scope" }); i >= 0 {
				result.Checks[i].Reason += "; " + reason
			} else {
				result.Checks = append(result.Checks, onboard.WorkflowCheck{Name: "ci_scope", Status: onboard.StatusInconclusive, Reason: reason})
			}
		}
		result.Checks = append(result.Checks, onboard.WorkflowCheck{Name: "preflight_preparation", Status: "passed", Reason: fmt.Sprintf("Verified preflight reused: %t. Test execution is always fresh.", reused)})
		t.setPlanTracer(plan, selected)
		if !onboard.SupportsAutomaticSetup(name) {
			// The manual setup instructions hold the exact CI setup, as for
			// unresolved scope; only the tracer release they leave to the
			// reader is named here.
			setting := "dd-trace " + selected + " in place of its tracer release placeholder (" + onboard.CIProviderTracerSetting("js", selected) + ")"
			if site := onboard.CIProviderSiteSetting(plan.Site); site != "" {
				// The instructions show the default site; resume_command
				// validates the selected one.
				setting = site + ", and " + setting
			}
			result.Location = "CI initialization"
			result.RequiredInput = []string{"the " + onboard.CIProviderDisplayName() + " setup next_command prints, exactly as shown, with " + setting}
			result.ResumeCommand = t.workflowValidationResume(attempt.recoveryCommand[0], name, selected, options)
			attempt.checkpoint.Apply = false
			attempt.checkpoint.All = true
			result.ResumeCommand = append(result.ResumeCommand, "--resume", attempt.checkpoint.RunID)
			result.NextCommand = onboard.DDTestCommand("onboard", "--framework", name, "--instructions")
			return fail("unsupported", "ci_edit_unsupported", "onboard --apply does not write the "+displayName(name)+" CI setup on "+onboard.CIProviderDisplayName()+". next_command prints it: apply that setup exactly as shown, with "+setting+": what ddtest selected and resume_command validates.", "Run next_command, apply only the CI setup it prints, exactly as shown and with "+setting+", then run resume_command. Do not write a loader, hook or plugin integration of your own, and do not run testdrive or the tests first.")
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
				// The manifest and lockfile changed only through the install ddtest
				// prescribed: extend them without asking for --allow-dirty.
				var dirty *onboard.DirtyWorkflowGap
				if errors.As(err, &dirty) && attempt.checkpoint != nil && onboard.OnlyPrescribedDependencyChanges(t.repositoryRoot, dirty.Files, attempt.checkpoint.DependencyPackages) {
					result.Changes, err = sourcePlan.Apply(true)
				}
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
			return attempt.failSetupEdits(t, err, selected, fingerprint)
		}
		// Bind the accepted plan and facts to the snapshot this owned edit produced
		// at once, so a later failure resumes with them instead of a dead end.
		applied, err := workflowFingerprint(ctx, t.repositoryRoot, name)
		if err != nil {
			return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore source input access. Completed source edits are listed in changes.")
		}
		result.Fingerprint = applied
		attempt.checkpoint.Engine, attempt.checkpoint.Fingerprint, attempt.checkpoint.FactsSHA256, attempt.checkpoint.Plan = engineIdentity(), applied, attempt.factsHash, plan
		attempt.checkpoint.Tracer, attempt.checkpoint.Node = selected, t.workflowNodeVersion()
		if err := errors.Join(attempt.checkpoint.save(), attempt.checkpoint.saveFactsBinding()); err != nil {
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
		plan.Scope = scope
		t.setPlanTracer(plan, selected)
		t.workflowScope = &scope
		t.tracerVersion = selected
		// Prepare once more to bind installation commands to the exact release.
		prepared, err := Prepare(PrepareOptions{TracerVersion: selected, All: true, Timeout: t.commandTimeout, AllowReviewedBuilds: t.allowReviewedBuilds, AcceptSubstituteRuntime: t.acceptSubstituteRuntime})
		if err != nil {
			return fail("needs_input", "validation_preparation", err.Error(), "Resolve the named installed dependency or command prerequisite.")
		}
		t = prepared.withWorkflowState(t)
		// A setup the edit just added may depend on facts only the CI server
		// shows; the user states them before anything runs.
		if err := attempt.checkCIConfirmations(t); err != nil {
			return err
		}
	}
	t.setPlanTracer(plan, t.tracerVersion)
	attempt.checkpoint.Engine = engineIdentity()
	attempt.checkpoint.Fingerprint, attempt.checkpoint.Node, attempt.checkpoint.FactsSHA256, attempt.checkpoint.Tracer, attempt.checkpoint.Plan = plan.Fingerprint, t.workflowNodeVersion(), attempt.factsHash, plan.Tracer, plan
	// Cache only successful selection, not failed preflight or a test verdict.
	if !options.Apply {
		attempt.checkpoint.Tracer = ""
	}
	// Inventory dependencies before anything runs; the checks share one owned
	// copy for this invocation.
	t.reuseCheckCopy = true
	defer func() { t.releaseCheckCopy() }()
	setup, err := t.lang().dependencyReadiness(t, ctx, scope)
	if err != nil {
		return fail("blocked_environment", "dependency_readiness_unavailable", err.Error(), "Restore the named local dependency check capability, then rerun next_command. No tests ran.")
	}
	if len(setup) > 0 {
		result.SetupCommands = setup
		result.NextCommand = setup[0]
		result.ResumeCommand = append(append([]string{}, attempt.recoveryCommand...), "--resume", attempt.checkpoint.RunID)
		result.RequiredInput = []string{"Run every setup_commands entry once, in order, from the repository root, then run resume_command."}
		result.Instruction = onboard.PrescribedEditInstruction
		return fail("needs_input", "project_dependencies_missing", fmt.Sprintf("dependency setup is missing for %d of the selected configurations", len(setup)), "Run every setup_commands entry (next_command is the first) from the repository root, then run resume_command. This readiness check installed nothing and ran no tests. Preserve the selected framework, CI test commands and scope.")
	}
	// A missing Git exclusion of the reports would fail the run only after
	// every configuration ran, so it is checked first. Every exclusion is
	// checked again after the run: a package includes reports only once
	// they exist, and npm falls back to .gitignore.
	if git := t.gitReportExclusions(ctx); git.Status != "passed" {
		blocker := "workflow " + git.Name + ": " + git.Status + " — " + reportText(git.Reason)
		result.RequiredInput = []string{blocker}
		result.Instruction = "Make only the ignore-file edit this check names, then run resume_command. It changes no test input; list it in your answer."
		return fail("needs_input", "report_exclusions_required", blocker, "No tests ran. Make only the named ignore-file edit, then run resume_command; validation runs once it passes.")
	}
	progress("validation")
	before := fingerprint
	if options.Apply {
		before, err = workflowFingerprint(ctx, t.repositoryRoot, name)
	}
	if err != nil {
		return fail("blocked_environment", "input_snapshot_unavailable", err.Error(), "Restore source input access.")
	}
	plan.Fingerprint = before
	attempt.checkpoint.Fingerprint = before
	if err := errors.Join(attempt.checkpoint.save(), attempt.checkpoint.saveFactsBinding()); err != nil {
		return fail("blocked_environment", "checkpoint_unavailable", err.Error(), "Restore private temporary storage.")
	}
	started := time.Now()
	attempt.started = started
	logs := io.Discard
	if options.Format == "verbose" {
		logs = output
	}
	if t.framework.Name() == "cypress" {
		// Each browser launch is expensive. Stop before the next one when an
		// external edit invalidates the original checkout snapshot.
		t.workflowSourceCheck = func(ctx context.Context) error {
			after, err := workflowFingerprint(ctx, t.repositoryRoot, name)
			if err != nil {
				return fmt.Errorf("verify repository snapshot before Cypress execution: %w", err)
			}
			if after != before {
				return errors.New("repository inputs changed during Cypress validation; remaining browser runs stopped. Finish all edits, including documentation and formatting, then rerun the full validation command")
			}
			return nil
		}
	}
	run := t.Run
	if t.workflowRun != nil {
		run = t.workflowRun
	}
	runErr := run(ctx, logs)
	usagetelemetry.ObserveTroubleshootingError(output, "test_execution", runErr)
	validation, err := readCurrentOnboardingResult(t.repositoryRoot, started)
	usagetelemetry.ObserveTroubleshootingError(output, "report_read", err)
	if err != nil {
		return fail("validation_failed", "validation_report_unavailable", fmt.Sprint(errors.Join(runErr, err)), "No current full report exists. Resolve this execution failure; old/check-only evidence cannot certify completion.")
	}
	attempt.executed = true
	plan.CIRuntime = validation.CIRuntime
	if validation.Selection != nil {
		t.setPlanTracer(plan, validation.Selection.Version)
	} else if t.lang().labelsTracerWithoutSelection() {
		t.setPlanTracer(plan, strings.TrimPrefix(validation.Tracer, t.lang().tracerPackage()+"@"))
	}
	var runtimeMetadata *platform.MetadataError
	if errors.As(runErr, &runtimeMetadata) {
		result.Capabilities = []string{workflowMetadataCapability(runtimeMetadata)}
		result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
		return fail("blocked_environment", runtimeMetadata.Code, runErr.Error(), "Resolve only the named registry/storage capability, then rerun next_command. The failed attempt remains incomplete in json_report.")
	}
	var localRuntime *localRuntimeError
	if errors.As(runErr, &localRuntime) {
		result.Capabilities = []string{localRuntime.Capability()}
		result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
		return fail("blocked_environment", "local_runtime_mismatch", runErr.Error(), "Provide only the named runtime: put an existing installation first on PATH (a version manager or a user-provided install), then run resume_command unchanged. Ask the user before downloading a runtime and never install one inside the repository. Do not change CI, the tracer or test scope to avoid this path.")
	}
	if workspace.FileDescriptorsExhausted(runErr) {
		result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
		return fail("blocked_environment", fileDescriptorsExhaustedCode, runErr.Error(), "The host ran out of open file descriptors while isolating the checkout, often because other heavy processes share it. Wait for or stop those processes, or raise the open-file limit, then run next_command unchanged. If ddtest already edited CI, next_command recognizes those edits and makes no further change before validating again; do not edit or align them by hand. This says nothing about the tests or the tracer; do not change CI, the tracer or test scope.")
	}
	var supervision *processSupervisionError
	if errors.As(runErr, &supervision) {
		result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
		if supervision.Code == "unsupported_detached_spawn" {
			return fail("unsupported", supervision.Code, supervision.Error(), "Preserve the original test command/configuration. This detached spawn cannot be supervised safely; it is not a pre-existing test failure or tracer comparison.")
		}
		result.Capabilities = []string{"local owned-process socket connections"}
		return fail("blocked_environment", supervision.Code, supervision.Error(), "Restore the named local process capability, then rerun next_command. The test comparison is unverified.")
	}
	if runErr != nil && validation.Success {
		result.JSONReport = validation.jsonReportPath(t.repositoryRoot)
		return fail("validation_failed", "execution_result_conflict", runErr.Error(), "Resolve the execution/report discrepancy before claiming completion.")
	}
	progress("verification")
	workflow := &WorkflowEvidence{ValidationCommand: attempt.recoveryCommand, Plan: plan, Fingerprint: before, Node: t.workflowNodeVersion(), Runtime: languageRuntime, FactsSHA256: attempt.factsHash, Changes: result.Changes}
	if attempt.checkpoint != nil {
		workflow.EarlierAttempts = slices.Clone(attempt.checkpoint.Failures)
		workflow.EarlierVaryingTests = slices.Clone(attempt.checkpoint.VaryingTests)
	}
	workflow.Checks = append(refreshScopeCheck(result.Checks, validation.Scope), t.verifyReportExclusions(ctx)...)
	after, snapshotErr := workflowFingerprint(ctx, t.repositoryRoot, name)
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
	finalizeErr := finishValidation(quietReportOutput{output}, t.repositoryRoot, validation)
	validation, err = readCurrentOnboardingResult(t.repositoryRoot, started)
	if err != nil {
		return fail("blocked_environment", "report_invalid", fmt.Sprint(errors.Join(finalizeErr, err)), "Restore report access; preserve execution evidence.")
	}
	if finalizeErr != nil && validation.Summary.Status == "COMPLETE LOCALLY" {
		return fail("blocked_environment", "report_write_failed", finalizeErr.Error(), "Restore report output access before claiming completion.")
	}
	return attempt.complete(t, validation, workflow, before)
}

// withWorkflowState carries the workflow's scope, plan, run and selected site
// from current into t, a drive prepared again for another tracer release.
func (t *Testdrive) withWorkflowState(current *Testdrive) *Testdrive {
	t.workflowScope, t.workflowPlan, t.workflowRoot, t.workflowRun = current.workflowScope, current.workflowPlan, current.workflowRoot, current.workflowRun
	t.selectedSite = current.selectedSite
	return t
}

func (t *Testdrive) workflowNodeVersion() string {
	if t.nodeVersion == nil {
		return ""
	}
	return t.nodeVersion()
}

// scopeCheck records that ready configurations proceeded around unresolved CI.
// The summary lists the entries once, as scope blockers, not again as a check.
func scopeCheck(unresolved []string) onboard.WorkflowCheck {
	return onboard.WorkflowCheck{Name: "ci_scope", Status: onboard.StatusInconclusive, Reason: "Ready configurations proceed; unresolved CI is reported and not validated locally (setup edits a CI step only where it recognizes the test command): " + strings.Join(unresolved, "; ")}
}

// refreshScopeCheck re-evaluates the pre-setup ci_scope check against the scope
// discovered after setup: ddtest's own edits, such as a Windows guard, can
// resolve entries that blocked before them.
func refreshScopeCheck(checks []onboard.WorkflowCheck, scope *onboard.ValidationScope) []onboard.WorkflowCheck {
	checks = slices.Clone(checks)
	for i, check := range checks {
		if check.Name != "ci_scope" || scope == nil {
			continue
		}
		if len(scope.Unresolved) == 0 {
			checks[i] = onboard.WorkflowCheck{Name: "ci_scope", Status: "passed", Reason: "No unresolved CI remains after setup; every discovered entry is validated, excluded or reported as a caveat."}
		} else {
			checks[i] = scopeCheck(scope.Unresolved)
		}
	}
	return checks
}

// setPlanTracer records the selected tracer and recomputes the known
// limitations, which depend on it: a release with a tracer fix lifts them.
// manualCITracerPrefix starts the sentence naming the tracer release manual
// CI edits pin.
const manualCITracerPrefix = " For manual CI edits use "

// scopeUnresolvedNeeds sends unresolved CI to the manual instructions,
// keeping the tracer release every CI Node runs that prior named for them.
func scopeUnresolvedNeeds(prior, site string) string {
	needs := "Run next_command for the unsupported CI integration, then validate the discovered commands with testdrive. Keep unresolved CI explicitly unvalidated and report it. " + onboard.ScopeBlockerInstruction + " Setup facts cannot resolve CI parsing or shrink scope; do not guess a facts schema, inspect the binary, or retry unchanged --apply."
	if _, tracer, found := strings.Cut(prior, manualCITracerPrefix); found {
		needs += manualCITracerPrefix + tracer
	}
	return needs + manualSiteSentence(site)
}

// manualSiteSentence names the selected site for CI setup written by hand
// from onboard --instructions, which shows the default site; empty for that
// default.
func manualSiteSentence(site string) string {
	if setting := onboard.CIProviderSiteSetting(site); setting != "" {
		return " In CI setup written by hand, use " + setting + " that onboard --instructions shows."
	}
	return ""
}

func (t *Testdrive) setPlanTracer(plan *onboard.ExecutionPlan, tracer string) {
	plan.Tracer = tracer
	plan.KnownLimitations = installedFeatureLimitations(t.repositoryRoot, plan.Framework, tracer)
}

// isScopeBlocker reports a blocker for CI scope ddtest could not resolve.
func isScopeBlocker(check string) bool {
	return strings.HasPrefix(check, "unresolved CI scope")
}
