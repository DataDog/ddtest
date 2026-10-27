// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
)

// Expand only bounded, scalar matrices. Includes augment original combinations;
// rows introduced by an include are never candidates for subsequent includes.
// https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/run-job-variations
func runtimeMatrix(value any) ([]map[string]any, error) {
	rows := []map[string]any{{}}
	if value == nil {
		return rows, nil
	}
	matrix, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dynamic CI matrix cannot be resolved statically")
	}
	axes := 0
	for _, key := range slices.Sorted(maps.Keys(matrix)) {
		if key == "include" || key == "exclude" {
			continue
		}
		axes++
		values, ok := matrix[key].([]any)
		if !ok || len(values) == 0 || len(rows)*len(values) > 256 {
			return nil, fmt.Errorf("matrix axis %s is not a bounded static list", key)
		}
		var expanded []map[string]any
		for _, row := range rows {
			for _, value := range values {
				if !staticScalar(value) {
					return nil, fmt.Errorf("matrix axis %s contains an expression or non-scalar value", key)
				}
				next := maps.Clone(row)
				next[key] = value
				expanded = append(expanded, next)
			}
		}
		rows = expanded
	}
	exclusions, err := matrixEntries(matrix, "exclude")
	if err != nil {
		return nil, err
	}
	rows = slices.DeleteFunc(rows, func(row map[string]any) bool {
		for _, exclude := range exclusions {
			match := true
			for key, value := range exclude {
				actual, ok := row[key]
				match = match && ok && reflect.DeepEqual(actual, value)
			}
			if match {
				return true
			}
		}
		return false
	})
	if axes == 0 {
		rows = nil
	}
	originals := make([]map[string]any, len(rows))
	for i, row := range rows {
		originals[i] = maps.Clone(row)
	}
	includes, err := matrixEntries(matrix, "include")
	if err != nil {
		return nil, err
	}
	for _, include := range includes {
		matched := false
		for i, original := range originals {
			compatible := true
			for key, value := range include {
				if actual, exists := original[key]; exists && !reflect.DeepEqual(actual, value) {
					compatible = false
				}
			}
			if compatible {
				maps.Copy(rows[i], include)
				matched = true
			}
		}
		if !matched {
			rows = append(rows, maps.Clone(include))
		}
		if len(rows) > 256 {
			return nil, fmt.Errorf("matrix expands beyond 256 entries")
		}
	}
	return rows, nil
}

func staticScalar(value any) bool {
	switch v := value.(type) {
	case string:
		return !strings.Contains(v, "${{")
	case int, float64, bool:
		return true
	default:
		return false
	}
}

func matrixEntries(matrix map[string]any, name string) ([]map[string]any, error) {
	value, exists := matrix[name]
	if !exists {
		return nil, nil
	}
	entries, ok := value.([]any)
	if !ok || len(entries) > 256 {
		return nil, fmt.Errorf("matrix %s must be a bounded static list", name)
	}
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok || len(row) == 0 {
			return nil, fmt.Errorf("matrix %s requires nonempty scalar objects", name)
		}
		for _, v := range row {
			if !staticScalar(v) {
				return nil, fmt.Errorf("matrix %s contains a dynamic or non-scalar value", name)
			}
		}
		result = append(result, row)
	}
	return result, nil
}

var matrixReference = regexp.MustCompile(`^\$\{\{\s*matrix\.([a-zA-Z0-9_-]+)\s*\}\}$`)

// unresolvedCIValue marks a value only CI can resolve, such as a variable
// reading a predefined CI variable or a command substitution, with the text
// it was written as. Resolving it fails with that text, never the marker.
func unresolvedCIValue(written string) string {
	return "${{ unresolved " + strconv.Quote(written) + " }}"
}

var unresolvedCIMarker = regexp.MustCompile(`\$\{\{ unresolved ("(?:[^"\\]|\\.)*") \}\}`)

// unresolvedCIError explains a value holding an unresolvedCIValue marker.
func unresolvedCIError(value string) error {
	match := unresolvedCIMarker.FindStringSubmatch(value)
	if match == nil {
		return nil
	}
	written, err := strconv.Unquote(match[1])
	if err != nil {
		written = match[1]
	}
	return fmt.Errorf("its CI value %s reads values only CI knows (predefined CI variables or command output); ddtest cannot resolve it locally", written)
}

