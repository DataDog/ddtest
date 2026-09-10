package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
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
	return e.runBatchWithTIASkippedTestSuites(testFiles, nodeIndex, workerIndex, "")
}

func (e testExecutor) runBatchWithTIASkippedTestSuites(testFiles []string, nodeIndex int, workerIndex int, artifactPath string) error {
	workerEnv := createWorkerEnv(e.workerEnvMap, nodeIndex, workerIndex)
	if artifactPath != "" {
		workerEnv[constants.TestOptimizationTIASkippedTestSuitesFileEnvVar] = artifactPath
	}

	slog.Info("Running tests in worker", "nodeIndex", nodeIndex, "workerIndex", workerIndex, "testFilesCount", len(testFiles), "workerEnvKeys", workerEnvKeys(workerEnv))
	return e.framework.RunTests(e.ctx, testFiles, workerEnv)
}

func tiaSkippedTestSuitesFileForRunner(runnerIndex int) string {
	return tiaSkippedTestSuitesFile(filepath.Join(
		constants.TIASkippedTestSuitesDir,
		fmt.Sprintf("runner-%d.json", runnerIndex),
	))
}

func tiaSkippedTestSuitesFileForSplit(splitFileName string) string {
	return tiaSkippedTestSuitesFile(filepath.Join(constants.TIASkippedTestSuitesDir, splitFileName+".json"))
}

func tiaSkippedTestSuitesFile(artifactPath string) string {
	data, err := os.ReadFile(artifactPath)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		slog.Warn("Failed to read TIA-skipped test suites artifact", "path", artifactPath, "error", err)
		return ""
	}

	var artifact struct {
		Version    int      `json:"version"`
		TestSuites []string `json:"test_suites"`
	}
	if err := json.Unmarshal(data, &artifact); err != nil {
		slog.Warn("Failed to parse TIA-skipped test suites artifact", "path", artifactPath, "error", err)
		return ""
	}
	if artifact.Version != 1 || len(artifact.TestSuites) == 0 {
		return ""
	}

	absolutePath, err := filepath.Abs(artifactPath)
	if err != nil {
		return artifactPath
	}
	return absolutePath
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
