package onboard

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Partition only fully resolved static matrices. Unsupported rows keep their
// original test command and receive neither installation nor preload.
func setupRuntimeCondition(root string, workflow ciWorkflow, job runtimeJob, requirement string, esm bool) (string, error) {
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
			if !strings.HasPrefix(strings.ToLower(step.Uses), "actions/setup-node@") && !strings.Contains(step.Run, "nvm") {
				continue
			}
			active, err := stepCondition(step, row, conditionEnvironment(workflow, job, step, row))
			if err != nil {
				return "", fmt.Errorf("cannot resolve runtime setup condition: %w", err)
			}
			if !active {
				continue
			}
			if strings.HasPrefix(strings.ToLower(step.Uses), "actions/setup-node@") {
				node, err = setupNodeVersion(root, step, row)
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
		status, reason := StatusCompatible, ""
		if requirement != "" {
			status, reason = CompareNodeRequirement(node, requirement)
		}
		if esm && status == StatusCompatible {
			// setup-node's major/minor selectors choose a release, not x.0.0.
			esmStatus, _ := CompareNodeRequirement(node, ">=18.19.0 <19 || >=20.6.0")
			if esmStatus != StatusCompatible && (strings.Count(strings.TrimPrefix(node, "v"), ".") < 2 || strings.ContainsAny(node, "x*")) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				resolved, err := setupNodeResolver(&http.Client{Timeout: 10 * time.Second})(ctx, node, nodeDistributionPlatform(job.RunsOn, "", row))
				cancel()
				if err != nil {
					return "", fmt.Errorf("resolve ESM CI runtime: %w", err)
				}
				node = resolved.Version
			}
			status, reason = CompareNodeRequirement(node, ">=18.19.0 <19 || >=20.6.0")
		}
		if status != "compatible" && status != "incompatible" {
			return "", fmt.Errorf("cannot partition unresolved CI runtime: %s", reason)
		}
		compatible = append(compatible, status == "compatible")
	}
	if !slices.Contains(compatible, false) {
		return "", nil
	}
	if !slices.Contains(compatible, true) {
		return "", &NodeRuntimeGap{Requirement: requirement, ESM: esm}
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
