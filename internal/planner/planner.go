package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
	"github.com/DataDog/ddtest/internal/utils"
	"golang.org/x/sync/errgroup"
)

type Planner interface {
	Plan(ctx context.Context) error
	LoadPlan() (PlanMetadata, error)
	DistributeTestFiles(testFiles []string, parallelRunners int) [][]string
}

type testOptimizationClient interface {
	Initialize(tags map[string]string) error
	GetSettings() *api.SettingsResponseData
	GetSkippables() api.Skippables
	GetKnownTests() *api.KnownTestsResponseData
	GetTestManagementTestsData() *api.TestManagementTestsResponseDataModules
	GetDisabledTests() map[string]bool
	GetTestSuiteDurations() *api.TestSuiteDurationsResponseData
	BackendRequestTimings() testoptimization.BackendRequestTimings
	StoreCacheAndExit()
}

type PlanMetadata struct {
	Platform          string            `json:"platform"`
	Framework         string            `json:"framework"`
	TestSkippingLevel string            `json:"testSkippingLevel,omitempty"`
	OSTags            map[string]string `json:"osTags"`
	RuntimeTags       map[string]string `json:"runtimeTags"`
}

func NewPlanMetadata(tags map[string]string, platformName, frameworkName string, testSkippingLevel settings.TestSkippingLevel) PlanMetadata {
	return PlanMetadata{
		Platform:          platformName,
		Framework:         frameworkName,
		TestSkippingLevel: testSkippingLevel.String(),
		OSTags:            selectTags(tags, constants.OSPlatform, constants.OSArchitecture, constants.OSVersion),
		RuntimeTags:       selectTags(tags, constants.RuntimeName, constants.RuntimeVersion),
	}
}

func (p PlanMetadata) IsZero() bool {
	return p.Platform == "" &&
		p.Framework == "" &&
		p.TestSkippingLevel == "" &&
		len(p.OSTags) == 0 &&
		len(p.RuntimeTags) == 0
}

type TestPlanner struct {
	testFiles               map[string]struct{}
	suiteAggregates         map[testSuiteKey]testSuiteAggregate
	suitesBySourceFile      map[string][]testSuiteKey
	testSuiteDurations      map[string]map[string]api.TestSuiteDurationInfo
	testFileWeights         map[string]int
	testFileDurationSources map[string]testFileDurationSource
	reportStats             planningReportStats
	skippablePercentage     float64
	planLoaded              bool
	runInfo                 runmetadata.RunInfo
	planMetadata            PlanMetadata
	platformDetector        platform.PlatformDetector
	optimizationClient      testOptimizationClient
	newOptimizationClient   func(testSkippingLevel settings.TestSkippingLevel) testOptimizationClient
	ciProviderDetector      environment.CIProviderDetector
	reportWriter            io.Writer
}

const (
	slowestTestSuitesReportLimit = 10
)

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
	planner.newOptimizationClient = func(testSkippingLevel settings.TestSkippingLevel) testOptimizationClient {
		return testoptimization.NewTestOptimizationClientWithTestSkippingLevel(testSkippingLevel)
	}
	planner.ciProviderDetector = environment.NewCIProviderDetector()
	return planner
}

func NewWithDependencies(
	platformDetector platform.PlatformDetector,
	optimizationClient testOptimizationClient,
	ciProviderDetector environment.CIProviderDetector,
) *TestPlanner {
	planner := newTestPlannerWithDefaults()
	planner.platformDetector = platformDetector
	planner.optimizationClient = optimizationClient
	planner.ciProviderDetector = ciProviderDetector
	return planner
}

