// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"errors"
	"maps"
	"slices"
	"strings"
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
	request := language.ciSetupRequest(root, framework, site, provider.setupTerms())
	request.TracerVersion = version
	var targets []ciSetupTarget
	configured := 0
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
				continue
			}
			location := workflow.Path + " / " + name
			first, setupIndex := -1, -1
			var resolution commandResolution
			var tests []TestCommand
			// Direct invocations (no package script) of a scoped framework.
			type directLeaf struct {
				step runtimeStep
				test TestCommand
			}
			var direct []directLeaf
			for i, step := range job.Steps {
				if setup, ok := datadogSetup(step); ok {
					if languages := setup.Inputs["languages"]; strings.Contains(languages, "$") {
						return nil, &SetupGap{location, "The existing " + provider.setupName() + " selects its languages from " + languages + ", which ddtest cannot resolve; review it manually instead of adding a second setup."}
					}
					if selected, err := setupSelectsLanguage(setup, nil, request.Language); err == nil && selected {
						if first >= 0 {
							return nil, &SetupGap{location, "The " + provider.setupName() + " runs after the first " + framework + " test command; move it before the tests or review it manually without adding a second one."}
						}
						setupIndex = i
					}
				}
				current := resolveTestStep(root, workflow, job, step, "javascript", framework)
				if !current.Matched {
					continue
				}
				// Every selected test command decides the loader and coverage.
				tests = append(tests, current.Tests...)
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
			if resolution.OtherFramework && scoped == "NODE_OPTIONS" {
				return nil, &SetupGap{location, "The test command also runs another framework; initialize only the selected framework explicitly."}
			}
			// A job that sets up the tracer by hand keeps that setup.
			var handWritten *handWrittenSetup
			if setupIndex < 0 {
				handWritten = handWrittenCISetup(root, workflow, job, first, nil, "javascript", framework, site)
			}
			inherited := []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"}
			if scoped != "NODE_OPTIONS" && setupIndex < 0 {
				inherited = append(inherited, scoped)
			}
			if handWritten != nil {
				inherited = nil
			}
			for _, layer := range []map[string]string{workflow.Env, job.Env, job.Steps[first].Env} {
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
			esm := len(request.After) > 0
			if framework == "mocha" {
				esm = false
				for _, command := range tests {
					esm = esm || command.mochaESM
					if !command.usesNYC && !command.mochaIstanbul && setupIndex < 0 {
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
					break
				}
				if framework == "webdriverio" {
					if local, err := localWDIOLeaf(root, leaf.test.Command); err != nil {
						return nil, &SetupGap{location, err.Error()}
					} else if !local {
						continue // Explicit browser runner remains outside instrumentation.
					}
				}
				command, err := directScopedCommand(leaf.step, leaf.test, framework)
				if err != nil {
					return nil, &SetupGap{location, err.Error()}
				}
				if command != "" {
					return nil, &EditThenApplyGap{SetupGap{directLeafLocation(location, leaf.step), directLeafReason(provider, framework, leaf.test.Command, command, setupIndex >= 0)}}
				}
			}
			if handWritten != nil {
				if problem := handWritten.problem(); problem != "" {
					return nil, &SetupGap{location, problem + " Then rerun onboard --apply."}
				}
				configured++
				continue
			}
			if setupIndex >= 0 {
				setup, _ := datadogSetup(job.Steps[setupIndex])
				if problem := provider.setupProblem(setup, request.TracerInput, func(value string) bool { return value == version }, version, site); problem != "" {
					return nil, &SetupGap{location, "Existing Datadog setup differs: " + problem + " Otherwise review it manually without adding a second setup."}
				}
				// A setup that leaves the preload in NODE_OPTIONS still reaches the
				// scoped scripts; only the options the scoped variable adds, such
				// as the Istanbul ESM loader, would be missing.
				if _, exported := job.Steps[first].Env[scoped]; !exported && scopedProviderOptions(root, framework) != "" && !provider.setupTerms().Plugin {
					return nil, &SetupGap{location, "The existing " + provider.setupName() + " leaves the tracer's preload in NODE_OPTIONS without the " + scopedProviderOptions(root, framework) + " native ESM coverage needs. Add " + provider.setupTerms().Persist(request.After[0]) + " right after it, then rerun onboard --apply; or review it manually without adding a second setup."}
				}
				configured++
				continue
			}
			target := ciSetupTarget{Workflow: workflow.Path, Job: name, Step: first}
			if framework == "mocha" && !esm {
				target.After = []string{}
			}
			targets = append(targets, target)
		}
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
	ignored, err := ignoreSetupEdits(root, coverageAdded)
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
	token := setupActionLanguage(language)
	instrumented := false
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if disabledCondition(job.If) {
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
				if setup == nil && resolution.Reason == "" {
					// A complete setup the job writes by hand counts as its own.
					if handWritten := handWrittenCISetup(root, workflow, job, i, nil, language, framework, site); handWritten != nil && handWritten.complete() {
						instrumented = true
						break
					}
				}
				if resolution.Reason != "" || setup == nil || provider.setupProblem(*setup, tracerInput, exact, "", site) != "" {
					return false
				}
				instrumented = true
				break
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
			if disabledCondition(job.If) {
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
					if handWritten := handWrittenCISetup(root, workflow, job, i, nil, language, framework, site); handWritten != nil {
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
// container cannot run the provider's setup as configured, such as an image
// without curl, so the person learns it before the setup is written; empty
// when there are none. Fixing an image is not part of the prescribed setup.
func setupImageNote(root, language, framework string) string {
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
			if job.SetupImageProblem == "" || disabledCondition(job.If) {
				continue
			}
			if slices.ContainsFunc(job.Steps, func(step runtimeStep) bool {
				return resolveTestStep(root, workflow, job, step, language, framework).Matched
			}) {
				problems = append(problems, workflow.Path+" / "+name+": "+job.SetupImageProblem)
			}
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return "These CI steps cannot run the " + provider.setupName() + " in their current image, so their tests would stay untraced even with it in place; changing an image or Dockerfile is not part of this setup, so tell the user and ask before making it:\n" + strings.Join(problems, "\n")
}
