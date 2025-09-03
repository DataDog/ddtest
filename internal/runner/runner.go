package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/datadog-test-runner/internal/ciprovider"
	"github.com/DataDog/datadog-test-runner/internal/framework"
	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/settings"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

const TestFilesOutputPath = ".dd/test-files.txt"
const SkippablePercentageOutputPath = ".dd/skippable-percentage.txt"
const ParallelRunnersOutputPath = ".dd/parallel-runners.txt"
const TestsSplitDir = ".dd/tests-split"
const NodeIndexPlaceholder = "{{nodeIndex}}"

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

	// Parse worker environment variables if provided in settings
	workerEnvMap := make(map[string]string)
	if workerEnv := settings.GetWorkerEnv(); workerEnv != "" {
		for pair := range strings.SplitSeq(workerEnv, ";") {
			if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				if key != "" {
					workerEnvMap[key] = value
				}
			}
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

	ciNode := settings.GetCiNode()
	if ciNode >= 0 {
		// Run only the specific ci-node runner file if ci-node is specified
		runnerFilePath := fmt.Sprintf("%s/runner-%d", TestsSplitDir, ciNode)
		if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
			return fmt.Errorf("runner file for ci-node %d does not exist: %s", ciNode, runnerFilePath)
		}

		slog.Debug("Running tests for specific CI node", "ciNode", ciNode, "filePath", runnerFilePath)
		if err := tr.runTestsFromFile(framework, runnerFilePath, workerEnvMap, ciNode); err != nil {
			return fmt.Errorf("failed to run tests for ci-node %d: %w", ciNode, err)
		}
	} else if parallelRunners > 1 {
		// Read test files from split directory and run in parallel
		entries, err := os.ReadDir(TestsSplitDir)
		if err != nil {
			return fmt.Errorf("failed to read tests split directory %s: %w", TestsSplitDir, err)
		}

		g, _ := errgroup.WithContext(ctx)

		for workerIndex, entry := range entries {
			if entry.IsDir() {
				continue
			}

			splitFilePath := filepath.Join(TestsSplitDir, entry.Name())
			g.Go(func() error {
				return tr.runTestsFromFile(framework, splitFilePath, workerEnvMap, workerIndex)
			})
		}

		if err := g.Wait(); err != nil {
			return fmt.Errorf("failed to run parallel tests: %w", err)
		}
	} else {
		// Read test files from main output file and run sequentially
		if err := tr.runTestsFromFile(framework, TestFilesOutputPath, workerEnvMap, 0); err != nil {
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
func (tr *TestRunner) runTestsFromFile(framework framework.Framework, filePath string, workerEnvMap map[string]string, workerIndex int) error {
	testFiles, err := readTestFilesFromFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read test files from %s: %w", filePath, err)
	}

	if len(testFiles) > 0 {
		// Create a copy of the worker env map and replace nodeIndex placeholder
		processedEnvMap := make(map[string]string)
		for key, value := range workerEnvMap {
			processedEnvMap[key] = strings.ReplaceAll(value, NodeIndexPlaceholder, fmt.Sprintf("%d", workerIndex))
		}

		slog.Debug("Running tests", "testFilesCount", len(testFiles), "workerIndex", workerIndex)
		return framework.RunTests(testFiles, processedEnvMap)
	}
	return nil
}
