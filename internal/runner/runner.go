package runner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/datadog-test-runner/internal/ciprovider"
	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/settings"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

const TestFilesOutputPath = ".dd/test-files.txt"
const SkippablePercentageOutputPath = ".dd/skippable-percentage.txt"
const ParallelRunnersOutputPath = ".dd/parallel-runners.txt"

type Runner interface {
	Setup(ctx context.Context) error
	PrepareTestOptimization(ctx context.Context) error
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

func (tr *TestRunner) PrepareTestOptimization(ctx context.Context) error {
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	tags, err := detectedPlatform.CreateTagsMap()
	if err != nil {
		return fmt.Errorf("failed to create platform tags: %w", err)
	}

	var skippableTests map[string]bool
	var discoveredTests []testoptimization.Test

	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer tr.optimizationClient.StoreContextAndExit()

		if err := tr.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		skippableTests = tr.optimizationClient.GetSkippableTests()
		return nil
	})

	g.Go(func() error {
		framework, err := detectedPlatform.DetectFramework()
		if err != nil {
			return fmt.Errorf("failed to detect framework: %w", err)
		}

		discoveredTests, err = framework.DiscoverTests()
		return err
	})

	if err := g.Wait(); err != nil {
		return err
	}

	discoveredTestsCount := len(discoveredTests)
	skippableTestsCount := 0

	tr.testFiles = make(map[string]int)
	for _, test := range discoveredTests {
		if !skippableTests[test.FQN] {
			slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SuiteSourceFile)
			if test.SuiteSourceFile != "" {
				tr.testFiles[test.SuiteSourceFile]++
			}
		} else {
			skippableTestsCount++
		}
	}
	tr.skippablePercentage = float64(skippableTestsCount) / float64(discoveredTestsCount) * 100.0

	return nil
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

	return nil
}

// calculateParallelRunners determines the number of parallel runners based on skippable percentage
// and parallelism configuration
func calculateParallelRunners(skippablePercentage float64) int {
	return calculateParallelRunnersWithParams(skippablePercentage, settings.GetMinParallelism(), settings.GetMaxParallelism())
}

// calculateParallelRunnersWithParams is the testable version that accepts parameters directly
func calculateParallelRunnersWithParams(skippablePercentage float64, minParallelism, maxParallelism int) int {
	if maxParallelism == 1 {
		return 1
	}

	if minParallelism < 1 {
		slog.Warn("min_parallelism is less than 1, setting to 1", "min_parallelism", minParallelism)
		return 1
	}

	if maxParallelism < minParallelism {
		slog.Warn("max_parallelism is less than min_parallelism, using min_parallelism",
			"max_parallelism", maxParallelism, "min_parallelism", minParallelism)
		return minParallelism
	}

	percentage := math.Max(0.0, math.Min(100.0, skippablePercentage)) // Clamp to [0, 100]
	runners := float64(maxParallelism) - (percentage/100.0)*float64(maxParallelism-minParallelism)

	return int(math.Round(runners))
}