func newTestPlannerWithDefaults() *TestPlanner {
	return &TestPlanner{
		testFiles:               make(map[string]struct{}),
		suiteAggregates:         make(map[testSuiteKey]testSuiteAggregate),
		suitesBySourceFile:      make(map[string][]testSuiteKey),
		testSuiteDurations:      make(map[string]map[string]api.TestSuiteDurationInfo),
		testFileWeights:         make(map[string]int),
		testFileDurationSources: make(map[string]testFileDurationSource),
		reportStats:             newPlanningReportStats(),
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

	parallelRunnerSelection := calculateParallelRunnerSplitSelection(
		tp.testFileWeights,
		settings.GetMinParallelism(),
		settings.GetMaxParallelism(),
		settings.GetParallelRunnerOverhead(),
		settings.GetTargetTime(),
	)
	parallelRunnerSplit := parallelRunnerSelection.selected
	parallelRunners := parallelRunnerSplit.parallelRunners
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := writePlanFile(constants.ParallelRunnersOutputPath, []byte(runnersContent)); err != nil {
		return fmt.Errorf("failed to write parallel runners: %w", err)
	}

	if ciProvider, err := tp.ciProviderDetector.DetectCIProvider(); err == nil {
		slog.Debug("CI provider detected, configuring with parallel runners",
			"provider", ciProvider.Name(), "parallelRunners", parallelRunners)

		if err := ciProvider.Configure(parallelRunners); err != nil {
			slog.Warn("Failed to configure CI provider", "provider", ciProvider.Name(), "error", err)
		}
	} else {
		slog.Info("No CI provider detected, running tests without CI integration", "error", err)
	}

	if err := tp.CreateTestSplits(tp.testFileWeights, parallelRunners, constants.TestFilesOutputPath); err != nil {
		return fmt.Errorf("failed to create test splits: %w", err)
	}

	if settings.GetReportEnabled() {
		printPlanReport(tp.reportWriter, tp, parallelRunnerSelection)
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
	testFramework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}
	slog.Info("Framework detected", "framework", testFramework.Name())
	testSkippingLevel := detectedPlatform.TestSkippingLevel()
	isSuiteLevelSkipping := testSkippingLevel == settings.TestSkippingLevelSuite
	isTestLevelSkipping := testSkippingLevel == settings.TestSkippingLevelTest
	fullTestDiscoverySupported := testFramework.SupportsFullTestDiscovery()
	forceFullTestDiscovery := settings.GetForceFullTestDiscovery()
	strictDiscovery := settings.GetStrictDiscovery()
	fullDiscoveryNeeded := fullTestDiscoverySupported && (isTestLevelSkipping || forceFullTestDiscovery)
	tp.runInfo = runmetadata.New(environment.GetCITags())
	tp.planMetadata = NewPlanMetadata(tags, detectedPlatform.Name(), testFramework.Name(), testSkippingLevel)
	if tp.optimizationClient == nil {
		if tp.newOptimizationClient == nil {
			return fmt.Errorf("failed to create optimization client: missing client factory")
		}
		tp.optimizationClient = tp.newOptimizationClient(testSkippingLevel)
	}

	resolvedTestFiles, err := discovery.ResolveTestFiles(testFramework.TestPattern(), settings.GetTestsExcludePattern())
	if err != nil {
		return err
	}

	var skipMatcher skippableMatcher
	var discoveredTestFiles []string
	var discoveredTests []testoptimization.Test
	var fullDiscoverySucceeded bool
	var fullDiscoveryErr error
	var fastDiscoveryErr error
	var tiaSkippingEnabled bool
	var fullDiscoveryDuration time.Duration
	var fastDiscoveryDuration time.Duration
	var selectedDiscoveryMode discoveryMode
	var selectedDiscoveryDuration time.Duration
	var cacheResult discoveryCacheResult

	tp.resetDiscoveryResults()
	tp.testSuiteDurations = make(map[string]map[string]api.TestSuiteDurationInfo)

	if cwdSubdirPrefix := utils.CwdSubdirPrefix(); cwdSubdirPrefix != "" {
		slog.Info("Running from subdirectory, will normalize repo-root-relative paths", "subdirPrefix", cwdSubdirPrefix)
	}

	discoveryCache := newDiscoveryCache(detectedPlatform.Name(), testFramework)
	g, planningCtx := errgroup.WithContext(ctx)
	// planningCtx cancels discovery if a required planning goroutine fails;
	// cancelDiscovery lets backend settings stop only full discovery.
	discoveryCtx, cancelDiscovery := context.WithCancel(planningCtx)
	defer cancelDiscovery()

	// Goroutine 1: Initialize optimization client and check settings
	g.Go(func() error {
		defer tp.optimizationClient.StoreCacheAndExit()

		if err := tp.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		repositorySettings := tp.optimizationClient.GetSettings()
		tiaSkippingEnabled = false
		if repositorySettings != nil {
			slog.Debug("Repository settings", "tia_enabled", repositorySettings.ItrEnabled, "tests_skipping", repositorySettings.TestsSkipping)
			tiaSkippingEnabled = repositorySettings.ItrEnabled && repositorySettings.TestsSkipping

			if tiaSkippingEnabled && isTestLevelSkipping && !fullTestDiscoverySupported {
				slog.Info("Framework does not support full test discovery; TIA skippables will not be applied during planning", "framework", testFramework.Name())
				tiaSkippingEnabled = false
			}

			if !tiaSkippingEnabled {
				slog.Info("TIA or test skipping disabled, cancelling full test discovery")
				cancelDiscovery()
			}
		}

		skipMatcher = tp.fetchSkippables(tiaSkippingEnabled)

		if tiaSkippingEnabled && skipMatcher.TIASkippablesCount() == 0 && !forceFullTestDiscovery {
			slog.Info("No TIA-skippable tests or suites found for this run, cancelling full test discovery")
			cancelDiscovery()
		}

		testSuiteDurations := tp.optimizationClient.GetTestSuiteDurations()
		if testSuiteDurations != nil && testSuiteDurations.TestSuites != nil {
			tp.testSuiteDurations = testSuiteDurations.TestSuites
		}

		return nil
	})

	// Goroutine 2: Tests discovery (respects context cancellation)
	g.Go(func() error {
		fullDiscoveryStartTime := time.Now()
		if !fullDiscoveryNeeded {
			if isSuiteLevelSkipping && !forceFullTestDiscovery {
				slog.Info("Suite-level skipping does not require full test discovery; using fast test file discovery fallback", "framework", testFramework.Name())
				return nil
			}
			if forceFullTestDiscovery && !fullTestDiscoverySupported {
				slog.Warn("Full test discovery was forced but is not supported by framework; using fast test file discovery fallback", "framework", testFramework.Name())
				return nil
			}
			slog.Info("Full test discovery is not supported by framework; using fast test file discovery fallback", "framework", testFramework.Name())
			return nil
		}

		res, restoredCacheResult := discoveryCache.restore()
		cacheResult = restoredCacheResult
		if restoredCacheResult.Used {
			discoveredTests = res
			fullDiscoverySucceeded = true
			fullDiscoveryDuration = time.Since(fullDiscoveryStartTime)
			return nil
		}

		res, discoveryErr := discoverLocalTests(discoveryCtx, testFramework, resolvedTestFiles)
		if discoveryErr != nil {
			if discoveryCtx.Err() == nil {
				fullDiscoveryErr = discoveryErr
			}
			return nil // Don't fail the entire process, we have fast discovery as fallback.
		}
		discoveryCache.store()
		discoveredTests = res
		fullDiscoverySucceeded = true
		fullDiscoveryDuration = time.Since(fullDiscoveryStartTime)

		return nil
	})

	// Goroutine 3: Test files discovery (fast, must always complete)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering test files (fast)...", "framework", testFramework.Name())
		var res []string
		res, discErr := testFramework.DiscoverTestFiles(ctx, resolvedTestFiles)
		if discErr != nil {
			fastDiscoveryErr = discErr
			slog.Warn("Fast test discovery failed", "error", discErr)
			return nil // Don't fail the entire process if full discovery succeeded
		}
		discoveredTestFiles = res
		fastDiscoveryDuration = time.Since(startTime)
		slog.Info("Discovered test files (fast)", "duration", fastDiscoveryDuration, "count", len(discoveredTestFiles))

		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// Full discovery starts optimistically in parallel. If it finishes before
	// backend data cancels it, use it even when TIA has no skips: full discovery
	// is more precise than fast file discovery.
	if fullDiscoverySucceeded {
		if err := tp.recordFullDiscoveryResults(discoveredTests, resolvedTestFiles, skipMatcher); err != nil {
			return err
		}
		selectedDiscoveryMode = discoveryModeFull
		selectedDiscoveryDuration = fullDiscoveryDuration
		// if we have data on which tests exist in the local repository, we will aggregate them
		// into a collection of testSuiteAggregate structs.
		// This collection is used to calculate the skippable percentage and the weighted test files.
		tp.estimateDiscoveredSuiteDurations()

		slog.Info("Full test discovery succeeded; using full discovery results and ignoring fast-discovered-only files",
			"fastDiscoveredTestFilesCount", len(discoveredTestFiles))
	} else {
		if strictDiscovery && fullDiscoveryErr != nil {
			return fmt.Errorf("full test discovery failed: %w", fullDiscoveryErr)
		}
		if fastDiscoveryErr != nil {
			return fmt.Errorf("test discovery failed: %w", fastDiscoveryErr)
		}
		if err := tp.recordFastDiscoveryFallbackFiles(discoveredTestFiles); err != nil {
			return err
		}
		selectedDiscoveryMode = discoveryModeFast
		selectedDiscoveryDuration = fastDiscoveryDuration
		tp.addDurationDataForFastDiscoveryFallback()
		if isSuiteLevelSkipping && tiaSkippingEnabled {
			tp.recordSuiteLevelSkippables(skipMatcher, testFramework)
		}

		slog.Info("Full test discovery did not run or failed; using fast test file discovery fallback",
			"fastDiscoveredTestFilesCount", len(discoveredTestFiles))
	}

	tp.keepUnskippableMarkerSuitesRunnable(testFramework)
	tp.suitesBySourceFile = indexSuitesBySourceFile(tp.suiteAggregates)
	tp.skippablePercentage = calculateSavedTimePercentage(tp.suiteAggregates)
	tp.testFileWeights = tp.calculateFileWeights()

	tp.recordDiscoveryReport(selectedDiscoveryMode, cacheResult, selectedDiscoveryDuration)

	slog.Info("Test files prepared", "testFilesCount", len(tp.testFiles))

	return nil
}

func discoverLocalTests(ctx context.Context, testFramework framework.Framework, testFiles discovery.TestFileSet) ([]testoptimization.Test, error) {
	startTime := time.Now()
	slog.Info("Discovering local tests...", "framework", testFramework.Name())
	tests, err := testFramework.DiscoverTests(ctx, testFiles)
	if err != nil {
		if ctx.Err() != nil {
			slog.Debug("Full test discovery was cancelled")
		} else {
			slog.Warn("Full test discovery failed", "error", err)
		}
		return nil, err
	}
	if err := ensureDiscoveredTests(tests); err != nil {
		slog.Warn("Full test discovery results could not be processed; using fast test file discovery fallback",
			"duration", time.Since(startTime),
			"error", err)
		return nil, err
	}

	slog.Info("Discovered local tests", "duration", time.Since(startTime), "count", len(tests))
	return tests, nil
}

func (tp *TestPlanner) fetchSkippables(tiaSkippingEnabled bool) skippableMatcher {
	startTime := time.Now()
	slog.Info("Fetching skippables from Datadog...")

	skippables := api.NewSkippables()
	if tiaSkippingEnabled {
		skippables = tp.optimizationClient.GetSkippables()
	}

	disabledTests := tp.optimizationClient.GetDisabledTests()
	matcher := newSkippableMatcher(skippables, disabledTests)

	slog.Info("Fetched skippables",
		"duration", time.Since(startTime),
		"tiaSkippableTestsCount", len(skippables.Tests),
		"tiaSkippableSuitesCount", len(skippables.Suites),
		"disabledTestsCount", len(disabledTests))

	return matcher
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

func (tp *TestPlanner) addDurationDataForFastDiscoveryFallback() {
	seenSourceFiles := make(map[string]struct{})
	for module, suites := range tp.testSuiteDurations {
		for suite, suiteInfo := range suites {
			key := testSuiteKey{Module: module, Suite: suite}
			if _, ok := tp.suiteAggregates[key]; ok {
				continue
			}

			// Fast discovery stores normalized file paths in tp.testFiles, so normalize
			// backend duration paths the same way before checking whether the file survived discovery.
			sourceFile := utils.StripCwdSubdirPrefix(suiteInfo.SourceFile)
			sourceFile = utils.NormalizePath(sourceFile)
			if _, ok := tp.testFiles[sourceFile]; !ok {
				continue
			}

			duration, ok := parseDurationP50(suiteInfo)
			if !ok {
				continue
			}

			// Fast discovery only sees files, not test cases. For suite-level TIA we
			// assume a single backend suite maps to a single source file, so one
			// duration-backed aggregate per source file is enough and avoids double-counting.
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
	testSuiteDurations map[string]map[string]api.TestSuiteDurationInfo,
	key testSuiteKey,
) (api.TestSuiteDurationInfo, bool) {
	if suiteDurations, ok := testSuiteDurations[key.Module]; ok {
		suiteInfo, ok := suiteDurations[key.Suite]
		return suiteInfo, ok
	}
	return api.TestSuiteDurationInfo{}, false
}

func parseDurationP50(suiteInfo api.TestSuiteDurationInfo) (float64, bool) {
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

func countSuiteAggregateTests(suiteAggregates map[testSuiteKey]testSuiteAggregate) int {
	count := 0
	for _, aggregate := range suiteAggregates {
		count += aggregate.NumTests
	}
	return count
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
			weight: constants.DefaultTestFileWeight,
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
			weight: constants.DefaultTestFileWeight,
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
