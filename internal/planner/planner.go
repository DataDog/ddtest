package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"time"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/civisibility/utils"
	"github.com/DataDog/ddtest/internal/ciprovider"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

type Planner interface {
	Plan(ctx context.Context) error
	LoadPlan() (PlanInfo, error)
	DistributeTestFiles(testFiles []string, parallelRunners int) [][]string
}

type PlanInfo struct {
	Platform    string            `json:"platform"`
	Framework   string            `json:"framework"`
	OSTags      map[string]string `json:"osTags"`
	RuntimeTags map[string]string `json:"runtimeTags"`
}

func NewPlanInfo(tags map[string]string, platformName, frameworkName string) PlanInfo {
	return PlanInfo{
		Platform:    platformName,
		Framework:   frameworkName,
		OSTags:      selectTags(tags, ciConstants.OSPlatform, ciConstants.OSArchitecture, ciConstants.OSVersion),
		RuntimeTags: selectTags(tags, ciConstants.RuntimeName, ciConstants.RuntimeVersion),
	}
}

func (p PlanInfo) IsZero() bool {
	return p.Platform == "" &&
		p.Framework == "" &&
		len(p.OSTags) == 0 &&
		len(p.RuntimeTags) == 0
}

type TestPlanner struct {
	testFiles               map[string]struct{}
	suiteAggregates         map[testSuiteKey]testSuiteAggregate
	suitesBySourceFile      map[string][]testSuiteKey
	testSuiteDurations      map[string]map[string]testoptimization.TestSuiteDurationInfo
	testFileWeights         map[string]int
	testFileDurationSources map[string]testFileDurationSource
	skippablePercentage     float64
	planReport              planReport
	planLoaded              bool
	runInfo                 runmetadata.RunInfo
	planInfo                PlanInfo
	platformDetector        platform.PlatformDetector
	optimizationClient      testoptimization.TestOptimizationClient
	durationsClient         testoptimization.TestSuiteDurationsClient
	ciProviderDetector      ciprovider.CIProviderDetector
	reportWriter            io.Writer
}

const DefaultTestFileWeight = int(time.Second / time.Millisecond)

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

type testFileDurationSource string

const (
	testFileDurationSourceKnown   testFileDurationSource = "known"
	testFileDurationSourceDefault testFileDurationSource = "default"
)

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

type testFileWeightEstimate struct {
	weight int
	source testFileDurationSource
}

func selectTags(tags map[string]string, keys ...string) map[string]string {
	selected := make(map[string]string)
	for _, key := range keys {
		if value := tags[key]; value != "" {
			selected[key] = value
		}
	}
	return selected
}

func Plan(ctx context.Context) error {
	return New().Plan(ctx)
}

func New() *TestPlanner {
	planner := newTestPlannerWithDefaults()
	planner.platformDetector = platform.NewPlatformDetector()
	planner.optimizationClient = testoptimization.NewDatadogClient()
	planner.durationsClient = testoptimization.NewDurationsClient()
	planner.ciProviderDetector = ciprovider.NewCIProviderDetector()
	return planner
}

func NewWithDependencies(
	platformDetector platform.PlatformDetector,
	optimizationClient testoptimization.TestOptimizationClient,
	durationsClient testoptimization.TestSuiteDurationsClient,
	ciProviderDetector ciprovider.CIProviderDetector,
) *TestPlanner {
	planner := newTestPlannerWithDefaults()
	planner.platformDetector = platformDetector
	planner.optimizationClient = optimizationClient
	planner.durationsClient = durationsClient
	planner.ciProviderDetector = ciProviderDetector
	return planner
}

func newTestPlannerWithDefaults() *TestPlanner {
	return &TestPlanner{
		testFiles:               make(map[string]struct{}),
		suiteAggregates:         make(map[testSuiteKey]testSuiteAggregate),
		suitesBySourceFile:      make(map[string][]testSuiteKey),
		testSuiteDurations:      make(map[string]map[string]testoptimization.TestSuiteDurationInfo),
		testFileWeights:         make(map[string]int),
		testFileDurationSources: make(map[string]testFileDurationSource),
		skippablePercentage:     0.0,
		reportWriter:            os.Stderr,
	}
}

