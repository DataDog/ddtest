// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"path/filepath"
)

const sourceIdentityScope = "Native file and full test name, including reported project/browser/repeat identity. Counts cover reporting-only results, including skipped results, once per configuration; retries remain attempts. Matching source identities across commands do not establish equivalent configurations or environments."

// Keep identity keys only while combining results. The compact report retains
// counts, not a second copy of every native test or telemetry event.
type executionCounts struct {
	TestExecutions       int    `json:"test_executions"`
	SourceTestIdentities *int   `json:"source_test_identities"`
	IdentityScope        string `json:"identity_scope"`
	Reason               string `json:"reason,omitempty"`
	identities           map[sourceTestIdentity]struct{}
}

type sourceTestIdentity struct {
	Framework, File, Name, Browser, Project string
	Repeat                                  int
}

func nativeExecutionCounts(run validationRun) *executionCounts {
	if run.Tests == nil {
		return nil
	}
	counts := &executionCounts{TestExecutions: len(run.Tests), IdentityScope: sourceIdentityScope, identities: make(map[sourceTestIdentity]struct{})}
	for _, test := range run.Tests {
		file := filepath.Clean(test.File)
		if filepath.IsAbs(file) && run.root != "" {
			var err error
			file, err = filepath.Rel(run.root, file)
			if err != nil {
				file = ""
			}
		}
		if test.File == "" || !filepath.IsLocal(file) {
			counts.Reason = "Source-test identities are unavailable because a native test file is missing or outside the repository."
			break
		}
		identity := sourceTestIdentity{Framework: run.framework, File: filepath.ToSlash(file), Name: normalizeOwnedTemporaryPath(run, test.Name), Project: run.Project}
		if _, duplicate := counts.identities[identity]; duplicate {
			counts.Reason = "Source-test identities are unavailable because native file/name identities repeat within a configuration."
			break
		}
		counts.identities[identity] = struct{}{}
	}
	if run.ResultError != "" {
		counts.Reason = "Source-test identities are unavailable because native results are incomplete."
	}
	if counts.Reason == "" {
		unique := len(counts.identities)
		counts.SourceTestIdentities = &unique
	} else {
		counts.identities = nil
	}
	return counts
}

func combineExecutionCounts(parts []*executionCounts) *executionCounts {
	if len(parts) == 0 {
		return nil
	}
	combined := &executionCounts{IdentityScope: sourceIdentityScope, identities: make(map[sourceTestIdentity]struct{})}
	for _, part := range parts {
		if part == nil {
			return nil
		}
		combined.TestExecutions += part.TestExecutions
		if part.SourceTestIdentities == nil || part.identities == nil {
			combined.Reason = "Source-test identities are unavailable because at least one configuration has missing or ambiguous native identities."
		}
		for key := range part.identities {
			combined.identities[key] = struct{}{}
		}
	}
	if combined.Reason == "" {
		unique := len(combined.identities)
		combined.SourceTestIdentities = &unique
	} else {
		combined.identities = nil
	}
	return combined
}

func reportingExecutionCounts(runs []runSummary) *executionCounts {
	var parts []*executionCounts
	for _, run := range runs {
		if run.Role == roleReporting {
			parts = append(parts, run.NativeCounts)
		}
	}
	return combineExecutionCounts(parts)
}
