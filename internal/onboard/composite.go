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

func stepCondition(step runtimeStep, row map[string]any) (bool, error) {
	for _, condition := range append(slices.Clone(step.Parents), step.If) {
		active, err := runtimeCondition(condition, row)
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
			child.Parents = append(slices.Clone(step.Parents), step.If)
			child.With = maps.Clone(child.With)
			for key, value := range child.With {
				if match := inputReference.FindStringSubmatch(strings.TrimSpace(value)); match != nil {
					if resolved, ok := inputs[match[1]]; ok {
						child.With[key] = resolved
					}
				}
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

var inputReference = regexp.MustCompile(`^\$\{\{\s*inputs\.([a-zA-Z0-9_-]+)\s*\}\}$`)

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
