package runner

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
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
const TestsSplitDir = ".dd/tests-split"

type Runner interface {
	Setup(ctx context.Context) error
	PrepareTestOptimization(ctx context.Context) error
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

	// Split test files for runners when there's more than one runner
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
	}

	return nil
}

// calculateParallelRunners determines the number of parallel runners based on skippable percentage
// and parallelism configuration
func calculateParallelRunners(skippablePercentage float64) int {
	return calculateParallelRunnersWithParams(skippablePercentage, settings.GetMinParallelism(), settings.GetMaxParallelism())
}

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

// DistributeTestFiles distributes test files across parallel runners using bin packing algorithm
func DistributeTestFiles(testFiles map[string]int, parallelRunners int) [][]string {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	if len(testFiles) == 0 {
		result := make([][]string, parallelRunners)
		for i := range result {
			result[i] = []string{}
		}
		return result
	}

	// Convert map to sorted slice (largest first)
	files := make([]struct {
		path  string
		count int
	}, 0, len(testFiles))
	for path, count := range testFiles {
		files = append(files, struct {
			path  string
			count int
		}{path: path, count: count})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].count > files[j].count
	})

	// loads tracks current test duration assigned to each bin
	loads := make([]int, parallelRunners)
	// result tracks files assigned to each bin (can be returned directly)
	result := make([][]string, parallelRunners)
	for i := range result {
		result[i] = []string{}
	}

	// First Fit Decreasing algorithm for bin packing
	// On each step take the file in decreasing order of load
	// and put it into the bin with minimum load
	//
	// Time complexity is N * M where
	// N - number of bins (estimated about 10^2)
	// M - number of test files (estimated about 10^4)
	for _, file := range files {
		minBin := 0
		for i := 1; i < len(loads); i++ {
			if loads[i] < loads[minBin] {
				minBin = i
			}
		}

		loads[minBin] += file.count
		result[minBin] = append(result[minBin], file.path)
	}

	return result
}

func (tr *TestRunner) Run(ctx context.Context) error {
	// Check if parallel runners output file exists
	if _, err := os.Stat(ParallelRunnersOutputPath); os.IsNotExist(err) {
		// Run Setup if the file doesn't exist
		if err := tr.Setup(ctx); err != nil {
			return fmt.Errorf("failed to run setup: %w", err)
		}
	}

	// Detect platform and framework
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}

	// Read the number of parallel runners
	runnersData, err := os.ReadFile(ParallelRunnersOutputPath)
	if err != nil {
		return fmt.Errorf("failed to read parallel runners from %s: %w", ParallelRunnersOutputPath, err)
	}

	parallelRunners := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(runnersData)), "%d", &parallelRunners); err != nil {
		return fmt.Errorf("failed to parse parallel runners count: %w", err)
	}

	if parallelRunners > 1 {
		// Read test files from split directory and run in parallel
		entries, err := os.ReadDir(TestsSplitDir)
		if err != nil {
			return fmt.Errorf("failed to read tests split directory %s: %w", TestsSplitDir, err)
		}

		g, _ := errgroup.WithContext(ctx)

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			splitFilePath := filepath.Join(TestsSplitDir, entry.Name())
			entryName := entry.Name()
			g.Go(func() error {
				return tr.runTestsFromFile(framework, splitFilePath, "split file: "+entryName)
			})
		}

		if err := g.Wait(); err != nil {
			return fmt.Errorf("failed to run parallel tests: %w", err)
		}
	} else {
		// Read test files from main output file and run sequentially
		if err := tr.runTestsFromFile(framework, TestFilesOutputPath, "sequential"); err != nil {
			return fmt.Errorf("failed to run tests: %w", err)
		}
	}

	slog.Debug("Run method completed", "parallelRunners", parallelRunners)
	return nil
}

// readTestFilesFromFile reads a file containing test file paths (one per line)
// and returns them as a slice of strings
func readTestFilesFromFile(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return []string{}, nil
	}

	lines := strings.Split(content, "\n")
	testFiles := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			testFiles = append(testFiles, line)
		}
	}

	return testFiles, nil
}

// runTestsFromFile reads test files from the given file path and runs them using the framework
func (tr *TestRunner) runTestsFromFile(framework interface{ RunTests([]string) error }, filePath, logLabel string) error {
	testFiles, err := readTestFilesFromFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read test files from %s: %w", filePath, err)
	}

	if len(testFiles) > 0 {
		slog.Debug("Running tests", "label", logLabel, "testCount", len(testFiles))
		return framework.RunTests(testFiles)
	}
	return nil
}
