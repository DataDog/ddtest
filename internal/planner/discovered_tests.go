package planner

import (
	"context"
	"log/slog"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
)

func (tp *TestPlanner) resetDiscoveryResults() {
	tp.testFiles = make(map[string]struct{})
	tp.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	tp.suitesBySourceFile = make(map[string][]testSuiteKey)
}

func (tp *TestPlanner) recordFullDiscoveryResults(
	discoveredTests []testoptimization.Test,
	skippableMatcher skippableMatcher,
) error {
	excluder, err := discovery.NewExcluder(settings.GetTestsExcludePattern())
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
		slog.Debug("Skippable matcher size", "size", skippableMatcher.Count())
	}

	skippableTestsCount := 0
	excludedTestsCount := 0
	for _, test := range discoveredTests {
		normalizedSourceFile := utils.StripCwdSubdirPrefix(test.SuiteSourceFile)
		normalizedSourceFile = utils.NormalizePath(normalizedSourceFile)
		// Full discovery should already receive filtered files when exclude is configured.
		// Keep this planner-side guard so normalized tracer-reported paths cannot re-enter
		// the runnable file set or suite aggregates.
		if normalizedSourceFile != "" && excluder.Match(normalizedSourceFile) {
			excludedTestsCount++
			continue
		}
		if normalizedSourceFile != "" {
			tp.testFiles[normalizedSourceFile] = struct{}{}
		}

		if !skippableMatcher.Contains(test) {
			slog.Debug("Test is not skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(tp.suiteAggregates, test, normalizedSourceFile)
		} else {
			slog.Debug("Test will be skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordSkippedTest(tp.suiteAggregates, test, normalizedSourceFile)
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

func (s skippableMatcher) Contains(test testoptimization.Test) bool {
	suite := api.SkippableSuite{Module: test.Module, Suite: test.Suite}
	return s.tiaSkippableTests[test.DatadogTestId()] ||
		s.tiaSkippableSuites[suite] ||
		s.disabledTests[test.FQN()]
}

func (s skippableMatcher) Count() int {
	return s.TIASkippablesCount() + len(s.disabledTests)
}

func (s skippableMatcher) TIASkippablesCount() int {
	return len(s.tiaSkippableTests) + len(s.tiaSkippableSuites)
}

func (s skippableMatcher) TIATestsCount() int {
	return len(s.tiaSkippableTests)
}

func (s skippableMatcher) TIASuitesCount() int {
	return len(s.tiaSkippableSuites)
}

func (s skippableMatcher) DisabledTestsCount() int {
	return len(s.disabledTests)
}

func (tp *TestPlanner) recordFastDiscoveryFallbackFiles(discoveredTestFiles []string) error {
	excluder, err := discovery.NewExcluder(settings.GetTestsExcludePattern())
	if err != nil {
		return err
	}

	for _, testFile := range discoveredTestFiles {
		normalizedTestFile := utils.NormalizePath(testFile)
		if normalizedTestFile != "" && !excluder.Match(normalizedTestFile) {
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
	}
}

func (tp *TestPlanner) keepUnskippableMarkerSuitesRunnable(testFramework framework.Framework) {
	if testFramework == nil {
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
}

func (tp *TestPlanner) sourceFileForSuite(key testSuiteKey, testFramework framework.Framework) (string, bool) {
	if suiteInfo, ok := getTestSuiteDuration(tp.testSuiteDurations, key); ok {
		if sourceFile := normalizeSuiteSourceFile(suiteInfo.SourceFile); sourceFile != "" {
			return sourceFile, true
		}
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
