package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepeatedComparisonRetainsRecurringDifferencesBesideVariableTests(t *testing.T) {
	base := validationRun{root: "/repo", Tests: []jestTest{{File: "/repo/test.ts", Name: "memory", Status: "passed"}, {File: "/repo/test.ts", Name: "timing", Status: "passed"}}}
	first := validationRun{ExitCode: 1, Tests: []jestTest{{File: "/repo/test.ts", Name: "memory", Status: "failed", Failure: "expected rounds > 1, received 0"}, {File: "/repo/test.ts", Name: "timing", Status: "failed"}}}
	second := validationRun{ExitCode: 1, Tests: []jestTest{first.Tests[0], base.Tests[1]}}
	got := compareRepeatedJavaScript(base, first, base, second)
	require.Equal(t, "suspected regression", got.Status)
	require.Equal(t, "recurring_difference", got.Code)
	require.Contains(t, got.Differences[0], "Recurring difference in both pairs: test.ts › memory")
	require.Contains(t, got.Differences[0], "received 0")
	require.Contains(t, got.Differences[1], "Varies between repeats: test.ts › timing")
	root := t.TempDir()
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Compatibility: got}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, got.Differences, report.Compatibility.Differences)
	require.Contains(t, strings.Join(report.Compatibility.Differences, "\n"), "test.ts › memory")
}

func TestRepeatedComparisonPreservesProcessAndMissingResultFailures(t *testing.T) {
	base := withoutTelemetry(testRun("passed", 0))
	first := testRun("failed", 1)
	require.Equal(t, "suspected regression", compareRepeatedJavaScript(base, first, base, first).Status)
	missing := validationRun{ExitCode: 1, ResultError: "missing native results"}
	got := compareRepeatedJavaScript(base, first, base, missing)
	require.Equal(t, "inconclusive", got.Status)
	require.NotContains(t, got.Reason, "test identities")
	repeat := first
	repeat.Tests = nil
	got = compareRepeatedJavaScript(base, first, base, repeat)
	require.Contains(t, strings.Join(got.Differences, "\n"), "not reported")
	pass := testRun("passed", 0)
	crash := testRun("passed", 1)
	got = compareRepeatedJavaScript(base, crash, base, pass)
	require.Equal(t, "inconclusive", got.Status)
	require.Contains(t, strings.Join(got.Differences, "\n"), "exit=1")
}

func TestRepeatedComparisonCountsDuplicateNamesAndBoundsReport(t *testing.T) {
	base := validationRun{Tests: []jestTest{{File: "test.ts", Name: "same", Status: "passed"}, {File: "test.ts", Name: "same", Status: "passed"}}}
	first := validationRun{Tests: []jestTest{{File: "test.ts", Name: "same", Status: "failed", Failure: strings.Repeat("x", 5000)}}}
	got := compareRepeatedJavaScript(base, first, base, base)
	require.Contains(t, got.Differences[0], "passed x2")
	root := t.TempDir()
	require.Error(t, finishValidation(&bytes.Buffer{}, root, validationResult{Compatibility: got}))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.Less(t, len([]rune(report.Compatibility.Differences[0])), 1100)
}

func TestRepeatedComparisonWithOnlyVariableDifferencesStaysInconclusive(t *testing.T) {
	baseline := withoutTelemetry(testRun("passed", 0))
	different := testRun("failed", 1)
	got := compareRepeatedJavaScript(baseline, different, baseline, testRun("passed", 0))
	require.Equal(t, "inconclusive", got.Status)
	require.Equal(t, "varying_outcomes", got.Code)
}

func TestRepeatedComparisonKeepsRecurringProcessFailuresBesideFlakyTests(t *testing.T) {
	flaky := func(status string, exit int, suite ...string) validationRun {
		return validationRun{root: "/repo", ExitCode: exit, SuiteFailures: suite, Tests: []jestTest{{File: "/repo/a.test.js", Name: "stable", Status: "passed"}, {File: "/repo/b.test.js", Name: "timing", Status: status}}}
	}
	// The unrelated test changes identically on both sides between repeats,
	// while only the instrumented runs exit non-zero, or fail in teardown.
	for name, runs := range map[string][4]validationRun{
		"exit code":     {flaky("passed", 0), flaky("passed", 1), flaky("failed", 0), flaky("failed", 1)},
		"suite failure": {flaky("passed", 0), flaky("passed", 0, "teardown leaked a handle"), flaky("failed", 0), flaky("failed", 0, "teardown leaked a handle")},
	} {
		t.Run(name, func(t *testing.T) {
			got := compareRepeatedJavaScript(runs[0], runs[1], runs[2], runs[3])
			require.Equal(t, "suspected regression", got.Status)
			require.Equal(t, "recurring_difference", got.Code)
			differences := strings.Join(got.Differences, "\n")
			require.Contains(t, differences, "Recurring process difference in both pairs")
			require.Contains(t, differences, "Varies between repeats: b.test.js › timing")
			require.True(t, strings.HasPrefix(got.Reason, "The same differences recur in both pairs"), got.Reason)
			require.NotContains(t, got.Reason, "prevents attributing")
		})
	}
	got := compareRepeatedJavaScript(flaky("passed", 0), flaky("passed", 1), flaky("passed", 0), flaky("passed", 0))
	require.Equal(t, "inconclusive", got.Status, "a process difference that does not recur stays inconclusive")
	require.True(t, strings.HasPrefix(got.Reason, "Outcomes changed between repeated runs"), got.Reason)
}
