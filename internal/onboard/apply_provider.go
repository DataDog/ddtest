// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"
)

// planProviderJavaScriptSetup plans the JavaScript setup on a CI provider
// without the shared workflow syntax: the provider's setup step before the
// first test command of every job that runs framework's tests, with the
// tracer pinned to version and reporting to site. Jobs whose existing setup
// is already complete keep it; a different one is left for review.
func planProviderJavaScriptSetup(root string, provider ciProvider, workflows []ciWorkflow, framework, version, site, nodeRequirement string) (*SetupPlan, error) {
	language := javascriptDiscovery{}
	if !provider.editsSetup() {
		return nil, &SetupGap{provider.displayName(), "onboard --apply does not edit the " + provider.displayName() + " configuration. Add the " + provider.setupName() + " from onboard --agent instructions, then validate with testdrive --all."}
	}
	// Cucumber and WebdriverIO load the tracer only in their package-script
	// leaves, as on GitHub Actions: the scripts consume the scoped variable
	// the setup's following command exports (see providerScopedCommand).
	scoped := scopedEnvironment(framework)
	edits, _, err := scopedSetupEdits(root, framework, true)
	if err != nil {
		return nil, err
	}
	impact, coverageAdded := scopedSetupImpact(edits, framework)
	options := scopedProviderOptions(root, framework)
	request := language.ciSetupRequest(root, framework, site, provider.setupTerms())
	request.TracerVersion, request.Framework = version, framework
	var targets, unsupported []ciSetupTarget
	configured := 0
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			location := workflow.Path + " / " + name
			// The job's own setup mechanism, where the provider has several.
			provider, request := setupProviderFor(provider, job), request
			if job.SetupProvider != nil {
				request = language.ciSetupRequest(root, framework, site, provider.setupTerms())
				request.TracerVersion, request.Framework = version, framework
			}
			// A CI server plugin's setup is one option the provider merges
			// into, so one that differs is completed instead of left for
			// review.
			plugin := provider.setupTerms().Plugin
			first, setupIndex := -1, -1
			var resolution commandResolution
			var tests []TestCommand
			// Steps that run the tests, and setups whose exports reach only
			// the later commands of their own shell (see ciSetup.shellLocal),
			// which each shell that runs the tests needs.
			var testSteps, shellSetups []int
			// Direct invocations (no package script) of a scoped framework.
			var direct []directLeaf
			var matched []matchedTestStep
			for i, step := range job.Steps {
				if setup, ok := datadogSetup(step); ok {
					if languages := setup.Inputs["languages"]; strings.Contains(languages, "$") {
						return nil, &SetupGap{location, "The existing " + provider.setupName() + " selects its languages from " + languages + ", which ddtest cannot resolve; review it manually instead of adding a second setup."}
					}
					if selected, err := setupSelectsLanguage(setup, nil, request.Language); err == nil && selected {
						// A shell-local setup comes too late only for a test
						// command of its own shell; another shell needs its own.
						late := first >= 0
						if setup.shellLocal {
							late = slices.ContainsFunc(testSteps, func(test int) bool { return sameShell(job.Steps[test], step) })
							shellSetups = append(shellSetups, i)
						}
						if late {
							return nil, &SetupGap{location, "The " + provider.setupName() + " runs after the first " + framework + " test command; move it before the tests or review it manually without adding a second one."}
						}
						if !setup.shellLocal || first < 0 {
							setupIndex = i
						}
					}
				}
				current := resolveTestStep(root, workflow, job, step, "javascript", framework)
				if !current.Matched {
					continue
				}
				// Every selected test command decides the loader and coverage.
				tests = append(tests, current.Tests...)
				testSteps = append(testSteps, i)
				matched = append(matched, matchedTestStep{step, current.Tests})
				for _, test := range current.Tests {
					if scoped != "NODE_OPTIONS" && len(test.packageScripts) == 0 {
						direct = append(direct, directLeaf{step, test})
					}
				}
				if first < 0 {
					first, resolution = i, current
				}
			}
			if first < 0 {
				continue
			}
			// Shell-local setups count only in the shell they run in: one in
			// a shell without tests sets up nothing, and a shell that runs
			// the tests without one still needs it.
			uncovered := false
			if len(shellSetups) > 0 {
				live := slices.DeleteFunc(slices.Clone(shellSetups), func(setup int) bool {
					return !slices.ContainsFunc(testSteps, func(test int) bool { return test > setup && sameShell(job.Steps[setup], job.Steps[test]) })
				})
				if setupIndex >= 0 && !sameShell(job.Steps[setupIndex], job.Steps[first]) {
					setupIndex = -1
				}
				uncovered = slices.ContainsFunc(testSteps, func(test int) bool {
					return !slices.ContainsFunc(live, func(setup int) bool { return setup < test && sameShell(job.Steps[setup], job.Steps[test]) })
				})
				shellSetups = live
			}
			if resolution.OtherFramework && scoped == "NODE_OPTIONS" {
				return nil, &SetupGap{location, "The test command also runs another framework; initialize only the selected framework explicitly."}
			}
			// A job that sets up the tracer by hand keeps that setup.
			var handWritten *handWrittenSetup
			if setupIndex < 0 {
				handWritten = jobHandWrittenSetup(root, workflow, job, first, "javascript", framework, site)
			}
			inherited := []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"}
			if scoped != "NODE_OPTIONS" && setupIndex < 0 {
				inherited = append(inherited, scoped)
			}
			if handWritten != nil {
				inherited = nil
			}
			// Every test step's own variables count: a later one can override
			// what the first one inherits.
			layers := []map[string]string{workflow.Env, job.Env}
			for _, step := range matched {
				layers = append(layers, step.step.Env)
			}
			for _, layer := range layers {
				for _, key := range inherited {
					if _, ok := layer[key]; ok {
						return nil, &SetupGap{location, "Custom inherited instrumentation requires review: " + key}
					}
				}
			}
			// The runtime the tests run on: the image's, or one a modeled
			// installer selects before them.
			node, _ := jobBaseNode(job, nil)
			if selected, err := rowSetupNodeBefore(root, workflow, job, nil, first); err == nil {
				node = selected
			}
			if UnsupportedNode(node) {
				continue // No tracer release runs here; validation reports it as not tried.
			}
			if plugin && node != "" && job.SetupUnsupported == "" && handWritten == nil && version != snippetProbeRelease {
				// The plugin installs the latest dd-trace whatever the Node. A
				// probe (see handWrittenSetupGap) reads only the repository.
				if err := floatingNodeGap(root, provider, workflow, job, location, node); err != nil {
					return nil, err
				}
			}
			esm := len(request.After) > 0
			if framework == "mocha" {
				esm = false
				for _, command := range tests {
					esm = esm || command.mochaESM
					if !command.mochaCoverageSettled() && (setupIndex < 0 || uncovered) {
						// The GitHub Actions editor wraps the command with NYC; this
						// editor only inserts the setup, so stop before any edit.
						next := "then rerun onboard --apply, which adds the Datadog setup."
						if handWritten != nil {
							next = "then rerun onboard --apply; the job's hand-written setup stays."
						}
						return nil, &EditThenApplyGap{SetupGap{location, MochaCoverageGap + command.Command + ". Wrap it with npx --no-install nyc -- in CI (adding nyc as a dev dependency if the project does not declare it), " + next}}
					}
				}
			}
			if node != "" && (nodeRequirement != "" || esm) {
				// The tracer's Node range, and for the ESM loader Node's module
				// register API, resolving floating image tags such as node:20.
				requirement := nodeRequirement
				if requirement == "" {
					requirement = ">=0.0.0"
				}
				if _, err := setupRuntimeCondition(root, workflow, job, requirement, esm); err != nil {
					var runtimeGap *NodeRuntimeGap
					if errors.As(err, &runtimeGap) {
						runtimeGap.Location, runtimeGap.Requirement = location, nodeRequirement
						return nil, runtimeGap
					}
					return nil, &SetupGap{location, err.Error()}
				}
			}
			// A direct invocation reads the scoped variable the setup's
			// following command exports only through the scoped prefix, which
			// the CI command itself must carry; a setup that leaves the preload
			// in NODE_OPTIONS, or a CI server plugin's, reaches it without one.
			for _, leaf := range direct {
				if _, exported := leaf.step.Env[scoped]; handWritten != nil || provider.setupTerms().Plugin || setupIndex >= 0 && !exported {
					continue
				}
				if framework == "webdriverio" {
					if local, err := localWDIOLeaf(root, leaf.test.Directory, leaf.test.Command); err != nil {
						return nil, &SetupGap{location, err.Error()}
					} else if !local {
						continue // Explicit browser runner remains outside instrumentation.
					}
				}
				command, err := directScopedCommand(leaf.step, leaf.test, framework)
				if err != nil {
					return nil, directScopedGap(provider, location, leaf.step, err)
				}
				if command != "" {
					return nil, &EditThenApplyGap{SetupGap{directLeafLocation(location, leaf.step), directLeafReason(provider, framework, leaf.test.Command, command, setupIndex >= 0)}}
				}
			}
			if handWritten != nil {
				if problem := handWritten.problem(); problem != "" {
					return nil, &handWrittenGap{SetupGap{location, problem + " Then rerun onboard --apply."}}
				}
				if framework == "cucumber" {
					// A direct invocation needs NYC in the CI command itself.
					if gap := directCucumberCoverageGap(provider, location, workflow, job, direct, "the job's hand-written setup stays."); gap != nil {
						return nil, gap
					}
				}
				configured++
				continue
			}
			target := ciSetupTarget{Workflow: workflow.Path, Job: name, Step: first}
			if framework == "mocha" && !esm {
				target.After = []string{}
			}
			existing := shellSetups
			if len(existing) == 0 && setupIndex >= 0 {
				existing = []int{setupIndex}
			}
			complete := len(existing) > 0
			for _, index := range existing {
				setup, _ := datadogSetup(job.Steps[index])
				problem := provider.setupProblem(setup, request.TracerInput, func(value string) bool { return value == version }, version, site)
				switch {
				case plugin && (problem != "" || esm):
					// The provider completes the option and adds the commands
					// that follow it, keeping what is already there.
					complete = false
				case problem != "":
					return nil, &SetupGap{location, "Existing Datadog setup differs: " + problem + " Otherwise review it manually without adding a second setup."}
				}
			}
			if complete && len(shellSetups) == 0 {
				// Each test step either reads the preload from the scoped variable
				// the setup's following command exports, which a later command
				// must not replace, or still finds it in NODE_OPTIONS, which
				// reaches the scoped scripts; only the options the scoped variable
				// adds, such as the Istanbul ESM loader, would be missing then.
				var unexported []matchedTestStep
				if scoped != "NODE_OPTIONS" && !plugin {
					for _, step := range matched {
						if _, exported := step.step.Env[scoped]; !exported {
							unexported = append(unexported, step)
						} else if !scopedPreloadReached(workflow, job, step.step, scoped) {
							return nil, &SetupGap{directLeafLocation(location, step.step), "Custom inherited instrumentation requires review: " + scoped + " no longer carries the tracer's preload the " + provider.setupName() + " exports in this test step, so its tests would run untraced. Remove the command that replaces it, or review it manually without adding a second setup."}
						}
					}
				}
				if len(unexported) > 0 {
					if gap := existingScopedSetupGap(provider, workflow, job, unexported, framework, options, request.After[0]); gap != "" {
						return nil, &SetupGap{location, gap}
					}
					// The preload reaches a direct command without the scoped
					// prefix, but NYC must still wrap Cucumber there.
					if framework == "cucumber" {
						var bare []directLeaf
						for _, leaf := range direct {
							if _, exported := leaf.step.Env[scoped]; !exported {
								bare = append(bare, leaf)
							}
						}
						if gap := directCucumberCoverageGap(provider, location, workflow, job, bare, "the job's "+provider.setupName()+" stays."); gap != nil {
							return nil, gap
						}
					}
				}
				configured++
				continue
			}
			// Shell-local setups are the provider's to plan shell by shell:
			// it adds the setup to each test shell without one and checks
			// every shell, with the commands that follow the setup, before
			// writing anything; a complete job keeps its configuration.
			if job.SetupUnsupported != "" {
				// The provider reports it; it decides the plan only when no
				// other job runs the tests.
				unsupported = append(unsupported, target)
				continue
			}
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 && configured == 0 {
		targets = unsupported
	}
	if len(targets) == 0 && configured == 0 {
		return nil, &SetupGap{provider.displayName(), "No literal " + framework + " CI step found. Identify its real CI entry point before applying onboarding."}
	}
	if len(targets) > 0 {
		ciEdits, ciImpact, err := provider.planSetupEdits(root, workflows, targets, request)
		if err != nil {
			return nil, err
		}
		edits, impact = append(edits, ciEdits...), append(impact, ciImpact...)
	}
	var extra []string
	if ignorer, ok := provider.(ciSetupIgnorer); ok {
		extra = ignorer.setupIgnores(edits)
	}
	ignored, err := ignoreSetupEdits(root, coverageAdded, extra...)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		// Configured CI needs no ignore-only edit to be complete.
		return &SetupPlan{root: root, impact: impact}, nil
	}
	if coverageAdded {
		impact = append(impact, nycOutputImpact)
	}
	return &SetupPlan{root: root, edits: append(edits, ignored...), impact: impact}, nil
}

