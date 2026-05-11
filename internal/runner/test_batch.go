package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
)

// runTestBatchFromFile reads a test batch from the given file path and runs it using the framework.
func runTestBatchFromFile(ctx context.Context, framework framework.Framework, filePath string, workerEnvMap map[string]string, nodeIndex int, workerIndex int) error {
	slog.Info("Reading prepared files list", "filePath", filePath, "nodeIndex", nodeIndex, "workerIndex", workerIndex)

	testFiles, err := loadTestBatch(filePath)
	if err != nil {
		return fmt.Errorf("failed to read test files from %s: %w", filePath, err)
	}

	if len(testFiles) > 0 {
		return runTestBatch(ctx, framework, testFiles, workerEnvMap, nodeIndex, workerIndex)
	}

	slog.Info("No tests to run", "nodeIndex", nodeIndex, "workerIndex", workerIndex)
	return nil
}

// runTestBatch executes an already selected batch of test files in one worker.
func runTestBatch(ctx context.Context, framework framework.Framework, testFiles []string, workerEnvMap map[string]string, nodeIndex int, workerIndex int) error {
	workerEnv := createWorkerEnv(workerEnvMap, nodeIndex, workerIndex)

	slog.Info("Running tests in worker", "nodeIndex", nodeIndex, "workerIndex", workerIndex, "testFilesCount", len(testFiles), "workerEnv", workerEnv)
	return framework.RunTests(ctx, testFiles, workerEnv)
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
