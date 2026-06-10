package planner

import (
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/utils/net"
)

const slowestTestSuitesReportLimit = 10

type datadogSettingsReport struct {
	Available            bool
	TestImpactAnalysis   bool
	TestSkipping         bool
	TestImpactCollection bool
	KnownTests           bool
	ImpactedTests        bool
	EarlyFlakeDetection  bool
	AutoTestRetries      bool
	FlakyTestManagement  bool
}

func newDatadogSettingsReport(settings *net.SettingsResponseData) datadogSettingsReport {
	if settings == nil {
		return datadogSettingsReport{}
	}
	return datadogSettingsReport{
		Available:            true,
		TestImpactAnalysis:   settings.ItrEnabled,
		TestSkipping:         settings.TestsSkipping,
		TestImpactCollection: settings.CodeCoverage,
		KnownTests:           settings.KnownTestsEnabled,
		ImpactedTests:        settings.ImpactedTestsEnabled,
		EarlyFlakeDetection:  settings.EarlyFlakeDetection.Enabled,
		AutoTestRetries:      settings.FlakyTestRetriesEnabled,
		FlakyTestManagement:  settings.TestManagement.Enabled,
	}
}

type knownTestsReport struct {
	Available bool
	Modules   int
	Suites    int
	Tests     int
}

func newKnownTestsReport(knownTests *net.KnownTestsResponseData) knownTestsReport {
	if knownTests == nil {
		return knownTestsReport{}
	}

	report := knownTestsReport{
		Available: true,
		Modules:   len(knownTests.Tests),
	}
	for _, suites := range knownTests.Tests {
		report.Suites += len(suites)
		for _, tests := range suites {
			report.Tests += len(tests)
		}
	}
	return report
}

type managedFlakyTestsReport struct {
	Available    bool
	Total        int
	Quarantined  int
	Disabled     int
	AttemptToFix int
}

func newManagedFlakyTestsReport(testManagementTests *net.TestManagementTestsResponseDataModules) managedFlakyTestsReport {
	if testManagementTests == nil {
		return managedFlakyTestsReport{}
	}

	report := managedFlakyTestsReport{Available: true}
	for _, suites := range testManagementTests.Modules {
		for _, tests := range suites.Suites {
			for _, test := range tests.Tests {
				report.Total++
				if test.Properties.Quarantined {
					report.Quarantined++
				}
				if test.Properties.Disabled {
					report.Disabled++
				}
				if test.Properties.AttemptToFix {
					report.AttemptToFix++
				}
			}
		}
	}
	return report
}

type durationSourceReport struct {
	Known   int
	Default int
}

type planningReport struct {
	TestFilesDiscovered int
	FullySkippedFiles   int
	TestFilesToRun      int
	DurationSources     durationSourceReport
	EstimatedTimeSaved  float64
}

type testSuiteTimingReport struct {
	Runner            int
	Module            string
	Suite             string
	SourceFile        string
	TotalDuration     time.Duration
	EstimatedDuration time.Duration
	DurationSource    testFileDurationSource
}

type planReport struct {
	RunInfo                  runmetadata.RunInfo
	PlanInfo                 PlanInfo
	DDTestSettings           *settings.Config
	DatadogSettings          datadogSettingsReport
	KnownTests               knownTestsReport
	SkippableTestsCount      int
	ManagedFlakyTests        managedFlakyTestsReport
	Planning                 planningReport
	LongSeparateRunnerSuites []testSuiteTimingReport
	SlowestTestSuitesOverall []testSuiteTimingReport
	Split                    splitScore
}

func (tp *TestPlanner) newPlanningReport() planningReport {
	fullySkippedFiles := len(tp.testFiles) - len(tp.testFileWeights)
	if fullySkippedFiles < 0 {
		fullySkippedFiles = 0
	}

	return planningReport{
		TestFilesDiscovered: len(tp.testFiles),
		FullySkippedFiles:   fullySkippedFiles,
		TestFilesToRun:      len(tp.testFileWeights),
		DurationSources:     tp.durationSourceReport(),
		EstimatedTimeSaved:  tp.skippablePercentage,
	}
}

func (tp *TestPlanner) durationSourceReport() durationSourceReport {
	var report durationSourceReport
	for _, source := range tp.testFileDurationSources {
		switch source {
		case testFileDurationSourceKnown:
			report.Known++
		default:
			report.Default++
		}
	}
	return report
}

