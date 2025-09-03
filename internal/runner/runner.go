package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/datadog-test-runner/internal/ciprovider"
	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/settings"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

const TestFilesOutputPath = ".dd/test-files.txt"
const SkippablePercentageOutputPath = ".dd/skippable-percentage.txt"
const ParallelRunnersOutputPath = ".dd/parallel-runners.txt"
const TestsSplitDir = ".dd/tests-split"

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

	if err := os.MkdirAll(filepath.Dir(TestFilesOutputPath), 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	testFileNames := make([]string, 0, len(tr.testFiles))
	for testFile := range tr.testFiles {
		testFileNames = append(testFileNames, testFile)
	}

	content := strings.Join(testFileNames, "\n")
	if len(testFileNames) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(TestFilesOutputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write test files to %s: %w", TestFilesOutputPath, err)
	}

	percentageContent := fmt.Sprintf("%.2f", tr.skippablePercentage)
	if err := os.WriteFile(SkippablePercentageOutputPath, []byte(percentageContent), 0644); err != nil {
		return fmt.Errorf("failed to write skippable percentage to %s: %w", SkippablePercentageOutputPath, err)
	}

	// Calculate and write parallel runners count
	parallelRunners := calculateParallelRunners(tr.skippablePercentage)
	runnersContent := fmt.Sprintf("%d", parallelRunners)
	if err := os.WriteFile(ParallelRunnersOutputPath, []byte(runnersContent), 0644); err != nil {
		return fmt.Errorf("failed to write parallel runners to %s: %w", ParallelRunnersOutputPath, err)
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
	if parallelRunners > 1 {
		// Distribute test files across parallel runners using bin packing
		distribution := DistributeTestFiles(tr.testFiles, parallelRunners)

		// Create tests-split directory
		if err := os.MkdirAll(TestsSplitDir, 0755); err != nil {
			return fmt.Errorf("failed to create tests-split directory: %w", err)
		}

		// Save each runner's test files to separate files
		for i, runnerFiles := range distribution {
			runnerContent := strings.Join(runnerFiles, "\n")
			if len(runnerFiles) > 0 {
				runnerContent += "\n"
			}

			runnerFilePath := fmt.Sprintf("%s/runner-%d", TestsSplitDir, i)
			if err := os.WriteFile(runnerFilePath, []byte(runnerContent), 0644); err != nil {
				return fmt.Errorf("failed to write runner-%d files to %s: %w", i, runnerFilePath, err)
			}
		}
	} else {
		// For single runner, copy test-files.txt to runner-0
		if err := os.MkdirAll(TestsSplitDir, 0755); err != nil {
			return fmt.Errorf("failed to create tests-split directory: %w", err)
		}

		runnerFilePath := fmt.Sprintf("%s/runner-0", TestsSplitDir)
		testFilesData, err := os.ReadFile(TestFilesOutputPath)
		if err != nil {
			return fmt.Errorf("failed to read test files from %s: %w", TestFilesOutputPath, err)
		}

		if err := os.WriteFile(runnerFilePath, testFilesData, 0644); err != nil {
			return fmt.Errorf("failed to copy test files to %s: %w", runnerFilePath, err)
		}
	}

	return nil
}

func (tr *TestRunner) Run(ctx context.Context) error {
	// Check if parallel runners output file exists
	if _, err := os.Stat(ParallelRunnersOutputPath); os.IsNotExist(err) {
		// Run Setup if the file doesn't exist
		if err := tr.Setup(ctx); err != nil {
			return fmt.Errorf("failed to run setup: %w", err)
		}
	}
	runnersData, err := os.ReadFile(ParallelRunnersOutputPath)
	if err != nil {
		return fmt.Errorf("failed to read parallel runners from %s: %w", ParallelRunnersOutputPath, err)
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
