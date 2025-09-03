package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/datadog-test-runner/internal/framework"
	"golang.org/x/sync/errgroup"
)

const NodeIndexPlaceholder = "{{nodeIndex}}"

// runCINodeTests executes tests for a specific CI node (one split, not the whole tests set)
func runCINodeTests(framework framework.Framework, workerEnvMap map[string]string, ciNode int) error {
	runnerFilePath := fmt.Sprintf("%s/runner-%d", TestsSplitDir, ciNode)
	if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
		return fmt.Errorf("runner file for ci-node %d does not exist: %s", ciNode, runnerFilePath)
	}

	slog.Debug("Running tests for specific CI node", "ciNode", ciNode, "filePath", runnerFilePath)
	if err := runTestsFromFile(framework, runnerFilePath, workerEnvMap, ciNode); err != nil {
		return fmt.Errorf("failed to run tests for ci-node %d: %w", ciNode, err)
	}
	return nil
}

// runParallelTests executes tests across multiple parallel runners on a single node
func runParallelTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string) error {
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
			return runTestsFromFile(framework, splitFilePath, workerEnvMap, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to run parallel tests: %w", err)
	}
	return nil
}

// runSequentialTests executes tests in a single sequential runner
func runSequentialTests(framework framework.Framework, workerEnvMap map[string]string) error {
	if err := runTestsFromFile(framework, TestFilesOutputPath, workerEnvMap, 0); err != nil {
		return fmt.Errorf("failed to run tests: %w", err)
	}
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
func runTestsFromFile(framework framework.Framework, filePath string, workerEnvMap map[string]string, workerIndex int) error {
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
