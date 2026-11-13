// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
)

type StartOptions struct {
	Format              string
	Site                string
	Tracer              string
	Facts               string
	Platform            string
	Command             string
	Human               bool
	Timeout             string
	AllowReviewedBuilds bool
	// AcceptSubstituteRuntime is forwarded to the onboarding handoff.
	AcceptSubstituteRuntime bool
	// SetupApplied reports that onboard --apply just wrote the CI
	// provider's plugin setup: the rest of the language's setup comes first,
	// and the facts to confirm then come with validation.
	SetupApplied bool
}

func Start(output io.Writer) error { return StartWithOptions(output, StartOptions{}) }

// literalSite reports a site the handoff accepts: empty, or a literal
// Datadog hostname.
func literalSite(site string) bool {
	return strings.Trim(site, "abcdefghijklmnopqrstuvwxyz0123456789.-") == ""
}

func StartWithOptions(output io.Writer, options StartOptions) error {
	fail := func(code string, err error) error {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: code, Reason: err.Error(), NextAction: "supply_input"}))
	}

	detected, err := platform.DetectPlatform()
	if err != nil {
		return fail("platform_unresolved", err)
	}
	frameworkName, err := onboardingFramework(detected)
	if err != nil {
		return fail("framework_unresolved", err)
	}
	if root, err := os.Getwd(); err == nil {
		if err := CheckCIProviderSupport(root, detected.Name(), frameworkName); err != nil {
			var choice *CIProviderChoice
			var unsupported *CIProviderUnsupportedError
			switch {
			case errors.As(err, &choice):
				return errors.Join(err, WriteAgentResult(output, ProviderChoiceResult(choice)))
			case errors.As(err, &unsupported) && options.Human:
				observeAgentResult(output, ProviderUnsupportedResult(unsupported))
				return err
			case errors.As(err, &unsupported):
				return errors.Join(err, WriteAgentResult(output, ProviderUnsupportedResult(unsupported)))
			default:
				return fail("ci_discovery", err)
			}
		}
	}
	if discoveryLanguageNamed(detected.Name()) == nil {
		if options.Format != "" {
			err := fmt.Errorf("controlled validation is not available for %s", detected.Name())
			return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "unsupported", Phase: "onboarding", Code: "framework_unsupported", Reason: err.Error(), NextAction: "report_result"}))
		}
		return Run(output)
	}
	if options.Command != "" {
		// onboard --apply validates every discovered configuration, which rejects
		// a single-command override; never hand off a command that cannot run.
		err := fmt.Errorf("--command selects one suite, but onboarding applies and validates every discovered CI configuration")
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "invalid_options", Reason: err.Error(), NextAction: "supply_input", RequiredInput: []string{"Omit --command to onboard every discovered configuration, or validate one suite with testdrive --framework " + frameworkName + " --command <command>."}}))
	}
	selected, err := SelectTracerRelease(detected.Name(), options.Tracer)
	if err != nil {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "invalid_options", Reason: err.Error(), NextAction: "supply_input"}))
	}
	options.Tracer = selected
	if root, err := os.Getwd(); err == nil {
		// Only the user can tell a CI job's runtime its configuration doesn't
		// name; ask before any setup is prescribed for it.
		if required := RequiredCIRuntimes(context.Background(), root, detected.Name(), frameworkName); required != nil {
			if options.Human {
				observeAgentResult(output, CIRuntimeRequiredResult(required))
				return fmt.Errorf("%w Rerun with --ci-runtime %s=<version> (or <job>:%s=<version> per job) once you know it", required, required.Flag, required.Flag)
			}
			return errors.Join(required, WriteAgentResult(output, CIRuntimeRequiredResult(required)))
		}
		// Facts only the CI server shows come next, once the setup is there.
		if required := RequiredCIConfirmations(root, detected.Name(), frameworkName); required != nil && (!options.SetupApplied || discoveryLanguageNamed(detected.Name()).ciSetupConfigured(root, frameworkName, options.Site)) {
			if options.Human {
				observeAgentResult(output, CIConfirmationRequiredResult(required))
				return fmt.Errorf("%w Rerun with --ci-confirm <fact>[=<value>] for each fact once you know it", required)
			}
			return errors.Join(required, WriteAgentResult(output, CIConfirmationRequiredResult(required)))
		}
	}
	automatic := discoveryLanguageAutomaticSetup(detected.Name())
	// A hand-written setup onboard --apply would refuse as incomplete.
	var handWritten *handWrittenGap
	if root, err := os.Getwd(); err == nil && automatic {
		if provider, err := currentCIProvider(root); err == nil && !provider.sharedSyntax() {
			// onboard --apply checks the edit against the site it reports to,
			// DD_SITE by default; one it would refuse is left to the
			// instructions, which name the problem.
			site := options.Site
			if site == "" {
				site = os.Getenv("DD_SITE")
			}
			automatic = providerAutomaticSetup(root, provider, discoveryLanguageNamed(detected.Name()), frameworkName, site)
			if automatic && !options.Human {
				handWritten = handWrittenSetupGap(root, frameworkName, site)
			}
		}
	}
	if !automatic && options.Site == "" {
		// Like onboard --apply, DD_SITE is the default site. The agent writes
		// it into the action and the validation command records it.
		options.Site = os.Getenv("DD_SITE")
	}
	if !automatic && !literalSite(options.Site) {
		err := fmt.Errorf("site must be a literal Datadog hostname: %q", options.Site)
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "invalid_options", Reason: err.Error(), NextAction: "supply_input"}))
	}
	command := OnboardingCommand(frameworkName)
	// The CI provider's plugin setup onboard --apply writes for a language
	// whose own setup the agent applies.
	apply := OnboardingCommand(frameworkName)
	if !automatic {
		// The agent applies CI setup by hand; validation runs through the coordinator.
		command = DDTestCommand("testdrive", "--framework", frameworkName, "--all", "--agent", "--yes")
	}
	if len(command) == 0 {
		return errors.Join(fmt.Errorf("%s", TransientExecutableReason), WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "onboarding", Code: "transient_executable", Reason: TransientExecutableReason, NextAction: "supply_input"}))
	}
	handoffFormat := options.Format
	if handoffFormat == "" && !options.Human {
		handoffFormat = "agent"
	}
	for _, flag := range []struct{ name, value string }{{"format", handoffFormat}, {"site", options.Site}, {"tracer-version", options.Tracer}, {"facts", options.Facts}, {"platform", options.Platform}, {"command", options.Command}, {"timeout", options.Timeout}} {
		// --agent already selects the agent format of the validation command.
		if flag.value != "" && (automatic || flag.name != "format" || flag.value != "agent") {
			command = append(command, "--"+flag.name, flag.value)
		}
		if flag.value != "" {
			apply = append(apply, "--"+flag.name, flag.value)
		}
	}
	if options.AllowReviewedBuilds {
		command = append(command, "--allow-reviewed-builds")
		apply = append(apply, "--allow-reviewed-builds")
	}
	if options.AcceptSubstituteRuntime {
		command = append(command, "--accept-substitute-runtime")
		apply = append(apply, "--accept-substitute-runtime")
	}
	notice := ExecutableFallbackNotice()
	if !automatic || handWritten != nil {
		// The job keeps its hand-written setup: the agent completes it, then
		// resumes with command, which onboard --apply then accepts.
		return manualSetupHandoff(output, options, detected.Name(), frameworkName, command, apply, notice, handWritten)
	}
	if options.Human {
		text := humanOnboardingHandoff(frameworkName, command)
		if notice != "" {
			text += notice + "\n"
		}
		_, err := io.WriteString(output, text)
		return err
	}
	return WriteAgentResult(output, AgentResult{
		Status: "ready", Phase: "onboarding", NextAction: "run_command", NextCommand: command,
		Capabilities: validationCapabilities,
		Reason:       strings.TrimSpace(foregroundRunInstruction + " " + AgentScope + " " + validationWaitInstruction + " ddtest owns tracer selection, recognized CI edits, testdrive, package exclusions and cleanup. It returns exact blockers for unsupported setup. No Datadog key is required locally. Do not write CI or launch a parallel preflight first. " + notice),
	})
}