func (tp *TestPlanner) Plan(ctx context.Context) error {
	slog.Info("Planning test execution...")

	if err := tp.PreparePlanningData(ctx); err != nil {
		return err
	}

	if err := writePlanFile(constants.ManifestPath, []byte(constants.ManifestVersion+"\n")); err != nil {
		return fmt.Errorf("failed to write test optimization manifest: %w", err)
	}

	if err := tp.storeTestOptimizationPlanCache(); err != nil {
		return fmt.Errorf("failed to store test optimization plan cache: %w", err)
	}

	if err := writeTestFilesArtifact(tp.testFileWeights); err != nil {
		return err
	}

	percentageContent := fmt.Sprintf("%.2f", tp.skippablePercentage)
	if err := writePlanFile(constants.SkippablePercentageOutputPath, []byte(percentageContent)); err != nil {
		return fmt.Errorf("failed to write skippable percentage: %w", err)
	}

	parallelRunnerSplit := calculateParallelRunnerSplit(
		tp.testFileWeights,
		settings.GetMinParallelism(),
		settings.GetMaxParallelism(),
		settings.GetParallelRunnerOverhead(),
	)
	parallelRunners := parallelRunnerSplit.parallelRunners
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := writePlanFile(constants.ParallelRunnersOutputPath, []byte(runnersContent)); err != nil {
		return fmt.Errorf("failed to write parallel runners: %w", err)
	}

	if ciProvider, err := tp.ciProviderDetector.DetectCIProvider(); err == nil {
		slog.Info("CI provider detected, configuring with parallel runners",
			"provider", ciProvider.Name(), "parallelRunners", parallelRunners)

		if err := ciProvider.Configure(parallelRunners); err != nil {
			slog.Warn("Failed to configure CI provider", "provider", ciProvider.Name(), "error", err)
		}
	} else {
		slog.Info("No CI provider detected or CI provider is not supported, running tests without CI integration", "error", err)
	}

	if err := tp.CreateTestSplits(tp.testFileWeights, parallelRunners, constants.TestFilesOutputPath); err != nil {
		return fmt.Errorf("failed to create test splits: %w", err)
	}

	tp.planReport.Split = parallelRunnerSplit

	if settings.GetReportEnabled() {
		tp.planReport.DDTestSettings = settings.Get()
		tp.planReport.LongSeparateRunnerSuites = tp.longSeparateRunnerSuitesReport(parallelRunners, parallelRunnerSplit)
		tp.planReport.SlowestTestSuitesOverall = tp.slowestTestSuitesOverallReport(slowestTestSuitesReportLimit)
		printPlanReport(tp.reportWriter, tp.planReport)
	}

	tp.planLoaded = true
	return nil
}

