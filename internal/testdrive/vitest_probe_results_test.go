// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestVitestProbeChecksOnlyExcludeKnownSkippedNeighbours(t *testing.T) {
	for _, problem := range []string{"", "legacy probe", "native execution", "telemetry execution", "unknown name", "unknown file", "extra attempt"} {
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
			if problem == "" || problem == "legacy probe" {
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
