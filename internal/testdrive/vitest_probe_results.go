// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"path/filepath"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

// An appended probe shares a file with tests excluded by --testNamePattern.
// Keep those native results and events in the report, but evaluate the probe
// separately. Exclude only known, skipped or todo neighbours, never executed tests or
// unmatched telemetry. The paired full-selection checks are untouched.
func probeCheckRun(run validationRun) validationRun {
	if run.framework != "vitest" {
		return run
	}
	file := ""
	for _, test := range run.Tests {
		if test.Name == probeName || test.probe {
			file = test.File
			break
		}
	}
	if file == "" {
		return run
	}
	skipped := map[string]int{}
	tests := make([]jestTest, 0, len(run.Tests))
	for _, test := range run.Tests {
		if test.File == file && test.Name != probeName && !test.probe && (test.Status == "skipped" || test.Status == "todo") {
			name := test.Name
			if test.telemetryName != nil {
				name = *test.telemetryName
			}
			skipped[name]++
		} else {
			tests = append(tests, test)
		}
	}
	run.Tests = tests
	observed := make([]intake.Test, 0, len(run.Facts.Tests))
	for _, test := range run.Facts.Tests {
		path, base := test.SourceFile, run.checkoutRoot
		if path == "" {
			path, base = test.Suite, run.root
		}
		if base == "" {
			base = run.root
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		filtered := filepath.Clean(path) == file && len(test.Attempts) > 0 && len(test.Attempts) <= skipped[test.Name]
		for _, attempt := range test.Attempts {
			filtered = filtered && attempt.Status == "skip" && !attempt.Retry
		}
		if filtered {
			skipped[test.Name] -= len(test.Attempts)
			run.Facts.TestEventCount -= len(test.Attempts)
		} else {
			if run.ResultError == "" && (filepath.Clean(path) != file || test.Name != probeName) {
				run.ResultError = "vitest scenario reported telemetry beyond the controlled probe"
			}
			observed = append(observed, test)
		}
	}
	run.Facts.Tests = observed
	return run
}
