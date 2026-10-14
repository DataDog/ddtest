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
func CIMatrixRows(root string) ([]string, error) {
	workflows, err := readWorkflows(root)
	if err != nil {
		return nil, err
	}
	rows := []string{}
	for _, workflow := range workflows {
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]
			matrix, err := runtimeMatrix(job.Strategy.Matrix)
			if err != nil {
				return nil, err
			}
			for _, row := range matrix {
				active, err := runtimeCondition(job.If, row)
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
