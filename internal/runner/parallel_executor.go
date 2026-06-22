package runner

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
	"golang.org/x/sync/errgroup"
)

const runModeParallel = "parallel"

// runParallel executes tests across multiple parallel runners on a single node.
func (e testExecutor) runParallel() runExecutionResult {
	report := runExecutionReport{
		Mode: runModeParallel,
	}
	slog.Info("Running tests in parallel mode")

	entries, err := os.ReadDir(constants.TestsSplitDir)
	if err != nil {
		return report.failure(fmt.Errorf("failed to read tests split directory %s: %w", constants.TestsSplitDir, err))
	}

	var g errgroup.Group

	for workerIndex, entry := range entries {
		if entry.IsDir() {
			continue
		}

		report.LocalWorkers++
		splitFilePath := filepath.Join(constants.TestsSplitDir, entry.Name())
		testFiles, err := loadTestBatch(splitFilePath)
		if err != nil {
			return report.failure(fmt.Errorf("failed to read test files from %s: %w", splitFilePath, err))
		}
		report.TestFilesRun += len(testFiles)
		if len(testFiles) == 0 {
			continue
		}

		g.Go(func() error {
			return e.runBatch(testFiles, 0, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return report.failure(fmt.Errorf("failed to run parallel tests: %w", err))
	}
	return report.success()
}
