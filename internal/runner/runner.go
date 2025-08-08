package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
	"golang.org/x/sync/errgroup"
)

type Runner interface {
	PrintTestFiles(ctx context.Context) error
	GetTestFiles(ctx context.Context) (map[string]bool, error)
}

type TestRunner struct{}

func New() *TestRunner {
	return &TestRunner{}
}

func (tr *TestRunner) GetTestFiles(ctx context.Context) (map[string]bool, error) {
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return nil, fmt.Errorf("failed to detect platform: %w", err)
	}

	tags, err := detectedPlatform.CreateTagsMap()
	if err != nil {
		return nil, fmt.Errorf("failed to create platform tags: %w", err)
	}

	var skippableTests map[string]bool
	var discoveredTests []testoptimization.Test

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		client := testoptimization.NewDatadogClient()
		defer client.Shutdown()

		if err := client.Initialize(tags); err != nil {
			return fmt.Errorf("failed to initialize optimization client: %w", err)
		}

		skippableTests = client.GetSkippableTests()
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
		return nil, err
	}

	testFiles := make(map[string]bool)
	for _, test := range discoveredTests {
		if !skippableTests[test.FQN] {
			slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SourceFile)
			testFiles[test.SourceFile] = true
		}
	}

	return testFiles, nil
}

func (tr *TestRunner) PrintTestFiles(ctx context.Context) error {
	testFiles, err := tr.GetTestFiles(ctx)
	if err != nil {
		return err
	}

	for testFile := range testFiles {
		fmt.Print(testFile + " ")
	}
	fmt.Println()

	return nil
}
