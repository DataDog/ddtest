package onboard

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const githubAction = "datadog/test-visibility-github-action"

type workflowDiscovery struct {
	Workflows, Configured, Unresolved, Review []string
}

type runDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
	Shell            string `yaml:"shell"`
}

type ciDefaults struct {
	Run runDefaults `yaml:"run"`
}

type ciWorkflow struct {
	On       any `yaml:"on"`
	Path     string
	Defaults ciDefaults            `yaml:"defaults"`
	Env      map[string]string     `yaml:"env"`
	Jobs     map[string]runtimeJob `yaml:"jobs"`
}

func findWorkflows(root, language, name string) (workflowDiscovery, error) {
	var result workflowDiscovery
	workflows, err := readWorkflows(root)
	if err != nil {
		return result, err
	}
	actionLanguage := setupActionLanguage(language)
	for _, workflow := range workflows {
		found, configured := false, true
		for _, jobName := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[jobName]
			hasAction := false
			for i, step := range job.Steps {
				condition := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(step.If), "${{"), "}}"))
				if condition == "false" {
					continue
				}
				uses, _, hasVersion := strings.Cut(strings.ToLower(step.Uses), "@")
				languages := step.With["languages"]
				if uses == githubAction && hasVersion && (condition == "" || condition == "true" || condition == "success()") && !strings.Contains(languages, "${{") {
					hasAction = hasAction || languages == "all" || slices.Contains(strings.Fields(languages), actionLanguage)
				}
				resolution := resolveTestStep(root, workflow, job, step, language, name)
				if resolution.Review {
					result.Review = append(result.Review, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), step.Run, resolution.Reason))
					continue
				}
				if resolution.Matched || resolution.Reason != "" {
					found = true
					configured = configured && hasAction && resolution.Reason == ""
				}
				if resolution.Reason != "" {
					result.Unresolved = append(result.Unresolved, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), step.Run, resolution.Reason))
				}
			}
		}
		if found {
			result.Workflows = append(result.Workflows, workflow.Path)
			if configured {
				result.Configured = append(result.Configured, workflow.Path)
			}
		}
	}
	return result, nil
}
