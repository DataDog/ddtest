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
	require.Equal(t, "inconclusive", got.Status)
	require.Contains(t, got.Reason, "1 test identities have recurring differences")
	require.Contains(t, got.Reason, "1 vary between repeats")
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
	require.Contains(t, strings.Join(report.Summary.Facts, "\n"), "test.ts › memory")
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
