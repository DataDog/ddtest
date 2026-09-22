// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"github.com/tinylib/msgp/msgp"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestAnalyzeTestsFindsFailuresRetriesAndSlowTests(t *testing.T) {
	tests := []testReference{
		{name: "fast", suite: "one.test.js", status: "pass", duration: 10 * time.Millisecond},
		{name: "flaky", suite: "one.test.js", status: "fail", duration: 20 * time.Millisecond},
		{name: "flaky", suite: "one.test.js", status: "pass", duration: 30 * time.Millisecond, isRetry: true},
		{name: "slow", suite: "two.test.js", status: "pass", duration: 400 * time.Millisecond},
		{name: "broken", suite: "two.test.js", status: "fail", duration: 15 * time.Millisecond},
	}

	all, failed, flaky, slow, median := analyzeTests(tests)
	require.Len(t, all, 4)
	require.Equal(t, []string{"fast", "flaky", "broken", "slow"}, []string{all[0].Name, all[1].Name, all[2].Name, all[3].Name})
	require.Equal(t, []TestFinding{{
		Name: "broken", Suite: "two.test.js", Status: "fail", Duration: 15 * time.Millisecond,
		Attempts: []TestAttempt{{Status: "fail", Duration: 15 * time.Millisecond}},
	}}, failed)
	require.Equal(t, []TestFinding{{
		Name: "flaky", Suite: "one.test.js", Status: "pass", Duration: 20 * time.Millisecond,
		Attempts: []TestAttempt{
			{Status: "fail", Duration: 20 * time.Millisecond},
			{Status: "pass", Duration: 30 * time.Millisecond, Retry: true},
		},
	}}, flaky)
	require.Equal(t, []TestFinding{{
		Name: "slow", Suite: "two.test.js", Status: "pass", Duration: 400 * time.Millisecond,
		Attempts: []TestAttempt{{Status: "pass", Duration: 400 * time.Millisecond}},
	}}, slow)
	require.Equal(t, 17500*time.Microsecond, median)
}

func TestAnalyzeTestsUsesFinalStatusAndMarksMixedOutcomesFlaky(t *testing.T) {
	tests := []testReference{
		{name: "flaky", suite: "one.test.js", status: "pass", finalStatus: "pass", duration: 10 * time.Millisecond},
		{name: "flaky", suite: "one.test.js", status: "fail", duration: 12 * time.Millisecond, isRetry: true},
	}

	all, failed, flaky, _, _ := analyzeTests(tests)
	require.Empty(t, failed)
	require.Len(t, flaky, 1)
	require.Equal(t, "pass", flaky[0].Status)
	require.Equal(t, 10*time.Millisecond, flaky[0].Duration)
	require.Equal(t, "pass", all[0].Status)
}

func TestAddCoverageToTestsUsesActiveCoverageLevel(t *testing.T) {
	tests := []testReference{
		{sessionID: 1, suiteID: 10, spanID: 100, name: "one", suite: "one.test.js"},
		{sessionID: 1, suiteID: 10, spanID: 101, name: "two", suite: "one.test.js"},
	}
	coverages := []coverageReference{
		{testReference: testReference{spanID: 100}, files: []string{"specific.js"}},
		{testReference: testReference{sessionID: 1, suiteID: 10}, files: []string{"shared.js"}},
	}

	t.Run("test", func(t *testing.T) {
		findings, _, _, _, _ := analyzeTests(tests)
		addCoverageToTests(findings, tests, coverages, "test")
		require.Equal(t, "test", findings[0].CoverageLevel)
		require.Equal(t, []string{"specific.js"}, findings[0].CoveredFiles)
		require.Empty(t, findings[1].CoverageLevel)
	})

	t.Run("suite", func(t *testing.T) {
		findings, _, _, _, _ := analyzeTests(tests)
		addCoverageToTests(findings, tests, coverages, "suite")
		for _, finding := range findings {
			require.Equal(t, "suite", finding.CoverageLevel)
			require.Equal(t, []string{"shared.js"}, finding.CoveredFiles)
		}
	})
}

func TestAnalyzeCoverageUsesActiveCoverageLevel(t *testing.T) {
	tests := []testReference{
		{sessionID: 1, suiteID: 10, spanID: 100, name: "narrow", suite: "one.test.js"},
		{sessionID: 1, suiteID: 11, spanID: 110, name: "also narrow", suite: "one.test.js"},
		{sessionID: 1, suiteID: 20, spanID: 200, name: "broad", suite: "two.test.js"},
		{sessionID: 1, suiteID: 30, spanID: 300, name: "suite test", suite: "three.test.js"},
	}
	coverages := []coverageReference{
		{testReference: testReference{spanID: 100}, fileCount: 1},
		{testReference: testReference{spanID: 110}, fileCount: 2},
		{testReference: testReference{spanID: 200}, fileCount: 12},
		{testReference: testReference{spanID: 999}, fileCount: 100},
		{testReference: testReference{sessionID: 1, suiteID: 10}, fileCount: 1},
		{testReference: testReference{sessionID: 1, suiteID: 20}, fileCount: 2},
		{testReference: testReference{sessionID: 1, suiteID: 30}, fileCount: 14},
		{fileCount: 100},
	}

	testFindings, testMedian := analyzeCoverage(tests, coverages, "test")
	require.Equal(t, []CoverageFinding{
		{Name: "two.test.js › broad", Level: "test", FileCount: 12},
	}, testFindings)
	require.Equal(t, 2, testMedian)

	suiteFindings, suiteMedian := analyzeCoverage(tests, coverages, "suite")
	require.Equal(t, []CoverageFinding{
		{Name: "three.test.js", Level: "suite", FileCount: 14},
	}, suiteFindings)
	require.Equal(t, 2, suiteMedian)
}