// ciSetupIgnorer is a provider whose setup leaves files in the CI
// workspace, which the repository's .gitignore keeps untracked.
type ciSetupIgnorer interface {
	// setupIgnores are the .gitignore entries for what edits set up.
	setupIgnores(edits []setupEdit) []string
}

// floatingNodeGap refuses a CI server plugin's setup for a job whose Node
// the latest dd-trace, which the plugin installs on every build, does not
// support: no other release can be selected.
func floatingNodeGap(root string, provider ciProvider, workflow ciWorkflow, job runtimeJob, location, node string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	latest, err := floatingTracerRequirement(ctx)
	if err != nil {
		return &SetupGap{location, "ddtest could not read the latest dd-trace release's Node requirement, which the " + provider.setupName() + " installs on every build: " + err.Error()}
	}
	condition, err := setupRuntimeCondition(root, workflow, job, latest.Node, false)
	var runtimeGap *NodeRuntimeGap
	switch {
	case errors.As(err, &runtimeGap) || err == nil && condition != "":
		return &SetupGap{location, floatingTracerNodeProblem(provider.setupName(), latest, node)}
	case err != nil:
		return &SetupGap{location, err.Error()}
	}
	return nil
}

// providerWritesPluginSetup reports whether onboard --apply writes the
// provider's setup for every language it supports: a CI server plugin that
// the configuration only enables, whose one language input is the plugin's
// language token (see discoveryLanguage.ciPluginLanguage). The language's
// other setup, such as pinning its tracer in the project, stays
// instructions.
func providerWritesPluginSetup(provider ciProvider) bool {
	return provider.editsSetup() && provider.setupTerms().Plugin
}

