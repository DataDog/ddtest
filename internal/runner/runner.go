package runner

import (
	"fmt"
	"log/slog"

	"github.com/DataDog/datadog-test-runner/internal/platform"
	"github.com/DataDog/datadog-test-runner/internal/testoptimization"
)

type Runner interface {
	PrintTestFiles() error
	GetTestFiles() (map[string]bool, error)
}

type TestRunner struct{}

func New() *TestRunner {
	return &TestRunner{}
}

func (tr *TestRunner) GetTestFiles() (map[string]bool, error) {
	detectedPlatform, err := platform.DetectPlatform()
	if err != nil {
		return nil, fmt.Errorf("failed to detect platform: %w", err)
	}

	tags := detectedPlatform.CreateTagsMap()

	client := testoptimization.NewDatadogClient()
	if err := client.Initialize(tags); err != nil {
		return nil, fmt.Errorf("failed to initialize optimization client: %w", err)
	}

	ddSkippedTests := client.GetSkippableTests()
	client.Shutdown()

	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return nil, fmt.Errorf("failed to detect framework: %w", err)
	}

	tests, err := framework.DiscoverTests()
	if err != nil {
		return nil, fmt.Errorf("failed to discover tests: %w", err)
	}

	testFiles := make(map[string]bool)
	for _, test := range tests {
		if !ddSkippedTests[test.FQN] {
			slog.Debug("Test is not skipped", "test", test.FQN, "sourceFile", test.SourceFile)
			testFiles[test.SourceFile] = true
		}
	}

	return testFiles, nil
}

func (tr *TestRunner) PrintTestFiles() error {
	testFiles, err := tr.GetTestFiles()
	if err != nil {
		return err
	}

	for testFile := range testFiles {
		fmt.Print(testFile + " ")
	}
	fmt.Println()

	return nil
}
