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
	if !language.automaticProviderSetup(framework) {
		return nil, &SetupGap{"CI initialization", framework + " initializes the tracer through package scripts, which onboard --apply does not edit on " + provider.displayName() + ". Add the setup from onboard --instructions manually, then validate with testdrive --all."}
	}
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
				if first < 0 {
					first, resolution = i, current
				}
			}
			if first < 0 {
				continue
			}
			if resolution.OtherFramework {
				return nil, &SetupGap{location, "The test command also runs another framework; initialize only the selected framework explicitly."}
			}
			for _, layer := range []map[string]string{workflow.Env, job.Env, job.Steps[first].Env} {
				for _, key := range []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"} {
					if _, ok := layer[key]; ok {
						return nil, &SetupGap{location, "Custom inherited instrumentation requires review: " + key}
					}
				}
			}
			// The runtime the tests run on: the image's, or one a modeled
			// installer selects before them.
			node := imageNode(job.Image)
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
						return nil, &EditThenApplyGap{SetupGap{location, MochaCoverageGap + command.Command + ". Wrap it with npx --no-install nyc -- in CI (adding nyc as a dev dependency if the project does not declare it), then rerun onboard --apply, which adds the Datadog setup."}}
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
			if setupIndex >= 0 {
				setup, _ := datadogSetup(job.Steps[setupIndex])
				if problem := provider.setupProblem(setup, request.TracerInput, func(value string) bool { return value == version }, version, site); problem != "" {
					return nil, &SetupGap{location, "Existing Datadog setup differs: " + problem + " Otherwise review it manually without adding a second setup."}
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
	var edits []setupEdit
	var impact []string
	if len(targets) > 0 {
		var err error
		if edits, impact, err = provider.planSetupEdits(root, workflows, targets, request); err != nil {
			return nil, err
		}
	}
	ignored, err := ignoreSetupEdits(root, false)
	if err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		// Configured CI needs no ignore-only edit to be complete.
		return &SetupPlan{root: root, impact: impact}, nil
	}
	return &SetupPlan{root: root, edits: append(edits, ignored...), impact: impact}, nil
}

// providerSetupConfigured reports whether, on a provider without the shared
// workflow syntax, a complete setup for language (see
// ciProvider.setupProblem) precedes the first test command of every job that
// runs framework's tests, with at least one such job. A test command ddtest
// cannot resolve keeps the setup incomplete.
func providerSetupConfigured(root string, provider ciProvider, language, framework, site, tracerInput string, exact func(string) bool) bool {
	workflows, err := provider.readWorkflows(root)
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
			for _, step := range job.Steps {
				if current, ok := datadogSetup(step); ok {
					if selected, err := setupSelectsLanguage(current, nil, token); err == nil && selected {
						setup = &current
					}
				}
				resolution := resolveTestStep(root, workflow, job, step, language, framework)
				if resolution.Review || !resolution.Matched && (resolution.Reason == "" || !stepMayStartTests(root, workflow, job, step, framework)) {
					continue
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

// setupImageNote names the CI jobs running the framework's tests whose
// container cannot run the provider's setup as configured, such as an image
// without curl, so the person learns it before the setup is written; empty
// when there are none. Fixing an image is not part of the prescribed setup.
func setupImageNote(root, language, framework string) string {
	provider, err := currentCIProvider(root)
	if err != nil {
		return ""
	}
	workflows, err := provider.readWorkflows(root)
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
