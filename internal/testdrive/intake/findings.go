// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package intake

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/tinylib/msgp/msgp"
	"slices"
	"sort"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
)

const (
	minimumSlowTestDuration   = time.Second
	minimumSlowSuiteDuration  = 5 * time.Second
	minimumBroadCoverageFiles = 5
)

// Test describes one observed test and its attempts.
type Test struct {
	Module        string
	Parameters    string
	Name          string
	Suite         string
	SourceFile    string
	SourceStart   int
	SourceEnd     int
	Status        string
	Duration      time.Duration
	Attempts      []TestRun
	CoverageLevel string
	CoveredFiles  []string
}

// TestRun describes one observed run of a test.
type TestRun struct {
	Status       string
	Duration     time.Duration
	Retry        bool
	RetryReason  string
	ErrorType    string
	ErrorMessage string
	ErrorStack   string
}

// CoverageFact describes one test or suite with unusually broad coverage.
type CoverageFact struct {
	Name        string
	Level       string
	SourceFile  string
	SourceStart int
	SourceEnd   int
	FileCount   int
	Files       []string
}

// SuiteCoverage holds files covered by a suite, separate from individual tests.
type SuiteCoverage struct {
	Module       string
	Suite        string
	Files        []string
	CoveredTests int
}

// SlowSuite describes a suite whose combined test duration is unusually high.
type SlowSuite struct {
	Module   string
	Suite    string
	Duration time.Duration
}

// Facts contains the facts shown in the testdrive report.
type Facts struct {
	ConfigurationErrors     []string
	EmptyCoverageEntryCount int
	MissingCoverage         bool
	TestCount               int
	TestEventCount          int
	CoveredTestCount        int
	TestDurationMedian      time.Duration
	SuiteDurationMedian     time.Duration
	CoveredFilesMedian      int
	CoverageLevel           string
	SuiteCoverages          []SuiteCoverage
	Tests                   []Test
	FailedTests             []Test
	FlakyTests              []Test
	SlowTests               []Test
	SlowSuites              []SlowSuite
	BroadCoverage           []CoverageFact
}

// Facts analyzes the test and coverage events captured by the intake.
func (s *Server) Facts() (Facts, error) {
	tests, err := s.testReferences()
	if err != nil {
		return Facts{}, err
	}
	coverages, emptyEntries, err := s.coverageReferences()
	if err != nil {
		return Facts{}, err
	}

	findings := Facts{TestEventCount: len(tests), CoverageLevel: coverageLevel(coverages), EmptyCoverageEntryCount: emptyEntries}
	findings.Tests, findings.FailedTests, findings.FlakyTests, findings.SlowTests, findings.TestDurationMedian = analyzeTests(tests, coverages, findings.CoverageLevel)
	if findings.CoverageLevel == "suite" {
		findings.SuiteCoverages = suiteCoverages(tests, coverages)
	}
	findings.SlowSuites, findings.SuiteDurationMedian = slowSuites(findings.Tests)
	findings.TestCount = len(findings.Tests)
	findings.CoveredTestCount = uniqueCoveredTestCount(tests, coverages)
	findings.MissingCoverage = len(tests) > 0 && len(coverages) == 0 && emptyEntries == 0
	findings.BroadCoverage, findings.CoveredFilesMedian = analyzeCoverage(tests, coverages, findings.CoverageLevel)
	findings.ConfigurationErrors, err = s.configurationErrors()
	return findings, err
}

