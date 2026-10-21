// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// RuntimeCheck is separate from local test compatibility: a local run cannot
// validate the other runtimes selected by a CI workflow.
type RuntimeCheck struct {
	Status string           `json:"status"`
	Reason string           `json:"reason"`
	Jobs   []RuntimeFinding `json:"jobs,omitempty"`
	Review []RuntimeFinding `json:"review,omitempty"`
}

// RuntimeFinding records the metadata used to check one CI runtime.
type RuntimeFinding struct {
	Workflow        string          `json:"workflow"`
	Job             string          `json:"job"`
	Step            int             `json:"step,omitempty"`
	Source          string          `json:"source,omitempty"`
	Command         string          `json:"command,omitempty"`
	Resolution      string          `json:"resolution,omitempty"`
	Node            string          `json:"node,omitempty"`
	NodeSource      string          `json:"node_source,omitempty"`
	NodeResolution  *NodeResolution `json:"node_resolution,omitempty"`
	Action          string          `json:"action,omitempty"`
	Tracer          string          `json:"tracer,omitempty"`
	TracerRequested string          `json:"tracer_requested,omitempty"`
	TracerFloating  bool            `json:"tracer_floating"`
	Requirement     string          `json:"requirement,omitempty"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason"`
	Code            string          `json:"code,omitempty"`
}

// A composite call's condition sees the caller's environment and string inputs,
// before any descendant overrides. These snapshots are discovery data only.
type runtimeConditionScope struct {
	Expression string
	Env        map[string]string
	Inputs     map[string]string
}

type runtimeStep struct {
	Inputs           map[string]string       `yaml:"-"`
	ID               string                  `yaml:"id"`
	Number           int                     `yaml:"-"`
	Source           string                  `yaml:"-"`
	Parents          []runtimeConditionScope `yaml:"-"`
	ResolutionError  string                  `yaml:"-"`
	WorkingDirectory string                  `yaml:"working-directory"`
	Shell            string                  `yaml:"shell"`
	Env              map[string]string       `yaml:"env"`
	Uses             string                  `yaml:"uses"`
	Run              string                  `yaml:"run"`
	If               string                  `yaml:"if"`
	With             map[string]string       `yaml:"with"`
}

