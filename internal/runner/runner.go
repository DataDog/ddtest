package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/ciprovider"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Runner interface {
	Plan(ctx context.Context) error
	Run(ctx context.Context) error
}

type TestRunner struct {
	testFiles           map[string]struct{}
	suiteAggregates     map[testSuiteKey]testSuiteAggregate
	suitesBySourceFile  map[string][]testSuiteKey
	testSuiteDurations  map[string]map[string]testoptimization.TestSuiteDurationInfo
	testFileWeights     map[string]int
	skippablePercentage float64
	platformDetector    platform.PlatformDetector
	optimizationClient  testoptimization.TestOptimizationClient
	durationsClient     testoptimization.TestSuiteDurationsClient
	ciProviderDetector  ciprovider.CIProviderDetector
}

func New() *TestRunner {
	return &TestRunner{
		testFiles:           make(map[string]struct{}),
		suiteAggregates:     make(map[testSuiteKey]testSuiteAggregate),
		suitesBySourceFile:  make(map[string][]testSuiteKey),
		testSuiteDurations:  make(map[string]map[string]testoptimization.TestSuiteDurationInfo),
		testFileWeights:     make(map[string]int),
		skippablePercentage: 0.0,
		platformDetector:    platform.NewPlatformDetector(),
		optimizationClient:  testoptimization.NewDatadogClient(),
		durationsClient:     testoptimization.NewDurationsClient(),
		ciProviderDetector:  ciprovider.NewCIProviderDetector(),
	}
}

func NewWithDependencies(
	platformDetector platform.PlatformDetector,
	optimizationClient testoptimization.TestOptimizationClient,
	durationsClient testoptimization.TestSuiteDurationsClient,
	ciProviderDetector ciprovider.CIProviderDetector,
) *TestRunner {
	return &TestRunner{
		testFiles:           make(map[string]struct{}),
		suiteAggregates:     make(map[testSuiteKey]testSuiteAggregate),
		suitesBySourceFile:  make(map[string][]testSuiteKey),
		testSuiteDurations:  make(map[string]map[string]testoptimization.TestSuiteDurationInfo),
		testFileWeights:     make(map[string]int),
		skippablePercentage: 0.0,
		platformDetector:    platformDetector,
		optimizationClient:  optimizationClient,
		durationsClient:     durationsClient,
		ciProviderDetector:  ciProviderDetector,
	}
}

