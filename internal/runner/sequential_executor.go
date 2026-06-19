package runner

import (
	"fmt"
	"log/slog"

	"github.com/DataDog/ddtest/internal/constants"
)

const runModeSequential = "sequential"

// runSequential executes tests in a single sequential runner.
func (e testExecutor) runSequential() runExecutionResult {
	report := runExecutionReport{
		Mode:         runModeSequential,
		LocalWorkers: 1,
	}
	slog.Info("Running all tests in a single process")

	testFiles, err := loadTestBatch(constants.TestFilesOutputPath)
	if err != nil {
		return report.failure(fmt.Errorf("failed to read test files from %s: %w", constants.TestFilesOutputPath, err))
	}
	report.TestFilesRun = len(testFiles)

	if len(testFiles) == 0 {
		slog.Info("No tests to run", "nodeIndex", 0, "workerIndex", 0)
		return report.success()
	}

	if err := e.runBatch(testFiles, 0, 0); err != nil {
		return report.failure(fmt.Errorf("failed to run tests: %w", err))
	}
	return report.success()
}