type runtimeJob struct {
	Context  map[string]string `yaml:"-"`
	RunsOn   any               `yaml:"runs-on"`
	Defaults ciDefaults        `yaml:"defaults"`
	Env      map[string]string `yaml:"env"`
	If       string            `yaml:"if"`
	Strategy struct {
		Matrix any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []runtimeStep `yaml:"steps"`
}

type tracerRequirement struct{ Version, Node, Requested string }

type requirementResolver func(context.Context, string, string) (tracerRequirement, error)

// CheckCIRuntimes checks Jest jobs in GitHub Actions without executing workflow
// code or contacting Datadog. Unknown expressions are never treated as a pass.
func CheckCIRuntimes(ctx context.Context, root string, frameworks ...string) RuntimeCheck {
	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkCIRuntimes(ctx, root, setupNodeResolver(client), func(ctx context.Context, action, version string) (tracerRequirement, error) {
		return resolveRequirement(ctx, client, action, version, platform.NewJavaScript().ResolveRegistryTracer)
	}, frameworks...)
}

func checkCIRuntimes(ctx context.Context, root string, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) RuntimeCheck {
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	result := RuntimeCheck{Status: StatusNotApplicable, Reason: "No candidate GitHub Actions " + framework + " test commands found; CI runtime compatibility was not checked."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: StatusInconclusive, Reason: err.Error()}
	}
	type cachedRequirement struct {
		value tracerRequirement
		err   error
	}
	cache := map[string]cachedRequirement{}
	cachedResolve := func(ctx context.Context, action, version string) (tracerRequirement, error) {
		key := action + "\n" + version
		if value, ok := cache[key]; ok {
			return value.value, value.err
		}
		value, err := resolve(ctx, action, version)
		cache[key] = cachedRequirement{value, err}
		return value, err
	}
	for _, workflow := range workflows {
		path := workflow.Path
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]

			resolutions := make([]commandResolution, len(job.Steps))
			candidate := false
			for i, step := range job.Steps {
				resolutions[i] = resolveTestStep(root, workflow, job, step, "javascript", framework)
				if resolutions[i].Review {
					result.Review = append(result.Review, RuntimeFinding{Workflow: path, Job: name, Step: stepNumber(step, i), Command: step.Run, Status: StatusNotExercised, Code: CodeUnresolvedCommand, Reason: resolutions[i].Reason})
					resolutions[i] = commandResolution{}
				}
				candidate = candidate || resolutions[i].Matched || resolutions[i].Reason != ""
			}
			if !candidate {
				continue
			}
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: StatusInconclusive, Code: CodeInvalidMatrix, Reason: err.Error()})
				continue
			}
			if len(rows) == 0 {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: StatusInconclusive, Code: CodeInvalidMatrix, Reason: "Static matrix has no entries; no instrumented CI runtime could be checked."})
				continue
			}
			for _, row := range rows {
				rowResolutions := slices.Clone(resolutions)
				rowJob := job
				rowJob.Steps = slices.Clone(job.Steps)
				rowCandidate := false
				for i, step := range job.Steps {
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
					if uses, _, _ := strings.Cut(strings.ToLower(resolved.Uses), "@"); uses == "actions/setup-node" {
						version, versionErr := setupNodeVersion(root, resolved, row)
						resolved.With = maps.Clone(resolved.With)
						if resolved.With == nil {
							resolved.With = map[string]string{}
						}
						resolved.With["node-version"] = version
						if versionErr != nil {
							resolved.ResolutionError = versionErr.Error()
						}
					}
					rowJob.Steps[i] = resolved
					rowResolutions[i] = resolveTestStep(root, workflow, job, resolved, "javascript", framework)
					if rowResolutions[i].Review {
						rowResolutions[i] = commandResolution{}
					} else if resolveErr != nil {
						rowResolutions[i].Reason = resolveErr.Error()
					}
					rowCandidate = rowCandidate || rowResolutions[i].Matched || rowResolutions[i].Reason != ""
				}
				if !rowCandidate {
					continue
				}
				result.Jobs = append(result.Jobs, checkRuntimeJob(ctx, workflow, name, rowJob, rowResolutions, row, resolveNode, cachedResolve)...)

			}
		}
	}
	if len(result.Jobs) == 0 {
		return result
	}
	result.Status = StatusCompatible
	result.Reason = "Every identified instrumented " + framework + " step satisfies its selected tracer's Node requirement. Other entry points listed for review are outside this check. This does not execute CI or verify the Datadog backend."
	checked := false
	for _, job := range result.Jobs {
		checked = checked || job.Status == StatusCompatible
		if job.Status == StatusIncompatible {
			result.Status = StatusIncompatible
			break
		}
		if job.Status == StatusInconclusive {
			result.Status = StatusInconclusive
		}
	}
	if !checked && result.Status == StatusCompatible {
		result.Status = StatusInconclusive
	}
	if result.Status != StatusCompatible {
		result.Reason = "CI runtime validation is incomplete or incompatible. Fix known incompatibilities. Unknown syntax or metadata requires review; preserve the workflow and report that tool limitation without claiming CI was validated."
	}
	return result
}

type runtimeState struct {
	node, source, platform, action, tracer string
	skippedAction                          bool
}

func (state *runtimeState) trackNode(job runtimeJob, step runtimeStep, row map[string]any) {
	uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
	if uses == "actions/setup-node" {
		state.node = step.With["node-version"]
		state.source = step.Source
		state.platform = nodeDistributionPlatform(job.RunsOn, step.With["architecture"], row)
	} else if changesNodeRuntime(step.Run) {
		state.node = "" // A run step cannot establish later shells' PATH without verified persistence.
		state.source = step.Source
		state.platform = nodeDistributionPlatform(job.RunsOn, "", row)
	}
}

func (state *runtimeState) trackAction(step runtimeStep, row map[string]any) error {
	selected, err := actionSelectsJavaScript(step, row)
	if err != nil || !selected {
		return err
	}

	state.action = step.Uses
	state.tracer, err = runtimeValue(step.With["js-tracer-version"], row)
	if _, explicit := step.With["js-tracer-version"]; explicit && state.tracer == "" {
		state.tracer = "latest"
	}
	return err
}

