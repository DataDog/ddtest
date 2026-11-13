// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestVitestProbeChecksOnlyExcludeKnownSkippedNeighbours(t *testing.T) {
	for _, problem := range []string{"", "legacy probe", "todo", "native execution", "telemetry execution", "unknown name", "unknown file", "extra attempt"} {
		t.Run(problem, func(t *testing.T) {
			run := validationRun{framework: "vitest", root: "/repo", Tests: []jestTest{
				{File: "/repo/a.test.js", Name: probeName, Status: "passed"},
				{File: "/repo/a.test.js", Name: "filtered neighbour", Status: "skipped"},
			}, Facts: intake.Facts{TestEventCount: 2, Tests: []intake.Test{
				{SourceFile: "a.test.js", Name: probeName, Attempts: []intake.TestRun{{Status: "pass"}}},
				{SourceFile: "a.test.js", Name: "filtered neighbour", Attempts: []intake.TestRun{{Status: "skip"}}},
			}}}
			switch problem {
			case "legacy probe":
				name := probeName
				run.Tests[0].Name, run.Tests[0].telemetryName, run.Tests[0].probe = " "+name, &name, true
			case "todo":
				run.Tests[1].Status = "todo"
			case "native execution":
				run.Tests[1].Status = "passed"
			case "telemetry execution":
				run.Facts.Tests[1].Attempts[0].Status = "pass"
			case "unknown name":
				run.Facts.Tests[1].Name = "unexpected"
			case "unknown file":
				run.Facts.Tests[1].SourceFile = "other.test.js"
			case "extra attempt":
				run.Facts.Tests[1].Attempts = append(run.Facts.Tests[1].Attempts, intake.TestRun{Status: "skip"})
				run.Facts.TestEventCount++
			}
			scoped := probeCheckRun(run)
			if problem == "" || problem == "legacy probe" || problem == "todo" {
				require.True(t, validProbeControl(scoped, "passed"))
				require.Equal(t, 1, scoped.Facts.TestEventCount)
			} else {
				require.False(t, validProbeControl(scoped, "passed"))
			}
			require.Len(t, run.Tests, 2, "the report retains all native results")
			require.Len(t, run.Facts.Tests, 2, "the report retains all telemetry")
		})
	}
}

func TestVitestFeaturesRejectUnexpectedNeighbourTelemetry(t *testing.T) {
	for _, scenario := range []struct{ feature, status string }{
		{"early-flake-detection", "skipped"},
		{"early-flake-detection", "todo"},
		{"attempt-to-fix", "skipped"},
		{"attempt-to-fix", "todo"},
	} {
		feature := scenario.feature
		for _, tc := range []struct {
			name, file, test string
			attempts         []intake.TestRun
			want             string
		}{
			{"no neighbour telemetry", "a.test.js", "filtered neighbour", nil, "passed"},
			{"known skipped neighbour", "a.test.js", "filtered neighbour", []intake.TestRun{{Status: "skip"}}, "passed"},
			{"passing neighbour", "a.test.js", "filtered neighbour", []intake.TestRun{{Status: "pass"}}, "inconclusive"},
			{"failing neighbour", "a.test.js", "filtered neighbour", []intake.TestRun{{Status: "fail"}}, "inconclusive"},
			{"retrying neighbour", "a.test.js", "filtered neighbour", []intake.TestRun{{Status: "skip", Retry: true}}, "inconclusive"},
			{"extra neighbour attempt", "a.test.js", "filtered neighbour", []intake.TestRun{{Status: "skip"}, {Status: "skip"}}, "inconclusive"},
			{"unknown name", "a.test.js", "unexpected", []intake.TestRun{{Status: "skip"}}, "inconclusive"},
			{"probe name in another file", "other.test.js", probeName, []intake.TestRun{{Status: "skip"}}, "inconclusive"},
		} {
			t.Run(feature+"/"+scenario.status+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				identity := intake.Test{Module: "vitest", Suite: "a.test.js", Name: probeName, SourceFile: "a.test.js"}
				tags := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "pass", "test.test_management.is_attempt_to_fix": "true"}
				retry := maps.Clone(tags)
				retry["test.is_retry"] = "true"
				retry["test.retry_reason"] = map[string]string{"early-flake-detection": "early_flake_detection", "attempt-to-fix": "attempt_to_fix"}[feature]
				run := validationRun{framework: "vitest", root: root, Tests: []jestTest{
					{File: filepath.Join(root, "a.test.js"), Name: probeName, Status: "passed"},
					{File: filepath.Join(root, "a.test.js"), Name: "filtered neighbour", Status: scenario.status},
				}, Facts: intake.Facts{TestEventCount: 2 + len(tc.attempts), Events: []intake.Event{{Type: "test", Tags: tags}, {Type: "test", Tags: retry}}, Tests: []intake.Test{
					{SourceFile: "a.test.js", Name: probeName, Attempts: []intake.TestRun{{Status: "pass"}, {Status: "pass", Retry: true}}},
				}}}
				if len(tc.attempts) > 0 {
					run.Facts.Tests = append(run.Facts.Tests, intake.Test{SourceFile: tc.file, Name: tc.test, Attempts: tc.attempts})
				}
				for _, attempt := range tc.attempts {
					neighbour := maps.Clone(tags)
					neighbour["test.name"], neighbour["test.status"], neighbour["test.source.file"] = tc.test, attempt.Status, tc.file
					if attempt.Retry {
						neighbour["test.is_retry"] = "true"
					}
					run.Facts.Events = append(run.Facts.Events, intake.Event{Type: "test", Tags: neighbour})
				}
				originalTelemetryCount := len(run.Facts.Tests)
				require.Equal(t, tc.want, evaluateFeature(feature, probeCheckRun(run), identity).Status)
				require.Len(t, run.Tests, 2, "the report retains the original native results")
				require.Len(t, run.Facts.Tests, originalTelemetryCount, "the report retains all telemetry")
				require.Equal(t, 2+len(tc.attempts), run.Facts.TestEventCount, "the report retains all telemetry")
			})
		}
	}
}
