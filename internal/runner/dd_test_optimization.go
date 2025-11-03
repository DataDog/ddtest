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

	// Detect framework once to avoid duplicate work
	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}
	slog.Info("Framework detected", "framework", framework.Name())

	// Create a cancellable context for test discovery
	discoveryCtx, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()

	var skippableTests map[string]bool
	var discoveredTests []testoptimization.Test
	var discoveredTestFiles []string
	var fullDiscoverySucceeded bool

	g, _ := errgroup.WithContext(ctx)

	// Goroutine 1: Initialize optimization client and check settings
	g.Go(func() error {
		defer tr.optimizationClient.StoreCacheAndExit()

		if err := tr.optimizationClient.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		// Get settings to check if ITR is enabled
		settings := tr.optimizationClient.GetSettings()
		if settings != nil {
			slog.Debug("Repository settings", "itr_enabled", settings.ItrEnabled, "tests_skipping", settings.TestsSkipping)

			if !settings.ItrEnabled || !settings.TestsSkipping {
				slog.Info("ITR or test skipping disabled, cancelling full test discovery")
				cancelDiscovery()
			}
		}

		startTime := time.Now()
		slog.Info("Fetching skippable tests from Datadog...")
		skippableTests = tr.optimizationClient.GetSkippableTests()
		slog.Info("Fetched skippable tests", "duration", time.Since(startTime))

		return nil
	})

	// Goroutine 2: Full test discovery (respects context cancellation)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering local tests...", "framework", framework.Name())
		discoveredTests, err = framework.DiscoverTests(discoveryCtx)
		if err != nil {
			slog.Warn("Full test discovery failed or was cancelled", "error", err)
			return nil // Don't fail the entire process, we have fast discovery as fallback
		}
		fullDiscoverySucceeded = true
		slog.Info("Discovered local tests", "duration", time.Since(startTime), "count", len(discoveredTests))

		return nil
	})

	// Goroutine 3: Fast test discovery (always completes)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering test files (fast)...", "framework", framework.Name())
		discoveredTestFiles, err = framework.DiscoverTestFiles()
		if err != nil {
			return fmt.Errorf("failed to discover test files: %w", err)
		}
		slog.Info("Discovered test files (fast)", "duration", time.Since(startTime), "count", len(discoveredTestFiles))

		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// Process results based on which discovery method succeeded
	if fullDiscoverySucceeded && len(discoveredTests) > 0 {
		slog.Info("Using full test discovery results")
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
	} else {
		slog.Info("Using fast test discovery results (ITR disabled or full discovery failed)")
		tr.testFiles = make(map[string]int)
		for _, testFile := range discoveredTestFiles {
			tr.testFiles[testFile] = 1
		}
		tr.skippablePercentage = 0.0

		slog.Info("Test files prepared from fast discovery", "testFilesCount", len(tr.testFiles))
	}

	return nil
}