func analyzeTests(tests []testReference, coverages []coverageReference, level string) ([]Test, []Test, []Test, []Test, time.Duration) {
	var filesByTest map[string][]string
	if level == "test" {
		filesByTest = coverageFilesByTest(tests, coverages)
	}
	testsByName := make(map[string][]testReference)
	order := make([]string, 0)
	for _, test := range tests {
		key := testIdentity(test)
		if _, found := testsByName[key]; !found {
			order = append(order, key)
		}
		testsByName[key] = append(testsByName[key], test)
	}

	failed := make([]Test, 0)
	flaky := make([]Test, 0)
	all := make([]Test, 0, len(order))
	for _, key := range order {
		attempts := testsByName[key]
		finding := testFromReference(attempts[0])
		if files, covered := filesByTest[key]; covered {
			finding.CoverageLevel = "test"
			finding.CoveredFiles = files
		}
		finding.Attempts = make([]TestRun, 0, len(attempts))
		sawPass := false
		sawFailure := false
		for _, attempt := range attempts {
			if attempt.finalStatus != "" {
				finding.Status = attempt.finalStatus
				finding.Duration = attempt.duration
			}
			sawPass = sawPass || attempt.status == "pass" || attempt.finalStatus == "pass"
			sawFailure = sawFailure || attempt.status == "fail" || attempt.finalStatus == "fail"
			finding.Attempts = append(finding.Attempts, TestRun{
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
	sortTests(failed)
	sortTests(flaky)
	return all, failed, flaky, slow, median
}

func coverageFilesByTest(tests []testReference, coverages []coverageReference) map[string][]string {
	testsBySpan := make(map[uint64]string, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = testIdentity(test)
	}
	filesByTest := make(map[string]map[string]struct{})
	for _, coverage := range coverages {
		if coverage.spanID == 0 {
			continue
		}
		if identity := testsBySpan[coverage.spanID]; identity != "" {
			if filesByTest[identity] == nil {
				filesByTest[identity] = make(map[string]struct{})
			}
			for _, file := range coverage.files {
				filesByTest[identity][file] = struct{}{}
			}
		}
	}
	testFiles := make(map[string][]string, len(filesByTest))
	for identity, files := range filesByTest {
		testFiles[identity] = sortedCoverageFiles(files)
	}
	return testFiles
}

func suiteCoverages(tests []testReference, coverages []coverageReference) []SuiteCoverage {
	testsBySuite := make(map[suiteReference]testReference)
	for _, test := range tests {
		testsBySuite[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}] = test
	}
	byName := make(map[string]*SuiteCoverage)
	filesByName := make(map[string]map[string]struct{})
	coveredSuites := make(map[suiteReference]struct{})
	for _, coverage := range coverages {
		if coverage.spanID != 0 {
			continue
		}
		suiteID := suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}
		test, found := testsBySuite[suiteID]
		if !found {
			continue
		}
		coveredSuites[suiteID] = struct{}{}
		key := test.module + "\x00" + test.suite
		if _, found := byName[key]; !found {
			byName[key] = &SuiteCoverage{Module: test.module, Suite: test.suite}
			filesByName[key] = make(map[string]struct{})
		}
		for _, file := range coverage.files {
			filesByName[key][file] = struct{}{}
		}
	}
	coveredTests := make(map[string]map[string]struct{})
	for _, test := range tests {
		if _, found := coveredSuites[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}]; !found {
			continue
		}
		key := test.module + "\x00" + test.suite
		if coveredTests[key] == nil {
			coveredTests[key] = make(map[string]struct{})
		}
		coveredTests[key][testIdentity(test)] = struct{}{}
	}
	result := make([]SuiteCoverage, 0, len(byName))
	for key, finding := range byName {
		finding.Files = sortedCoverageFiles(filesByName[key])
		finding.CoveredTests = len(coveredTests[key])
		result = append(result, *finding)
	}
	return result
}

func sortedCoverageFiles(files map[string]struct{}) []string {
	result := make([]string, 0, len(files))
	for file := range files {
		result = append(result, file)
	}
	slices.Sort(result)
	return result
}

func slowTests(tests []Test) ([]Test, time.Duration) {
	if len(tests) < 2 {
		return nil, medianTestDuration(tests)
	}
	median := medianTestDuration(tests)

	slow := make([]Test, 0)
	for _, test := range tests {
		if test.Duration > minimumSlowTestDuration && test.Duration >= median*5 {
			slow = append(slow, test)
		}
	}
	sortTests(slow)
	return slow, median
}

func slowSuites(tests []Test) ([]SlowSuite, time.Duration) {
	bySuite := make(map[string]SlowSuite)
	for _, test := range tests {
		key := test.Module + "\x00" + test.Suite
		suite := bySuite[key]
		suite.Module, suite.Suite = test.Module, test.Suite
		suite.Duration += test.Duration
		bySuite[key] = suite
	}
	durations := make([]time.Duration, 0, len(bySuite))
	for _, suite := range bySuite {
		durations = append(durations, suite.Duration)
	}
	median := medianDurations(durations)
	var slow []SlowSuite
	if len(bySuite) < 2 {
		return slow, median
	}
	for _, suite := range bySuite {
		if suite.Duration > minimumSlowSuiteDuration && suite.Duration >= median*5 {
			slow = append(slow, suite)
		}
	}
	sort.Slice(slow, func(i, j int) bool {
		if slow[i].Duration != slow[j].Duration {
			return slow[i].Duration > slow[j].Duration
		}
		if slow[i].Module != slow[j].Module {
			return slow[i].Module < slow[j].Module
		}
		return slow[i].Suite < slow[j].Suite
	})
	return slow, median
}

func medianTestDuration(tests []Test) time.Duration {
	if len(tests) == 0 {
		return 0
	}
	durations := make([]time.Duration, 0, len(tests))
	for _, test := range tests {
		durations = append(durations, test.Duration)
	}
	return medianDurations(durations)
}

func medianDurations(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		return 0
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

func analyzeCoverage(tests []testReference, coverages []coverageReference, level string) ([]CoverageFact, int) {
	if level == "" {
		return nil, 0
	}
	testsBySpan := make(map[uint64]testReference, len(tests))
	testsBySuite := make(map[suiteReference]testReference, len(tests))
	for _, test := range tests {
		testsBySpan[test.spanID] = test
		testsBySuite[suiteReference{sessionID: test.sessionID, suiteID: test.suiteID}] = test
	}

	findingsByName := make(map[string]CoverageFact, len(coverages))
	for _, coverage := range coverages {
		if (level == "test" && coverage.spanID == 0) || (level == "suite" && coverage.spanID != 0) {
			continue
		}
		var key string
		finding := CoverageFact{FileCount: coverage.fileCount, Files: coverage.files}
		if coverage.spanID != 0 {
			testReference, found := testsBySpan[coverage.spanID]
			if !found {
				continue
			}
			key = testIdentity(testReference)
			finding.Level = "test"
			test := testFromReference(testReference)
			finding.Name = test.label()
			finding.SourceFile = test.SourceFile
			finding.SourceStart = test.SourceStart
			finding.SourceEnd = test.SourceEnd
		} else {
			test, found := testsBySuite[suiteReference{sessionID: coverage.sessionID, suiteID: coverage.suiteID}]
			if !found {
				continue
			}
			key = fmt.Sprintf("%d/%d", coverage.sessionID, coverage.suiteID)
			finding.Level = "suite"
			finding.Name = test.suite
			if test.module != "" {
				finding.Name = test.module + " › " + test.suite
			}
			finding.SourceFile = test.sourceFile
		}
		if current, found := findingsByName[key]; !found || finding.FileCount > current.FileCount {
			findingsByName[key] = finding
		}
	}
	if len(findingsByName) < 2 {
		return nil, medianCoveredFiles(findingsByName)
	}

	findings := make([]CoverageFact, 0, len(findingsByName))
	fileCounts := make([]int, 0, len(findingsByName))
	for _, finding := range findingsByName {
		findings = append(findings, finding)
		fileCounts = append(fileCounts, finding.FileCount)
	}

	sort.Ints(fileCounts)
	median := medianInts(fileCounts)
	threshold := max(minimumBroadCoverageFiles, median*2)
	broad := make([]CoverageFact, 0)
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

func medianCoveredFiles(findings map[string]CoverageFact) int {
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
		// Match tracer identity: source locations describe a test, but do not identify it.
		return fmt.Sprintf("%q/%q/%q/%q", test.module, test.suite, test.name, test.parameters)
	}
	return fmt.Sprintf("%d/%d/%d", test.sessionID, test.suiteID, test.spanID)
}

func testFromReference(test testReference) Test {
	name := test.name
	if name == "" {
		name = fmt.Sprintf("test %d", test.spanID)
	}
	return Test{
		Name: name, Suite: test.suite, Module: test.module, Parameters: test.parameters, SourceFile: test.sourceFile,
		SourceStart: test.sourceStart, SourceEnd: test.sourceEnd, Duration: test.duration,
	}
}

func (f Test) label() string {
	if f.Suite == "" {
		return f.Name
	}
	return f.Suite + " › " + f.Name
}

func sortTests(findings []Test) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Duration == findings[j].Duration {
			return findings[i].label() < findings[j].label()
		}
		return findings[i].Duration > findings[j].Duration
	})
}

