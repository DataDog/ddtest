package planner

import (
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

type datadogSettingsReport struct {
	Available            bool
	TestImpactAnalysis   bool
	TestSkipping         bool
	TestImpactCollection bool
	KnownTests           bool
	EarlyFlakeDetection  bool
	AutoTestRetries      bool
	FlakyTestManagement  bool
}

func newDatadogSettingsReport(settings *api.SettingsResponseData) datadogSettingsReport {
	if settings == nil {
		return datadogSettingsReport{}
	}
	return datadogSettingsReport{
		Available:            true,
		TestImpactAnalysis:   settings.ItrEnabled,
		TestSkipping:         settings.TestsSkipping,
		TestImpactCollection: settings.CodeCoverage,
		KnownTests:           settings.KnownTestsEnabled,
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

func newKnownTestsReport(knownTests *api.KnownTestsResponseData) knownTestsReport {
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

func newManagedFlakyTestsReport(testManagementTests *api.TestManagementTestsResponseDataModules) managedFlakyTestsReport {
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

type skippablesReport struct {
	Available         bool
	TestSkippingLevel settings.TestSkippingLevel
	TIATests          int
	TIASuites         int
	DisabledTests     int
}

func newSkippablesReport(skippables skippableMatcher, testSkippingLevel settings.TestSkippingLevel) skippablesReport {
	return skippablesReport{
		Available:         true,
		TestSkippingLevel: testSkippingLevel,
		TIATests:          skippables.TIATestsCount(),
		TIASuites:         skippables.TIASuitesCount(),
		DisabledTests:     skippables.DisabledTestsCount(),
	}
}

type testSuiteDurationsReport struct {
	Available bool
	Modules   int
	Suites    int
}

func newTestSuiteDurationsReport(testSuiteDurations *api.TestSuiteDurationsResponseData) testSuiteDurationsReport {
	if testSuiteDurations == nil {
		return testSuiteDurationsReport{}
	}

	report := testSuiteDurationsReport{
		Available: true,
		Modules:   len(testSuiteDurations.TestSuites),
	}
	for _, suites := range testSuiteDurations.TestSuites {
		report.Suites += len(suites)
	}
	return report
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

type discoveryMode string

const (
	discoveryModeUnknown discoveryMode = ""
	discoveryModeFast    discoveryMode = "fast"
	discoveryModeFull    discoveryMode = "full"
)

type discoveryReport struct {
	Available bool
	Mode      discoveryMode
	TestFiles int
	Suites    int
	Tests     int
}

type durationApplicationReport struct {
	Available               bool
	BackendDurationsApplied int
	BackendSuitesAdded      int
	SuitesWithoutDurations  int
	FilesWithoutDurations   int
	ExpectedFullDuration    time.Duration
}

type skippingApplicationReport struct {
	Available                     bool
	TIATests                      int
	TIASuites                     int
	DisabledTests                 int
	UnskippableMarkerSuitesForced int
	FullySkippedFiles             int
}

type planningReport struct {
	Discovery          discoveryReport
	Durations          durationApplicationReport
	Skipping           skippingApplicationReport
	TestFilesToRun     int
	EstimatedTimeSaved float64
}

type planReport struct {
	RunInfo                  runmetadata.RunInfo
	PlanInfo                 PlanInfo
	DDTestSettings           *settings.Config
	DatadogSettings          datadogSettingsReport
	KnownTests               knownTestsReport
	Skippables               skippablesReport
	ManagedFlakyTests        managedFlakyTestsReport
	TestSuiteDurations       testSuiteDurationsReport
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
		Discovery: discoveryReport{
			Available: true,
			Mode:      tp.discoveryMode,
			TestFiles: len(tp.testFiles),
			Suites:    tp.localDiscoveredSuites,
			Tests:     tp.localDiscoveredTests,
		},
		Durations: durationApplicationReport{
			Available:               true,
			BackendDurationsApplied: tp.backendDurationApplicationsCount(),
			BackendSuitesAdded:      tp.backendSuitesAddedCount(),
			SuitesWithoutDurations:  tp.suitesWithoutBackendDurationsCount(),
			FilesWithoutDurations:   tp.filesWithoutBackendDurationsCount(),
			ExpectedFullDuration:    tp.expectedFullDuration(),
		},
		Skipping: skippingApplicationReport{
			Available:                     true,
			TIATests:                      tp.tiaSkippableTestsApplied,
			TIASuites:                     len(tp.tiaSkippableSuitesApplied),
			DisabledTests:                 tp.disabledTestsApplied,
			UnskippableMarkerSuitesForced: tp.unskippableMarkerSuitesForced,
			FullySkippedFiles:             fullySkippedFiles,
		},
		TestFilesToRun:     len(tp.testFileWeights),
		EstimatedTimeSaved: tp.skippablePercentage,
	}
}

func (tp *TestPlanner) backendDurationApplicationsCount() int {
	count := 0
	for _, aggregate := range tp.suiteAggregates {
		if aggregate.DurationSource == testFileDurationSourceKnown {
			count++
		}
	}
	return count
}

func (tp *TestPlanner) backendSuitesAddedCount() int {
	count := len(tp.suiteAggregates) - tp.localDiscoveredSuites
	if count < 0 {
		return 0
	}
	return count
}

func (tp *TestPlanner) suitesWithoutBackendDurationsCount() int {
	count := 0
	for _, aggregate := range tp.suiteAggregates {
		if aggregate.DurationSource != testFileDurationSourceKnown {
			count++
		}
	}
	return count
}

func (tp *TestPlanner) filesWithoutBackendDurationsCount() int {
	count := 0
	for _, source := range tp.testFileDurationSources {
		if source != testFileDurationSourceKnown {
			count++
		}
	}
	return count
}

func (tp *TestPlanner) expectedFullDuration() time.Duration {
	var total float64
	for testFile := range tp.testFiles {
		suiteKeys := tp.suitesBySourceFile[testFile]
		if len(suiteKeys) == 0 {
			total += float64(time.Duration(constants.DefaultTestFileWeight) * time.Millisecond)
			continue
		}
		for _, key := range suiteKeys {
			total += tp.suiteAggregates[key].TotalDuration
		}
	}
	return durationFromNanoseconds(total)
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
