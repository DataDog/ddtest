package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/ciprovider"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

type Runner interface {
	Setup(ctx context.Context) error
	Run(ctx context.Context) error
}

type TestRunner struct {
	testFiles           map[string]int
	skippablePercentage float64
	platformDetector    platform.PlatformDetector
	optimizationClient  testoptimization.TestOptimizationClient
	ciProviderDetector  ciprovider.CIProviderDetector
}

func New() *TestRunner {
	return &TestRunner{
		testFiles:           make(map[string]int),
		skippablePercentage: 0.0,
		platformDetector:    platform.NewPlatformDetector(),
		optimizationClient:  testoptimization.NewDatadogClient(),
		ciProviderDetector:  ciprovider.NewCIProviderDetector(),
	}
}

func NewWithDependencies(platformDetector platform.PlatformDetector, optimizationClient testoptimization.TestOptimizationClient, ciProviderDetector ciprovider.CIProviderDetector) *TestRunner {
	return &TestRunner{
		testFiles:           make(map[string]int),
		skippablePercentage: 0.0,
		platformDetector:    platformDetector,
		optimizationClient:  optimizationClient,
		ciProviderDetector:  ciProviderDetector,
	}
}

func (tr *TestRunner) Setup(ctx context.Context) error {
	if err := tr.PrepareTestOptimization(ctx); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(constants.TestFilesOutputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	testFileNames := make([]string, 0, len(tr.testFiles))
	for testFile := range tr.testFiles {
		testFileNames = append(testFileNames, testFile)
	}
	slices.Sort(testFileNames)

	content := strings.Join(testFileNames, "\n")
	if len(testFileNames) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(constants.TestFilesOutputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write test files to %s: %w", constants.TestFilesOutputPath, err)
	}

	percentageContent := fmt.Sprintf("%.2f", tr.skippablePercentage)
	if err := os.WriteFile(constants.SkippablePercentageOutputPath, []byte(percentageContent), 0644); err != nil {
		return fmt.Errorf("failed to write skippable percentage to %s: %w", constants.SkippablePercentageOutputPath, err)
	}

	// Calculate and write parallel runners count
	parallelRunners := calculateParallelRunners(tr.skippablePercentage)
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := os.WriteFile(constants.ParallelRunnersOutputPath, []byte(runnersContent), 0644); err != nil {
		return fmt.Errorf("failed to write parallel runners to %s: %w", constants.ParallelRunnersOutputPath, err)
	}

	// Detect and configure CI provider if available
	if ciProvider, err := tr.ciProviderDetector.DetectCIProvider(); err == nil {
		slog.Debug("CI provider detected, configuring with parallel runners",
			"provider", ciProvider.Name(), "parallelRunners", parallelRunners)

		if err := ciProvider.Configure(parallelRunners); err != nil {
			slog.Warn("Failed to configure CI provider", "provider", ciProvider.Name(), "error", err)
		}
	} else {
		slog.Debug("No CI provider detected or CI provider detection failed", "error", err)
	}

	// Split test files for runners
	if err := CreateTestSplits(tr.testFiles, parallelRunners, constants.TestFilesOutputPath); err != nil {
		return fmt.Errorf("failed to create test splits: %w", err)
	}

	return nil
}

func (tr *TestRunner) Run(ctx context.Context) error {
	// Check if parallel runners output file exists
	if _, err := os.Stat(constants.ParallelRunnersOutputPath); os.IsNotExist(err) {
		// Run Setup if the file doesn't exist
		if err := tr.Setup(ctx); err != nil {
			return fmt.Errorf("failed to run setup: %w", err)
		}
	}
	runnersData, err := os.ReadFile(constants.ParallelRunnersOutputPath)
	if err != nil {
		return fmt.Errorf("failed to read parallel runners from %s: %w", constants.ParallelRunnersOutputPath, err)
	}

	parallelRunners := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(runnersData)), "%d", &parallelRunners); err != nil {
		return fmt.Errorf("failed to parse parallel runners count: %w", err)
	}

	// Parse worker environment variables if provided in settings
	workerEnvMap := settings.GetWorkerEnvMap()

	// Detect platform and framework
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}

	ciNode := settings.GetCiNode()
	if ciNode >= 0 {
		return runCINodeTests(framework, workerEnvMap, ciNode)
	} else if parallelRunners > 1 {
		return runParallelTests(ctx, framework, workerEnvMap)
	} else {
		return runSequentialTests(framework, workerEnvMap)
	}
}
