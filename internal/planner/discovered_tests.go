package planner

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
)

func (tp *TestPlanner) resetDiscoveryResults() {
	tp.testFiles = make(map[string]struct{})
	tp.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	tp.suitesBySourceFile = make(map[string][]testSuiteKey)
	tp.reportStats = newPlanningReportStats()
}

// warnFastFilesMissingFromFullDiscovery logs files that fast (pattern)
// discovery found but full test discovery returned no tests for. Those files
// are not scheduled and will not run. An empty test file is expected here;
// a same-basename collection error (pytest import file mismatch) is the
// common silent cause.
func (tp *TestPlanner) warnFastFilesMissingFromFullDiscovery(fastDiscoveredTestFiles []string) {
	missing := make([]string, 0)
	for _, file := range fastDiscoveredTestFiles {
		normalized := utils.NormalizePath(file)
		if normalized == "" {
			continue
		}
		if _, ok := tp.testFiles[normalized]; !ok {
			missing = append(missing, normalized)
		}
	}
	if len(missing) == 0 {
		return
	}
	slices.Sort(missing)
	slog.Warn("Full test discovery returned no tests for some discovered files; they will not run. "+
		"This is expected for files without tests, but can also hide a collection error "+
		"(for example a pytest import file mismatch between same-basename files)",
		"files", missing, "count", len(missing))
}