// CIProviderWritesPluginSetup reports whether onboard --apply writes the
// selected CI provider's plugin setup for language, which has no automatic
// setup of its own (see providerWritesPluginSetup).
func CIProviderWritesPluginSetup(language string) bool {
	return writesPluginSetup(activeOrDefaultCIProvider(), language)
}

// writesPluginSetup reports whether onboard --apply writes provider's plugin
// setup for language, which has no automatic setup of its own.
func writesPluginSetup(provider ciProvider, language string) bool {
	registered := discoveryLanguageNamed(language)
	return registered != nil && !registered.automaticCISetup() && languageSupportsCIProvider(language, provider) && providerWritesPluginSetup(provider)
}

// pluginSetupApplies reports whether the selected provider's plugin setup
// can be written for framework in root, reporting to site (empty: any): the
// edit is planned, or needs one source edit first that its refusal names
// (see EditThenApplyGap). A refusal, such as a plugin that reports to
// another site than the selected one, leaves the setup to the instructions.
func pluginSetupApplies(root, framework, site string) bool {
	_, err := planProviderSetup(root, framework, site, snippetProbeRelease)
	var edit *EditThenApplyGap
	return err == nil || errors.As(err, &edit)
}

// handWrittenSetupGap is the gap onboard --apply reports in a CI job's
// hand-written JavaScript setup of framework in root, reporting to site
// (empty: the default), on a provider whose configuration it edits: it
// plans the same edit, so onboard --agent asks for the edit that completes
// that setup instead of handing off an apply that refuses it. nil when the
// plan finds none.
func handWrittenSetupGap(root, framework, site string) *handWrittenGap {
	provider, err := currentCIProvider(root)
	if err != nil || provider.sharedSyntax() || !provider.editsSetup() {
		return nil
	}
	_, err = PlanJavaScriptSetup(root, framework, snippetProbeRelease, site, "")
	var gap *handWrittenGap
	if errors.As(err, &gap) {
		return gap
	}
	return nil
}

