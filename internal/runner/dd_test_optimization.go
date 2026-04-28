package runner

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

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
		slog.Info("Fetched skippable tests", "duration", time.Since(startTime))

		return nil
	})

	// Goroutine 2: Full test discovery (respects context cancellation)
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

	// Goroutine 3: Fast test discovery (always completes)
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

	// Process results based on which discovery method succeeded
	if fullDiscoverySucceeded && len(discoveredTests) > 0 {
		slog.Info("Using full test discovery results")
		discoveredTestsCount, skippableTestsCount := recordDiscoveredTests(tr.suiteAggregates, tr.testFiles, discoveredTests, skippableTests, subdirPrefix)

		slog.Info("Test optimization data prepared", "skippableTestsCount", skippableTestsCount, "discoveredTestsCount", discoveredTestsCount)
	} else {
		slog.Info("Using fast test discovery results (ITR disabled or full discovery failed)")
		tr.skippablePercentage = 0.0
	}

	recordDiscoveredTestFiles(tr.testFiles, discoveredTestFiles)
	resolveSuiteDurations(tr.suiteAggregates, tr.testSuiteDurations)
	addBackendSuiteAggregates(tr.suiteAggregates, tr.testSuiteDurations, tr.testFiles, subdirPrefix)
	tr.suitesBySourceFile = indexSuitesBySourceFile(tr.suiteAggregates)
	tr.skippablePercentage = calculateSavedTimePercentage(tr.suiteAggregates)
	slog.Info("Test files prepared", "testFilesCount", len(tr.testFiles))

	return nil
}

func initializeDurationsFetchInputs() (string, string, error) {
	ciTags := utils.GetCITags()
	repositoryURL := ciTags[constants.GitRepositoryURL]
	if repositoryURL == "" {
		return "", "", fmt.Errorf("repository URL is required")
	}

	service := os.Getenv("DD_SERVICE")
	if service == "" {
		repoRegex := regexp.MustCompile(`(?m)/([a-zA-Z0-9\-_.]*)$`)
		matches := repoRegex.FindStringSubmatch(repositoryURL)
		if len(matches) > 1 {
			repositoryURL = strings.TrimSuffix(matches[1], ".git")
		}
		service = repositoryURL
	}

	return ciTags[constants.GitRepositoryURL], service, nil
}

