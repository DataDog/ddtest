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

func runtimeValue(value string, row map[string]any) (string, error) {
	if match := matrixReference.FindStringSubmatch(strings.TrimSpace(value)); match != nil {
		resolved, ok := row[match[1]]
		if !ok {
			return "", fmt.Errorf("unknown matrix reference %s", value)
		}
		return fmt.Sprint(resolved), nil
	}
	if strings.Contains(value, "${{") {
		return "", fmt.Errorf("cannot statically resolve %s", value)
	}
	return strings.TrimSpace(value), nil
}

var numericNode = regexp.MustCompile(`^v?([0-9]+)(?:\.([0-9]+|x|\*))?(?:\.([0-9]+|x|\*))?$`)
var minimumEngine = regexp.MustCompile(`^>=\s*([0-9]+(?:\.[0-9]+){0,2})$`)

func nodeInterval(value string) ([3]int, int, bool) {
	var version [3]int
	match := numericNode.FindStringSubmatch(value)
	if match == nil {
		return version, 0, false
	}
	precision := 0
	for i, part := range match[1:] {
		if part == "" || part == "x" || part == "*" {
			continue
		}
		if precision != i {
			return version, 0, false
		}
		number, err := strconv.Atoi(part)
		if err != nil || number > 1000000 {
			return version, 0, false
		}
		version[i] = number
		precision++
	}
	return version, precision, true
}

// CompareNodeRequirement checks a static Node version against a tracer minimum.
func CompareNodeRequirement(node, requirement string) (string, string) {
	minimum := minimumEngine.FindStringSubmatch(requirement)
	if minimum == nil {
		return "inconclusive", fmt.Sprintf("Unsupported Node engine range %q; no compatibility assumption was made.", requirement)
	}
	required, _, ok := nodeInterval(minimum[1])
	if !ok {
		return "inconclusive", "Cannot parse tracer Node requirement."
	}
	lower, precision, ok := nodeInterval(node)
	if !ok {
		return "inconclusive", fmt.Sprintf("Node version %q is missing or dynamic; use an explicit setup-node version to check it.", node)
	}
	if slices.Compare(lower[:], required[:]) >= 0 {
		return "compatible", fmt.Sprintf("Node %s satisfies %s.", node, requirement)
	}
	if precision < 3 {
		upper := lower
		upper[precision-1]++
		if slices.Compare(upper[:], required[:]) > 0 {
			return "inconclusive", fmt.Sprintf("Node %s can resolve below or above %s; use an exact version.", node, requirement)
		}
	}
	return "incompatible", fmt.Sprintf("Node %s does not satisfy %s. Keep this test coverage, but exclude this runtime from instrumentation or choose a compatible tracer/runtime.", node, requirement)
}
