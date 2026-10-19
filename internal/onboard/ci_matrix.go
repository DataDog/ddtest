// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// CIMatrixRows records active job/OS/runtime combinations independently of step
// numbering and instrumentation edits. Unknown conditions remain unvalidated.
func CIMatrixRows(root string, frameworks ...string) ([]string, error) {
	workflows, err := readWorkflows(root)
	if err != nil {
		return nil, err
	}
	rows := []string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			if len(frameworks) > 0 && frameworks[0] == "pytest" && !pythonJobCandidate(root, workflow, job) {
				continue
			}
			if len(frameworks) > 0 && frameworks[0] == "webdriverio" && !webdriverIOJobCandidate(root, workflow, job) {
				continue
			}
			if len(frameworks) > 0 && frameworks[0] == "cucumber" {
				candidate := false
				for _, step := range job.Steps {
					r := resolveTestStep(root, workflow, job, step, "javascript", "cucumber")
					candidate = candidate || !r.Review && (r.Matched || r.Reason != "")
				}
				if !candidate {
					continue
				}
			}
			matrix, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				return nil, err
			}
			for _, row := range matrix {
				active, err := possibleJobCondition(job.If, row)
				if err != nil {
					return nil, err
				}
				if !active {
					continue
				}
				data, err := json.Marshal(row)
				if err != nil {
					return nil, err
				}
				rows = append(rows, workflow.Path+" / "+name+" / "+string(data))
				if len(rows) > 4096 {
					return nil, fmt.Errorf("CI matrix inventory exceeds 4096 rows")
				}
			}
		}
	}
	slices.Sort(rows)
	return rows, nil
}
