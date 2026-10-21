// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

func stepNumber(step runtimeStep, index int) int {
	if step.Number != 0 {
		return step.Number
	}
	return index + 1
}

// environments contains workflow/job context only. Composite conditions must
// not be evaluated against a flattened child's overriding environment.
func stepCondition(step runtimeStep, row map[string]any, environments ...map[string]string) (bool, error) {
	var base map[string]string
	if len(environments) > 0 {
		base = environments[0]
	}
	conditions := append(slices.Clone(step.Parents), runtimeConditionScope{Expression: step.If, Env: step.Env, Inputs: step.Inputs})
	for _, condition := range conditions {
		env := conditionEnvironment(ciWorkflow{Env: base}, runtimeJob{}, runtimeStep{Env: condition.Env}, row)
		for key, value := range condition.Inputs {
			env["\x00inputs."+key] = value
		}
		active, err := runtimeCondition(condition.Expression, row, env)
		if err != nil || !active {
			return active, err
		}
	}
	return true, nil
}

// Only read repository-local composite actions. Preserve ordering and parent
// conditions; never execute an action or pretend to interpret arbitrary JS.
func expandCompositeSteps(root, source string, steps []runtimeStep) []runtimeStep {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	remaining := 256
	var expand func(runtimeStep, []string) []runtimeStep
	expand = func(step runtimeStep, stack []string) []runtimeStep {
		remaining--
		fail := func(err error) []runtimeStep {
			step.ResolutionError = fmt.Sprintf("%s: %v", step.Source, err)
			return []runtimeStep{step}
		}
		if remaining < 0 || len(stack) >= 16 {
			return fail(fmt.Errorf("local action expansion exceeds its depth or step limit"))
		}
		if !strings.HasPrefix(step.Uses, "./") {
			return []runtimeStep{step}
		}
		path, err := localActionPath(root, step.Uses)
		if err != nil {
			return fail(err)
		}
		if slices.Contains(stack, path) {
			return fail(fmt.Errorf("local composite action cycle: %s", step.Uses))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fail(err)
		}
		var action struct {
			Inputs map[string]struct{ Default string } `yaml:"inputs"`
			Runs   struct {
				Using string        `yaml:"using"`
				Steps []runtimeStep `yaml:"steps"`
			} `yaml:"runs"`
		}
		if err := yaml.Unmarshal(data, &action); err != nil {
			return fail(err)
		}
		if action.Runs.Using != "composite" {
			return fail(fmt.Errorf("local action %s is not a statically readable composite", step.Uses))
		}
		inputs := map[string]string{}
		for key, input := range action.Inputs {
			inputs[key] = input.Default
		}
		maps.Copy(inputs, step.With)
		relative, _ := filepath.Rel(root, path)
		var result []runtimeStep
		for i, child := range action.Runs.Steps {
			child.Number = step.Number
			child.Source = fmt.Sprintf("%s / step %d", filepath.ToSlash(relative), i+1)
			child.Parents = append(slices.Clone(step.Parents), runtimeConditionScope{Expression: step.If, Env: maps.Clone(step.Env), Inputs: maps.Clone(step.Inputs)})
			child.Inputs = maps.Clone(inputs)
			resolveInput := func(value string) string {
				return inputReference.ReplaceAllStringFunc(value, func(reference string) string {
					match := inputReference.FindStringSubmatch(reference)
					if resolved, ok := inputs[match[1]]; ok {
						return resolved
					}
					return reference
				})
			}
			child.Run = resolveInput(child.Run)
			child.WorkingDirectory = resolveInput(child.WorkingDirectory)
			child.Shell = resolveInput(child.Shell)
			// Keep input expressions typed: the string "false" is truthy.
			if child.WorkingDirectory == "" {
				child.WorkingDirectory = "."
			}
			child.Uses = resolveInput(child.Uses)
			child.With = maps.Clone(child.With)
			for key, value := range child.With {
				child.With[key] = resolveInput(value)
			}
			child.Env = maps.Clone(child.Env)
			for key, value := range child.Env {
				child.Env[key] = resolveInput(value)
			}
			env := maps.Clone(step.Env)
			if env == nil {
				env = map[string]string{}
			}
			maps.Copy(env, child.Env)
			child.Env = env
			result = append(result, expand(child, append(slices.Clone(stack), path))...)
			if remaining < 0 {
				break
			}
		}
		return result
	}
	var result []runtimeStep
	for i, step := range steps {
		step.Number = i + 1
		step.Source = fmt.Sprintf("%s / step %d", source, i+1)
		result = append(result, expand(step, nil)...)
		if remaining < 0 {
			break
		}
	}
	return result
}

var inputReference = regexp.MustCompile(`\$\{\{\s*inputs\.([a-zA-Z0-9_-]+)\s*\}\}`)

func localActionPath(root, uses string) (string, error) {
	if !filepath.IsLocal(uses) || strings.Contains(uses, "${{") {
		return "", fmt.Errorf("cannot resolve local action path %q", uses)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	for _, filename := range []string{"action.yml", "action.yaml"} {
		path, err := filepath.EvalSymlinks(filepath.Join(root, uses, filename))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || !filepath.IsLocal(relative) {
			return "", fmt.Errorf("local action escapes the repository: %s", uses)
		}
		return path, nil
	}
	return "", fmt.Errorf("local action metadata not found: %s", uses)
}