// manualSetupHandoff starts onboarding for a language whose CI setup the
// agent writes by hand. Once every discovered test step runs the Datadog
// action, it hands off validation; until then it returns the setup
// instructions with validation as resume_command. People get the guidance.
// handWrittenGap, when set, is what onboard --apply refuses in a CI job's
// hand-written setup, where it writes the provider's setup itself: the
// handoff asks for the edit that completes it, keeping it, with command
// (onboard --apply) as resume_command.
func manualSetupHandoff(output io.Writer, options StartOptions, language, framework string, command, apply []string, notice string, handWrittenGap *handWrittenGap) error {
	if options.Human {
		return Run(output)
	}
	root, err := os.Getwd()
	if err != nil {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "blocked_environment", Phase: "discovery", Code: "repository_unavailable", Reason: err.Error(), NextAction: "request_capability"}))
	}
	setup := "Datadog setup action"
	if provider, err := currentCIProvider(root); err == nil && !provider.sharedSyntax() {
		setup = setupProviderNames(testJobSetups(root, provider, language, framework))
	}
	// Jobs that set up the tracer by hand keep that setup.
	handWritten := handWrittenSetupNote(root, language, framework, options.Site) != ""
	configured := "CI already runs the " + setup + " for every discovered " + framework + " test step. "
	if handWritten {
		configured = "CI already sets up Test Optimization for every discovered " + framework + " test step, with the " + setup + " or a complete hand-written setup. "
	}
	if registered := discoveryLanguageNamed(language); handWrittenGap == nil && registered != nil && registered.ciSetupConfigured(root, framework, options.Site) {
		if required := RequiredCIEdits(context.Background(), root, language, framework, false); required != nil {
			// The setup is there, but a test command drops what it sets.
			result := CIEditRequiredResult(required, command)
			result.Capabilities = validationCapabilities
			result.Reason += " " + AgentScope + " " + notice
			return errors.Join(required, WriteAgentResult(output, result))
		}
		owns := " ddtest owns testdrive, report-exclusion checks and cleanup, and returns exact blockers for unsupported setup."
		if !registered.coordinated() {
			// Standalone validation checks no report exclusions.
			owns = " Before next_command, " + strings.ToLower(GitReportExclusion[:1]) + GitReportExclusion[1:] + " ddtest owns testdrive and cleanup, and returns exact blockers for unsupported setup. It returns one structured result: when it asks the user (next_action ask_user), stop and ask them; finish by rendering its final_response unchanged. " + StandaloneVerdictInstruction(framework)
		}
		return WriteAgentResult(output, AgentResult{
			Status: "ready", Phase: "onboarding", NextAction: "run_command", NextCommand: command,
			Capabilities: validationCapabilities,
			Reason:       strings.TrimSpace(configured + foregroundRunInstruction + " " + AgentScope + " " + validationWaitInstruction + owns + " No Datadog key is required locally. Do not launch a parallel preflight first. " + notice),
		})
	}
	scope, err := DiscoverValidationScope(root, framework)
	if err != nil {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "scope_unresolved", Reason: err.Error(), NextAction: "supply_input"}))
	}
	var unresolved []string
	for _, item := range scope.Unresolved {
		unresolved = append(unresolved, OneLineExcerpt(item))
	}
	if len(scope.Commands) == 0 && len(unresolved) > 0 {
		// Nothing would be validated: a CI setup handoff would only end in
		// blockers after a full run. The user decides the scope first.
		err := fmt.Errorf("no %s CI configuration can be validated", framework)
		return errors.Join(err, WriteAgentResult(output, AgentResult{
			Status: "needs_input", Phase: "discovery", Code: "scope_decision_required", NextAction: "supply_input",
			Location:      strings.Join(unresolved, "; "),
			RequiredInput: []string{"User decision for the CI steps in location: approve an explicit " + framework + " entry point in CI (for example its own step that runs only " + framework + "), then rerun this onboard command; or stop and report these steps as not validated."},
			Reason:        "ddtest cannot tell which " + framework + " tests these CI steps run, so it has no configuration to validate locally and will not prescribe CI setup for them. Stop here: report these steps and the options in required_input to the user, and continue only with their decision. " + ScopeBlockerInstruction + " Also, do not combine --command with --all: a single command cannot stand in for a CI step. " + AgentScope + " " + notice,
		}))
	}
	if !options.SetupApplied && pluginSetupWritable(root, language, framework, options.Site) {
		// ddtest writes the provider's part of the setup itself.
		err := fmt.Errorf("CI setup is required before validation")
		return errors.Join(err, WriteAgentResult(output, AgentResult{
			Status: "needs_input", Phase: "onboarding", Code: "ci_setup_required", NextAction: "run_command", NextCommand: apply,
			Reason: strings.TrimSpace("CI does not yet run the " + setup + " before every discovered " + framework + " test step, and onboard --apply writes it: run next_command once in the foreground and wait for its exit. It edits only the CI configuration, lists the changes, and returns the rest of the setup to apply and the validation command; it runs no tests. Do not edit the CI configuration yourself first. " + AgentScope + " " + notice),
		}))
	}
	var guidance strings.Builder
	if err := run(&guidance, &agentHandoff{site: options.Site, handWritten: handWrittenGap != nil}); err != nil {
		return errors.Join(err, WriteAgentResult(output, AgentResult{Status: "needs_input", Phase: "discovery", Code: "scope_unresolved", Reason: err.Error(), NextAction: "supply_input"}))
	}
	missing := "CI does not yet run the " + setup + " before every discovered " + framework + " test step. "
	if handWritten {
		missing = "CI does not yet set up Test Optimization completely before every discovered " + framework + " test step. Jobs that set it up by hand keep their setup and need only the changes agent_instruction lists for them; do not add the " + setup + " to them. "
	}
	reason := missing + "Apply only the CI setup in agent_instruction, then run resume_command once in the foreground with the listed capabilities and wait for its exit: it validates every discovered configuration and returns the next action. Do not run testdrive, a configuration check or the tests yourself first. Do not use nohup, shell background jobs, sleeps or process-name polling. If the host returns a task/session ID, wait with that host tool. "
	if options.SetupApplied {
		reason = "onboard --apply wrote the " + setup + " (changes, setup_diff); do not edit it again. Apply only the rest of the CI setup in agent_instruction, then run resume_command once in the foreground with the listed capabilities and wait for its exit: it validates every discovered configuration and returns the next action. Do not run testdrive, a configuration check or the tests yourself first. Do not use nohup, shell background jobs, sleeps or process-name polling. If the host returns a task/session ID, wait with that host tool. "
	}
	problems := existingSetupProblems(root, language, framework, options.Site)
	if handWrittenGap != nil && !slices.ContainsFunc(problems, func(problem string) bool {
		_, text, _ := strings.Cut(problem, ": ")
		return strings.HasPrefix(handWrittenGap.Reason, text)
	}) {
		// What onboard --apply refuses, word for word, when no other check
		// names it.
		problems = append(problems, handWrittenGap.Error())
	}
	if len(problems) > 0 {
		reason += "The existing setup needs changes; fix it as the instructions show: " + strings.Join(problems, " ") + " "
	}
	if refusal := pluginSetupRefusal(root, framework); refusal != "" {
		reason += "onboard --apply cannot write this setup (" + refusal + "), so apply it from the instructions. "
	} else if edit := pluginSetupEditFirst(root, framework); edit != "" && !discoveryLanguageAutomaticSetup(language) {
		reason += "onboard --apply writes the CI configuration's part of this setup once one edit is made (" + edit + "): make it as the instructions show, then rerun onboard --agent. "
	}
	if required := RequiredCIEdits(context.Background(), root, language, framework, true); required != nil {
		reason += "With the setup, make exactly these edits to the test commands: " + strings.Join(ciEditList(required), " ") + " "
	}
	if len(unresolved) > 0 {
		reason += fmt.Sprintf("%d CI step(s) listed as unresolved scope in agent_instruction cannot be validated and will remain blockers. They do not hold up this setup: first apply the CI setup and run resume_command, then report each such step and its scope decision to the user with the validation result. %s ", len(unresolved), ScopeBlockerInstruction)
	}
	var notes []string
	for _, entry := range scope.Commands {
		for _, note := range entry.StepScopeNotes {
			if !slices.Contains(notes, note) {
				notes = append(notes, note)
				reason += "Shared CI step: " + note + " "
			}
		}
	}
	reason += AgentScope + " " + notice
	err = fmt.Errorf("CI setup is required before validation")
	return errors.Join(err, WriteAgentResult(output, AgentResult{
		Status: "needs_input", Phase: "onboarding", Code: "ci_setup_required", NextAction: "apply_source_edit", ResumeCommand: command,
		Capabilities: validationCapabilities, Instruction: strings.TrimSpace(guidance.String()), Reason: strings.TrimSpace(reason),
	}))
}

