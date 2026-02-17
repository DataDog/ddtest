package runner

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

func (tr *TestRunner) PrepareTestOptimization(ctx context.Context) error {
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}

	// Get platform-detected tags first
	tags, err := detectedPlatform.CreateTagsMap()
	if err != nil {
		return fmt.Errorf("failed to create platform tags: %w", err)
	}

	// Check if runtime tags override is provided and merge onto detected tags
	overrideTags, err := settings.GetRuntimeTagsMap()
	if err != nil {
		return fmt.Errorf("failed to parse runtime tags override: %w", err)
	}

	if overrideTags != nil {
		// Merge override tags onto detected tags (override values take precedence)
		maps.Copy(tags, overrideTags)
		slog.Info("Merged runtime tags override from --runtime-tags", "overrideTags", overrideTags, "mergedTags", tags)
	} else {
		slog.Info("Preparing test optimization data", "runtimeTags", tags, "platform", detectedPlatform.Name())
	}

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
	var fullDiscoveryErr error
	var fastDiscoveryErr error

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
		res, discErr := framework.DiscoverTests(discoveryCtx)
		if discErr != nil {
			fullDiscoveryErr = discErr
			slog.Warn("Full test discovery failed or was cancelled", "error", discErr)
			return nil // Don't fail the entire process, we have fast discovery as fallback
		}
		discoveredTests = res
		fullDiscoverySucceeded = true
		slog.Info("Discovered local tests", "duration", time.Since(startTime), "count", len(discoveredTests))

		return nil
	})

	// Goroutine 3: Fast test discovery (always completes)
	g.Go(func() error {
		startTime := time.Now()
		slog.Info("Discovering test files (fast)...", "framework", framework.Name())
		res, discErr := framework.DiscoverTestFiles()
		if discErr != nil {
			fastDiscoveryErr = discErr
			slog.Warn("Fast test discovery failed", "error", discErr)
			return nil // Don't fail the entire process if full discovery succeeded
		}
		discoveredTestFiles = res
		slog.Info("Discovered test files (fast)", "duration", time.Since(startTime), "count", len(discoveredTestFiles))

		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// If both discovery methods failed, return an error
	if fullDiscoveryErr != nil && fastDiscoveryErr != nil {
		return fmt.Errorf("both test discovery methods failed - full: %w, fast: %v", fullDiscoveryErr, fastDiscoveryErr)
	}

	// Process results based on which discovery method succeeded
	if fullDiscoverySucceeded && len(discoveredTests) > 0 {
		slog.Info("Using full test discovery results")
		discoveredTestsCount := len(discoveredTests)
		skippableTestsCount := 0

		// Compute subdirectory prefix once for all paths.
		// When running from a monorepo subdirectory (e.g., "cd core && ddtest plan"),
		// full discovery may return repo-root-relative paths (e.g., "core/spec/...").
		// We normalize them to CWD-relative paths so workers can find the files.
		subdirPrefix := getCwdSubdirPrefix()
		if subdirPrefix != "" {
			slog.Info("Running from subdirectory, will normalize repo-root-relative paths", "subdirPrefix", subdirPrefix)
		}

		tr.testFiles = make(map[string]int)
		for _, test := range discoveredTests {
			if !skippableTests[test.FQN()] {
				slog.Debug("Test is not skipped", "test", test.FQN(), "sourceFile", test.SuiteSourceFile)
				if test.SuiteSourceFile != "" {
					// Normalize repo-root-relative path to CWD-relative path.
					// No-op when running from repo root or when path doesn't match subdir prefix.
					normalizedPath := normalizeTestFilePathWithPrefix(test.SuiteSourceFile, subdirPrefix)
					// increment the number of tests in the file
					// it should track the test suite's duration here in the future
					tr.testFiles[normalizedPath]++
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
			// As we don't know what tests are there we just set the number of tests to 1
			//
			// When we'll have data for "known test suites" with their durations we will
			// be able to use test suites durations here
			tr.testFiles[testFile] = 1
		}
		tr.skippablePercentage = 0.0

		slog.Info("Test files prepared from fast discovery", "testFilesCount", len(tr.testFiles))
	}

	return nil
}