// pluginSetupWritable reports whether onboard --apply would write the
// selected provider's plugin setup for a language without automatic setup
// of its own (see CIProviderWritesPluginSetup): it plans at least one edit.
func pluginSetupWritable(root, language, framework, site string) bool {
	if provider, err := currentCIProvider(root); err != nil || !writesPluginSetup(provider, language) {
		return false
	}
	plan, err := PlanProviderSetup(root, framework, site)
	return err == nil && !plan.Configured()
}

// pluginSetupRefusal is why onboard --apply cannot write the selected
// provider's plugin setup for framework in root; empty when it can, or the
// provider has none.
func pluginSetupRefusal(root, framework string) string {
	provider, err := currentCIProvider(root)
	if err != nil || !providerWritesPluginSetup(provider) {
		return ""
	}
	// A language whose onboard --apply selects the tracer release a setup
	// snippet installs is refused for other reasons only.
	release := ""
	if language := discoveryLanguageFor(framework); language != nil && language.automaticProviderSetup(framework) {
		release = snippetProbeRelease
	}
	_, err = planProviderSetup(root, framework, "", release)
	var gap *SetupGap
	var edit *EditThenApplyGap
	if errors.As(err, &gap) && !errors.As(err, &edit) {
		return gap.Error()
	}
	return ""
}

// pluginSetupEditFirst is the source edit onboard --apply needs before it
// writes the selected provider's plugin setup for framework in root (see
// EditThenApplyGap); empty when it needs none, or cannot write it.
func pluginSetupEditFirst(root, framework string) string {
	provider, err := currentCIProvider(root)
	if err != nil || !providerWritesPluginSetup(provider) {
		return ""
	}
	_, err = PlanProviderSetup(root, framework, "")
	var edit *EditThenApplyGap
	if errors.As(err, &edit) {
		return edit.Error()
	}
	return ""
}

