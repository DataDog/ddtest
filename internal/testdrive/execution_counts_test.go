// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestAggregateCountsDistinguishRepeatedExecutionsWithoutDroppingContexts(t *testing.T) {
	root := t.TempDir()
	var models []reportModel
	var runs []runSummary
	for configuration := range 3 {
		configurationRoot := filepath.Join(root, fmt.Sprintf("workspace-%d", configuration))
		run := validationRun{Role: roleReporting, Name: "reporting-only", Instrumented: true, root: configurationRoot, Command: fmt.Sprintf("jest --configuration=%d", configuration)}
		for index := range 32 {
			name := fmt.Sprintf("native test %d", index)
			run.Tests = append(run.Tests, jestTest{File: filepath.Join(configurationRoot, "test.js"), Name: name, Status: "passed"})
			run.Facts.Tests = append(run.Facts.Tests, intake.Test{SourceFile: "test.js", Suite: "suite", Name: name, Status: "pass"})
		}
		run.Facts.TestEventCount = len(run.Tests)
		model := buildReport(configurationRoot, run.Facts, false, reportRuntime{Framework: "Jest", Command: run.Command})
		model.ExecutionCounts = nativeExecutionCounts(run)
		model.Configuration = run.Command
		models = append(models, model)
		runs = append(runs, run.summary())
	}
	require.NoError(t, writeConfigurationHTML(&bytes.Buffer{}, root, models))
	page, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(page), "32 tests · 96 results across test configurations")
	require.NotContains(t, string(page), `<dl class="facts">`)
	require.Equal(t, 96, strings.Count(string(page), `id="test-detail-`))
	for configuration := range 3 {
		require.Contains(t, string(page), fmt.Sprintf("jest --configuration=%d", configuration))
	}
	// Baselines and synthetic feature probes must not inflate the original-suite
	// reporting counts. They still remain individual evidence in runs.
	runs = append(runs, runSummary{Role: roleBaseline, Name: "baseline", NativeCounts: models[0].ExecutionCounts}, runSummary{Role: roleProbeControl, Name: "reporting-only", Instrumented: true, ProbeMode: "pass", NativeCounts: models[0].ExecutionCounts})
	_ = finishValidation(&bytes.Buffer{}, root, validationResult{Framework: "jest", Runs: runs})
	result := readValidationReport(t, root)
	require.NotNil(t, result.ReportingCounts)
	require.Equal(t, 96, result.ReportingCounts.TestExecutions)
	require.Equal(t, 32, *result.ReportingCounts.SourceTestIdentities)
	require.Len(t, result.Runs, 5)
	encoded, err := json.Marshal(result.ReportingCounts)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "native test")
	require.NotContains(t, string(encoded), "test.js")
	require.Less(t, len(encoded), 600, "JSON stores aggregate counts, never per-test identities")
	// The coordinator reloads compact JSON to append workflow checks. Native
	// identities are intentionally absent, so finalizing again must keep counts.
	_ = finishValidation(&bytes.Buffer{}, root, result)
	reloaded := readValidationReport(t, root)
	require.Equal(t, result.ReportingCounts, reloaded.ReportingCounts)
}

func TestSingleConfigurationReportDoesNotRepeatTheTestCount(t *testing.T) {
	for _, tc := range []struct {
		name       string
		tests      []jestTest
		want       string
		identifies bool
	}{
		{name: "four tests", tests: []jestTest{{File: "test.js", Name: "one"}, {File: "test.js", Name: "two"}, {File: "test.js", Name: "three"}, {File: "test.js", Name: "four"}}, want: "4 tests", identifies: true},
		{name: "one test", tests: []jestTest{{File: "test.js", Name: "one"}}, want: "1 test", identifies: true},
		{name: "empty suite", tests: []jestTest{}, want: "0 tests", identifies: true},
		{name: "missing file", tests: []jestTest{{Name: "one"}}, want: "1 test result"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			counts := nativeExecutionCounts(validationRun{Tests: tc.tests})
			require.NoError(t, writeConfigurationHTML(&bytes.Buffer{}, root, []reportModel{{ExecutionCounts: counts}}))
			page, err := os.ReadFile(htmlReportPath(root))
			require.NoError(t, err)
			report := string(page)
			require.Contains(t, report, `<p class="run-totals">`+tc.want+` · 0 suites</p>`)
			require.NotContains(t, report, "reported test executions")
			require.NotContains(t, report, "source-test identities")
			require.NotContains(t, report, "<dt>Tests")
			require.NotContains(t, report, "<dt>Test results")
			if tc.identifies {
				require.NotContains(t, report, `<dl class="facts">`)
			} else {
				require.Contains(t, report, "<dt>Unique tests")
				require.Contains(t, report, "<dd>Unavailable</dd>")
				require.Contains(t, report, "Not enough information to count unique tests.")
			}
		})
	}
}

func TestAmbiguousNativeIdentityDoesNotInventUniqueCount(t *testing.T) {
	for _, tests := range [][]jestTest{
		{{File: "test.js", Name: "same"}, {File: "test.js", Name: "same"}},
		{{Name: "missing file"}},
		{{File: "../outside.js", Name: "outside repository"}},
	} {
		counts := nativeExecutionCounts(validationRun{Tests: tests})
		require.Nil(t, counts.SourceTestIdentities)
		require.NotEmpty(t, counts.Reason)
		require.Equal(t, len(tests), counts.TestExecutions)
		combined := combineExecutionCounts([]*executionCounts{nativeExecutionCounts(validationRun{Tests: []jestTest{{File: "test.js", Name: "works"}}}), counts})
		require.Nil(t, combined.SourceTestIdentities)
		require.NotEmpty(t, combined.Reason)
		require.Equal(t, len(tests)+1, combined.TestExecutions)
	}
	require.Nil(t, nativeExecutionCounts(validationRun{}), "telemetry-only reports do not prove native identities")
}
