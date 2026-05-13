package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"golang.org/x/sync/errgroup"
)

// runCINodeTests executes tests for a specific CI node (one split, not the whole tests set).
// It further splits the node's tests among local workers based on ci_node_workers setting.
func runCINodeTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string, ciNode int, ciNodeWorkers int, testFileWeights map[string]int) error {
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

	if testFileWeights == nil {
		testFileWeights = map[string]int{}
	}
	groups := subsplitTestsBetweenWorkers(testFiles, ciNodeWorkers, testFileWeights)

	var g errgroup.Group
	for workerIndex, groupFiles := range groups {
		if len(groupFiles) == 0 {
			continue
		}

		slog.Debug("Assigned test files to CI node worker",
			"ciNode", ciNode,
			"workerIndex", workerIndex,
			"testFiles", groupFiles)

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
// using the same weighted distribution algorithm used for CI node splits.
func subsplitTestsBetweenWorkers(testFiles []string, n int, testFileWeights map[string]int) [][]string {
	if n <= 0 {
		n = 1
	}

	nodeTestFiles := make(map[string]int, len(testFiles))
	for _, testFile := range testFiles {
		if cachedWeight, ok := testFileWeights[testFile]; ok && cachedWeight > 0 {
			nodeTestFiles[testFile] = cachedWeight
		} else {
			nodeTestFiles[testFile] = defaultTestFileWeight
		}
	}

	return DistributeTestFiles(nodeTestFiles, n)
}