// PlanProviderSetup plans enabling the selected CI provider's plugin setup
// (see providerWritesPluginSetup) for every job that runs framework's tests,
// reporting to site (empty: any), without changing the repository. A job
// whose setup is already complete keeps it; one that differs is completed.
func PlanProviderSetup(root, framework, site string) (*SetupPlan, error) {
	return planProviderSetup(root, framework, site, "")
}

// snippetProbeRelease is the tracer release a plan that only checks whether
// the plugin's setup can be written gives a setup snippet (see
// pluginSetupApplies): onboard --apply selects the one it writes.
const snippetProbeRelease = "0.0.0"

// planProviderSetup is PlanProviderSetup, with release the tracer release
// of the setup snippets a provider adds beside its plugin (see
// jenkinsContainer); empty uses the one the project's dependencies pin.
func planProviderSetup(root, framework, site, release string) (*SetupPlan, error) {
	provider, err := currentCIProvider(root)
	if err != nil {
		return nil, err
	}
	language := discoveryLanguageFor(framework)
	if language == nil || !providerWritesPluginSetup(provider) {
		return nil, &SetupGap{provider.displayName(), "onboard --apply does not edit the " + provider.displayName() + " configuration for " + framework + ". Add the setup from onboard --agent instructions, then validate with testdrive --all."}
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil, err
	}
	// An empty site accepts any the setup reports to.
	request := language.ciSetupRequest(root, framework, site, provider.setupTerms())
	request.Framework = framework
	if locked, ok := language.(ciLockedTracer); ok && release == "" {
		release = locked.lockedTracer(root)
	}
	request.TracerVersion = release
	var targets []ciSetupTarget
	handWritten := 0
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			for i, step := range job.Steps {
				if !resolveTestStep(root, workflow, job, step, language.name(), framework).Matched {
					continue
				}
				// A job that sets up the tracer by hand keeps that setup: the
				// provider's is never added beside it, and what it lacks is
				// for the user to complete.
				if setup := keptHandWrittenSetup(root, workflow, job, i, language, framework, site); setup != nil {
					if problem := setup.problem(); problem != "" {
						return nil, &handWrittenGap{SetupGap{workflow.Path + " / " + name, problem + " Then rerun onboard --apply."}}
					}
					handWritten++
					break
				}
				targets = append(targets, ciSetupTarget{Workflow: workflow.Path, Job: name, Step: i})
				break
			}
		}
	}
	switch {
	case len(targets) == 0 && handWritten > 0:
		return &SetupPlan{root: root}, nil
	case len(targets) == 0:
		return nil, &SetupGap{provider.displayName(), "No literal " + framework + " CI step found. Identify its real CI entry point before applying onboarding."}
	}
	edits, impact, err := provider.planSetupEdits(root, workflows, targets, request)
	if err != nil {
		return nil, err
	}
	if ignorer, ok := provider.(ciSetupIgnorer); ok && len(edits) > 0 {
		if entries := ignorer.setupIgnores(edits); len(entries) > 0 {
			ignored, err := ignoreEdits(root, entries)
			if err != nil {
				return nil, err
			}
			edits = append(edits, ignored...)
		}
	}
	return &SetupPlan{root: root, edits: edits, impact: impact}, nil
}

// keptHandWrittenSetup is the hand-written setup of job's tests from step
// first on, for a language whose jobs keep such a setup (see
// keepsHandWrittenJobs); nil otherwise.
func keptHandWrittenSetup(root string, workflow ciWorkflow, job runtimeJob, first int, language discoveryLanguage, framework, site string) *handWrittenSetup {
	if !language.keepsHandWrittenJobs() {
		return nil
	}
	return jobHandWrittenSetup(root, workflow, job, first, language.name(), framework, site)
}

// ciShellOrigin is a step origin that names the shell the step runs in,
// such as a Jenkinsfile sh step's.
type ciShellOrigin interface{ shell() int }

// sameShell reports two steps a provider read from one shell.
func sameShell(a, b runtimeStep) bool {
	first, ok := a.origin.(ciShellOrigin)
	second, same := b.origin.(ciShellOrigin)
	return ok && same && first.shell() == second.shell()
}

