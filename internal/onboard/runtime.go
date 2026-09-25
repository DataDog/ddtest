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

	"github.com/kballard/go-shellquote"
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
	Workflow        string          `json:"workflow"`
	Job             string          `json:"job"`
	Step            int             `json:"step,omitempty"`
	Command         string          `json:"command,omitempty"`
	Resolution      string          `json:"resolution,omitempty"`
	Node            string          `json:"node,omitempty"`
	NodeResolution  *NodeResolution `json:"node_resolution,omitempty"`
	Action          string          `json:"action,omitempty"`
	Tracer          string          `json:"tracer,omitempty"`
	TracerRequested string          `json:"tracer_requested,omitempty"`
	TracerFloating  bool            `json:"tracer_floating"`
	Requirement     string          `json:"requirement,omitempty"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason"`
}

type runtimeStep struct {
	WorkingDirectory string            `yaml:"working-directory"`
	Shell            string            `yaml:"shell"`
	Env              map[string]string `yaml:"env"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	If               string            `yaml:"if"`
	With             map[string]string `yaml:"with"`
}

type runtimeJob struct {
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
func CheckCIRuntimes(ctx context.Context, root string) RuntimeCheck {
	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkCIRuntimes(ctx, root, ltsNodeResolver(client), func(ctx context.Context, action, version string) (tracerRequirement, error) {
		return resolveRequirement(ctx, client, action, version)
	})
}

func checkCIRuntimes(ctx context.Context, root string, resolveNode nodeResolver, resolve requirementResolver) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No candidate GitHub Actions Jest test commands found; CI runtime compatibility was not checked."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
	}
	cache := map[string]tracerRequirement{}
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
				resolutions[i] = resolveTestStep(root, workflow, job, step, "javascript", "jest")
				if resolutions[i].Review {
					result.Review = append(result.Review, RuntimeFinding{Workflow: path, Job: name, Step: i + 1, Command: step.Run, Status: "not checked", Reason: resolutions[i].Reason})
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
				result.Jobs = append(result.Jobs, checkRuntimeJob(ctx, workflow, name, job, resolutions, row, resolveNode, cachedResolve)...)
			}
		}
	}
	if len(result.Jobs) == 0 {
		return result
	}
	result.Status = "compatible"
	result.Reason = "Every identified instrumented Jest step satisfies its selected tracer's Node requirement. Other entry points listed for review are outside this check. This does not execute CI or verify the Datadog backend."
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

func checkRuntimeJob(ctx context.Context, workflow ciWorkflow, name string, job runtimeJob, resolutions []commandResolution, row map[string]any, resolveNode nodeResolver, resolve requirementResolver) []RuntimeFinding {
	finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive"}
	var findings []RuntimeFinding
	fail := func(reason string) []RuntimeFinding {
		finding.Status = "inconclusive"
		finding.Reason = reason
		return append(findings, finding)
	}
	active, err := runtimeCondition(job.If, nil)
	if err != nil {
		return fail(err.Error())
	}
	if !active {
		finding.Status = "excluded"
		finding.Reason = "Job is explicitly excluded."
		return []RuntimeFinding{finding}
	}
	var node, action, version string
	skippedAction := false
	for i, step := range job.Steps {
		uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
		resolution := resolutions[i]
		test := resolution.Matched || resolution.Reason != ""
		if uses != "actions/setup-node" && uses != githubAction && !test {
			continue
		}
		active, err := runtimeCondition(step.If, row)
		if err != nil {
			return fail(err.Error())
		}
		if !active {
			if uses == githubAction {
				skippedAction = true
			}
			continue
		}
		switch uses {
		case "actions/setup-node":
			node, err = runtimeValue(step.With["node-version"], row)
			if err != nil {
				return fail(err.Error())
			}
		case githubAction:
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
		finding = RuntimeFinding{Workflow: workflow.Path, Job: name, Step: i + 1, Command: step.Run, Resolution: resolution.Evidence, Node: node, Status: "inconclusive"}
		if resolution.Reason != "" {
			finding.Reason = "Could not resolve CI test command: " + resolution.Reason
			findings = append(findings, finding)
			continue
		}
		if action == "" {
			finding.Status = "inconclusive"
			if skippedAction {
				finding.Status = "excluded"
			}
			finding.Reason = "No Datadog JavaScript action runs before this test step for this matrix entry. It is not validated as instrumented."
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
			if strings.HasPrefix(node, "lts/") && resolveNode != nil {
				resolution, err := resolveNode(ctx, node)
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
				if reason := checkJestBootstrap(workflow, job, step); reason != "" {
					finding.Status, finding.Reason = "inconclusive", reason
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

// Only certify the action-provided preload when it reaches the test step through
// the workflow environment. Inline/custom loaders need separate review.
func checkJestBootstrap(workflow ciWorkflow, job runtimeJob, step runtimeStep) string {
	var options string
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if value, ok := env["NODE_OPTIONS"]; ok {
			options = value
		}
	}
	options = strings.NewReplacer(
		"${{ env.DD_TRACE_PACKAGE }}", "__DD_ACTION_PRELOAD__",
		"${{env.DD_TRACE_PACKAGE}}", "__DD_ACTION_PRELOAD__",
		"${{ env.DD_TRACE_ESM_IMPORT }}", "__DD_ACTION_IMPORT__",
		"${{env.DD_TRACE_ESM_IMPORT}}", "__DD_ACTION_IMPORT__",
	).Replace(options)
	words, err := shellquote.Split(options)
	if err == nil && !strings.ContainsAny(options, "$`") {
		for i, word := range words {
			if word == "--require=__DD_ACTION_PRELOAD__" || ((word == "-r" || word == "--require") && i+1 < len(words) && words[i+1] == "__DD_ACTION_PRELOAD__") {
				return ""
			}
		}
	}
	return "Cannot verify the action's JavaScript preload in this test step's effective NODE_OPTIONS. Preserve custom initialization and report it for review."
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
