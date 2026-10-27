// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kballard/go-shellquote"
)

func resolveNativeTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, framework string) (runtimeStep, commandResolution, error) {
	resolved, _, err := resolveVariantStep(workflow, job, step, nil, framework)
	probe := resolved
	probe.ResolutionError = ""
	r := resolveTestStep(root, workflow, job, probe, "javascript", framework)
	if resolved.ResolutionError != "" {
		r.Reason = resolved.ResolutionError
	}
	if err != nil {
		r.Reason = err.Error()
	}
	return resolved, r, err
}

func nativeInlineControls(step runtimeStep, env map[string]string, provider string) (runtimeStep, map[string]string, error) {
	if provider == "" || provider == "github" {
		return step, env, nil
	}
	commands, err := staticCommands(step.Run)
	if err != nil {
		return step, env, nil // The command resolver reports unsupported syntax.
	}
	for _, words := range commands {
		_, values, err := commandEnvironment(words)
		if err != nil {
			continue
		}
		if value, ok := values["DD_CIVISIBILITY_ENABLED"]; ok {
			if len(commands) != 1 || value != "false" && value != "true" {
				return step, env, fmt.Errorf("native inline instrumentation controls require one literal command and boolean value")
			}
			step.Env = mergeEnvironment(step.Env, map[string]string{"DD_CIVISIBILITY_ENABLED": value})
		}
	}
	return step, env, nil
}

func compareNativeNode(node, requirement string) (string, string) {
	if numericNode.MatchString(node) && (strings.Count(node, ".") != 2 || strings.ContainsAny(node, "x*")) {
		return StatusInconclusive, fmt.Sprintf("CI Node selector %q is unresolved; supply the actual runtime version before checking compatibility.", node)
	}
	return CompareNodeRequirement(node, requirement)
}

func nativeTestRuntime(w ciWorkflow, job runtimeJob, index int, resolution commandResolution) (string, string) {
	node, source := nativeStepRuntime(w, job, index)
	for _, test := range resolution.Tests {
		words, _ := shellquote.Split(test.Command)
		if len(words) > 0 && !slices.Contains([]string{"node", "node.exe"}, words[0]) && slices.Contains([]string{"node", "node.exe"}, filepath.Base(words[0])) {
			return "", "explicit Node executable " + words[0]
		}
	}
	return node, source
}

// GitLab and Buildkite entries share a shell; CircleCI run and Jenkins sh do
// not. Resolve runtime switches at the same test step for discovery and edits.
func nativeStepRuntime(w ciWorkflow, job runtimeJob, index int) (string, string) {
	node, source := job.Node, job.NodeSource
	if len(job.Steps) == 0 {
		return node, source
	}
	index = min(index, len(job.Steps)-1)
	if w.Provider == "jenkins" && job.Steps[index].Native != nil {
		node, source = job.Steps[index].Native.NodeVersion, job.Steps[index].Native.NodeSource
	}
	start := index
	if w.Provider == "gitlab" || w.Provider == "buildkite" {
		start = 0
	}
	for _, step := range job.Steps[start : index+1] {
		if selected := nvmRuntime(step.Run); selected != "" {
			node, source = selected, step.Source
		} else if changesNodeRuntime(step.Run) {
			node, source = "", step.Source
		}
	}
	return node, source
}

