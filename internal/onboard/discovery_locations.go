package onboard

import (
	"encoding/json"
	"fmt"
	"github.com/DataDog/ddtest/internal/settings"
	"maps"
	"slices"
	"strings"
)

type commandLocation struct {
	Workflow, Job string
	Matrix        map[string]any
	Step          int
	Command       string
}

type scopeDiagnostic struct {
	Location commandLocation
	Reason   string
}

func matrixLabel(row map[string]any) string {
	var values []string
	for _, key := range slices.Sorted(maps.Keys(row)) {
		value, _ := json.Marshal(row[key])
		values = append(values, key+": "+string(value))
	}
	return strings.Join(values, ", ")
}

func (location commandLocation) String() string {
	label := location.Workflow + " / " + location.Job
	if len(location.Matrix) > 0 {
		label += " (" + matrixLabel(location.Matrix) + ")"
	}
	if location.Step > 0 {
		label += fmt.Sprintf(" / step %d (%s)", location.Step, location.Command)
	}
	return label
}

// Group by structured identity. Delimiters inside job names, commands or matrix
// values are display data and can never change the grouping key.
func groupMatrixDiagnostics(diagnostics []scopeDiagnostic) []string {
	type identity struct {
		workflow, job, command, reason string
		step                           int
		matrix                         bool
	}
	type group struct {
		location commandLocation
		reason   string
		rows     []string
	}
	indexes := map[identity]int{}
	var groups []group
	for _, diagnostic := range diagnostics {
		loc := diagnostic.Location
		key := identity{loc.Workflow, loc.Job, loc.Command, diagnostic.Reason, loc.Step, len(loc.Matrix) > 0}
		index, found := indexes[key]
		if !found {
			index = len(groups)
			indexes[key] = index
			loc.Matrix = nil
			groups = append(groups, group{location: loc, reason: diagnostic.Reason})
		}
		if len(diagnostic.Location.Matrix) > 0 {
			row := matrixLabel(diagnostic.Location.Matrix)
			if !slices.Contains(groups[index].rows, row) {
				groups[index].rows = append(groups[index].rows, row)
			}
		}
	}
	result := make([]string, 0, len(groups))
	for _, group := range groups {
		text := group.location.String() + ": " + group.reason
		if len(group.rows) > 0 {
			text += fmt.Sprintf(" [affects %d matrix entries; e.g. %s]", len(group.rows), strings.Join(group.rows[:min(2, len(group.rows))], "; "))
		}
		result = append(result, text)
	}
	return result
}

// WithGitHubActionsDefaults adds the variables every GitHub Actions step sees,
// so a local replay of a CI configuration doesn't fall back to local settings
// when the configuration branches on them. Explicit values take precedence.
func WithGitHubActionsDefaults(env map[string]string) map[string]string {
	return mergeEnvironment(map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}, env)
}

func mergeEnvironment(outer, inner map[string]string) map[string]string {
	if len(outer)+len(inner) == 0 {
		return nil
	}
	result := maps.Clone(outer)
	if result == nil {
		result = map[string]string{}
	}
	maps.Copy(result, inner)
	return result
}

// WithCIDefaults preserves the selected provider; native jobs never acquire
// GITHUB_ACTIONS merely because the validation runs locally.
func WithCIDefaults(env map[string]string, providers ...string) map[string]string {
	provider := settings.GetCIProvider()
	if len(providers) > 0 && providers[0] != "" {
		provider = providers[0]
	}
	if provider == "" {
		for name, key := range map[string]string{"gitlab": "GITLAB_CI", "circleci": "CIRCLECI", "buildkite": "BUILDKITE"} {
			if env[key] == "true" {
				provider = name
				break
			}
		}
	}
	if provider == "" {
		provider = "github"
	}
	return providerEnvironment(provider, env)
}
