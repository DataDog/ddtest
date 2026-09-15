// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"sort"
	"time"
)

const (
	minimumSlowDuration       = 100 * time.Millisecond
	minimumBroadCoverageFiles = 5
)

// TestFinding describes one test worth calling out in the testdrive report.
type TestFinding struct {
	Name     string
	Suite    string
	Duration time.Duration
}

// CoverageFinding describes one test or suite with unusually broad coverage.
type CoverageFinding struct {
	Name      string
	Level     string
	FileCount int
}

// Findings contains the facts shown in the testdrive report.
type Findings struct {
	TestCount        int
	TestEventCount   int
	CoveredTestCount int
	FailedTests      []TestFinding
	PassedOnRetry    []TestFinding
	SlowTests        []TestFinding
	BroadCoverage    []CoverageFinding
}

// Findings analyzes the test and coverage events captured by the intake.
func (s *Server) Findings() (Findings, error) {
	tests, err := s.testReferences()
	if err != nil {
		return Findings{}, err
	}
	coverages, err := s.coverageReferences()
	if err != nil {
		return Findings{}, err
	}

	findings := Findings{
		TestEventCount:   len(tests),
		CoveredTestCount: uniqueCoveredTestCount(tests, coverages),
	}
	findings.FailedTests, findings.PassedOnRetry, findings.SlowTests, findings.TestCount = analyzeTests(tests)
	findings.BroadCoverage = analyzeCoverage(tests, coverages)
	return findings, nil
}

func analyzeTests(tests []testReference) ([]TestFinding, []TestFinding, []TestFinding, int) {
	testsByName := make(map[string][]testReference)
	order := make([]string, 0)
	for _, test := range tests {
		key := testIdentity(test)
		if _, found := testsByName[key]; !found {
			order = append(order, key)
		}
		testsByName[key] = append(testsByName[key], test)
	}

	failed := make([]TestFinding, 0)
	passedOnRetry := make([]TestFinding, 0)
	durations := make([]TestFinding, 0, len(order))
	for _, key := range order {
		attempts := testsByName[key]
		last := attempts[len(attempts)-1]
		finding := testFinding(last)
		for _, attempt := range attempts {
			if attempt.duration > finding.Duration {
				finding.Duration = attempt.duration
			}
		}
		durations = append(durations, finding)

		if last.status == "fail" {
			failed = append(failed, finding)
		}

		sawFailure := false
		for attemptIndex, attempt := range attempts {
			if attempt.status == "fail" {
				sawFailure = true
				continue
			}
			if sawFailure && attempt.status == "pass" && (attempt.isRetry || attemptIndex > 0) {
				passedOnRetry = append(passedOnRetry, finding)
				break
			}
		}
	}

	slow := slowTests(durations)
	sortTestFindings(failed)
	sortTestFindings(passedOnRetry)
	return failed, passedOnRetry, slow, len(order)
}

func slowTests(tests []TestFinding) []TestFinding {
	if len(tests) < 2 {
		return nil
	}
	durations := make([]time.Duration, 0, len(tests))
	for _, test := range tests {
		durations = append(durations, test.Duration)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	median := durations[(len(durations)-1)/2]
	threshold := max(minimumSlowDuration, median*2)

	slow := make([]TestFinding, 0)
	for _, test := range tests {
		if test.Duration >= threshold && test.Duration > median {
			slow = append(slow, test)
		}
	}
	sortTestFindings(slow)
	return slow
}

func analyzeCoverage(tests []testReference, coverages []coverageReference) []CoverageFinding {
	if len(coverages) < 2 {
		return nil
	}
	testsBySpan := make(map[uint64]testReference, len(tests))
	testsBySuite := make(map[suiteReference]testReference, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = test
		testsBySuite[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}] = test
	}

	findingsByName := make(map[string]CoverageFinding, len(coverages))
	for _, coverage := range coverages {
		finding := CoverageFinding{FileCount: coverage.fileCount}
		if coverage.spanID != 0 {
			finding.Level = "test"
			finding.Name = testFinding(testsBySpan[coverage.spanID]).label()
		} else {
			finding.Level = "suite"
			test := testsBySuite[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}]
			finding.Name = test.suite
			if finding.Name == "" {
				finding.Name = fmt.Sprintf("suite %d", coverage.suiteID)
			}
		}
		key := finding.Level + "\x00" + finding.Name
		if current, found := findingsByName[key]; !found || finding.FileCount > current.FileCount {
			findingsByName[key] = finding
		}
	}
	if len(findingsByName) < 2 {
		return nil
	}

	findings := make([]CoverageFinding, 0, len(findingsByName))
	fileCounts := make([]int, 0, len(findingsByName))
	for _, finding := range findingsByName {
		findings = append(findings, finding)
		fileCounts = append(fileCounts, finding.FileCount)
	}

	sort.Ints(fileCounts)
	median := fileCounts[(len(fileCounts)-1)/2]
	threshold := max(minimumBroadCoverageFiles, median*2)
	broad := make([]CoverageFinding, 0)
	for _, finding := range findings {
		if finding.FileCount >= threshold && finding.FileCount > median {
			broad = append(broad, finding)
		}
	}
	sort.Slice(broad, func(i, j int) bool {
		if broad[i].FileCount == broad[j].FileCount {
			return broad[i].Name < broad[j].Name
		}
		return broad[i].FileCount > broad[j].FileCount
	})
	return broad
}

func uniqueCoveredTestCount(tests []testReference, coverages []coverageReference) int {
	coveredTests := make(map[uint64]struct{}, len(coverages))
	coveredSuites := make(map[suiteReference]struct{}, len(coverages))
	for _, coverage := range coverages {
		if coverage.spanID != 0 {
			coveredTests[coverage.spanID] = struct{}{}
		} else {
			coveredSuites[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}] = struct{}{}
		}
	}

	coveredIdentities := make(map[string]struct{})
	for _, test := range tests {
		_, testCovered := coveredTests[test.spanID]
		_, suiteCovered := coveredSuites[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}]
		if testCovered || suiteCovered {
			coveredIdentities[testIdentity(test)] = struct{}{}
		}
	}
	return len(coveredIdentities)
}

func testIdentity(test testReference) string {
	if test.name != "" || test.suite != "" {
		return test.suite + "\x00" + test.name
	}
	return fmt.Sprintf("%d/%d", test.suiteID, test.spanID)
}

func testFinding(test testReference) TestFinding {
	name := test.name
	if name == "" {
		name = fmt.Sprintf("test %d", test.spanID)
	}
	return TestFinding{Name: name, Suite: test.suite, Duration: test.duration}
}

func (f TestFinding) label() string {
	if f.Suite == "" {
		return f.Name
	}
	return f.Suite + " › " + f.Name
}

func sortTestFindings(findings []TestFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Duration == findings[j].Duration {
			return findings[i].label() < findings[j].label()
		}
		return findings[i].Duration > findings[j].Duration
	})
}