func (tr *TestRunner) Plan(ctx context.Context) error {
	slog.Info("Planning test execution...")

	if err := tr.PrepareTestOptimization(ctx); err != nil {
		return err
	}

	if err := writePlanFile(constants.ManifestPath, []byte(constants.ManifestVersion+"\n")); err != nil {
		return fmt.Errorf("failed to write test optimization manifest: %w", err)
	}

	if err := tr.storeTestSuiteDurationsCache(); err != nil {
		return fmt.Errorf("failed to store test suite durations cache: %w", err)
	}

	testFileNames := make([]string, 0, len(tr.testFileWeights))
	for testFile := range tr.testFileWeights {
		testFileNames = append(testFileNames, testFile)
	}
	slices.Sort(testFileNames)

	content := strings.Join(testFileNames, "\n")
	if len(testFileNames) > 0 {
		content += "\n"
	}

	if err := writePlanFileCopies([]byte(content), constants.TestFilesOutputPath, constants.LegacyTestFilesOutputPath); err != nil {
		return fmt.Errorf("failed to write test files: %w", err)
	}

	percentageContent := fmt.Sprintf("%.2f", tr.skippablePercentage)
	if err := writePlanFileCopies([]byte(percentageContent), constants.SkippablePercentageOutputPath, constants.LegacySkippablePercentageOutputPath); err != nil {
		return fmt.Errorf("failed to write skippable percentage: %w", err)
	}

	// Calculate and write parallel runners count
	parallelRunnerSplit := calculateParallelRunnerSplit(
		tr.testFileWeights,
		settings.GetMinParallelism(),
		settings.GetMaxParallelism(),
	)
	parallelRunners := parallelRunnerSplit.parallelRunners
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := writePlanFileCopies([]byte(runnersContent), constants.ParallelRunnersOutputPath, constants.LegacyParallelRunnersOutputPath); err != nil {
		return fmt.Errorf("failed to write parallel runners: %w", err)
	}

	// Detect and configure CI provider if available
	if ciProvider, err := tr.ciProviderDetector.DetectCIProvider(); err == nil {
		slog.Info("CI provider detected, configuring with parallel runners",
			"provider", ciProvider.Name(), "parallelRunners", parallelRunners)

		if err := ciProvider.Configure(parallelRunners); err != nil {
			slog.Warn("Failed to configure CI provider", "provider", ciProvider.Name(), "error", err)
		}
	} else {
		slog.Info("No CI provider detected or CI provider is not supported, running tests without CI integration", "error", err)
	}

	// Split test files for runners
	if err := CreateTestSplits(tr.testFileWeights, parallelRunners, constants.TestFilesOutputPath); err != nil {
		return fmt.Errorf("failed to create test splits: %w", err)
	}

	slog.Info("Test execution planning completed",
		"parallelRunners", parallelRunners,
		"expectedWallTime", parallelRunnerSplit.wallTimeDuration(),
		"imbalance", parallelRunnerSplit.imbalanceDuration(),
		"expectedTotalRuntime", parallelRunnerSplit.totalRuntimeDuration(),
		"testFilesCount", len(tr.testFileWeights))

	return nil
}

func (tr *TestRunner) Run(ctx context.Context) error {
	// Check if parallel runners output file exists
	if _, err := os.Stat(constants.ParallelRunnersOutputPath); os.IsNotExist(err) {
		slog.Info("Test optimization planning data not found, running planning phase...")

		// Run Setup if the file doesn't exist
		if err := tr.Plan(ctx); err != nil {
			return fmt.Errorf("failed to run planning phase: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check parallel runners count at %s: %w", constants.ParallelRunnersOutputPath, err)
	}

	if err := tr.restoreTestSuiteDurationsCache(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("Test suite durations cache not found; CI-node subsplits will use default weights",
				"file", testoptimization.TestSuiteDurationsCacheFile)
		} else {
			slog.Warn("Failed to restore test suite durations cache; CI-node subsplits will use default weights",
				"file", testoptimization.TestSuiteDurationsCacheFile, "error", err)
		}
	}

	runnersData, err := os.ReadFile(constants.ParallelRunnersOutputPath)
	if err != nil {
		return fmt.Errorf("failed to read parallel runners count from %s: %w", constants.ParallelRunnersOutputPath, err)
	}
	runnersString := strings.TrimSpace(string(runnersData))

	parallelRunners := 0
	if _, err := fmt.Sscanf(runnersString, "%d", &parallelRunners); err != nil {
		return fmt.Errorf("failed to parse parallel runners count from %s: %w", runnersString, err)
	}

	slog.Info("Got parallel runners count", "parallelRunners", parallelRunners)

	// Parse worker environment variables if provided in settings
	workerEnvMap := settings.GetWorkerEnvMap()
	slog.Info("Worker environment variables", "workerEnvMap", workerEnvMap)

	// Detect platform and framework
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}
	slog.Info("Platform detected", "platform", detectedPlatform.Name())

	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}
	slog.Info("Framework detected", "framework", framework.Name())

	ciNode := settings.GetCiNode()
	if ciNode >= 0 {
		return runCINodeTests(ctx, framework, workerEnvMap, ciNode, settings.GetCiNodeWorkers(), tr.testFileWeights)
	} else if parallelRunners > 1 {
		return runParallelTests(ctx, framework, workerEnvMap)
	} else {
		return runSequentialTests(ctx, framework, workerEnvMap)
	}
}