// Configuration errors can be attached to session/suite events or shared metadata,
// not just individual tests. Keep them separate from application test failures.
func (s *Server) configurationErrors() ([]string, error) {
	var failures []string
	collect := func(metadata map[string]any) {
		for key, value := range metadata {
			feature, ok := strings.CutPrefix(key, "_dd.ci.library_configuration_error.")
			if ok && (value == true || value == "true") {
				failures = append(failures, feature)
			}
		}
	}
	for _, request := range s.Requests() {
		if request.Method != http.MethodPost || request.Path != constants.TestCycleURLPath {
			continue
		}
		body, err := uncompressRequestBody(request)
		if err != nil {
			return nil, err
		}
		payload, _, err := msgp.ReadMapStrIntfBytes(body, nil)
		if err != nil {
			return nil, fmt.Errorf("read tracer configuration errors: %w", err)
		}
		if metadata, ok := payload["metadata"].(map[string]any); ok {
			for _, value := range metadata {
				if tags, ok := value.(map[string]any); ok {
					collect(tags)
				}
			}
		}
		if events, ok := payload["events"].([]any); ok {
			for _, value := range events {
				event, _ := value.(map[string]any)
				content, _ := event["content"].(map[string]any)
				metadata, _ := content["meta"].(map[string]any)
				collect(metadata)
			}
		}
	}
	slices.Sort(failures)
	return slices.Compact(failures), nil
}
