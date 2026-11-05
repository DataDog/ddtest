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
				setup, isSetup := datadogSetup(step)
				languages := setup.Inputs["languages"]
				if isSetup && setup.Pinned && (condition == "" || condition == "true" || condition == "success()") && !strings.Contains(languages, "${{") {
					hasAction = hasAction || languages == "all" || slices.Contains(strings.Fields(languages), actionLanguage)
				}
				resolution := resolveTestStep(root, workflow, job, step, language, name)
				if resolution.Review {
					result.Review = append(result.Review, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), StepCommandLabel(step.Run), resolution.Reason))
					continue
				}
				if resolution.Matched || resolution.Reason != "" {
					found = true
					configured = configured && hasAction && resolution.Reason == ""
				}
				if resolution.Reason != "" {
					result.Unresolved = append(result.Unresolved, fmt.Sprintf("%s / %s / step %d (%s): %s", workflow.Path, jobName, stepNumber(step, i), StepCommandLabel(step.Run), resolution.Reason))
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