func (tp *TestPlanner) recordFullDiscoveryResults(
	discoveredTests []testoptimization.Test,
	resolvedTestFiles discovery.TestFileSet,
	skippableMatcher skippableMatcher,
	testFramework framework.Framework,
) error {
	testFileMatcher, err := discovery.NewTestFileSetMatcher(resolvedTestFiles, settings.GetTestsExcludePattern())
	if err != nil {
		return err
	}
	selection, err := utils.NewPathMatcher(settings.GetTestsSelectionPattern())
	if err != nil {
		return err
	}

	discoveredTestsCount := len(discoveredTests)
	if discoveredTestsCount == 0 {
		slog.Info("Full test discovery returned no tests")
		return nil
	}

	slog.Info("Using full test discovery results")

	if slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		i := 0
		for _, test := range discoveredTests {
			slog.Debug("Discovered test ID (format: module.suite.name.params)", "id", test.DatadogTestId(), "module", test.Module, "suite", test.Suite, "name", test.Name)
			i++
			if i >= 5 {
				slog.Debug("...and more discovered tests (showing first 5)")
				break
			}
		}
	}

	skippableTestsCount := 0
	excludedTestsCount := 0
	for _, test := range discoveredTests {
		normalizedSourceFile := utils.StripCwdSubdirPrefix(test.SuiteSourceFile)
		normalizedSourceFile = utils.NormalizePath(normalizedSourceFile)
		// Full discovery receives the resolved test selection, but frameworks can still
		// report extra tests loaded by process startup. Keep this planner-side guard so
		// out-of-selection paths cannot enter the runnable file set or suite aggregates.
		if (!selection.Empty() && !selection.MatchNormalizedPath(normalizedSourceFile)) ||
			(normalizedSourceFile != "" && !testFileMatcher.MatchNormalizedPath(normalizedSourceFile)) {
			excludedTestsCount++
			continue
		}
		if normalizedSourceFile != "" {
			tp.testFiles[normalizedSourceFile] = struct{}{}
		}

		match := skippableMatcher.Match(test)
		// Suite-level matches in full discovery would rely on the collected
		// tests' own source files. Keep the same gate as fast suite-mode
		// planning: a suite is only omitted when backend suite metadata maps it
		// to a source file, so forcing full discovery cannot change which
		// files are skipped.
		if match.kind == skippableMatchTIASuite {
			if sourceFile, ok := tp.sourceFileForSuite(match.suiteKey, testFramework); !ok || sourceFile != normalizedSourceFile {
				slog.Debug("Skippable suite has no matching source-file mapping; keeping suite runnable",
					"module", match.suiteKey.Module, "suite", match.suiteKey.Suite)
				match = skippableMatch{}
			}
		}
		if !match.Skipped() {
			slog.Debug("Test is not skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(tp.suiteAggregates, test, normalizedSourceFile)
		} else {
			slog.Debug("Test will be skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordSkippedTest(tp.suiteAggregates, test, normalizedSourceFile)
			tp.recordAppliedSkippable(match, normalizedSourceFile)
			skippableTestsCount++
		}
	}

	slog.Info("Processed the discovered tests",
		"skippableTestsCount", skippableTestsCount,
		"excludedTestsCount", excludedTestsCount,
		"discoveredTestsCount", discoveredTestsCount)
	return nil
}

type skippableMatcher struct {
	tiaSkippableTests  map[string]bool
	tiaSkippableSuites api.SkippableSuites
	disabledTests      map[string]bool
}

type skippableMatch struct {
	kind     skippableMatchKind
	suiteKey testSuiteKey
}

type skippableMatchKind int

const (
	skippableMatchNone skippableMatchKind = iota
	skippableMatchTIATest
	skippableMatchTIASuite
	skippableMatchDisabledTest
)

func (m skippableMatch) Skipped() bool {
	return m.kind != skippableMatchNone
}

func newSkippableMatcher(skippables api.Skippables, disabledTests map[string]bool) skippableMatcher {
	if skippables.Tests == nil {
		skippables.Tests = api.SkippableTests{}
	}
	if skippables.Suites == nil {
		skippables.Suites = api.SkippableSuites{}
	}
	if disabledTests == nil {
		disabledTests = map[string]bool{}
	}

	return skippableMatcher{
		tiaSkippableTests:  skippables.Tests,
		tiaSkippableSuites: skippables.Suites,
		disabledTests:      disabledTests,
	}
}

func (s skippableMatcher) Match(test testoptimization.Test) skippableMatch {
	suite := api.SkippableSuite{Module: test.Module, Suite: test.Suite}
	if s.disabledTests[test.FQN()] {
		return skippableMatch{kind: skippableMatchDisabledTest}
	}
	if s.tiaSkippableSuites[suite] {
		return skippableMatch{
			kind:     skippableMatchTIASuite,
			suiteKey: testSuiteKey{Module: test.Module, Suite: test.Suite},
		}
	}
	if s.tiaSkippableTests[test.DatadogTestId()] {
		return skippableMatch{kind: skippableMatchTIATest}
	}
	return skippableMatch{}
}

func (s skippableMatcher) TIASkippablesCount() int {
	return len(s.tiaSkippableTests) + len(s.tiaSkippableSuites)
}

func (tp *TestPlanner) recordAppliedSkippable(match skippableMatch, sourceFile string) {
	switch match.kind {
	case skippableMatchTIATest:
		tp.reportStats.tiaSkippableTestsApplied++
		tp.reportStats.tiaSkippedTestsByFile[sourceFile]++
	case skippableMatchTIASuite:
		// Full discovery sees suite-level skippables once per test, so keep a set
		// and report the number of suites rather than the number of skipped tests.
		tp.reportStats.uniqueTIASkippableSuitesApplied[match.suiteKey] = struct{}{}
	case skippableMatchDisabledTest:
		tp.reportStats.disabledTestsApplied++
	}
}

// recordITRSkippedTelemetry reports the number of tests/suites the plan
// actually omits. It must run after file weights are calculated: matched
// skippables in files the plan retains (e.g. marker-guarded, or partially
// skippable files left to the tracer) are not skipped by ddtest, and the
// tracer accounts for what it skips at runtime.
func (tp *TestPlanner) recordITRSkippedTelemetry(suiteLevel bool) {
	eventType := telemetry.EventTypeTest
	count := tp.droppedTIATestsCount()
	if suiteLevel {
		eventType = telemetry.EventTypeSuite
		count = tp.droppedTIASuitesCount()
	}
	if count > 0 {
		telemetry.ITRSkipped(tp.telemetryClient, eventType, count)
	}
}

// droppedTIATestsCount returns the number of TIA-skippable tests in files
// the plan drops entirely. Tests in retained files are skipped (and counted)
// by the tracer at runtime instead.
func (tp *TestPlanner) droppedTIATestsCount() int {
	count := 0
	for sourceFile, skipped := range tp.reportStats.tiaSkippedTestsByFile {
		if _, runnable := tp.testFileWeights[sourceFile]; !runnable {
			count += skipped
		}
	}
	return count
}

// droppedTIASuitesCount returns the number of applied skippable suites whose
// source file the plan drops. Marker-guarded suites stay runnable and are
// not counted.
func (tp *TestPlanner) droppedTIASuitesCount() int {
	count := 0
	for key := range tp.reportStats.uniqueTIASkippableSuitesApplied {
		aggregate, ok := tp.suiteAggregates[key]
		if !ok || aggregate.SourceFile == "" {
			continue
		}
		if _, runnable := tp.testFileWeights[aggregate.SourceFile]; !runnable {
			count++
		}
	}
	return count
}

func (tp *TestPlanner) recordFastDiscoveryFallbackFiles(discoveredTestFiles []string) error {
	testFileMatcher, err := discovery.NewTestFileSetMatcher(discovery.TestFileSet{}, settings.GetTestsExcludePattern())
	if err != nil {
		return err
	}
	selection, err := utils.NewPathMatcher(settings.GetTestsSelectionPattern())
	if err != nil {
		return err
	}

	for _, testFile := range discoveredTestFiles {
		normalizedTestFile := utils.NormalizePath(testFile)
		if normalizedTestFile != "" && testFileMatcher.MatchNormalizedPath(normalizedTestFile) &&
			(selection.Empty() || selection.MatchNormalizedPath(normalizedTestFile)) {
			tp.testFiles[normalizedTestFile] = struct{}{}
		}
	}
	return nil
}

func (tp *TestPlanner) recordSuiteLevelSkippables(skippableMatcher skippableMatcher, testFramework framework.Framework) {
	if len(skippableMatcher.tiaSkippableSuites) == 0 {
		return
	}

	for suite := range skippableMatcher.tiaSkippableSuites {
		key := testSuiteKey{Module: suite.Module, Suite: suite.Suite}
		sourceFile, ok := tp.sourceFileForSuite(key, testFramework)
		if !ok {
			slog.Debug("Could not resolve source file for skippable suite; keeping file runnable", "module", suite.Module, "suite", suite.Suite)
			continue
		}
		if _, ok := tp.testFiles[sourceFile]; !ok {
			slog.Debug("Skippable suite source file was not discovered or was excluded; ignoring suite", "module", suite.Module, "suite", suite.Suite, "sourceFile", sourceFile)
			continue
		}

		duration := float64(time.Second)
		durationSource := testFileDurationSourceDefault
		if suiteInfo, ok := getTestSuiteDuration(tp.testSuiteDurations, key); ok {
			if p50, ok := parseDurationP50(suiteInfo); ok {
				duration = p50
				durationSource = testFileDurationSourceKnown
			}
		}

		aggregate := tp.suiteAggregates[key]
		aggregate.Module = key.Module
		aggregate.Suite = key.Suite
		aggregate.SourceFile = sourceFile
		if aggregate.TotalDuration <= 0 {
			aggregate.TotalDuration = duration
		}
		aggregate.EstimatedDuration = 0
		if aggregate.DurationSource == "" {
			aggregate.DurationSource = durationSource
		}
		if aggregate.NumTests == 0 {
			aggregate.NumTests = 1
		}
		aggregate.NumTestsSkipped = aggregate.NumTests
		tp.suiteAggregates[key] = aggregate
		tp.reportStats.uniqueTIASkippableSuitesApplied[key] = struct{}{}
	}
}

func (tp *TestPlanner) keepUnskippableMarkerSuitesRunnable(testFramework framework.Framework) {
	if testFramework == nil {
		tp.reportStats.unskippableMarkerSuitesForced = 0
		return
	}

	startTime := time.Now()
	forceRunnableSuiteAggregatesCount := 0
	unskippableFiles := make(map[string]bool)
	for key, aggregate := range tp.suiteAggregates {
		if aggregate.SourceFile == "" || aggregate.NumTestsSkipped == 0 {
			continue
		}

		unskippable, ok := unskippableFiles[aggregate.SourceFile]
		if !ok {
			unskippable = testFramework.HasUnskippableMarker(aggregate.SourceFile)
			unskippableFiles[aggregate.SourceFile] = unskippable
		}
		if !unskippable {
			continue
		}

		aggregate.NumTestsSkipped = 0
		aggregate.EstimatedDuration = aggregate.TotalDuration
		tp.suiteAggregates[key] = aggregate
		forceRunnableSuiteAggregatesCount++
	}

	slog.Info("Checked unskippable marker suites",
		"duration", time.Since(startTime),
		"forceRunnableSuiteAggregatesCount", forceRunnableSuiteAggregatesCount)
	tp.reportStats.unskippableMarkerSuitesForced = forceRunnableSuiteAggregatesCount
}

func (tp *TestPlanner) sourceFileForSuite(key testSuiteKey, testFramework framework.Framework) (string, bool) {
	if suiteInfo, ok := getTestSuiteDuration(tp.testSuiteDurations, key); ok {
		if sourceFile := normalizeSuiteSourceFile(suiteInfo.SourceFile); sourceFile != "" {
			return sourceFile, true
		}
	}

	if testFramework == nil {
		return "", false
	}
	sourceFile, ok := testFramework.SourceFileForSuite(key.Suite)
	if !ok {
		return "", false
	}
	sourceFile = normalizeSuiteSourceFile(sourceFile)
	if sourceFile == "" {
		return "", false
	}
	return sourceFile, true
}

func normalizeSuiteSourceFile(sourceFile string) string {
	sourceFile = utils.StripCwdSubdirPrefix(sourceFile)
	return utils.NormalizePath(sourceFile)
}

func recordRunnableTest(suiteAggregates map[testSuiteKey]testSuiteAggregate, test testoptimization.Test, sourceFile string) {
	aggregate := suiteAggregateForTest(suiteAggregates, test, sourceFile)
	aggregate.NumTests++
	suiteAggregates[testSuiteKey{Module: test.Module, Suite: test.Suite}] = aggregate
}

func recordSkippedTest(suiteAggregates map[testSuiteKey]testSuiteAggregate, test testoptimization.Test, sourceFile string) {
	aggregate := suiteAggregateForTest(suiteAggregates, test, sourceFile)
	aggregate.NumTests++
	aggregate.NumTestsSkipped++
	suiteAggregates[testSuiteKey{Module: test.Module, Suite: test.Suite}] = aggregate
}

func suiteAggregateForTest(suiteAggregates map[testSuiteKey]testSuiteAggregate, test testoptimization.Test, sourceFile string) testSuiteAggregate {
	key := testSuiteKey{
		Module: test.Module,
		Suite:  test.Suite,
	}
	aggregate := suiteAggregates[key]
	if aggregate.SourceFile == "" {
		aggregate.Module = test.Module
		aggregate.Suite = test.Suite
		aggregate.SourceFile = sourceFile
	}
	return aggregate
}
