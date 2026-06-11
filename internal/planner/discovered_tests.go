package planner

import (
	"log/slog"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

func (tp *TestPlanner) recordFullDiscoveryResults(
	discoveredTests []testoptimization.Test,
	skippableTests testSkipper,
	subdirPrefix string,
) {
	discoveredTestsCount := len(discoveredTests)
	if discoveredTestsCount == 0 {
		slog.Info("Full test discovery returned no tests")
		return
	}

	slog.Info("Using full test discovery results")

	if slog.Default().Enabled(nil, slog.LevelDebug) {
		i := 0
		for _, test := range discoveredTests {
			slog.Debug("Discovered test ID (format: module.suite.name.params)", "id", test.DatadogTestId(), "module", test.Module, "suite", test.Suite, "name", test.Name)
			i++
			if i >= 5 {
				slog.Debug("...and more discovered tests (showing first 5)")
				break
			}
		}
		slog.Debug("Skippable tests map size for matching", "size", skippableTests.Count())
	}

	skippableTestsCount := 0
	for _, test := range discoveredTests {
		normalizedSourceFile := stripCwdSubdirPrefix(test.SuiteSourceFile, subdirPrefix)
		if normalizedSourceFile != "" {
			tp.testFiles[normalizedSourceFile] = struct{}{}
		}

		if !skippableTests.Contains(test) {
			slog.Debug("Test is not skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(tp.suiteAggregates, test, normalizedSourceFile)
		} else {
			slog.Debug("Test will be skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordSkippedTest(tp.suiteAggregates, test, normalizedSourceFile)
			skippableTestsCount++
		}
	}

	slog.Info("Processed the discovered tests", "skippableTestsCount", skippableTestsCount, "discoveredTestsCount", discoveredTestsCount)
}

type testSkipper struct {
	tiaSkippableTests map[string]bool
	disabledTests     map[string]bool
}

func newTestSkipper(tiaSkippableTests, disabledTests map[string]bool) testSkipper {
	return testSkipper{
		tiaSkippableTests: tiaSkippableTests,
		disabledTests:     disabledTests,
	}
}

func (s testSkipper) Contains(test testoptimization.Test) bool {
	return s.tiaSkippableTests[test.DatadogTestId()] || s.disabledTests[test.FQN()]
}

func (s testSkipper) Count() int {
	return len(s.tiaSkippableTests) + len(s.disabledTests)
}

func (tp *TestPlanner) recordFastDiscoveryFallbackFiles(discoveredTestFiles []string) {
	for _, testFile := range discoveredTestFiles {
		if testFile != "" {
			tp.testFiles[testFile] = struct{}{}
		}
	}
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
