package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"golang.org/x/sync/errgroup"
)

// splitTestFilesIntoGroups splits a slice of test files into n groups
// using simple round-robin distribution
func splitTestFilesIntoGroups(testFiles []string, n int) [][]string {
	if n <= 0 {
		n = 1
	}

	result := make([][]string, n)
	for i := range result {
		result[i] = []string{}
	}

	for i, file := range testFiles {
		groupIndex := i % n
		result[groupIndex] = append(result[groupIndex], file)
	}

	return result
}

// runCINodeTests executes tests for a specific CI node (one split, not the whole tests set)
// It further splits the node's tests among local workers based on ci_node_workers setting.
func runCINodeTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string, ciNode int) error {
	return runCINodeTestsWithWorkers(ctx, framework, workerEnvMap, ciNode, settings.GetCiNodeWorkers())
}

// runCINodeTestsWithWorkers is the internal implementation that accepts ciNodeWorkers as a parameter
// for easier testing.
func runCINodeTestsWithWorkers(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string, ciNode int, ciNodeWorkers int) error {
	runnerFilePath := fmt.Sprintf("%s/runner-%d", constants.TestsSplitDir, ciNode)
	if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
		return fmt.Errorf("runner file for ci-node %d does not exist: %s", ciNode, runnerFilePath)
	}

	testFiles, err := readTestFilesFromFile(runnerFilePath)
	if err != nil {
		return fmt.Errorf("failed to read test files for ci-node %d from %s: %w", ciNode, runnerFilePath, err)
	}

	if len(testFiles) == 0 {
		slog.Info("No tests to run for CI node", "ciNode", ciNode)
		return nil
	}

	// Single worker mode: run all tests with nodeIndex matching ciNode
	if ciNodeWorkers <= 1 {
		slog.Info("Running tests for CI node in single-worker mode", "ciNode", ciNode, "nodeIndex", ciNode, "workerIndex", 0)
		return runTestsWithIndexes(ctx, framework, testFiles, workerEnvMap, ciNode, 0)
	}

	// Multi-worker mode: split tests among local workers
	slog.Info("Running tests for CI node in parallel mode",
		"ciNode", ciNode, "ciNodeWorkers", ciNodeWorkers, "testFilesCount", len(testFiles))

	groups := splitTestFilesIntoGroups(testFiles, ciNodeWorkers)

	var g errgroup.Group
	for workerIndex, groupFiles := range groups {
		if len(groupFiles) == 0 {
			continue
		}

		g.Go(func() error {
			return runTestsWithIndexes(ctx, framework, groupFiles, workerEnvMap, ciNode, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to run tests for ci-node %d: %w", ciNode, err)
	}
	return nil
}

// runTestsWithIndexes runs a set of test files with node and worker indexes for env templating.
func runTestsWithIndexes(ctx context.Context, framework framework.Framework, testFiles []string, workerEnvMap map[string]string, nodeIndex int, workerIndex int) error {
	workerEnv := replaceIndexPlaceholders(workerEnvMap, nodeIndex, workerIndex)

	slog.Info("Running tests in worker", "nodeIndex", nodeIndex, "workerIndex", workerIndex, "testFilesCount", len(testFiles), "workerEnv", workerEnv)
	return framework.RunTests(ctx, testFiles, workerEnv)
}

func replaceIndexPlaceholders(workerEnvMap map[string]string, nodeIndex int, workerIndex int) map[string]string {
	workerEnv := make(map[string]string)
	nodeIndexValue := fmt.Sprintf("%d", nodeIndex)
	workerIndexValue := fmt.Sprintf("%d", workerIndex)
	for key, value := range workerEnvMap {
		value = strings.ReplaceAll(value, constants.NodeIndexPlaceholder, nodeIndexValue)
		value = strings.ReplaceAll(value, constants.WorkerIndexPlaceholder, workerIndexValue)
		workerEnv[key] = value
	}
	return workerEnv
}

// runParallelTests executes tests across multiple parallel runners on a single node
func runParallelTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string) error {
	slog.Info("Running tests in parallel mode")

	entries, err := os.ReadDir(constants.TestsSplitDir)
	if err != nil {
		return fmt.Errorf("failed to read tests split directory %s: %w", constants.TestsSplitDir, err)
	}

	var g errgroup.Group

	for workerIndex, entry := range entries {
		if entry.IsDir() {
			continue
		}

		splitFilePath := filepath.Join(constants.TestsSplitDir, entry.Name())
		g.Go(func() error {
			return runTestsFromFile(ctx, framework, splitFilePath, workerEnvMap, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to run parallel tests: %w", err)
	}
	return nil
}

// runSequentialTests executes tests in a single sequential runner
func runSequentialTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string) error {
	slog.Info("Running all tests in a single process")

	if err := runTestsFromFile(ctx, framework, constants.TestFilesOutputPath, workerEnvMap, 0); err != nil {
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
func runTestsFromFile(ctx context.Context, framework framework.Framework, filePath string, workerEnvMap map[string]string, workerIndex int) error {
	slog.Info("Reading prepared files list", "filePath", filePath, "workerIndex", workerIndex)

	testFiles, err := readTestFilesFromFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read test files from %s: %w", filePath, err)
	}

	if len(testFiles) > 0 {
		workerEnv := replaceIndexPlaceholders(workerEnvMap, 0, workerIndex)

		slog.Info("Running tests in worker", "workerIndex", workerIndex, "testFilesCount", len(testFiles), "workerEnv", workerEnv)
		return framework.RunTests(ctx, testFiles, workerEnv)
	}

	slog.Info("No tests to run", "workerIndex", workerIndex)
	return nil
}
