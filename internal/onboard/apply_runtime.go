package onboard

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Partition only fully resolved static matrices. Unsupported rows keep their
// original test command and receive neither installation nor preload.
func setupRuntimeCondition(root string, job runtimeJob, requirement string) (string, error) {
	if requirement == "" {
		return "", nil
	}
	rows, err := runtimeMatrix(job.Strategy.Matrix)
	if err != nil {
		return "", err
	}
	var compatible []bool
	for _, row := range rows {
		node := ""
		for _, step := range job.Steps {
			if strings.HasPrefix(strings.ToLower(step.Uses), "actions/setup-node@") {
				node, err = cypressNodeVersion(root, step, row)
			} else if strings.Contains(step.Run, "nvm") {
				resolved, e := interpolateMatrix(step.Run, row)
				if e != nil {
					return "", e
				}
				if value := nvmRuntime(resolved); value != "" {
					node = value
				}
			}
			if err != nil {
				return "", err
			}
		}
		status, reason := CompareNodeRequirement(node, requirement)
		if status != "compatible" && status != "incompatible" {
			return "", fmt.Errorf("cannot partition unresolved CI runtime: %s", reason)
		}
		compatible = append(compatible, status == "compatible")
	}
	if !slices.Contains(compatible, false) {
		return "", nil
	}
	if !slices.Contains(compatible, true) {
		return "", fmt.Errorf("no CI runtime supports tracer Node requirement %s", requirement)
	}
	// Find one scalar matrix column that identifies the same partition. This
	// avoids replacing the customer's strategy or adding metadata to every row.
	for _, key := range slices.Sorted(maps.Keys(rows[0])) {
		classes, values, valid := map[string]bool{}, []string{}, true
		for i, row := range rows {
			value, exists := row[key]
			if !exists {
				valid = false
				break
			}
			text := ""
			switch v := value.(type) {
			case string:
				text = "'" + strings.ReplaceAll(v, "'", "''") + "'"
			case int, float64, bool:
				text = fmt.Sprint(v)
			default:
				valid = false
			}
			if old, exists := classes[text]; exists && old != compatible[i] {
				valid = false
			}
			classes[text] = compatible[i]
			if compatible[i] && !slices.Contains(values, text) {
				values = append(values, text)
			}
		}
		if !valid {
			continue
		}
		var parts []string
		for _, value := range values {
			parts = append(parts, "matrix."+key+" == "+value)
		}
		condition := strings.Join(parts, " || ")
		if len(condition) <= 2048 {
			return condition, nil
		}
	}
	return "", fmt.Errorf("runtime partition needs a scalar matrix selector; preserve the matrix and provide explicit initialization")
}