func TestUniqueCoveredTestCountDoesNotCountRetriesTwice(t *testing.T) {
	tests := []testReference{
		{sessionID: 1, suiteID: 10, spanID: 100, name: "flaky", suite: "one.test.js"},
		{sessionID: 1, suiteID: 10, spanID: 101, name: "flaky", suite: "one.test.js", isRetry: true},
		{sessionID: 1, suiteID: 10, spanID: 102, name: "stable", suite: "one.test.js"},
	}
	coverages := []coverageReference{
		{testReference: testReference{spanID: 100}},
		{testReference: testReference{spanID: 101}},
		{testReference: testReference{spanID: 102}},
	}

	require.Equal(t, 2, uniqueCoveredTestCount(tests, coverages))
}

func TestAnalyzeCoverageUsesLargestDuplicateAndReportsSingleMedian(t *testing.T) {
	tests := []testReference{{spanID: 100, name: "one", suite: "one.test.js"}}
	coverages := []coverageReference{
		{testReference: testReference{spanID: 100}, fileCount: 2},
		{testReference: testReference{spanID: 100}, fileCount: 7},
	}

	broad, median := analyzeCoverage(tests, coverages, "test")
	require.Empty(t, broad)
	require.Equal(t, 7, median)
}

func TestAnalyzeTestsHandlesMissingNamesAndStableOrdering(t *testing.T) {
	tests := []testReference{
		{suiteID: 2, spanID: 20, status: "fail", duration: time.Second},
		{suiteID: 1, spanID: 10, status: "fail", duration: time.Second},
		{name: "named", status: "pass", duration: time.Millisecond},
	}

	all, failed, _, _, _ := analyzeTests(tests)
	require.Equal(t, []string{"named", "test 10", "test 20"}, []string{all[0].Name, all[1].Name, all[2].Name})
	require.Equal(t, []string{"test 10", "test 20"}, []string{failed[0].Name, failed[1].Name})
	require.Equal(t, "named", all[0].label())
}

func TestMedianHelpers(t *testing.T) {
	require.Equal(t, 0, medianInts(nil))
	require.Equal(t, 4, medianInts([]int{4}))
	require.Equal(t, 4, medianInts([]int{2, 6}))
	require.Equal(t, 6, medianInts([]int{2, 6, 9}))
	require.Equal(t, 0, medianCoveredFiles(nil))
	require.Equal(t, 5, medianCoveredFiles(map[string]CoverageFinding{
		"one": {FileCount: 3},
		"two": {FileCount: 7},
	}))
}

func TestCoverageLevelAndAppendUnique(t *testing.T) {
	require.Empty(t, coverageLevel(nil))
	require.Equal(t, "suite", coverageLevel([]coverageReference{{testReference: testReference{suiteID: 1}}}))
	require.Equal(t, "test", coverageLevel([]coverageReference{
		{testReference: testReference{suiteID: 1}},
		{testReference: testReference{spanID: 2}},
	}))
	require.Equal(t, []string{"a.js", "b.js"}, appendUnique([]string{"b.js"}, "a.js", "b.js"))
}

func TestFindingsIncludeConfigurationErrorsAcrossEventLevels(t *testing.T) {
	payload, err := msgp.AppendIntf(nil, map[string]any{
		"metadata": map[string]any{"*": map[string]any{"_dd.ci.library_configuration_error.settings": "true"}},
		"events": []any{
			map[string]any{"type": "test", "content": map[string]any{"meta": map[string]any{"test.name": "passes", "test.status": "pass"}}},
			map[string]any{"type": "test_session_end", "content": map[string]any{"meta": map[string]any{"_dd.ci.library_configuration_error.skippable_tests": "true"}}},
			map[string]any{"type": "test_suite_end", "content": map[string]any{"meta": map[string]any{"_dd.ci.library_configuration_error.skippable_tests": true, "_dd.ci.library_configuration_error.known_tests": "false"}}},
		},
	})
	require.NoError(t, err)
	server := &Server{requests: []RawRequest{{Method: http.MethodPost, Path: constants.TestCycleURLPath, Body: payload}}}
	findings, err := server.Findings()
	require.NoError(t, err)
	require.Equal(t, 1, findings.TestCount)
	require.Empty(t, findings.FailedTests)
	require.Equal(t, []string{"settings", "skippable_tests"}, findings.ConfigurationErrors)
}