func (tp *TestPlanner) PreparePlanningData(ctx context.Context) error {
	detectedPlatform, err := tp.platformDetector.DetectPlatform()
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
	tp.runInfo = runmetadata.New(utils.GetCITags())
	tp.planInfo = NewPlanInfo(tags, detectedPlatform.Name(), framework.Name())

	// Create a cancellable context for test discovery
	discoveryCtx, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()

	var skippedTests testSkipper
	var discoveredTests []testoptimization.Test
	var discoveredTestFiles []string
	var fullDiscoverySucceeded bool
	var fullDiscoveryErr error
	var fastDiscoveryErr error

	tp.testFiles = make(map[string]struct{})
	tp.suiteAggregates = make(map[testSuiteKey]testSuiteAggregate)
	tp.suitesBySourceFile = make(map[string][]testSuiteKey)
	tp.testSuiteDurations = make(map[string]map[string]testoptimization.TestSuiteDurationInfo)

	g, _ := errgroup.WithContext(ctx)

	// Goroutine 1: Initialize optimization client and check settings
	g.Go(func() error {
		defer tp.optimizationClient.StoreCacheAndExit()

		if err := tp.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		repositorySettings := tp.optimizationClient.GetSettings()
		tp.planReport.DatadogSettings = newDatadogSettingsReport(repositorySettings)
		tiaSkippingEnabled := false
		if repositorySettings != nil {
			slog.Debug("Repository settings", "tia_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)
			tiaSkippingEnabled = repositorySettings.ItrEnabled && repositorySettings.TestsSkipping

			if !tiaSkippingEnabled {
				slog.Info("TIA or test skipping disabled, cancelling full test discovery")
				cancelDiscovery()
			}
		}

		skippedTests = tp.fetchTestsToSkip(tiaSkippingEnabled)
		tp.planReport.SkippableTestsCount = skippedTests.Count()

		if tiaSkippingEnabled && len(skippedTests.tiaSkippableTests) == 0 {
			slog.Info("No TIA-skippable tests found for this run, cancelling full test discovery")
			cancelDiscovery()
		}

		tp.testSuiteDurations = tp.durationsClient.GetTestSuiteDurations()

		return nil
	})

	// Goroutine 2: Tests discovery (respects context cancellation)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering local tests...", "framework", framework.Name())
		res, discErr := framework.DiscoverTests(discoveryCtx)
		if discErr != nil {
			fullDiscoveryErr = discErr
			if errors.Is(discErr, context.Canceled) {
				slog.Debug("Full test discovery was cancelled")
			} else {
				slog.Warn("Full test discovery failed", "error", discErr)
			}
			return nil // Don't fail the entire process, we have fast discovery as fallback
		}
		if len(res) == 0 {
			fullDiscoveryErr = fmt.Errorf("full test discovery returned no tests")
			slog.Warn("Full test discovery returned no tests; using fast test file discovery fallback",
				"duration", time.Since(startTime),
				"error", fullDiscoveryErr)
			return nil
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
		tp.recordFullDiscoveryResults(discoveredTests, skippedTests, subdirPrefix)
		tp.estimateDiscoveredSuiteDurations()

		slog.Info("Full test discovery succeeded; using full discovery results and ignoring fast-discovered-only files",
			"fastDiscoveredTestFilesCount", len(discoveredTestFiles))
	} else {
		tp.recordFastDiscoveryFallbackFiles(discoveredTestFiles)
		tp.addDurationDataForFastDiscoveryFallback(subdirPrefix)

		slog.Info("Full test discovery did not run or failed; using fast test file discovery fallback",
			"fastDiscoveredTestFilesCount", len(discoveredTestFiles))
	}

	tp.suitesBySourceFile = indexSuitesBySourceFile(tp.suiteAggregates)
	tp.skippablePercentage = calculateSavedTimePercentage(tp.suiteAggregates)
	tp.testFileWeights = tp.calculateFileWeights()

	tp.planReport.RunInfo = tp.runInfo
	tp.planReport.PlanInfo = tp.planInfo
	tp.planReport.Planning = tp.newPlanningReport()

	slog.Info("Test files prepared", "testFilesCount", len(tp.testFiles))

	return nil
}

func (tp *TestPlanner) fetchTestsToSkip(tiaSkippingEnabled bool) testSkipper {
	startTime := time.Now()
	slog.Info("Fetching tests to skip from Datadog...")

	tiaSkippableTests := map[string]bool{}
	if tiaSkippingEnabled {
		tiaSkippableTests = tp.optimizationClient.GetSkippableTests()
	}

	tp.planReport.KnownTests = newKnownTestsReport(tp.optimizationClient.GetKnownTests())
	testManagementTests := tp.optimizationClient.GetTestManagementTestsData()
	tp.planReport.ManagedFlakyTests = newManagedFlakyTestsReport(testManagementTests)

	disabledTests := testoptimization.DisabledTestsFromTestManagementData(testManagementTests)
	slog.Info("Fetched tests to skip",
		"duration", time.Since(startTime),
		"tiaSkippableTestsCount", len(tiaSkippableTests),
		"disabledTestsCount", len(disabledTests))

	return newTestSkipper(tiaSkippableTests, disabledTests)
}

func (tp *TestPlanner) estimateDiscoveredSuiteDurations() {
	for key, aggregate := range tp.suiteAggregates {
		// Without backend timing data, use test counts as the estimate:
		// TotalDuration is the full suite before TIA skips, while EstimatedDuration
		// is the runnable remainder after skipped tests are removed.
		aggregate.TotalDuration = float64(aggregate.NumTests) * float64(time.Second)
		aggregate.EstimatedDuration = float64(aggregate.NumTests-aggregate.NumTestsSkipped) * float64(time.Second)
		aggregate.DurationSource = testFileDurationSourceDefault
		if suiteInfo, ok := getTestSuiteDuration(tp.testSuiteDurations, key); ok {
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
		tp.suiteAggregates[key] = aggregate
	}
}

func (tp *TestPlanner) addDurationDataForFastDiscoveryFallback(subdirPrefix string) {
	seenSourceFiles := make(map[string]struct{})
	for module, suites := range tp.testSuiteDurations {
		for suite, suiteInfo := range suites {
			key := testSuiteKey{Module: module, Suite: suite}
			if _, ok := tp.suiteAggregates[key]; ok {
				continue
			}

			sourceFile := stripCwdSubdirPrefix(suiteInfo.SourceFile, subdirPrefix)
			if _, ok := tp.testFiles[sourceFile]; !ok {
				continue
			}

			duration, ok := parseDurationP50(suiteInfo)
			if !ok {
				continue
			}

			// Backend durations can contain duplicate suite names for the same source file.
			// Fast discovery only tells us the file exists, so keep one backend fallback row per file.
			if _, ok := seenSourceFiles[sourceFile]; ok {
				continue
			}

			tp.suiteAggregates[key] = testSuiteAggregate{
				Module:            module,
				Suite:             suite,
				SourceFile:        sourceFile,
				TotalDuration:     duration,
				EstimatedDuration: duration,
				DurationSource:    testFileDurationSourceKnown,
				NumTests:          1,
				NumTestsSkipped:   0,
			}
			seenSourceFiles[sourceFile] = struct{}{}
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
	if p50 <= 0 {
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

func (tp *TestPlanner) calculateFileWeights() map[string]int {
	return tp.estimateTestFileWeights(tp.testFiles)
}

func (tp *TestPlanner) estimateTestFileWeights(testFiles map[string]struct{}) map[string]int {
	testFileWeights := make(map[string]int, len(testFiles))
	tp.testFileDurationSources = make(map[string]testFileDurationSource, len(testFiles))
	for testFile := range testFiles {
		estimate, ok := tp.estimateTestFileWeight(testFile)
		if ok {
			testFileWeights[testFile] = estimate.weight
			tp.testFileDurationSources[testFile] = estimate.source
		}
	}
	return testFileWeights
}

func (tp *TestPlanner) testFileWeight(testFile string) (int, bool) {
	estimate, ok := tp.estimateTestFileWeight(testFile)
	return estimate.weight, ok
}

func (tp *TestPlanner) estimateTestFileWeight(testFile string) (testFileWeightEstimate, bool) {
	suiteKeys := tp.suitesBySourceFile[testFile]
	if len(suiteKeys) == 0 {
		return testFileWeightEstimate{
			weight: DefaultTestFileWeight,
			source: testFileDurationSourceDefault,
		}, true
	}

	var duration float64
	var hasRunnableSuite bool
	var source testFileDurationSource
	for _, key := range suiteKeys {
		aggregate := tp.suiteAggregates[key]
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
			weight: DefaultTestFileWeight,
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