// validationCapabilities are what a local validation run needs from the host.
var validationCapabilities = []string{"registry metadata and package download network access", "local process execution and loopback listeners", "private temporary storage"}

// foregroundRunInstruction keeps validation a single foreground process.
const foregroundRunInstruction = "Run next_command once in the foreground with the listed capabilities, then wait for its exit. Do not use nohup, shell background jobs, sleeps or process-name polling. If the host returns a task/session ID, wait with that host tool."

// validationWaitInstruction keeps the checkout unchanged while validation runs.
const validationWaitInstruction = "Finish any edits a result asks for before launching validation. While it runs, only wait: do not edit repository files or run parallel cleanup/checks. Edits invalidate the snapshot and require another full run."

// AgentScope bounds what a coding agent changes during onboarding. Trials
// showed agents editing builds, manifests and test names to satisfy checks,
// and stopping a whole run to ask before an install ddtest itself prescribed.
const AgentScope = "Edit CI workflow and ignore files only where a result asks for it. " + PrescribedEditInstruction + " Any edit ddtest did not prescribe needs the user's approval first. Never rename tests, rewrite CI or package scripts so ddtest can parse them, retry variants of a CI edit until ddtest accepts one, disable an instrumentation ddtest did not prescribe disabling, or download a runtime; report the blocker instead. CI rows below Node 18, the oldest dd-trace major ddtest supports, are skipped by ddtest itself and reported as not tried; that needs no user approval. Do not inspect the ddtest binary. " + ReadWholeInstruction