func checkNativeRuntimes(ctx context.Context, root string, w ciWorkflow, framework string, resolve requirementResolver) []RuntimeFinding {
	var findings []RuntimeFinding
	for _, reason := range w.Unresolved {
		findings = append(findings, RuntimeFinding{Workflow: w.Path, Status: StatusInconclusive, Code: CodeUnresolvedCommand, Reason: reason})
	}
	for _, name := range slices.Sorted(maps.Keys(w.Jobs)) {
		job := w.Jobs[name]
		if job.If == "false" {
			continue
		}
		for i, step := range job.Steps {
			resolved, r, envErr := resolveNativeTestStep(root, w, job, step, framework)
			if !r.Matched {
				continue
			}
			node, nodeSource := nativeTestRuntime(w, job, i, r)
			finding := RuntimeFinding{Workflow: w.Path, Job: name, Step: stepNumber(step, i), Source: step.Source, Command: step.Run, Resolution: r.Evidence, Node: node, NodeSource: nodeSource, Status: StatusInconclusive}
			if mochaInstrumentationDisabled(w, job, resolved) {
				finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeExcludedRuntime, "DD_CIVISIBILITY_ENABLED=false leaves this CI entry uninstrumented; its runtime was not validated with the tracer."
				findings = append(findings, finding)
				continue
			}
			if envErr != nil {
				finding.Code, finding.Reason = CodeUnresolvedCommand, envErr.Error()
				findings = append(findings, finding)
				continue
			}
			if len(job.Gaps) > 0 {
				finding.Code, finding.Reason = CodeUnresolvedCommand, strings.Join(job.Gaps, "; ")
				findings = append(findings, finding)
				continue
			}
			if r.Reason != "" {
				finding.Code, finding.Reason = CodeUnresolvedCommand, r.Reason
				findings = append(findings, finding)
				continue
			}
			if strings.HasPrefix(nodeSource, "explicit Node executable ") {
				finding.Code, finding.Reason = CodeRuntimeUnknown, "The test command selects an explicit Node executable; its CI version cannot be inferred from the job image or tools."
				findings = append(findings, finding)
				continue
			}
			if UnsupportedNode(node) {
				finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeUnsupportedNode, UnsupportedNodeReason(node)
				findings = append(findings, finding)
				continue
			}
			if step.Native == nil || step.Native.Tracer == "" {
				finding.Status, finding.Code, finding.Reason = StatusIncompatible, CodeMissingInstrumentation, "No verified tracer installation and initialization wraps this test command. Preview ddtest onboard --apply for the selected CI provider."
				findings = append(findings, finding)
				continue
			}
			if step.Native.Framework != "" && step.Native.Framework != framework {
				finding.Code, finding.Reason = CodeUnverifiedInitialization, "Existing initialization targets another framework"
				findings = append(findings, finding)
				continue
			}
			if framework == "mocha" && slices.ContainsFunc(r.Tests, func(test TestCommand) bool { return !test.usesNYC && !test.mochaIstanbul }) {
				finding.Code, finding.Reason = CodeUnverifiedInitialization, "Mocha source coverage is missing: preserve the existing tracer and configure Istanbul/NYC instrumentation for this test command."
				findings = append(findings, finding)
				continue
			}
			if setupNeedsESM(root, framework, w, job, resolved) && !step.Native.ESM {
				finding.Code, finding.Reason = CodeMissingInitialization, "ESM tests require --import dd-trace/register.js alongside the CI preload"
				findings = append(findings, finding)
				continue
			}
			if framework == "cucumber" && !step.Native.Manual && slices.ContainsFunc(r.Tests, func(test TestCommand) bool { return !test.cucumberScoped }) {
				finding.Code, finding.Reason = CodeUnverifiedInitialization, "Selected Cucumber leaf does not consume scoped DD_CUCUMBER_NODE_OPTIONS while preserving existing NODE_OPTIONS"
				findings = append(findings, finding)
				continue
			}
			requirement, err := resolve(ctx, "npm", step.Native.Tracer)
			if err != nil {
				finding.Code, finding.Reason = CodeTracerMetadata, err.Error()
				findings = append(findings, finding)
				continue
			}
			finding.Action, finding.Tracer, finding.TracerRequested, finding.Requirement = w.Provider+" / npm", "dd-trace@"+requirement.Version, step.Native.Tracer, requirement.Node
			if node == "" {
				finding.Code, finding.Reason = CodeRuntimeUnknown, "CI Node runtime is not statically known from the image or tools; provide the actual runtime instead of guessing"
			} else {
				finding.Status, finding.Reason = compareNativeNode(node, requirement.Node)
				if finding.Status == StatusInconclusive {
					finding.Code = CodeRuntimeUnknown
				}
			}
			if finding.Status == StatusCompatible && step.Native.ESM {
				finding.Status, finding.Reason = compareNativeNode(node, ">=18.19 <19 || >=20.6")
			}
			findings = append(findings, finding)
		}
	}
	return findings
}
