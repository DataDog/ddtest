// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeTestsFindsFailuresRetriesAndSlowTests(t *testing.T) {
	tests := []testReference{
		{name: "fast", suite: "one.test.js", status: "pass", duration: 10 * time.Millisecond},
		{name: "flaky", suite: "one.test.js", status: "fail", duration: 20 * time.Millisecond},
		{name: "flaky", suite: "one.test.js", status: "pass", duration: 30 * time.Millisecond, isRetry: true},
		{name: "slow", suite: "two.test.js", status: "pass", duration: 400 * time.Millisecond},
		{name: "broken", suite: "two.test.js", status: "fail", duration: 15 * time.Millisecond},
	}

	failed, passedOnRetry, slow, testCount := analyzeTests(tests)
	require.Equal(t, 4, testCount)
	require.Equal(t, []TestFinding{{Name: "broken", Suite: "two.test.js", Duration: 15 * time.Millisecond}}, failed)
	require.Equal(t, []TestFinding{{Name: "flaky", Suite: "one.test.js", Duration: 30 * time.Millisecond}}, passedOnRetry)
	require.Equal(t, []TestFinding{{Name: "slow", Suite: "two.test.js", Duration: 400 * time.Millisecond}}, slow)
}

func TestAnalyzeCoverageSupportsTestAndSuiteLevelOutliers(t *testing.T) {
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
		{testReference: testReference{sessionID: 1, suiteID: 30}, fileCount: 14},
	}

	require.Equal(t, []CoverageFinding{
		{Name: "three.test.js", Level: "suite", FileCount: 14},
		{Name: "two.test.js › broad", Level: "test", FileCount: 12},
	}, analyzeCoverage(tests, coverages))
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
