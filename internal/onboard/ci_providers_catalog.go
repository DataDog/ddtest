// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// CICommandJob retains the commands declared by one concrete CI job.
type CICommandJob struct {
	Name         string            `json:"name"`
	Stage        string            `json:"stage,omitempty"`
	Image        string            `json:"image,omitempty"`
	Variables    map[string]string `json:"variables,omitempty"`
	BeforeScript []string          `json:"before_script,omitempty"`
	Script       []string          `json:"script,omitempty"`
	AfterScript  []string          `json:"after_script,omitempty"`
	Unresolved   []string          `json:"unresolved,omitempty"`
}

// ReadGitLabCommandJobs reads an expanded pipeline without executing its scripts.
func ReadGitLabCommandJobs(root, file string) ([]CICommandJob, []string, error) {
	if !filepath.IsLocal(file) {
		return nil, nil, fmt.Errorf("CI configuration must be inside the repository")
	}
	file = filepath.ToSlash(filepath.Clean(file))
	config := &gitLabConfig{root: root, definitions: map[string]*yaml.Node{}, owners: map[string]string{}, files: map[*yaml.Node]string{}}
	if err := config.load(file, nil); err != nil {
		return nil, nil, err
	}
	globals := gitLabVariables(config.definitions["variables"])
	jobs := []CICommandJob{}
	for _, job := range config.jobs() {
		vars := gitLabInheritedVariables(globals, job.fields)
		if vars == nil {
			vars = map[string]string{}
		}
		maps.Copy(vars, gitLabVariables(job.fields["variables"]))
		image := gitLabScalar(job.fields["image"])
		if image == "" {
			image = gitLabScalar(gitLabMembers(job.fields["image"])["name"])
		}
		entry := CICommandJob{Name: job.name, Stage: gitLabScalar(job.fields["stage"]), Image: gitLabExpand(image, vars),
			Variables: vars, BeforeScript: gitLabStrings(job.fields["before_script"]),
			Script: gitLabStrings(job.fields["script"]), AfterScript: gitLabStrings(job.fields["after_script"])}
		if job.problem != "" {
			entry.Unresolved = append(entry.Unresolved, job.problem)
		}
		if node := gitLabAlias(job.fields["parallel"]); node != nil && node.Kind == yaml.MappingNode {
			entry.Unresolved = append(entry.Unresolved, "parallel matrix requires expansion")
		}
		for _, key := range []string{"rules", "only", "except", "trigger"} {
			if job.fields[key] != nil {
				entry.Unresolved = append(entry.Unresolved, key+" is not evaluated by catalog discovery")
			}
		}
		jobs = append(jobs, entry)
	}
	gaps := []string{}
	for _, gap := range config.gaps {
		gaps = append(gaps, gap.file+": "+gap.reason)
	}
	slices.Sort(gaps)
	return jobs, slices.Compact(gaps), nil
}

// GitLabRunnerArguments reads the literal arguments forwarded by a runner call.
func GitLabRunnerArguments(scripts []string) ([]string, error) {
	var matches [][]string
	for _, script := range scripts {
		file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
		if err != nil {
			return nil, fmt.Errorf("parse CI runner script: %w", err)
		}
		var problem error
		syntax.Walk(file, func(node syntax.Node) bool {
			call, ok := node.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			name, err := literalShellWord(call.Args[0])
			if err != nil || name != "scripts/run-tests" {
				return true
			}
			args := []string{}
			after := false
			for _, word := range call.Args[1:] {
				value, err := literalShellWord(word)
				if err == nil && value == "--" {
					after = true
					continue
				}
				if !after {
					continue
				}
				if err != nil {
					problem = fmt.Errorf("dynamic test arguments require resolution")
					return false
				}
				args = append(args, value)
			}
			matches = append(matches, args)
			return true
		})
		if problem != nil {
			return nil, problem
		}
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("expected one scripts/run-tests invocation, found %d", len(matches))
	}
	return matches[0], nil
}