func (tp *TestPlanner) longSeparateRunnerSuitesReport(parallelRunners int, split splitScore) []testSuiteTimingReport {
	longThreshold := averageRunnerRuntimeDuration(split)
	if parallelRunners <= 1 || longThreshold <= 0 {
		return nil
	}

	distribution := tp.DistributeWeightedTestFiles(tp.testFileWeights, parallelRunners)
	suites := make([]testSuiteTimingReport, 0)
	for runnerIndex, runnerFiles := range distribution {
		if len(runnerFiles) != 1 {
			continue
		}

		sourceFile := runnerFiles[0]
		if time.Duration(tp.testFileWeights[sourceFile])*time.Millisecond <= longThreshold {
			continue
		}

		for _, key := range tp.suitesBySourceFile[sourceFile] {
			aggregate := tp.suiteAggregates[key]
			suite := newTestSuiteTimingReport(key, aggregate)
			if suite.EstimatedDuration <= longThreshold {
				continue
			}
			suite.Runner = runnerIndex
			suites = append(suites, suite)
		}
	}

	slices.SortFunc(suites, compareSeparateRunnerSuiteTiming)
	return suites
}

func averageRunnerRuntimeDuration(split splitScore) time.Duration {
	if split.parallelRunners <= 0 || split.totalRuntime <= 0 {
		return 0
	}
	return time.Duration(split.totalRuntime/split.parallelRunners) * time.Millisecond
}

func (tp *TestPlanner) slowestTestSuitesOverallReport(limit int) []testSuiteTimingReport {
	if limit <= 0 {
		return nil
	}

	slowest := make([]testSuiteTimingReport, 0, limit)
	for key, aggregate := range tp.suiteAggregates {
		suite := newTestSuiteTimingReport(key, aggregate)
		if suite.TotalDuration <= 0 && suite.EstimatedDuration <= 0 {
			continue
		}
		slowest = insertSlowestTestSuite(slowest, suite, limit)
	}
	return slowest
}

func insertSlowestTestSuite(slowest []testSuiteTimingReport, suite testSuiteTimingReport, limit int) []testSuiteTimingReport {
	position := len(slowest)
	for position > 0 && compareSuiteTimingByTotalDuration(suite, slowest[position-1]) < 0 {
		position--
	}

	if len(slowest) < limit {
		slowest = append(slowest, testSuiteTimingReport{})
		copy(slowest[position+1:], slowest[position:])
		slowest[position] = suite
		return slowest
	}

	if position == len(slowest) {
		return slowest
	}

	copy(slowest[position+1:], slowest[position:len(slowest)-1])
	slowest[position] = suite
	return slowest
}

func newTestSuiteTimingReport(key testSuiteKey, aggregate testSuiteAggregate) testSuiteTimingReport {
	module := aggregate.Module
	if module == "" {
		module = key.Module
	}
	suite := aggregate.Suite
	if suite == "" {
		suite = key.Suite
	}

	return testSuiteTimingReport{
		Runner:            -1,
		Module:            module,
		Suite:             suite,
		SourceFile:        aggregate.SourceFile,
		TotalDuration:     durationFromNanoseconds(aggregate.TotalDuration),
		EstimatedDuration: durationFromNanoseconds(aggregate.EstimatedDuration),
		DurationSource:    aggregate.DurationSource,
	}
}

func durationFromNanoseconds(value float64) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value)
}

func compareSeparateRunnerSuiteTiming(a, b testSuiteTimingReport) int {
	if a.Runner != b.Runner {
		if a.Runner < b.Runner {
			return -1
		}
		return 1
	}
	if result := compareDurationDesc(a.EstimatedDuration, b.EstimatedDuration); result != 0 {
		return result
	}
	if result := compareDurationDesc(a.TotalDuration, b.TotalDuration); result != 0 {
		return result
	}
	return compareSuiteIdentity(a, b)
}

func compareSuiteTimingByTotalDuration(a, b testSuiteTimingReport) int {
	if result := compareDurationDesc(a.TotalDuration, b.TotalDuration); result != 0 {
		return result
	}
	if result := compareDurationDesc(a.EstimatedDuration, b.EstimatedDuration); result != 0 {
		return result
	}
	return compareSuiteIdentity(a, b)
}

func compareDurationDesc(a, b time.Duration) int {
	if a > b {
		return -1
	}
	if a < b {
		return 1
	}
	return 0
}

func compareSuiteIdentity(a, b testSuiteTimingReport) int {
	if a.SourceFile < b.SourceFile {
		return -1
	}
	if a.SourceFile > b.SourceFile {
		return 1
	}
	if a.Module < b.Module {
		return -1
	}
	if a.Module > b.Module {
		return 1
	}
	if a.Suite < b.Suite {
		return -1
	}
	if a.Suite > b.Suite {
		return 1
	}
	return 0
}
