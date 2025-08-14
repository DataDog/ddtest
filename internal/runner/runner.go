package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

const TestFilesOutputPath = ".dd/test-files.txt"
const SkippablePercentageOutputPath = ".dd/skippable-percentage.txt"

type Runner interface {
	Setup(ctx context.Context) error
	PrepareTestOptimization(ctx context.Context) error
}

type TestRunner struct {
	testFiles           []string
	skippablePercentage float64
	platformDetector    platform.PlatformDetector
	optimizationClient  testoptimization.TestOptimizationClient
}

func New() *TestRunner {
	return &TestRunner{
		testFiles:           nil,
		skippablePercentage: 0.0,
		platformDetector:    platform.NewPlatformDetector(),
		optimizationClient:  testoptimization.NewDatadogClient(),
	}
}

func NewWithDependencies(platformDetector platform.PlatformDetector, optimizationClient testoptimization.TestOptimizationClient) *TestRunner {
	return &TestRunner{
		testFiles:           nil,
		skippablePercentage: 0.0,
		platformDetector:    platformDetector,
		optimizationClient:  optimizationClient,
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

	testFilesMap := make(map[string]bool)
	for _, test := range discoveredTests {
		if !skippableTests[test.FQN] {
			slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SourceFile)
			testFilesMap[test.SourceFile] = true
		} else {
			skippableTestsCount++
		}
	}

	tr.testFiles = make([]string, 0, len(testFilesMap))
	for testFile := range testFilesMap {
		tr.testFiles = append(tr.testFiles, testFile)
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

	content := strings.Join(tr.testFiles, "\n")
	if len(tr.testFiles) > 0 {
		content += "\n"
	}

	if err := os.WriteFile(TestFilesOutputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write test files to %s: %w", TestFilesOutputPath, err)
	}

	percentageContent := fmt.Sprintf("%.2f", tr.skippablePercentage)
	if err := os.WriteFile(SkippablePercentageOutputPath, []byte(percentageContent), 0644); err != nil {
		return fmt.Errorf("failed to write skippable percentage to %s: %w", SkippablePercentageOutputPath, err)
	}

	return nil
}
