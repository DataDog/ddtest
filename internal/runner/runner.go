package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	ciUtils "github.com/DataDog/ddtest/civisibility/utils"
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
	testFiles               map[string]struct{}
	suiteAggregates         map[testSuiteKey]testSuiteAggregate
	suitesBySourceFile      map[string][]testSuiteKey
	testSuiteDurations      map[string]map[string]testoptimization.TestSuiteDurationInfo
	testFileWeights         map[string]int
	testFileDurationSources map[string]testFileDurationSource
	skippablePercentage     float64
	planReport              planReport
	runInfoReport           runInfoReport
	platformDetector        platform.PlatformDetector
	optimizationClient      testoptimization.TestOptimizationClient
	durationsClient         testoptimization.TestSuiteDurationsClient
	ciProviderDetector      ciprovider.CIProviderDetector
	reportWriter            io.Writer
}

func New() *TestRunner {
	runner := newTestRunnerWithDefaults()
	runner.platformDetector = platform.NewPlatformDetector()
	runner.optimizationClient = testoptimization.NewDatadogClient()
	runner.durationsClient = testoptimization.NewDurationsClient()
	runner.ciProviderDetector = ciprovider.NewCIProviderDetector()
	return runner
}

func NewWithDependencies(
	platformDetector platform.PlatformDetector,
	optimizationClient testoptimization.TestOptimizationClient,
	durationsClient testoptimization.TestSuiteDurationsClient,
	ciProviderDetector ciprovider.CIProviderDetector,
) *TestRunner {
	runner := newTestRunnerWithDefaults()
	runner.platformDetector = platformDetector
	runner.optimizationClient = optimizationClient
	runner.durationsClient = durationsClient
	runner.ciProviderDetector = ciProviderDetector
	return runner
}

func newTestRunnerWithDefaults() *TestRunner {
	return &TestRunner{
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

func (tr *TestRunner) Plan(ctx context.Context) error {
	slog.Info("Planning test execution...")

	if err := tr.PrepareTestOptimization(ctx); err != nil {
		return err
	}

	if err := writePlanFile(constants.ManifestPath, []byte(constants.ManifestVersion+"\n")); err != nil {
		return fmt.Errorf("failed to write test optimization manifest: %w", err)
	}

	if err := tr.storeTestOptimizationPlanCache(); err != nil {
		return fmt.Errorf("failed to store test optimization plan cache: %w", err)
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

	if err := writePlanFile(constants.TestFilesOutputPath, []byte(content)); err != nil {
		return fmt.Errorf("failed to write test files: %w", err)
	}

	percentageContent := fmt.Sprintf("%.2f", tr.skippablePercentage)
	if err := writePlanFile(constants.SkippablePercentageOutputPath, []byte(percentageContent)); err != nil {
		return fmt.Errorf("failed to write skippable percentage: %w", err)
	}

	// Calculate and write parallel runners count
	parallelRunnerSplit := calculateParallelRunnerSplit(
		tr.testFileWeights,
		settings.GetMinParallelism(),
		settings.GetMaxParallelism(),
		settings.GetParallelRunnerOverhead(),
	)
	parallelRunners := parallelRunnerSplit.parallelRunners
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := writePlanFile(constants.ParallelRunnersOutputPath, []byte(runnersContent)); err != nil {
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

	tr.planReport.Split = parallelRunnerSplit
	slog.Info("Test execution planning completed",
		"parallelRunners", parallelRunners,
		"expectedWallTime", parallelRunnerSplit.wallTimeDuration(),
		"imbalance", parallelRunnerSplit.imbalanceDuration(),
		"expectedTotalRuntime", parallelRunnerSplit.totalRuntimeDuration(),
		"testFilesCount", len(tr.testFileWeights))

	if settings.GetReportEnabled() {
		printPlanReport(tr.reportWriter, tr.planReport)
	}

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

	if err := tr.restoreTestOptimizationPlanCache(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("Test optimization plan cache not found; CI-node subsplits will use default weights",
				"file", testoptimization.TestOptimizationPlanCacheFile)
		} else {
			slog.Warn("Failed to restore test optimization plan cache; CI-node subsplits will use default weights",
				"file", testoptimization.TestOptimizationPlanCacheFile, "error", err)
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
	if tr.runInfoReport.isZero() {
		tr.runInfoReport = newRunInfoReport(ciUtils.GetCITags(), nil, detectedPlatform.Name(), framework.Name())
	}

	ciNode := settings.GetCiNode()
	startTime := time.Now()
	executor := newTestExecutor(ctx, framework, workerEnvMap)
	var executionResult runExecutionResult
	if ciNode >= 0 {
		executionResult = executor.runCINode(ciNode, settings.GetCiNodeWorkers(), tr.testFileWeights)
	} else if parallelRunners > 1 {
		executionResult = executor.runParallel()
	} else {
		executionResult = executor.runSequential()
	}

	if settings.GetReportEnabled() {
		printRunReport(tr.reportWriter, runReport{
			RunInfo:   tr.runInfoReport,
			Execution: executionResult.report,
			Duration:  time.Since(startTime),
			Err:       executionResult.err,
		})
	}
	return executionResult.err
}