// providerJobSetupProblem explains why a job of workflow does not run a
// complete setup for language (see ciProvider.setupProblem, with site)
// before every command that runs framework's tests; empty when it does, or
// runs none.
func providerJobSetupProblem(root string, provider ciProvider, workflow ciWorkflow, name, language, framework, site string) string {
	job, ok := workflow.Jobs[name]
	if !ok {
		return "the job is missing."
	}
	token := setupActionLanguage(language)
	var setup, other *ciSetup
	var setupStep runtimeStep
	for _, step := range job.Steps {
		if current, ok := datadogSetup(step); ok {
			if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
				setup, setupStep = &current, step
			} else if current.Problem != "" {
				other = &current
			}
		}
		resolution := resolveTestStep(root, workflow, job, step, language, framework)
		if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
			continue
		}
		switch {
		case resolution.Reason != "":
			return resolution.Reason
		case setup == nil && other != nil:
			return other.Problem
		case setup == nil:
			return "no " + setupProviderFor(provider, job).setupName() + " for the language runs before its tests."
		case setup.shellLocal && !sameShell(setupStep, step):
			// Its exports reach only the shell it runs in.
			return "the " + setupProviderFor(provider, job).setupName() + " at " + setupStep.Source + " runs in another shell than the test command at " + step.Source + ", and its exports reach only the later commands of its own shell; each shell that runs the tests needs it before its test command."
		}
		if problem := provider.setupProblem(*setup, "", func(string) bool { return true }, "", site); problem != "" {
			return problem
		}
	}
	return ""
}

// existingScopedSetupGap explains why an existing provider setup that
// exports no scoped variable for framework does not reach step's scoped
// scripts, or is "". Its preload must still be in NODE_OPTIONS: another
// framework's scoped command, such as Cucumber's in a job that also runs
// WebdriverIO, may have moved it. Native ESM Cucumber also needs the Istanbul
// ESM loader (options), which the selected scripts' scoped prefix or the
// step's NODE_OPTIONS may already load, as validation accepts (see
// checkCucumberBootstrap); command is the one that would add it.
func existingScopedSetupGap(provider ciProvider, workflow ciWorkflow, job runtimeJob, steps []matchedTestStep, framework, options, command string) string {
	scoped := scopedEnvironment(framework)
	if slices.ContainsFunc(steps, func(step matchedTestStep) bool { return checkJestBootstrap(workflow, job, step.step, nil, false) != "" }) {
		return "The existing " + provider.setupName() + " no longer leaves the tracer's preload in NODE_OPTIONS for this " + framework + " step, and exports no " + scoped + ": a command after it, such as another framework's scoped command, moves the preload. Export " + scoped + " with the tracer's preload by hand after the setup, or review it manually without adding a second setup."
	}
	if options == "" {
		return ""
	}
	if cucumberLoaderReached(workflow, job, steps) {
		return ""
	}
	return "The existing " + provider.setupName() + " leaves the tracer's preload in NODE_OPTIONS without the " + options + " native ESM coverage needs. Add " + provider.setupTerms().Persist(command) + " right after it, then rerun onboard --apply; or review it manually without adding a second setup."
}

// scopedPreloadReached reports whether the scoped variable a test step
// reads (scoped, which the step's environment sets) still carries the
// tracer's preload and ESM loader that the provider setup's following
// command exports: a command after it may replace it, such as an inline
// DD_CUCUMBER_NODE_OPTIONS= assignment.
func scopedPreloadReached(workflow ciWorkflow, job runtimeJob, step runtimeStep, scoped string) bool {
	configured := step
	configured.Env = maps.Clone(step.Env)
	configured.Env["NODE_OPTIONS"] = step.Env[scoped]
	return checkJestBootstrap(workflow, job, configured, nil, true) == ""
}

// directLeaf is a direct invocation, without a package script, of a scoped
// framework (Cucumber or WebdriverIO), and the CI step that runs it.
type directLeaf struct {
	step runtimeStep
	test TestCommand
}

// matchedTestStep is a CI step that runs the selected framework's tests,
// with the test commands it runs.
type matchedTestStep struct {
	step  runtimeStep
	tests []TestCommand
}

// cucumberLoaderReached reports whether native ESM Cucumber tests get the
// Istanbul ESM loader without the scoped variable carrying it, as validation
// accepts (see checkCucumberBootstrap): in every test step, every selected
// command loads it in its own scoped prefix, or the step's NODE_OPTIONS
// does. A step can set its own NODE_OPTIONS, so each one is checked.
func cucumberLoaderReached(workflow ciWorkflow, job runtimeJob, steps []matchedTestStep) bool {
	for _, matched := range steps {
		inline := len(matched.tests) > 0
		for _, test := range matched.tests {
			inline = inline && test.cucumberInlineIstanbul
		}
		current, err := effectiveBootstrapOptions(workflow, job, matched.step, nil, true)
		if !inline && (err != nil || !nodeOptionsLoad(current, "@istanbuljs/esm-loader-hook")) {
			return false
		}
	}
	return len(steps) > 0
}

