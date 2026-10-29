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

// brakeman's LoggerTests#test_logger_type fails the same way in every run;
// only the inspected object's address differs. Failure messages compare
// without object addresses, while a different assertion stays different.
func TestFailuresCompareWithoutObjectAddresses(t *testing.T) {
	failure := func(address, expected string) string {
		return failureSignature("Expected #<Brakeman::Logger::Plain:" + address + " @options={}> to be an instance of " + expected + ".")
	}
	require.Equal(t, failure("0x000000011f3a2b48", "Brakeman::Logger::Console"), failure("0x0000000120c4d9e0", "Brakeman::Logger::Console"))
	require.NotEqual(t, failure("0x000000011f3a2b48", "Brakeman::Logger::Console"), failure("0x000000011f3a2b48", "Brakeman::Logger::Quiet"))
	require.Equal(t, failureSignature("<object at 0x7f3a2b48c0d0> is not None"), failureSignature("<object at 0x7f3a2b48e110> is not None"))
	require.Equal(t, failureSignature("expected com.acme.Widget@1b6d3586 but was null"), failureSignature("expected com.acme.Widget@4554617c but was null"))
	require.NotEqual(t, failureSignature("expected 0x10 to equal 0x20"), failureSignature("expected 0x10 to equal 0x30"), "short hexadecimal values are data")

	run := func(address string) validationRun {
		return validationRun{ExitCode: 1, Tests: []jestTest{{File: "/repo/test/logger_test.rb", Name: "test_logger_type", Status: "failed", Failure: failure(address, "Brakeman::Logger::Console")}}}
	}
	got := compareRepeatedRuns(run("0x0000000100000001"), run("0x0000000100000002"), run("0x0000000100000003"), run("0x0000000100000004"), func(validationRun) verdict { return verdict{Status: "compatible"} })
	require.NotEqual(t, "varying_outcomes", got.Code)
	require.NotEqual(t, "suspected regression", got.Status)
}