func checkRuntimeJob(ctx context.Context, workflow ciWorkflow, name string, job runtimeJob, resolutions []commandResolution, row map[string]any, resolveNode nodeResolver, resolve requirementResolver) []RuntimeFinding {
	finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Status: StatusInconclusive}
	var findings []RuntimeFinding
	fail := func(code, reason string) []RuntimeFinding {
		finding.Status, finding.Code, finding.Reason = StatusInconclusive, code, reason
		return append(findings, finding)
	}
	active, err := possibleJobCondition(job.If, row)
	if err != nil {
		return fail(CodeUnresolvedCondition, err.Error())
	}
	if !active {
		finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeExcludedRuntime, "Job is explicitly excluded."
		return []RuntimeFinding{finding}
	}
	state := runtimeState{}
	for i, step := range job.Steps {
		uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
		resolution := resolutions[i]
		test := resolution.Matched || resolution.Reason != ""
		if uses != "actions/setup-node" && uses != githubAction && !changesNodeRuntime(step.Run) && !test {
			continue
		}
		finding = RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Source: step.Source, Command: step.Run, Resolution: resolution.Evidence, Node: state.node, NodeSource: state.source, Status: StatusInconclusive}
		if uses == githubAction {
			finding.Action = step.Uses
		}
		active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
		if err != nil {
			if uses == "actions/setup-node" || changesNodeRuntime(step.Run) {
				finding.Node = ""
				finding.NodeResolution = nil
			}
			return fail(CodeUnresolvedCondition, err.Error())
		}
		if !active {
			if uses == githubAction {
				selected, err := actionSelectsJavaScript(step, row)
				if err != nil && state.action == "" {
					return fail(CodeUnverifiedExclusion, err.Error())
				}
				state.skippedAction = state.skippedAction || selected
			}
			continue
		}
		if step.ResolutionError != "" {
			if uses == "actions/setup-node" || changesNodeRuntime(step.Run) {
				finding.Node = ""
				finding.NodeResolution = nil
			}
			return fail(CodeNodeResolution, step.ResolutionError)
		}
		state.trackNode(job, step, row)
		if uses == githubAction {
			if err := state.trackAction(step, row); err != nil {
				return fail(CodeTracerMetadata, err.Error())
			}
		}
		if !test {
			continue
		}
		finding.Node, finding.NodeSource = state.node, state.source
		if resolution.Reason != "" {
			finding.Code, finding.Reason = CodeUnresolvedCommand, "Could not resolve CI test command: "+resolution.Reason
			findings = append(findings, finding)
			if !resolution.Matched {
				continue
			}
		}
		findings = append(findings, evaluateRuntimeTest(ctx, workflow, job, step, row, resolution, state, finding, resolveNode, resolve))
	}
	if len(findings) == 0 {
		return fail(CodeUnresolvedCommand, "No active test step could be checked.")
	}
	return findings
}

