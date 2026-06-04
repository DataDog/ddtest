package runner

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
)

type testFilePlanner interface {
	DistributeTestFiles(testFiles []string, parallelRunners int) [][]string
}

type testExecutor struct {
	ctx          context.Context
	framework    framework.Framework
	workerEnvMap map[string]string
	planner      testFilePlanner
}

func newTestExecutor(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string, planner testFilePlanner) testExecutor {
	return testExecutor{
		ctx:          ctx,
		framework:    framework,
		workerEnvMap: workerEnvMap,
		planner:      planner,
	}
}

type runExecutionResult struct {
	report runExecutionReport
	err    error
}

func (r runExecutionReport) success() runExecutionResult {
	return runExecutionResult{report: r}
}

func (r runExecutionReport) failure(err error) runExecutionResult {
	return runExecutionResult{report: r, err: err}
}

// runBatch executes an already selected batch of test files in one worker.
func (e testExecutor) runBatch(testFiles []string, nodeIndex int, workerIndex int) error {
	workerEnv := createWorkerEnv(e.workerEnvMap, nodeIndex, workerIndex)

	slog.Info("Running tests in worker", "nodeIndex", nodeIndex, "workerIndex", workerIndex, "testFilesCount", len(testFiles), "workerEnvKeys", workerEnvKeys(workerEnv))
	return e.framework.RunTests(e.ctx, testFiles, workerEnv)
}

// loadTestBatch reads a file containing test file paths (one per line)
// and returns them as a slice of strings.
func loadTestBatch(filePath string) ([]string, error) {
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
