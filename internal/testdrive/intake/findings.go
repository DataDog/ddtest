// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"slices"
	"sort"
	"time"
)

const (
	minimumSlowDuration       = 100 * time.Millisecond
	minimumBroadCoverageFiles = 5
)

// TestFinding describes one observed test and its attempts.
type TestFinding struct {
	Name          string
	Suite         string
	SourceFile    string
	SourceStart   int
	SourceEnd     int
	Status        string
	Duration      time.Duration
	Attempts      []TestAttempt
	CoverageLevel string
	CoveredFiles  []string
}

// TestAttempt describes one observed run of a test.
type TestAttempt struct {
	Status       string
	Duration     time.Duration
	Retry        bool
	RetryReason  string
	ErrorType    string
	ErrorMessage string
	ErrorStack   string
}

// CoverageFinding describes one test or suite with unusually broad coverage.
type CoverageFinding struct {
	Name        string
	Level       string
	SourceFile  string
	SourceStart int
	SourceEnd   int
	FileCount   int
	Files       []string
}

// Findings contains the facts shown in the testdrive report.
type Findings struct {
	TestCount        int
	TestEventCount   int
	CoveredTestCount int
	Tests            []TestFinding
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

	findings := Findings{TestEventCount: len(tests)}
	findings.Tests, findings.FailedTests, findings.PassedOnRetry, findings.SlowTests = analyzeTests(tests)
	addCoverageToTests(findings.Tests, tests, coverages)
	findings.TestCount = len(findings.Tests)
	findings.CoveredTestCount = uniqueCoveredTestCount(tests, coverages)
	findings.BroadCoverage = analyzeCoverage(tests, coverages)
	return findings, nil
}

func analyzeTests(tests []testReference) ([]TestFinding, []TestFinding, []TestFinding, []TestFinding) {
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
	all := make([]TestFinding, 0, len(order))
	for _, key := range order {
		attempts := testsByName[key]
		last := attempts[len(attempts)-1]
		finding := testFinding(attempts[0])
		finding.Status = last.status
		finding.Attempts = make([]TestAttempt, 0, len(attempts))
		for _, attempt := range attempts {
			finding.Attempts = append(finding.Attempts, TestAttempt{
				Status:       attempt.status,
				Duration:     attempt.duration,
				Retry:        attempt.isRetry,
				RetryReason:  attempt.retryReason,
				ErrorType:    attempt.errorType,
				ErrorMessage: attempt.errorMessage,
				ErrorStack:   attempt.errorStack,
			})
		}
		all = append(all, finding)

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

	slow := slowTests(all)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Suite != all[j].Suite {
			return all[i].Suite < all[j].Suite
		}
		if all[i].SourceStart != all[j].SourceStart {
			return all[i].SourceStart < all[j].SourceStart
		}
		return all[i].Name < all[j].Name
	})
	sortTestFindings(failed)
	sortTestFindings(passedOnRetry)
	return all, failed, passedOnRetry, slow
}

func addCoverageToTests(findings []TestFinding, tests []testReference, coverages []coverageReference) {
	testsBySpan := make(map[uint64]string, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = testIdentity(test)
	}
	testFiles := make(map[string][]string)
	suiteFiles := make(map[suiteReference][]string)
	testCovered := make(map[string]bool)
	suiteCovered := make(map[suiteReference]bool)
	for _, coverage := range coverages {
		if coverage.spanID != 0 {
			if identity := testsBySpan[coverage.spanID]; identity != "" {
				testCovered[identity] = true
				testFiles[identity] = appendUnique(testFiles[identity], coverage.files...)
			}
			continue
		}
		key := suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}
		suiteCovered[key] = true
		suiteFiles[key] = appendUnique(suiteFiles[key], coverage.files...)
	}

	for findingIndex := range findings {
		finding := &findings[findingIndex]
		identity := finding.Suite + "\x00" + finding.Name
		finding.CoveredFiles = appendUnique(finding.CoveredFiles, testFiles[identity]...)
		if testCovered[identity] {
			finding.CoverageLevel = "test"
			continue
		}
		for _, test := range tests {
			if testIdentity(test) != identity {
				continue
			}
			key := suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}
			finding.CoveredFiles = appendUnique(finding.CoveredFiles, suiteFiles[key]...)
			if suiteCovered[key] {
				finding.CoverageLevel = "suite"
			}
		}
	}
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if slices.Contains(values, addition) {
			continue
		}
		values = append(values, addition)
	}
	slices.Sort(values)
	return values
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
		finding := CoverageFinding{FileCount: coverage.fileCount, Files: coverage.files}
		if coverage.spanID != 0 {
			finding.Level = "test"
			test := testFinding(testsBySpan[coverage.spanID])
			finding.Name = test.label()
			finding.SourceFile = test.SourceFile
			finding.SourceStart = test.SourceStart
			finding.SourceEnd = test.SourceEnd
		} else {
			finding.Level = "suite"
			test := testsBySuite[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}]
			finding.Name = test.suite
			finding.SourceFile = test.sourceFile
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
	return TestFinding{
		Name: name, Suite: test.suite, SourceFile: test.sourceFile,
		SourceStart: test.sourceStart, SourceEnd: test.sourceEnd, Duration: test.duration,
	}
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
