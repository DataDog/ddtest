package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

const defaultTestFileWeight = int(time.Second / time.Millisecond)

func (tr *TestRunner) PrepareTestOptimization(ctx context.Context) error {
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	// Get platform-detected tags first
	tags, err := detectedPlatform.CreateTagsMap()
	if err != nil {
		return fmt.Errorf("failed to create platform tags: %w", err)
	}

	// Check if runtime tags override is provided and merge onto detected tags
	overrideTags, err := settings.GetRuntimeTagsMap()
	if err != nil {
		return fmt.Errorf("failed to parse runtime tags override: %w", err)
	}

	if overrideTags != nil {
		// Merge override tags onto detected tags (override values take precedence)
		maps.Copy(tags, overrideTags)
		slog.Info("Merged runtime tags override from --runtime-tags", "overrideTags", overrideTags, "mergedTags", tags)
	} else {
		slog.Info("Preparing test optimization data", "runtimeTags", tags, "platform", detectedPlatform.Name())
	}

	// Detect framework once to avoid duplicate work
	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}
	slog.Info("Framework detected", "framework", framework.Name())
	tr.runInfoReport = newRunInfoReport(utils.GetCITags(), tags, detectedPlatform.Name(), framework.Name())

	// Create a cancellable context for test discovery
	discoveryCtx, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()

	var skippableTests map[string]bool
	var discoveredTests []testoptimization.Test
	var discoveredTestFiles []string
	var fullDiscoverySucceeded bool
	var fullDiscoveryErr error
	var fastDiscoveryErr error

	tr.testFiles = make(map[string]struct{})
	tr.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	tr.suitesBySourceFile = make(map[string][]testSuiteKey)
	tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)

	g, _ := errgroup.WithContext(ctx)

	// Goroutine 1: Initialize optimization client and check settings
	g.Go(func() error {
		defer tr.optimizationClient.StoreCacheAndExit()

		if err := tr.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		// Get settings to check if ITR is enabled
		settings := tr.optimizationClient.GetSettings()
		tr.planReport.DatadogSettings = newDatadogSettingsReport(settings)
		if settings != nil {
			slog.Debug("Repository settings", "itr_enabled", settings.ItrEnabled, "tests_skipping", settings.TestsSkipping)

			if !settings.ItrEnabled || !settings.TestsSkipping {
				slog.Info("ITR or test skipping disabled, cancelling full test discovery")
				cancelDiscovery()
			}
		}

		tr.fetchAndStoreTestSuiteDurations()

		startTime := time.Now()
		slog.Info("Fetching skippable tests from Datadog...")
		skippableTests = tr.optimizationClient.GetSkippableTests()
		tr.planReport.SkippableTestsCount = len(skippableTests)
		tr.planReport.KnownTests = newKnownTestsReport(tr.optimizationClient.GetKnownTests())
		tr.planReport.ManagedFlakyTests = newManagedFlakyTestsReport(tr.optimizationClient.GetTestManagementTestsData())
		slog.Info("Fetched skippable tests", "duration", time.Since(startTime))

		return nil
	})

	// Goroutine 2: Tests discovery (respects context cancellation)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering local tests...", "framework", framework.Name())
		res, discErr := framework.DiscoverTests(discoveryCtx)
		if discErr != nil {
			fullDiscoveryErr = discErr
			slog.Warn("Full test discovery failed or was cancelled", "error", discErr)
			return nil // Don't fail the entire process, we have fast discovery as fallback
		}
		discoveredTests = res
		fullDiscoverySucceeded = true
		slog.Info("Discovered local tests", "duration", time.Since(startTime), "count", len(discoveredTests))

		return nil
	})

	// Goroutine 3: Test files discovery (fast, must always complete)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering test files (fast)...", "framework", framework.Name())
		res, discErr := framework.DiscoverTestFiles()
		if discErr != nil {
			fastDiscoveryErr = discErr
			slog.Warn("Fast test discovery failed", "error", discErr)
			return nil // Don't fail the entire process if full discovery succeeded
		}
		discoveredTestFiles = res
		slog.Info("Discovered test files (fast)", "duration", time.Since(startTime), "count", len(discoveredTestFiles))

		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// If both discovery methods failed, return an error
	if fullDiscoveryErr != nil && fastDiscoveryErr != nil {
		return fmt.Errorf("both test discovery methods failed - full: %w, fast: %v", fullDiscoveryErr, fastDiscoveryErr)
	}

	// Compute subdirectory prefix once for all paths.
	// When running from a monorepo subdirectory (e.g., "cd core && ddtest plan"),
	// full discovery may return repo-root-relative paths (e.g., "core/spec/...").
	// We normalize them to CWD-relative paths so workers can find the files.
	subdirPrefix := getCwdSubdirPrefix()
	if subdirPrefix != "" {
		slog.Info("Running from subdirectory, will normalize repo-root-relative paths", "subdirPrefix", subdirPrefix)
	}

	// if we have data on which tests exist in the local repository, we will aggregate them
	// into a collection of testSuiteAggregate structs.
	// This collection is used to calculate the skippable percentage and the weighted test files.
	if fullDiscoverySucceeded {
		tr.processDiscoveredTests(discoveredTests, skippableTests, subdirPrefix)
	} else {
		slog.Info("Full test discovery did not run (failed or test impact analysis is not enabled)")
	}

	// Add local test files conforming to test framework pattern (spec/*_spec.rb for example)
	tr.processDiscoveredTestFiles(discoveredTestFiles)

	// Enrich test suite aggregates with the duration data we got from the backend
	tr.resolveSuiteDurations()
	// For the test files with no suite info, try to match them to our backend test suites data
	tr.addBackendTestSuites(subdirPrefix)

	tr.suitesBySourceFile = indexSuitesBySourceFile(tr.suiteAggregates)
	tr.skippablePercentage = calculateSavedTimePercentage(tr.suiteAggregates)
	tr.testFileWeights = tr.weightedTestFiles()
	tr.planReport.RunInfo = tr.runInfoReport
	tr.planReport.Planning = tr.newPlanningReport()

	slog.Info("Test files prepared", "testFilesCount", len(tr.testFiles))

	return nil
}