// directCucumberCoverageGap prescribes NYC for the first direct Cucumber
// invocation in direct that lacks it, in a job whose setup leaves the
// tracer's preload in NODE_OPTIONS (a hand-written setup, or the provider's
// without the scoped variable): no package script carries the wrapper
// onboard --apply adds, so the CI command itself must. stays says what
// happens to the job's setup. nil when every one has NYC.
func directCucumberCoverageGap(provider ciProvider, location string, workflow ciWorkflow, job runtimeJob, direct []directLeaf, stays string) error {
	for _, leaf := range direct {
		workaround := cucumberImpactedWorkaroundSet(workflow, job, leaf.step)
		if leaf.test.usesNYC && workaround {
			continue
		}
		command, err := directScopedCommand(leaf.step, leaf.test, "cucumber")
		if err != nil {
			return directScopedGap(provider, location, leaf.step, err)
		}
		if _, ok := provider.(buildkite); ok {
			command = buildkiteEscape(command) // pipeline upload would replace each $.
		}
		reason := CucumberCoverageGap + " CI runs " + leaf.test.Command + " directly, not through a package script, so onboard --apply cannot wrap it with NYC."
		if leaf.test.usesNYC {
			reason = "CI runs " + leaf.test.Command + " directly, not through a package script, without " + cucumberImpactedWorkaround + ", which onboard --apply adds to the package scripts it scopes and dd-trace's Cucumber instrumentation needs, so onboard --apply cannot add it to this command."
		}
		return &EditThenApplyGap{SetupGap{directLeafLocation(location, leaf.step), reason + " Change this CI command to exactly: " + command + ". Then rerun onboard --apply; " + stays}}
	}
	return nil
}

// directScopedGap is the gap of a direct invocation whose scoped command
// directScopedCommand could not write: the exact command that replaces a
// report pipe it splits (see ReportPipeSplit), or the reason it writes none.
func directScopedGap(provider ciProvider, location string, step runtimeStep, err error) error {
	var split *ReportPipeSplit
	if !errors.As(err, &split) {
		return &SetupGap{location, err.Error()}
	}
	if _, ok := provider.(buildkite); ok {
		escaped := *split // pipeline upload would replace each $.
		escaped.Command = buildkiteEscape(split.Command)
		split = &escaped
	}
	return &EditThenApplyGap{SetupGap{directLeafLocation(location, step), split.Error() + ": it adds the " + provider.setupName() + " before the tests when the job has none, and reads the conversion as outside the tests."}}
}

// cucumberImpactedWorkaroundSet reports whether a direct Cucumber
// invocation runs with Cucumber's impacted-tests workaround
// (cucumberImpactedWorkaround): right after its scoped prefix, or in the
// environment the step's command starts with.
func cucumberImpactedWorkaroundSet(workflow ciWorkflow, job runtimeJob, step runtimeStep) bool {
	value, set := handWrittenEnvironment(workflow, job, step)["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"]
	return step.impactedWorkaround || set && handWrittenFalse(value)
}

// directLeafLocation names a direct invocation's CI step by its source
// line, when the provider records one.
func directLeafLocation(location string, step runtimeStep) string {
	if step.Source != "" {
		return location + " (" + step.Source + ")"
	}
	return location
}

// directLeafReason prescribes the CI command a direct Cucumber or
// WebdriverIO invocation needs (see directScopedCommand), written as the
// provider's configuration holds it, and what onboard --apply does next.
func directLeafReason(provider ciProvider, framework, leaf, command string, configured bool) string {
	if _, ok := provider.(buildkite); ok {
		command = buildkiteEscape(command) // pipeline upload would replace each $.
	}
	edit := "it adds the " + provider.setupName() + " before this command, with the command that exports " + scopedEnvironment(framework) + "."
	if configured {
		edit = "the job's " + provider.setupName() + " already exports " + scopedEnvironment(framework) + "."
	}
	return "CI runs " + leaf + " directly, not through a package script, so the command itself must read the tracer's preload from " + scopedEnvironment(framework) + ", as a scoped package script does. Change this CI command to exactly: " + command + ". Then rerun onboard --apply: " + edit
}

// outsideTestJobs reports whether the language tells that job runs none of
// framework's tests (see discoveryLanguage.jobConsumer), as scope discovery
// reads it; such a job needs neither the setup nor the user's answers.
func outsideTestJobs(root string, workflow ciWorkflow, job runtimeJob, language, framework string) bool {
	registered := discoveryLanguageNamed(language)
	if registered == nil {
		return false
	}
	consumer, handled := registered.jobConsumer(root, workflow, job, framework)
	return handled && !consumer
}

