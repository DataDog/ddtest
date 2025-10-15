package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/DataDog/ddtest/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

func (tr *TestRunner) PrepareTestOptimization(ctx context.Context) error {
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	tags, err := detectedPlatform.CreateTagsMap()
	if err != nil {
		return fmt.Errorf("failed to create platform tags: %w", err)
	}

	slog.Info("Preparing test optimization data", "runtimeTags", tags, "platform", detectedPlatform.Name())

	var skippableTests map[string]bool
	var discoveredTests []testoptimization.Test

	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer tr.optimizationClient.StoreCacheAndExit()

		if err := tr.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		startTime := time.Now()
		slog.Info("Fetching skippable tests from Datadog...")
		skippableTests = tr.optimizationClient.GetSkippableTests()
		slog.Info("Fetched skippable tests", "duration", time.Since(startTime))

		return nil
	})

	g.Go(func() error {
		framework, err := detectedPlatform.DetectFramework()
		if err != nil {
			return fmt.Errorf("failed to detect framework: %w", err)
		}

		startTime := time.Now()
		slog.Info("Discovering local tests...", "framework", framework.Name())
		discoveredTests, err = framework.DiscoverTests()
		slog.Info("Discovered local tests", "duration", time.Since(startTime))

		return err
	})

	if err := g.Wait(); err != nil {
		return err
	}

	discoveredTestsCount := len(discoveredTests)
	skippableTestsCount := 0

	tr.testFiles = make(map[string]int)
	for _, test := range discoveredTests {
		if !skippableTests[test.FQN()] {
			slog.Debug("Test is not skipped", "test", test.FQN(), "sourceFile", test.SuiteSourceFile)
			if test.SuiteSourceFile != "" {
				tr.testFiles[test.SuiteSourceFile]++
			}
		} else {
			skippableTestsCount++
		}
	}
	tr.skippablePercentage = float64(skippableTestsCount) / float64(discoveredTestsCount) * 100.0

	slog.Info("Test optimization data prepared", "skippablePercentage", tr.skippablePercentage, "skippableTestsCount", skippableTestsCount, "discoveredTestsCount", discoveredTestsCount)

	return nil
}
