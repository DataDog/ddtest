// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed scripts/vitest_identities.mjs
var vitestIdentityReporter string

func readVitestResults(path string, run *validationRun) {
	start := len(run.Tests)
	readRunnerResults(path, "Vitest", run)
	if run.ResultError != "" {
		return
	}
	data, err := os.ReadFile(path + ".identities.json")
	var identities []struct{ File, ReportedName, Name, TelemetryName, Status string }
	if err == nil {
		err = json.Unmarshal(data, &identities)
	}
	tests := run.Tests[start:]
	if err != nil || len(identities) != len(tests) {
		run.ResultError = "Vitest collected identities are missing or do not match the JSON results"
		return
	}
	// Both collectors traverse each native file's task tree in order. Check every
	// file/name/status before using its original identity; counts alone cannot
	// establish correspondence when redaction collapses different test names.
	byFile := make(map[string][]int)
	for i, identity := range identities {
		file := filepath.Clean(identity.File)
		byFile[file] = append(byFile[file], i)
	}
	for i := range tests {
		test := &tests[i]
		indices := byFile[test.File]
		if len(indices) == 0 {
			run.ResultError = "Vitest collected identities omit a reported file"
			return
		}
		identity := identities[indices[0]]
		byFile[test.File] = indices[1:]
		if identity.ReportedName != test.Name || identity.Status != test.Status {
			run.ResultError = fmt.Sprintf("Vitest collected identity does not match the JSON result for %s", test.File)
			return
		}
		test.Name = identity.Name
		name := identity.TelemetryName
		test.telemetryName = &name
	}
}