// ScopeBlockerInstruction tells a coding agent what an unresolved CI scope
// entry asks of it. Trials showed agents rewriting a workflow condition in a
// loop, and editing whitespace or a variable, until ddtest accepted a step.
const ScopeBlockerInstruction = "Unresolved CI scope is a limit of ddtest's static analysis, not a defect to fix: report each such step and its options to the user (run the tests in a CI step ddtest can read, once the user approves that change, or keep the step reported as not validated). Do not edit CI configuration (steps, conditions, variables, error handling such as continue-on-error, or whitespace) to make ddtest accept a step unless a result prescribes that exact edit, and do not try variants until one is accepted."

// PrescribedEditInstruction lets the agent make the exact non-CI edits ddtest
// needs for validation (dev dependencies, package scripts, coverage config)
// without stopping to ask: the final report lists them with a revert.
const PrescribedEditInstruction = "Make the edits ddtest prescribes for validation, in a result or in onboard --instructions (dev dependencies through the exact install command, package scripts, coverage config and coverage.edit), exactly as given and without asking: do not modify or extend them. The final report lists every change outside CI and ignore files with a revert command."

// ReadWholeInstruction repeats in every result: trials truncated results with
// head/tail and missed the next step.
const ReadWholeInstruction = "Each result is one bounded JSON document: read it whole, without head, tail or other truncation."

// FinalResponseInstruction keeps the agent's answer factual: trials showed
// paraphrases turning an incomplete verdict into a completion claim.
const FinalResponseInstruction = "Begin your answer with final_response exactly, as its first block, and add context only after it. When it starts with NOT COMPLETE, do not call onboarding complete or successful, and do not record that result as expected or final in notes or memory."

// humanOnboardingHandoff points at the preview, which writes nothing, before
// the command that applies the edits and runs local validation.
func humanOnboardingHandoff(framework string, command []string) string {
	preview := slices.DeleteFunc(slices.Clone(command), func(arg string) bool { return arg == "--yes" })
	return fmt.Sprintf("Detected %s. Preview the recognized CI setup first; this writes nothing:\n  %s\nThen apply it and validate it locally:\n  %s\n", framework, shellquote.Join(preview...), shellquote.Join(command...))
}
