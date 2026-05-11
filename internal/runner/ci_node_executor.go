package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
	"golang.org/x/sync/errgroup"
)

// runCINodeTests executes tests for a specific CI node (one split, not the whole tests set).
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

	// Single worker mode: run all tests with nodeIndex matching ciNode.
	if ciNodeWorkers <= 1 {
		slog.Info("Running tests for CI node in single-worker mode", "ciNode", ciNode, "nodeIndex", ciNode, "workerIndex", 0)
		return runTestBatchFromFile(ctx, framework, runnerFilePath, workerEnvMap, ciNode, 0)
	}

	testFiles, err := loadTestBatch(runnerFilePath)
	if err != nil {
		return fmt.Errorf("failed to read test files for ci-node %d from %s: %w", ciNode, runnerFilePath, err)
	}

	if len(testFiles) == 0 {
		slog.Info("No tests to run for CI node", "ciNode", ciNode)
		return nil
	}

	// Multi-worker mode: split tests among local workers.
	slog.Info("Running tests for CI node in parallel mode",
		"ciNode", ciNode, "ciNodeWorkers", ciNodeWorkers, "testFilesCount", len(testFiles))

	groups := subsplitTestsBetweenWorkers(testFiles, ciNodeWorkers)

	var g errgroup.Group
	for workerIndex, groupFiles := range groups {
		if len(groupFiles) == 0 {
			continue
		}

		g.Go(func() error {
			return runTestBatch(ctx, framework, groupFiles, workerEnvMap, ciNode, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to run tests for ci-node %d: %w", ciNode, err)
	}
	return nil
}

// subsplitTestsBetweenWorkers splits a CI node's test files among local workers
// using simple round-robin distribution.
func subsplitTestsBetweenWorkers(testFiles []string, n int) [][]string {
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