func evaluateRuntimeTest(ctx context.Context, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, resolution commandResolution, state runtimeState, finding RuntimeFinding, resolveNode nodeResolver, resolve requirementResolver) RuntimeFinding {
	finding.Code = ""
	if state.action == "" {
		finding.Status, finding.Code = StatusIncompatible, CodeMissingInstrumentation
		if state.skippedAction {
			finding.Status, finding.Code = StatusExcluded, CodeExcludedRuntime
			if reason := checkExcludedJestBootstrap(workflow, job, step, row); reason != "" {
				finding.Status, finding.Code, finding.Reason = StatusInconclusive, CodeUnverifiedExclusion, reason
				return finding
			}
		}
		finding.Reason = "No Datadog JavaScript action runs before this test step for this matrix entry. It is not validated as instrumented."
		return finding
	}
	requirement, err := resolve(ctx, state.action, state.tracer)
	if err != nil {
		finding.Code, finding.Reason = CodeTracerMetadata, err.Error()
		return finding
	}
	finding.Action, finding.Tracer = state.action, "dd-trace@"+requirement.Version
	finding.TracerRequested = requirement.Requested
	if finding.TracerRequested == "" {
		finding.TracerRequested = state.tracer
	}
	finding.TracerFloating = finding.TracerRequested != "" && strings.TrimPrefix(finding.TracerRequested, "v") != requirement.Version
	finding.Requirement = requirement.Node
	for _, entry := range resolution.Tests {
		words, _ := shellquote.Split(entry.Command)
		if len(words) > 0 && !slices.Contains([]string{"node", "node.exe"}, words[0]) && (filepath.Base(words[0]) == "node" || filepath.Base(words[0]) == "node.exe") {
			finding.Node, finding.NodeResolution = "", nil
			finding.Code, finding.Reason = CodeRuntimeUnknown, "The test command selects an explicit Node executable; its CI version cannot be inferred from setup-node."
			return finding
		}
	}
	node := state.node
	if node == "" {
		finding.Code, finding.Reason = CodeRuntimeUnknown, "The CI Node runtime is unknown after setup; preserve the workflow and resolve it before claiming compatibility."
		return finding
	}
	if resolveNode != nil {
		resolved, err := resolveNode(ctx, node, state.platform)
		if err != nil {
			finding.Node = ""
			finding.Code, finding.Reason = CodeNodeResolution, err.Error()
			return finding
		}
		finding.NodeResolution = &resolved
		node = resolved.Version
	}
	finding.Status, finding.Reason = CompareNodeRequirement(node, requirement.Node)
	if finding.Status == StatusInconclusive {
		finding.Code = CodeRuntimeUnknown
	}
	if finding.Status == StatusCompatible {
		if reason := checkJestBootstrap(workflow, job, step, row); reason != "" {
			finding.Status, finding.Code, finding.Reason = StatusInconclusive, CodeUnverifiedInitialization, reason
		}
	}
	return finding
}

func changesNodeRuntime(command string) bool {
	file, ok := parseCIShell(command)
	if !ok {
		return false
	}
	changed := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			name := filepath.Base(call.Args[0].Lit())
			if name == "n" {
				changed = true
			}
			if (name == "nvm" || name == "fnm") && len(call.Args) > 1 && slices.Contains([]string{"use", "install", "exec", "env"}, call.Args[1].Lit()) {
				changed = true
			}
		}
		return true
	})
	return changed
}

func resolveRequirement(ctx context.Context, client *http.Client, action, version string, resolveRegistry func(context.Context, string) (platform.JSSelection, error)) (tracerRequirement, error) {
	if version == "" {
		_, ref, ok := strings.Cut(action, "@")
		if !ok || ref == "" {
			return tracerRequirement{}, fmt.Errorf("datadog action is missing its ref")
		}
		data, err := fetchRuntimeMetadata(ctx, client, "https://raw.githubusercontent.com/DataDog/test-visibility-github-action/"+url.PathEscape(ref)+"/action.yml")
		if err != nil {
			return tracerRequirement{}, err
		}
		var definition struct {
			Inputs map[string]struct {
				Default string `yaml:"default"`
			} `yaml:"inputs"`
		}
		if err := yaml.Unmarshal(data, &definition); err != nil {
			return tracerRequirement{}, fmt.Errorf("decode action metadata: %w", err)
		}
		version = definition.Inputs["js-tracer-version"].Default
		if version == "" {
			version = "latest"
		}
	}
	if version == "" || strings.Contains(version, "${{") {
		return tracerRequirement{}, fmt.Errorf("could not resolve the action's JavaScript tracer version")
	}
	selected, err := resolveRegistry(ctx, version)
	if err != nil {
		return tracerRequirement{}, err
	}
	return tracerRequirement{Version: selected.Version, Node: selected.Node, Requested: version}, nil
}

func fetchRuntimeMetadata(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read CI runtime metadata: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read CI runtime metadata from %s: HTTP %d", address, response.StatusCode)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("CI runtime metadata exceeds 1 MiB")
	}
	return data, nil
}

func actionSelectsJavaScript(step runtimeStep, row map[string]any) (bool, error) {
	languages, err := runtimeValue(step.With["languages"], row)
	if err != nil {
		return false, err
	}
	return languages == "all" || slices.Contains(strings.Fields(languages), "js"), nil
}