// matrixProperty looks up key the way GitHub resolves context properties,
// case-insensitively. Keys that differ only by case are ambiguous.
func matrixProperty(row map[string]any, key string) (any, bool, error) {
	var match string
	for name := range row {
		if strings.EqualFold(name, key) {
			if match != "" {
				return nil, false, fmt.Errorf("matrix property %s is ambiguous: keys differ only by case", key)
			}
			match = name
		}
	}
	if match == "" {
		return nil, false, nil
	}
	return row[match], true, nil
}

func runtimeValue(value string, row map[string]any) (string, error) {
	if err := unresolvedCIError(value); err != nil {
		return "", err
	}
	if match := matrixReference.FindStringSubmatch(strings.TrimSpace(value)); match != nil {
		resolved, ok, err := matrixProperty(row, match[1])
		if err != nil {
			return "", err
		}
		if !ok {
			if row != nil {
				return "", nil
			}
			return "", fmt.Errorf("unknown matrix reference %s", value)
		}
		return fmt.Sprint(resolved), nil
	}
	if strings.Contains(value, "${{") {
		return "", fmt.Errorf("cannot statically resolve %s", value)
	}
	return value, nil
}

var numericNode = regexp.MustCompile(`^v?([0-9]+)(?:\.([0-9]+|x|\*))?(?:\.([0-9]+|x|\*))?$`)

// Runtime selectors are resolved to exact releases before this comparison.
func CompareNodeRequirement(node, requirement string) (string, string) {
	constraint, err := semver.NewConstraint(requirement)
	if err != nil || strings.TrimSpace(requirement) == "" {
		return StatusInconclusive, fmt.Sprintf("Cannot parse Node engine requirement %q.", requirement)
	}
	version, err := semver.NewVersion(strings.TrimSuffix(strings.TrimSuffix(node, ".x"), ".*"))
	if err != nil {
		return StatusInconclusive, fmt.Sprintf("Node version %q is unresolved; compatibility was not verified.", node)
	}
	if constraint.Check(version) {
		return StatusCompatible, fmt.Sprintf("Node %s satisfies %s.", node, requirement)
	}
	return StatusIncompatible, fmt.Sprintf("Node %s does not satisfy %s. Preserve test coverage and choose a compatible tracer/runtime.", node, requirement)
}

// jobMatrix expands a job's matrix. An axis computed at runtime (for example
// fromJson(needs.versions.outputs.list)) that only step commands read becomes a
// single unverified value: the job's tests are still discovered and validated
// with the locally installed dependencies, and computed lists the axes whose
// other values remain unverified. Any other use of such an axis (runner, runtime
// selection, environment, working directory, conditions) keeps it unresolved.
func jobMatrix(job runtimeJob) (rows []map[string]any, computed []string, err error) {
	rows, err = runtimeMatrix(job.Strategy.Matrix)
	matrix, ok := job.Strategy.Matrix.(map[string]any)
	if err == nil || !ok {
		return rows, nil, err
	}
	bounded := maps.Clone(matrix)
	for key, value := range matrix {
		if text, isText := value.(string); isText && key != "include" && key != "exclude" && strings.Contains(text, "${{") {
			bounded[key] = []any{computedAxisSentinel}
			computed = append(computed, key)
		}
	}
	if len(computed) == 0 {
		return nil, nil, err
	}
	slices.Sort(computed)
	settings := job
	settings.Strategy.Matrix = nil
	settings.Steps = slices.Clone(job.Steps)
	for i := range settings.Steps {
		settings.Steps[i].Run = ""
	}
	encoded, marshalErr := yaml.Marshal(settings)
	if marshalErr != nil {
		return nil, nil, err
	}
	for _, axis := range computed {
		if regexp.MustCompile(`(?i)\bmatrix\.` + regexp.QuoteMeta(axis) + `\b`).Match(encoded) {
			return nil, nil, err
		}
	}
	rows, boundedErr := runtimeMatrix(bounded)
	if boundedErr != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		for key, value := range row {
			if value == computedAxisSentinel {
				row[key] = computedAxisValue(key)
			}
		}
	}
	return rows, computed, nil
}

const computedAxisSentinel = "\x00computed"

// The value stays a workflow expression, so any command that reads it remains
// visibly unresolved instead of running with a guessed value.
func computedAxisValue(axis string) string {
	return "${{ matrix." + axis + " }}"
}