// providerSetupConfigured reports whether, on a provider without the shared
// workflow syntax, a complete setup for language (see
// ciProvider.setupProblem) precedes the first test command of every job that
// runs framework's tests, with at least one such job. A test command ddtest
// cannot resolve keeps the setup incomplete.
func providerSetupConfigured(root string, provider ciProvider, language, framework, site, tracerInput string, exact func(string) bool) bool {
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return false
	}
	if len(sharedSetupLeaks(root, workflows, language, framework)) > 0 {
		return false
	}
	token := setupActionLanguage(language)
	instrumented := false
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) || outsideTestJobs(root, workflow, job, language, framework) {
				continue
			}
			var setup *ciSetup
			var setupStep runtimeStep
			for i, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
						setup, setupStep = &current, step
					}
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
				}
				if setup == nil && resolution.Reason == "" {
					// A complete setup the job writes by hand counts as its own.
					if handWritten := jobHandWrittenSetup(root, workflow, job, i, language, framework, site); handWritten != nil && handWritten.complete() {
						instrumented = true
						break
					}
				}
				if resolution.Reason != "" || setup == nil || setup.shellLocal && !sameShell(setupStep, step) || provider.setupProblem(*setup, tracerInput, exact, "", site) != "" {
					return false
				}
				instrumented = true
				if !setup.shellLocal {
					// Its exports reach every later step of the job; a
					// shell-local one's only its own shell's.
					break
				}
			}
		}
	}
	return instrumented
}

// existingSetupProblems lists, on a provider without the shared workflow
// syntax, why the setup that already precedes framework's tests in a job is
// not complete, as the provider read it (see ciProvider.setupProblem, with
// the selected site), one line per job; nil when none is incomplete.
func existingSetupProblems(root, language, framework, site string) []string {
	provider, err := currentCIProvider(root)
	if err != nil || provider.sharedSyntax() {
		return nil
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return nil
	}
	token := setupActionLanguage(language)
	var problems []string
	jobs := map[string][]string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) || outsideTestJobs(root, workflow, job, language, framework) {
				continue
			}
			var setup *ciSetup
			for i, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
						setup = &current
					}
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
				}
				problem := ""
				if setup == nil {
					// A setup the job writes by hand is reported with what it lacks.
					if handWritten := jobHandWrittenSetup(root, workflow, job, i, language, framework, site); handWritten != nil {
						problem = handWritten.problem()
					}
				} else {
					problem = provider.setupProblem(*setup, "", func(string) bool { return true }, "", site)
				}
				if problem != "" {
					if _, seen := jobs[problem]; !seen {
						problems = append(problems, problem)
					}
					jobs[problem] = append(jobs[problem], workflow.Path+" / "+name)
				}
				break
			}
		}
	}
	for i, problem := range problems {
		problems[i] = strings.Join(jobs[problem], ", ") + ": " + problem
	}
	for _, leak := range sharedSetupLeaks(root, workflows, language, framework) {
		problems = append(problems, leak.Workflow+" / "+leak.Job+": "+leak.Reason)
	}
	return problems
}

// setupUnsupportedNote names the CI jobs running the framework's tests that
// the provider's setup mechanism cannot instrument (see
// runtimeJob.SetupUnsupported), so the person learns it before the setup is
// written; empty when there are none.
func setupUnsupportedNote(root, language, framework string) string {
	provider, err := currentCIProvider(root)
	if err != nil {
		return ""
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return ""
	}
	var problems []string
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if job.SetupUnsupported == "" || disabledCondition(job.If) {
				continue
			}
			if slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				return resolveTestStep(root, workflow, job, step, language, framework).Matched
			}) {
				problems = append(problems, workflow.Path+" / "+name+": "+job.SetupUnsupported+".")
			}
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return "The " + provider.setupName() + " cannot instrument these CI jobs, so ddtest reports them as unsupported and does not validate them; tell the user, and do not move their tests to other agents without the user's approval:\n" + strings.Join(problems, "\n")
}

// setupImageNote names the CI jobs running the framework's tests whose
// container cannot run their setup mechanism as configured, such as an image
// without curl, so the person learns it before the setup is written; empty
// when there are none. Each job is named under its own setup mechanism (see
// setupProviderFor). Fixing an image is not part of the prescribed setup.
func setupImageNote(root, language, framework string) string {
	provider, err := currentCIProvider(root)
	if err != nil {
		return ""
	}
	workflows, err := readProviderWorkflows(provider, root)
	if err != nil {
		return ""
	}
	var setups []string
	problems := map[string][]string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if job.SetupImageProblem == "" || disabledCondition(job.If) {
				continue
			}
			if slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				return resolveTestStep(root, workflow, job, step, language, framework).Matched
			}) {
				setup := setupProviderFor(provider, job).setupName()
				if !slices.Contains(setups, setup) {
					setups = append(setups, setup)
				}
				problems[setup] = append(problems[setup], workflow.Path+" / "+name+": "+job.SetupImageProblem)
			}
		}
	}
	notes := make([]string, 0, len(setups))
	for _, setup := range setups {
		notes = append(notes, "ddtest cannot check that these CI steps can run the "+setup+" in their image, so it does not validate their setup. Never change an image or Dockerfile: tell the user.\n"+strings.Join(problems[setup], "\n"))
	}
	return strings.Join(notes, "\n")
}
