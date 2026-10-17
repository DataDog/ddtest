// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
)

// The first local check is the baseline, including any pre-existing user edits.
// Keep it inside the report rather than adding another customer-repo artifact.
func checkCIPreservation(root string, result *validationResult) error {
	if result.Framework != "mocha" && result.Framework != "cucumber" && result.Framework != "webdriverio" {
		return nil
	}
	previous, err := readPreviousValidation(root)
	if err != nil {
		return err
	}
	current, err := onboard.CIMatrixRows(root, result.Framework)
	if err != nil {
		result.CIPreservation = &verdict{Status: "inconclusive", Reason: reportText(err.Error())}
	} else {
		result.CIBaseline = current
		result.CIPreservation = &verdict{Status: "passed", Reason: "CI matrix matches the first local check. This is a snapshot, not proof of subsequent edits or CI execution."}
	}
	if previous != nil && previous.Framework == result.Framework && previous.CIBaseline != nil {
		result.CIBaseline = previous.CIBaseline
		if err == nil {
			var missing []string
			for _, row := range result.CIBaseline {
				if !preservedMatrixRow(row, current) {
					missing = append(missing, row)
				}
			}
			if len(missing) > 0 {
				result.CIPreservation = &verdict{Status: "failed", Reason: "CI matrix entries were removed after the first local check. Restore the original matrix; unsupported tracer/runtime combinations must remain explicit limitations.", DifferenceCount: len(missing), Differences: missing[:min(len(missing), maxReportDifferences)]}
			}
		}
	}
	return nil
}

// Added metadata does not remove coverage. Every original attribute must still
// exist with its original value in the same workflow/job; changed values fail.
func preservedMatrixRow(original string, current []string) bool {
	prefix, raw, ok := strings.Cut(original, " / {")
	if !ok {
		return false
	}
	var before map[string]any
	if json.Unmarshal([]byte("{"+raw), &before) != nil {
		return false
	}
	for _, candidate := range current {
		job, data, ok := strings.Cut(candidate, " / {")
		if !ok || job != prefix {
			continue
		}
		var after map[string]any
		if json.Unmarshal([]byte("{"+data), &after) != nil {
			continue
		}
		match := true
		for key, value := range before {
			actual, exists := after[key]
			match = match && exists && reflect.DeepEqual(actual, value)
		}
		if match {
			return true
		}
	}
	return false
}
