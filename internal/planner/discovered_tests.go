package planner

import (
	"log/slog"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/utils"
)

func (tp *TestPlanner) resetDiscoveryResults() {
	tp.testFiles = make(map[string]struct{})
	tp.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	tp.suitesBySourceFile = make(map[string][]testSuiteKey)
}

func (tp *TestPlanner) recordFullDiscoveryResults(
	discoveredTests []testoptimization.Test,
	skippableTests testSkipper,
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

		if !skippableTests.Contains(test) {
			slog.Debug("Test is not skipped", "test", test.DatadogTestId(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(tp.suiteAggregates, test, normalizedSourceFile)
		} else {
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
