package runner

import (
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sync/errgroup"
)

// runCINode executes tests for a specific CI node (one split, not the whole tests set).
// It further splits the node's tests among local workers based on ci_node_workers setting.
func (e testExecutor) runCINode(ciNode int, ciNodeWorkers int, testFileWeights map[string]int) runExecutionResult {
	report := newCINodeExecutionReport(ciNode, ciNodeWorkers)
	testFiles, err := loadCINodeTestFiles(ciNode)
	if err != nil {
		return report.failure(err)
	}
	report.TestFilesRun = len(testFiles)

	if report.LocalWorkers <= 1 {
		err = e.runCINodeSingleWorker(ciNode, testFiles)
	} else {
		err = e.runCINodeWorkers(ciNode, report.LocalWorkers, testFiles, testFileWeights)
	}
	if err != nil {
		return report.failure(err)
	}
	return report.success()
}

func newCINodeExecutionReport(ciNode int, ciNodeWorkers int) runExecutionReport {
	if ciNodeWorkers <= 0 {
		ciNodeWorkers = 1
	}

	return runExecutionReport{
		Mode:         runModeCINode,
		CINode:       ciNode,
		LocalWorkers: ciNodeWorkers,
	}
}

func loadCINodeTestFiles(ciNode int) ([]string, error) {
	runnerFilePath := runnerSplitPath(ciNode)
	testFiles, err := loadTestBatch(runnerFilePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("runner file for ci-node %d does not exist: %s", ciNode, runnerFilePath)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read test files for ci-node %d from %s: %w", ciNode, runnerFilePath, err)
	}
	return testFiles, nil
}

func (e testExecutor) runCINodeSingleWorker(ciNode int, testFiles []string) error {
	slog.Info("Running tests for CI node in single-worker mode", "ciNode", ciNode, "nodeIndex", ciNode, "workerIndex", 0)
	if len(testFiles) == 0 {
		slog.Info("No tests to run", "nodeIndex", ciNode, "workerIndex", 0)
		return nil
	}
	return e.runBatch(testFiles, ciNode, 0)
}

func (e testExecutor) runCINodeWorkers(ciNode int, ciNodeWorkers int, testFiles []string, testFileWeights map[string]int) error {
	if len(testFiles) == 0 {
		slog.Info("No tests to run for CI node", "ciNode", ciNode)
		return nil
	}

	slog.Info("Running tests for CI node in parallel mode",
		"ciNode", ciNode, "ciNodeWorkers", ciNodeWorkers, "testFilesCount", len(testFiles))

	if testFileWeights == nil {
		testFileWeights = map[string]int{}
	}
	groups := subsplitTestsBetweenWorkers(testFiles, ciNodeWorkers, testFileWeights)
	return e.runCINodeWorkerGroups(ciNode, groups)
}

func (e testExecutor) runCINodeWorkerGroups(ciNode int, groups [][]string) error {
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
			return e.runBatch(groupFiles, ciNode, workerIndex)
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
