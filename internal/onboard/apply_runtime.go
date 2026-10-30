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
	rows, _, err := jobMatrix(job)
	if err != nil {
		return "", err
	}
	var compatible []bool
	unsupported := true
	for _, row := range rows {
		node, err := rowSetupNode(root, workflow, job, row)
		if err != nil {
			return "", err
		}
		unsupported = unsupported && UnsupportedNode(node)
		status, reason := StatusCompatible, ""
		if requirement != "" {
			status, reason = CompareNodeRequirement(node, requirement)
		}
		if esm && status == StatusCompatible {
			// setup-node's major/minor selectors choose a release, not x.0.0.
			esmStatus, _ := CompareNodeRequirement(node, ">=18.19.0 <19 || >=20.6.0")
			if esmStatus != StatusCompatible && (strings.Count(strings.TrimPrefix(node, "v"), ".") < 2 || strings.ContainsAny(node, "x*")) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				platform := nodeDistributionPlatform(job.RunsOn, "", row)
				if platform == "" && (job.Image != "" || job.Stated["js"] != "") {
					platform = "linux-x64" // Container images run Linux; a stated host version resolves like an image tag.
				}
				resolved, err := setupNodeResolver(&http.Client{Timeout: 10 * time.Second})(ctx, node, platform)
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
		return "", &NodeRuntimeGap{Requirement: requirement, ESM: esm, Unsupported: unsupported}
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

// rowSetupNode returns the Node version the job's active setup steps select on
// row, or "" when no step selects one statically.
func rowSetupNode(root string, workflow ciWorkflow, job runtimeJob, row map[string]any) (string, error) {
	return rowSetupNodeBefore(root, workflow, job, row, len(job.Steps))
}

// rowSetupNodeBefore is the Node a row's steps select before step limit: the
// runtime active when that step runs, since a later setup-node can switch it.
func rowSetupNodeBefore(root string, workflow ciWorkflow, job runtimeJob, row map[string]any, limit int) (string, error) {
	node, _ := jobBaseNode(job, row)
	for _, step := range job.Steps[:min(limit, len(job.Steps))] {
		if version, selected := step.Runtime["js"]; selected {
			node = imageNode("node:" + version)
			continue
		}
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
			if node, err = setupNodeVersion(root, step, row); err != nil {
				return "", err
			}
			continue
		}
		resolved, err := interpolateMatrix(step.Run, row)
		if err != nil {
			return "", err
		}
		if value := nvmRuntime(resolved); value != "" {
			node = value
		}
	}
	return node, nil
}

// SupportedTracerNode is the Node range of the oldest dd-trace major ddtest
// validates (dd-trace 5 requires Node >=18). A CI row below it runs on no
// supported release, so ddtest neither instruments nor tries it, without
// asking: there is no tracer to choose.
const SupportedTracerNode = ">=18"

// UnsupportedNode reports whether a statically known CI Node version is below
// every supported dd-trace release. Unknown versions are never unsupported.
func UnsupportedNode(node string) bool {
	status, _ := CompareNodeRequirement(node, SupportedTracerNode)
	return node != "" && status == StatusIncompatible
}

// UnsupportedNodeReason explains a CI entry ddtest skips on its own.
func UnsupportedNodeReason(node string) string {
	return "Node.js " + node + " is not supported by any dd-trace release; this CI entry keeps running uninstrumented and was not tried."
}

// NotTriedNote names the CI job ddtest skips because no dd-trace release
// supports its Node versions.
func NotTriedNote(location string, nodes []string) string {
	return "Not tried: " + location + " on Node " + strings.Join(nodes, ", ") + " (below Node 18, the oldest dd-trace major ddtest supports; ddtest skips them without instrumenting, no approval needed)"
}