func initializeDurationsFetchInputs() (string, string, error) {
	ciTags := utils.GetCITags()
	repositoryURL := ciTags[constants.GitRepositoryURL]
	if repositoryURL == "" {
		return "", "", fmt.Errorf("repository URL is required")
	}

	service := resolveServiceName(repositoryURL)

	return ciTags[constants.GitRepositoryURL], service, nil
}

func (tr *TestRunner) fetchAndStoreTestSuiteDurations() {
	startTime := time.Now()
	repositoryURL, service, err := initializeDurationsFetchInputs()
	if err != nil {
		slog.Error("Test durations API errored", "duration", time.Since(startTime), "error", err)
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	durations, err := tr.durationsClient.GetTestSuiteDurations(repositoryURL, service)
	if err != nil {
		slog.Error("Test durations API errored",
			"service", service,
			"repositoryURL", repositoryURL,
			"duration", time.Since(startTime),
			"error", err)
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	totalSuites := countTestSuites(durations)

	if totalSuites == 0 {
		slog.Warn("Test durations API returned no test suites",
			"service", service,
			"repositoryURL", repositoryURL,
			"modulesCount", len(durations),
			"testSuitesCount", totalSuites,
			"duration", time.Since(startTime))
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	slog.Info("Fetched test suite durations",
		"service", service,
		"repositoryURL", repositoryURL,
		"modulesCount", len(durations),
		"testSuitesCount", totalSuites,
		"duration", time.Since(startTime))
	tr.testSuiteDurations = durations
}

type testSuiteKey struct {
	Module string `json:"module"`
	Suite  string `json:"suite"`
}

func (key testSuiteKey) MarshalText() ([]byte, error) {
	return json.Marshal([2]string{key.Module, key.Suite})
}

func (key *testSuiteKey) UnmarshalText(text []byte) error {
	var values [2]string
	if err := json.Unmarshal(text, &values); err != nil {
		return err
	}

	key.Module = values[0]
	key.Suite = values[1]
	return nil
}

type testSuiteAggregate struct {
	Module            string                 `json:"module"`
	Suite             string                 `json:"suite"`
	SourceFile        string                 `json:"sourceFile"`
	TotalDuration     float64                `json:"totalDuration"`
	EstimatedDuration float64                `json:"estimatedDuration"`
	DurationSource    testFileDurationSource `json:"durationSource,omitempty"`
	NumTests          int                    `json:"numTests"`
	NumTestsSkipped   int                    `json:"numTestsSkipped"`
}

func (tr *TestRunner) processDiscoveredTests(
	discoveredTests []testoptimization.Test,
	skippableTests map[string]bool,
	subdirPrefix string,
) {
	discoveredTestsCount := len(discoveredTests)
	if discoveredTestsCount == 0 {
		slog.Info("Full test discovery returned no tests, using only fast test discovery results")
		return
	}

	slog.Info("Using full test discovery results")
	skippableTestsCount := 0
	for _, test := range discoveredTests {
		normalizedSourceFile := stripCwdSubdirPrefix(test.SuiteSourceFile, subdirPrefix)
		if normalizedSourceFile != "" {
			tr.testFiles[normalizedSourceFile] = struct{}{}
		}

		if !skippableTests[test.FQN()] {
			slog.Debug("Test is not skipped", "test", test.FQN(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(tr.suiteAggregates, test, normalizedSourceFile)
		} else {
			recordSkippedTest(tr.suiteAggregates, test, normalizedSourceFile)
			skippableTestsCount++
		}
	}

	slog.Info("Processed the discovered tests", "skippableTestsCount", skippableTestsCount, "discoveredTestsCount", discoveredTestsCount)
}

func (tr *TestRunner) processDiscoveredTestFiles(discoveredTestFiles []string) {
	for _, testFile := range discoveredTestFiles {
		if testFile != "" {
			tr.testFiles[testFile] = struct{}{}
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

func (tr *TestRunner) resolveSuiteDurations() {
	for key, aggregate := range tr.suiteAggregates {
		// Without backend timing data, use test counts as the estimate:
		// TotalDuration is the full suite before ITR skips, while EstimatedDuration
		// is the runnable remainder after skipped tests are removed.
		aggregate.TotalDuration = float64(aggregate.NumTests) * float64(time.Second)
		aggregate.EstimatedDuration = float64(aggregate.NumTests-aggregate.NumTestsSkipped) * float64(time.Second)
		aggregate.DurationSource = testFileDurationSourceDefault
		if suiteInfo, ok := getTestSuiteDuration(tr.testSuiteDurations, key); ok {
			if p50, ok := parseDurationP50(suiteInfo); ok {
				aggregate.TotalDuration = p50
				aggregate.EstimatedDuration = p50
				if aggregate.NumTests > 0 {
					aggregate.EstimatedDuration = p50 * float64(aggregate.NumTests-aggregate.NumTestsSkipped) / float64(aggregate.NumTests)
				}
				if aggregate.EstimatedDuration > 0 {
					aggregate.DurationSource = testFileDurationSourceKnown
				}
			}
		}
		tr.suiteAggregates[key] = aggregate
	}
}

func (tr *TestRunner) addBackendTestSuites(subdirPrefix string) {
	for module, suites := range tr.testSuiteDurations {
		for suite, suiteInfo := range suites {
			key := testSuiteKey{Module: module, Suite: suite}
			if _, ok := tr.suiteAggregates[key]; ok {
				continue
			}

			sourceFile := stripCwdSubdirPrefix(suiteInfo.SourceFile, subdirPrefix)
			if _, ok := tr.testFiles[sourceFile]; !ok {
				continue
			}

			if duration, ok := parseDurationP50(suiteInfo); ok {
				tr.suiteAggregates[key] = testSuiteAggregate{
					Module:            module,
					Suite:             suite,
					SourceFile:        sourceFile,
					TotalDuration:     duration,
					EstimatedDuration: duration,
					DurationSource:    testFileDurationSourceKnown,
					NumTests:          1,
					NumTestsSkipped:   0,
				}
			}
		}
	}
}

func getTestSuiteDuration(
	testSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo,
	key testSuiteKey,
) (testoptimization.TestSuiteDurationInfo, bool) {
	if suiteDurations, ok := testSuiteDurations[key.Module]; ok {
		suiteInfo, ok := suiteDurations[key.Suite]
		return suiteInfo, ok
	}
	return testoptimization.TestSuiteDurationInfo{}, false
}

func parseDurationP50(suiteInfo testoptimization.TestSuiteDurationInfo) (float64, bool) {
	p50, err := strconv.ParseInt(suiteInfo.Duration.P50, 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(p50), true
}

func calculateSavedTimePercentage(suiteAggregates map[testSuiteKey]testSuiteAggregate) float64 {
	var totalDuration float64
	var estimatedDuration float64

	for _, aggregate := range suiteAggregates {
		if aggregate.NumTests == 0 {
			continue
		}

		totalDurationForSuite := aggregate.TotalDuration
		if totalDurationForSuite <= 0 {
			continue
		}

		totalDuration += totalDurationForSuite
		estimatedDuration += aggregate.EstimatedDuration
	}

	if totalDuration == 0 {
		return 0.0
	}

	return (totalDuration - estimatedDuration) / totalDuration * 100.0
}

func indexSuitesBySourceFile(suiteAggregates map[testSuiteKey]testSuiteAggregate) map[string][]testSuiteKey {
	sourceFileLookup := make(map[string][]testSuiteKey)
	for key, aggregate := range suiteAggregates {
		if aggregate.SourceFile == "" {
			continue
		}

		sourceFileLookup[aggregate.SourceFile] = append(sourceFileLookup[aggregate.SourceFile], key)
	}

	for sourceFile := range sourceFileLookup {
		slices.SortFunc(sourceFileLookup[sourceFile], func(a, b testSuiteKey) int {
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
		})
	}
	return sourceFileLookup
}

type testFileDurationSource string

const (
	testFileDurationSourceKnown   testFileDurationSource = "known"
	testFileDurationSourceDefault testFileDurationSource = "default"
)

type testFileWeightEstimate struct {
	weight int
	source testFileDurationSource
}

func (tr *TestRunner) weightedTestFiles() map[string]int {
	return tr.estimateTestFileWeights(tr.testFiles)
}

func (tr *TestRunner) estimateTestFileWeights(testFiles map[string]struct{}) map[string]int {
	testFileWeights := make(map[string]int, len(testFiles))
	tr.testFileDurationSources = make(map[string]testFileDurationSource, len(testFiles))
	for testFile := range testFiles {
		estimate, ok := tr.estimateTestFileWeight(testFile)
		if ok {
			testFileWeights[testFile] = estimate.weight
			tr.testFileDurationSources[testFile] = estimate.source
		}
	}
	return testFileWeights
}

func (tr *TestRunner) testFileWeight(testFile string) (int, bool) {
	estimate, ok := tr.estimateTestFileWeight(testFile)
	return estimate.weight, ok
}

func (tr *TestRunner) estimateTestFileWeight(testFile string) (testFileWeightEstimate, bool) {
	suiteKeys := tr.suitesBySourceFile[testFile]
	if len(suiteKeys) == 0 {
		return testFileWeightEstimate{
			weight: defaultTestFileWeight,
			source: testFileDurationSourceDefault,
		}, true
	}

	var duration float64
	var hasRunnableSuite bool
	var source testFileDurationSource
	for _, key := range suiteKeys {
		aggregate := tr.suiteAggregates[key]
		if aggregate.NumTests == aggregate.NumTestsSkipped {
			continue
		}
		hasRunnableSuite = true
		source = aggregate.DurationSource
		duration += aggregate.EstimatedDuration
	}
	if !hasRunnableSuite {
		return testFileWeightEstimate{}, false
	}
	if source == "" {
		source = testFileDurationSourceDefault
	}
	if duration <= 0 {
		return testFileWeightEstimate{
			weight: defaultTestFileWeight,
			source: source,
		}, true
	}

	weight := int(duration / float64(time.Millisecond))
	if weight < 1 {
		return testFileWeightEstimate{
			weight: 1,
			source: source,
		}, true
	}
	return testFileWeightEstimate{
		weight: weight,
		source: source,
	}, true
}
