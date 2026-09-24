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
	"os"
	"path/filepath"
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
}

// RuntimeFinding records the metadata used to check one CI runtime.
type RuntimeFinding struct {
	Workflow        string `json:"workflow"`
	Job             string `json:"job"`
	Node            string `json:"node,omitempty"`
	Action          string `json:"action,omitempty"`
	Tracer          string `json:"tracer,omitempty"`
	TracerRequested string `json:"tracer_requested,omitempty"`
	TracerFloating  bool   `json:"tracer_floating"`
	Requirement     string `json:"requirement,omitempty"`
	Status          string `json:"status"`
	Reason          string `json:"reason"`
}

type runtimeStep struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	If   string            `yaml:"if"`
	With map[string]string `yaml:"with"`
}

type runtimeJob struct {
	If       string `yaml:"if"`
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
	return checkCIRuntimes(ctx, root, func(ctx context.Context, action, version string) (tracerRequirement, error) {
		return resolveRequirement(ctx, client, action, version)
	})
}

func checkCIRuntimes(ctx context.Context, root string, resolve requirementResolver) RuntimeCheck {
	result := RuntimeCheck{Status: "not applicable", Reason: "No GitHub Actions Jest test workflows found; CI runtime compatibility was not checked."}
	workflows, _, err := findWorkflows(root, "javascript", "jest")
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
	for _, path := range workflows {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return RuntimeCheck{Status: "inconclusive", Reason: err.Error()}
		}
		var workflow struct {
			Jobs map[string]runtimeJob `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			return RuntimeCheck{Status: "inconclusive", Reason: fmt.Sprintf("Parse %s: %s", path, err)}
		}
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			var commands []string
			for _, step := range job.Steps {
				commands = append(commands, step.Run)
			}
			if !looksLikeTestJob(strings.ToLower(strings.Join(commands, "\n")), "javascript", "jest") {
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
				result.Jobs = append(result.Jobs, checkRuntimeJob(ctx, path, name, job, row, cachedResolve)...)
			}
		}
	}
	if len(result.Jobs) == 0 {
		return result
	}
	result.Status = "compatible"
	result.Reason = "Every checked instrumented CI runtime satisfies its selected tracer's Node requirement. This does not execute CI or verify the Datadog backend."
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

func checkRuntimeJob(ctx context.Context, path, name string, job runtimeJob, row map[string]any, resolve requirementResolver) []RuntimeFinding {
	finding := RuntimeFinding{Workflow: path, Job: name, Status: "inconclusive"}
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
	for _, step := range job.Steps {
		uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
		test := looksLikeTestJob(strings.ToLower(step.Run), "javascript", "jest")
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
		finding.Node = node
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
			finding.Status, finding.Reason = CompareNodeRequirement(node, requirement.Node)
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
