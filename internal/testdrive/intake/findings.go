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
	TestCount          int
	TestEventCount     int
	CoveredTestCount   int
	TestDurationMedian time.Duration
	CoveredFilesMedian int
	CoverageLevel      string
	Tests              []TestFinding
	FailedTests        []TestFinding
	FlakyTests         []TestFinding
	SlowTests          []TestFinding
	BroadCoverage      []CoverageFinding
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

	findings := Findings{TestEventCount: len(tests), CoverageLevel: coverageLevel(coverages)}
	findings.Tests, findings.FailedTests, findings.FlakyTests, findings.SlowTests, findings.TestDurationMedian = analyzeTests(tests)
	addCoverageToTests(findings.Tests, tests, coverages, findings.CoverageLevel)
	findings.TestCount = len(findings.Tests)
	findings.CoveredTestCount = uniqueCoveredTestCount(tests, coverages)
	findings.BroadCoverage, findings.CoveredFilesMedian = analyzeCoverage(tests, coverages, findings.CoverageLevel)
	return findings, nil
}

func analyzeTests(tests []testReference) ([]TestFinding, []TestFinding, []TestFinding, []TestFinding, time.Duration) {
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
	flaky := make([]TestFinding, 0)
	all := make([]TestFinding, 0, len(order))
	for _, key := range order {
		attempts := testsByName[key]
		finding := testFinding(attempts[0])
		finding.Attempts = make([]TestAttempt, 0, len(attempts))
		sawPass := false
		sawFailure := false
		for _, attempt := range attempts {
			if attempt.finalStatus != "" {
				finding.Status = attempt.finalStatus
				finding.Duration = attempt.duration
			}
			sawPass = sawPass || attempt.status == "pass" || attempt.finalStatus == "pass"
			sawFailure = sawFailure || attempt.status == "fail" || attempt.finalStatus == "fail"
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
		if finding.Status == "" {
			finding.Status = attempts[len(attempts)-1].status
		}
		all = append(all, finding)

		if sawPass && sawFailure {
			flaky = append(flaky, finding)
		} else if finding.Status == "fail" {
			failed = append(failed, finding)
		}
	}

	slow, median := slowTests(all)
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
	sortTestFindings(flaky)
	return all, failed, flaky, slow, median
}

func addCoverageToTests(findings []TestFinding, tests []testReference, coverages []coverageReference, level string) {
	testsBySpan := make(map[uint64]string, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = testIdentity(test)
	}
	testFiles := make(map[string][]string)
	suiteFiles := make(map[suiteReference][]string)
	testCovered := make(map[string]bool)
	suiteCovered := make(map[suiteReference]bool)
	for _, coverage := range coverages {
		if level == "test" && coverage.spanID != 0 {
			if identity := testsBySpan[coverage.spanID]; identity != "" {
				testCovered[identity] = true
				testFiles[identity] = appendUnique(testFiles[identity], coverage.files...)
			}
		}
		if level != "suite" || coverage.spanID != 0 {
			continue
		}
		key := suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}
		suiteCovered[key] = true
		suiteFiles[key] = appendUnique(suiteFiles[key], coverage.files...)
	}

	for findingIndex := range findings {
		finding := &findings[findingIndex]
		identity := finding.Suite + "\x00" + finding.Name
		if level == "test" && testCovered[identity] {
			finding.CoveredFiles = appendUnique(finding.CoveredFiles, testFiles[identity]...)
			finding.CoverageLevel = "test"
			continue
		}
		if level != "suite" {
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

func slowTests(tests []TestFinding) ([]TestFinding, time.Duration) {
	if len(tests) < 2 {
		return nil, medianTestDuration(tests)
	}
	median := medianTestDuration(tests)
	threshold := max(minimumSlowDuration, median*2)

	slow := make([]TestFinding, 0)
	for _, test := range tests {
		if test.Duration >= threshold && test.Duration > median {
			slow = append(slow, test)
		}
	}
	sortTestFindings(slow)
	return slow, median
}

func medianTestDuration(tests []TestFinding) time.Duration {
	if len(tests) == 0 {
		return 0
	}
	durations := make([]time.Duration, 0, len(tests))
	for _, test := range tests {
		durations = append(durations, test.Duration)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	middle := len(durations) / 2
	if len(durations)%2 == 1 {
		return durations[middle]
	}
	return durations[middle-1] + (durations[middle]-durations[middle-1])/2
}

func coverageLevel(coverages []coverageReference) string {
	level := ""
	for _, coverage := range coverages {
		if coverage.spanID != 0 {
			return "test"
		}
		level = "suite"
	}
	return level
}

func analyzeCoverage(tests []testReference, coverages []coverageReference, level string) ([]CoverageFinding, int) {
	if len(coverages) < 2 || level == "" {
		return nil, 0
	}
	testsBySpan := make(map[uint64]testReference, len(tests))
	testsBySuite := make(map[suiteReference]testReference, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = test
		testsBySuite[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}] = test
	}

	findingsByName := make(map[string]CoverageFinding, len(coverages))
	for _, coverage := range coverages {
		if (level == "test" && coverage.spanID == 0) || (level == "suite" && coverage.spanID != 0) {
			continue
		}
		finding := CoverageFinding{FileCount: coverage.fileCount, Files: coverage.files}
		if coverage.spanID != 0 {
			testReference, found := testsBySpan[coverage.spanID]
			if !found {
				continue
			}
			finding.Level = "test"
			test := testFinding(testReference)
			finding.Name = test.label()
			finding.SourceFile = test.SourceFile
			finding.SourceStart = test.SourceStart
			finding.SourceEnd = test.SourceEnd
		} else {
			test, found := testsBySuite[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}]
			if !found {
				continue
			}
			finding.Level = "suite"
			finding.Name = test.suite
			finding.SourceFile = test.sourceFile
		}
		key := finding.Level + "\x00" + finding.Name
		if current, found := findingsByName[key]; !found || finding.FileCount > current.FileCount {
			findingsByName[key] = finding
		}
	}
	if len(findingsByName) < 2 {
		return nil, medianCoveredFiles(findingsByName)
	}

	findings := make([]CoverageFinding, 0, len(findingsByName))
	fileCounts := make([]int, 0, len(findingsByName))
	for _, finding := range findingsByName {
		findings = append(findings, finding)
		fileCounts = append(fileCounts, finding.FileCount)
	}

	sort.Ints(fileCounts)
	median := medianInts(fileCounts)
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
	return broad, median
}

func medianCoveredFiles(findings map[string]CoverageFinding) int {
	fileCounts := make([]int, 0, len(findings))
	for _, finding := range findings {
		fileCounts = append(fileCounts, finding.FileCount)
	}
	sort.Ints(fileCounts)
	return medianInts(fileCounts)
}

func medianInts(values []int) int {
	if len(values) == 0 {
		return 0
	}
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return values[middle-1] + (values[middle]-values[middle-1])/2
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
