// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
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
	Scenario        string          `json:"scenario,omitempty"`
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

type runtimeStep struct {
	ID               string            `yaml:"id"`
	ActionNode       string            `yaml:"-"`
	Number           int               `yaml:"-"`
	Source           string            `yaml:"-"`
	Parents          []string          `yaml:"-"`
	ResolutionError  string            `yaml:"-"`
	WorkingDirectory string            `yaml:"working-directory"`
	Shell            string            `yaml:"shell"`
	Env              map[string]string `yaml:"env"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	If               string            `yaml:"if"`
	With             map[string]string `yaml:"with"`
}

type runtimeJob struct {
	Context   map[string]string `yaml:"-"`
	Container any               `yaml:"container"`
	RunsOn    any               `yaml:"runs-on"`
	Defaults  ciDefaults        `yaml:"defaults"`
	Env       map[string]string `yaml:"env"`
	If        string            `yaml:"if"`
	Strategy  struct {
		Matrix any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []runtimeStep `yaml:"steps"`
}

type tracerRequirement struct{ Version, Node, Requested string }
type requirementResolver func(context.Context, string, string) (tracerRequirement, error)

// CheckCIRuntimes checks Jest or Vitest jobs in GitHub Actions without executing workflow
// code or contacting Datadog. Unknown expressions are never treated as a pass.
func CheckCIRuntimes(ctx context.Context, root string, frameworks ...string) RuntimeCheck {
	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkCIRuntimes(ctx, root, setupNodeResolver(client), func(ctx context.Context, action, version string) (tracerRequirement, error) {
		return resolveRequirement(ctx, client, action, version)
	}, frameworks...)
}

func checkCIRuntimes(ctx context.Context, root string, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) RuntimeCheck {
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	result := RuntimeCheck{Status: "not applicable", Reason: "No candidate GitHub Actions " + framework + " test commands found; CI runtime compatibility was not checked."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	cache := map[string]tracerRequirement{}
	actionNodes := map[string]string{}
	cachedResolve := func(ctx context.Context, action, version string) (tracerRequirement, error) {
		key := action + "\n" + version
		if value, ok := cache[key]; ok {
			return value, nil
		}
		value, err := resolve(ctx, action, version)
		if err == nil {
			cache[key] = value
		}
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
					result.Review = append(result.Review, RuntimeFinding{Workflow: path, Job: name, Step: stepNumber(step, i), Command: step.Run, Status: "not checked", Reason: resolutions[i].Reason})
					resolutions[i] = commandResolution{}
				}
				candidate = candidate || resolutions[i].Matched || resolutions[i].Reason != ""
			}
			if !candidate {
				continue
			}
			rows, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: "inconclusive", Reason: err.Error()})
				continue
			}
			if len(rows) == 0 {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: "inconclusive", Reason: "Static matrix has no entries; no instrumented CI runtime could be checked."})
				continue
			}
			for _, row := range rows {
				rowResolutions := slices.Clone(resolutions)
				rowJob := job
				rowJob.Steps = slices.Clone(job.Steps)
				rowCandidate := false
				for i, step := range job.Steps {
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
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
				jobs := []runtimeJob{rowJob}
				if framework == "cypress" {
					for i, step := range rowJob.Steps {
						uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
						if uses == "actions/setup-node" {
							version, e := cypressNodeVersion(root, step, row)
							rowJob.Steps[i].With = maps.Clone(step.With)
							rowJob.Steps[i].With["node-version"] = version
							if e != nil {
								rowJob.Steps[i].ResolutionError = e.Error()
							}
						}
						if uses == "cypress-io/github-action" && step.With["command"] == "" && step.With["runTests"] != "false" {
							node := actionNodes[step.Uses]
							if node == "" {
								node, err = CypressActionNode(ctx, step.Uses)
								if err == nil {
									actionNodes[step.Uses] = node
								}
							}
							rowJob.Steps[i].ActionNode = node
							if node == "" {
								rowResolutions[i].Reason = "Cannot resolve Cypress action Node runtime"
							}
						}
					}
					var contextErr error
					jobs, contextErr = cypressContextJobs(root, workflow, rowJob)
					if contextErr != nil {
						result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: contextErr.Error()})
						continue
					}
				}
				for _, contextJob := range jobs {
					result.Jobs = append(result.Jobs, checkRuntimeJob(ctx, workflow, name, contextJob, rowResolutions, row, resolveNode, cachedResolve, framework)...)
				}
			}
		}
	}
	if len(result.Jobs) == 0 {
		return result
	}
	result.Status = "compatible"
	result.Reason = "Every identified instrumented " + framework + " step satisfies its selected tracer's Node requirement. Other entry points listed for review are outside this check. This does not execute CI or verify the Datadog backend."
	checked := false
	for _, job := range result.Jobs {
		checked = checked || job.Status == "compatible"
		if job.Status == "incompatible" {
			result.Status = "incompatible"
			break
		}
		if job.Status == "inconclusive" {
			result.Status = "inconclusive"
		}
	}
	if !checked && result.Status == "compatible" {
		result.Status = "inconclusive"
	}
	if result.Status != "compatible" {
		result.Reason = "CI runtime validation is incomplete or incompatible. Fix known incompatibilities. Unknown syntax or metadata requires review; preserve the workflow and report that tool limitation without claiming CI was validated."
	}
	return result
}

func checkRuntimeJob(ctx context.Context, workflow ciWorkflow, name string, job runtimeJob, resolutions []commandResolution, row map[string]any, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) []RuntimeFinding {
	finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Scenario: job.Context["scenario"], Status: "inconclusive"}
	var findings []RuntimeFinding
	fail := func(reason string) []RuntimeFinding {
		finding.Status = "inconclusive"
		finding.Reason = reason
		return append(findings, finding)
	}
	active, err := possibleJobCondition(job.If, row)
	if job.Context != nil {
		active, err = runtimeCondition(job.If, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
	}
	if err != nil {
		return fail(err.Error())
	}
	if !active {
		finding.Status = "excluded"
		finding.Reason = "Job is explicitly excluded."
		return []RuntimeFinding{finding}
	}
	var node, nodeSource, nodePlatform, action, version string
	if len(frameworks) > 0 && frameworks[0] == "cypress" {
		node = cypressContainerNode(job.Container)
		if node != "" {
			nodeSource = workflow.Path + " / job container"
		}
	}
	skippedAction := false
	for i, step := range job.Steps {
		uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
		resolution := resolutions[i]
		test := resolution.Matched || resolution.Reason != ""
		nvmNode := nvmRuntime(step.Run)
		if uses != "actions/setup-node" && uses != githubAction && nvmNode == "" && !test {
			continue
		}
		finding = RuntimeFinding{Workflow: workflow.Path, Job: name, Scenario: job.Context["scenario"], Step: stepNumber(step, i), Source: step.Source, Command: step.Run, Resolution: resolution.Evidence, Node: node, NodeSource: nodeSource, Status: "inconclusive"}
		if uses == githubAction {
			finding.Action = step.Uses
		}
		active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
		if err != nil {
			return fail(err.Error())
		}
		if !active {
			if job.Context != nil && step.ID != "" {
				job.Context["steps."+step.ID+".outcome"] = "skipped"
			}
			if uses == githubAction {
				skippedAction = true
			}
			continue
		}
		if job.Context != nil && step.ID != "" {
			job.Context["steps."+step.ID+".outcome"] = "success"
		}
		if step.ResolutionError != "" {
			return fail(step.ResolutionError)
		}
		if nvmNode != "" {
			node, nodeSource = nvmNode, step.Source
			nodePlatform = nodeDistributionPlatform(job.RunsOn, "", row)
		}
		switch uses {
		case "actions/setup-node":
			node, err = runtimeValue(step.With["node-version"], row)
			nodePlatform = nodeDistributionPlatform(job.RunsOn, step.With["architecture"], row)
			nodeSource = step.Source
			if err != nil {
				return fail(err.Error())
			}
		case githubAction:
			if strings.HasPrefix(job.Context["scenario"], "fork_pull_request") && (strings.Contains(step.With["api_key"], "secrets.") || strings.Contains(step.With["api-key"], "secrets.")) {
				finding.Status, finding.Code, finding.Reason = "incompatible", "missing_fork_guard", "Datadog installation still runs on a fork pull request without credentials. Guard installation and its preload together, preserving the existing test step."
				return append(findings, finding)
			}
			languages, err := runtimeValue(step.With["languages"], row)
			if err != nil {
				return fail(err.Error())
			}
			if languages != "all" && !slices.Contains(strings.Fields(languages), "js") {
				continue
			}
			action = step.Uses
			version, err = runtimeValue(step.With["js-tracer-version"], row)
			if err != nil {
				return fail(err.Error())
			}
			if _, explicit := step.With["js-tracer-version"]; explicit && version == "" {
				// An explicit empty input overrides the action default; its
				// installer requests npm's latest version instead.
				version = "latest"
			}
		}
		if !test {
			continue
		}
		if resolution.Reason != "" {
			finding.Reason = "Could not resolve CI test command: " + resolution.Reason
			findings = append(findings, finding)
			// Unknown siblings do not erase a known test's installation check.
			if !resolution.Matched {
				continue
			}
		}
		if len(frameworks) > 0 && frameworks[0] == "mocha" && mochaInstrumentationDisabled(workflow, job, step, row) {
			finding.Status, finding.Code = "excluded", "excluded_runtime"
			finding.Reason = "Mocha tests still run, but DD_CIVISIBILITY_ENABLED=false explicitly excludes this entry from instrumentation. This runtime was not validated with the tracer."
			findings = append(findings, finding)
			continue
		}
		if action == "" {
			finding.Status = "incompatible"
			finding.Code = "missing_instrumentation"
			if skippedAction {
				if len(frameworks) > 0 && frameworks[0] == "cypress" {
					if reason := cypressExcludedBootstrap(workflow, job, step, row); reason != "" {
						finding.Reason = reason
						findings = append(findings, finding)
						continue
					}
				}
				finding.Status = "excluded"
				finding.Code = "excluded_runtime"
			}
			finding.Reason = "No Datadog JavaScript action runs before this test step for this matrix entry. It is not validated as instrumented."
			if !skippedAction {
				finding.Reason += fmt.Sprintf(" Add tracer installation and initialization in %s / %s before step %d (%s). This is missing instrumentation, not unsupported syntax.", workflow.Path, name, finding.Step, step.Run)
			}
		} else {
			requirement, err := resolve(ctx, action, version)
			if err != nil {
				return fail(err.Error())
			}
			finding.Action = action
			finding.Tracer = "dd-trace@" + requirement.Version
			finding.TracerRequested = requirement.Requested
			if finding.TracerRequested == "" {
				finding.TracerRequested = version
			}
			finding.TracerFloating = finding.TracerRequested != "" && strings.TrimPrefix(finding.TracerRequested, "v") != requirement.Version
			finding.Requirement = requirement.Node
			resolvedNode := node
			if len(resolution.Tests) > 0 && resolution.Tests[0].Cypress != nil && resolution.Tests[0].Cypress.Action != "" {
				resolvedNode = step.ActionNode
				finding.Node, finding.NodeSource = resolvedNode, step.Uses+" / runs.using"
			}
			if (strings.HasPrefix(node, "lts/") || latestNodeAlias(node)) && resolveNode != nil {
				resolution, err := resolveNode(ctx, node, nodePlatform)
				if err != nil {
					finding.Reason = "Cannot resolve setup-node alias: " + err.Error()
					findings = append(findings, finding)
					continue
				}
				finding.NodeResolution = &resolution
				resolvedNode = resolution.Version
			}
			finding.Status, finding.Reason = CompareNodeRequirement(resolvedNode, requirement.Node)
			if finding.Status == "compatible" {
				if reason := checkTestBootstrap(workflow, job, step, row, resolution.Tests, frameworks...); reason != "" {
					finding.Status, finding.Reason = "inconclusive", reason
					finding.Code = "unverified_initialization"
				}
			}
		}
		findings = append(findings, finding)
	}
	if len(findings) == 0 {
		return fail("No active test step could be checked.")
	}
	return findings
}

func resolveRequirement(ctx context.Context, client *http.Client, action, version string) (tracerRequirement, error) {
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
	}
	if version == "" || strings.Contains(version, "${{") {
		return tracerRequirement{}, fmt.Errorf("could not resolve the action's JavaScript tracer version")
	}
	data, err := fetchRuntimeMetadata(ctx, client, "https://registry.npmjs.org/dd-trace/"+url.PathEscape(version))
	if err != nil {
		return tracerRequirement{}, err
	}
	var manifest struct {
		Version string `json:"version"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return tracerRequirement{}, fmt.Errorf("decode dd-trace metadata: %w", err)
	}
	if manifest.Version == "" || manifest.Engines.Node == "" {
		return tracerRequirement{}, fmt.Errorf("dd-trace metadata has no resolved version or Node requirement")
	}
	return tracerRequirement{Version: manifest.Version, Node: manifest.Engines.Node, Requested: version}, nil
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

// An explicit legacy test step can precede the conditional Datadog action.
// Honor effective literal overrides without inferring intent from a missing action.
func mochaInstrumentationDisabled(workflow ciWorkflow, job runtimeJob, step runtimeStep, rows ...map[string]any) bool {
	value := ""
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if current, ok := env["DD_CIVISIBILITY_ENABLED"]; ok {
			value = current
		}
	}
	if len(rows) > 0 {
		resolved, err := bootstrapOptions(value, rows[0], conditionEnvironment(workflow, job, step, rows[0]))
		return err == nil && resolved == "false"
	}
	return value == "false"
}