func (tr *TestRunner) fetchAndStoreTestSuiteDurations() {
	repositoryURL, service, err := initializeDurationsFetchInputs()
	if err != nil {
		logDurationsAPIError(err)
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	durations, err := tr.durationsClient.GetTestSuiteDurations(repositoryURL, service)
	if err != nil {
		logDurationsAPIError(err)
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	tr.storeTestSuiteDurations(repositoryURL, service, durations)
}

func (tr *TestRunner) storeTestSuiteDurations(
	repositoryURL, service string,
	durations map[string]map[string]testoptimization.TestSuiteDurationInfo,
) {
	totalSuites := countTestSuites(durations)
	if totalSuites == 0 {
		slog.Warn("Test durations API returned no test suites", "service", service, "repositoryURL", repositoryURL)
		tr.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)
		return
	}

	slog.Debug("Found test suite durations", "service", service, "repositoryURL", repositoryURL, "testSuitesCount", totalSuites)
	tr.testSuiteDurations = durations
}

func countTestSuites(durations map[string]map[string]testoptimization.TestSuiteDurationInfo) int {
	totalSuites := 0
	for _, suites := range durations {
		totalSuites += len(suites)
	}
	return totalSuites
}

func logDurationsAPIError(err error) {
	slog.Error("Test durations API errored", "error", err)
}

type testSuiteKey struct {
	Module string
	Suite  string
}

type testSuiteAggregate struct {
	Module            string
	Suite             string
	SourceFile        string
	TotalDuration     float64
	EstimatedDuration float64
	NumTests          int
	NumTestsSkipped   int
}

func recordDiscoveredTests(
	suiteAggregates map[testSuiteKey]testSuiteAggregate,
	testFiles map[string]struct{},
	discoveredTests []testoptimization.Test,
	skippableTests map[string]bool,
	subdirPrefix string,
) (int, int) {
	skippableTestsCount := 0

	for _, test := range discoveredTests {
		normalizedSourceFile := stripCwdSubdirPrefix(test.SuiteSourceFile, subdirPrefix)
		if normalizedSourceFile != "" {
			testFiles[normalizedSourceFile] = struct{}{}
		}

		if !skippableTests[test.FQN()] {
			slog.Debug("Test is not skipped", "test", test.FQN(), "sourceFile", test.SuiteSourceFile)
			recordRunnableTest(suiteAggregates, test, normalizedSourceFile)
		} else {
			recordSkippedTest(suiteAggregates, test, normalizedSourceFile)
			skippableTestsCount++
		}
	}

	return len(discoveredTests), skippableTestsCount
}

func recordDiscoveredTestFiles(testFiles map[string]struct{}, discoveredTestFiles []string) {
	for _, testFile := range discoveredTestFiles {
		if testFile != "" {
			testFiles[testFile] = struct{}{}
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

func resolveSuiteDurations(
	suiteAggregates map[testSuiteKey]testSuiteAggregate,
	testSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo,
) {
	for key, aggregate := range suiteAggregates {
		// Without backend timing data, use test counts as the estimate:
		// TotalDuration is the full suite before ITR skips, while EstimatedDuration
		// is the runnable remainder after skipped tests are removed.
		aggregate.TotalDuration = float64(aggregate.NumTests) * float64(time.Second)
		aggregate.EstimatedDuration = float64(aggregate.NumTests-aggregate.NumTestsSkipped) * float64(time.Second)
		if suiteInfo, ok := getTestSuiteDuration(testSuiteDurations, key); ok {
			if p50, ok := parseDurationP50(suiteInfo); ok {
				aggregate.TotalDuration = p50
				aggregate.EstimatedDuration = p50
				if aggregate.NumTests > 0 {
					aggregate.EstimatedDuration = p50 * float64(aggregate.NumTests-aggregate.NumTestsSkipped) / float64(aggregate.NumTests)
				}
			}
		}
		suiteAggregates[key] = aggregate
	}
}

func addBackendSuiteAggregates(
	suiteAggregates map[testSuiteKey]testSuiteAggregate,
	testSuiteDurations map[string]map[string]testoptimization.TestSuiteDurationInfo,
	testFiles map[string]struct{},
	subdirPrefix string,
) {
	for module, suites := range testSuiteDurations {
		for suite, suiteInfo := range suites {
			key := testSuiteKey{Module: module, Suite: suite}
			if _, ok := suiteAggregates[key]; ok {
				continue
			}

			sourceFile := stripCwdSubdirPrefix(suiteInfo.SourceFile, subdirPrefix)
			if _, ok := testFiles[sourceFile]; !ok {
				continue
			}

			if duration, ok := parseDurationP50(suiteInfo); ok {
				suiteAggregates[key] = testSuiteAggregate{
					Module:            module,
					Suite:             suite,
					SourceFile:        sourceFile,
					TotalDuration:     duration,
					EstimatedDuration: duration,
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
	return sourceFileLookup
}

func (tr *TestRunner) weightedTestFiles() map[string]int {
	testFileWeights := make(map[string]int, len(tr.testFiles))
	for testFile := range tr.testFiles {
		weight, ok := tr.testFileWeight(testFile)
		if ok {
			testFileWeights[testFile] = weight
		}
	}
	return testFileWeights
}

func (tr *TestRunner) testFileWeight(testFile string) (int, bool) {
	const defaultTestFileWeight = int(time.Second / time.Millisecond)
	suiteKeys := tr.suitesBySourceFile[testFile]
	if len(suiteKeys) == 0 {
		return defaultTestFileWeight, true
	}

	var duration float64
	var hasRunnableSuite bool
	for _, key := range suiteKeys {
		aggregate := tr.suiteAggregates[key]
		if aggregate.NumTests == aggregate.NumTestsSkipped {
			continue
		}
		hasRunnableSuite = true
		duration += aggregate.EstimatedDuration
	}
	if !hasRunnableSuite {
		return 0, false
	}
	if duration <= 0 {
		return defaultTestFileWeight, true
	}

	weight := int(duration / float64(time.Millisecond))
	if weight < 1 {
		return 1, true
	}
	return weight, true
}
