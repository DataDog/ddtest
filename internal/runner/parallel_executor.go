package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"golang.org/x/sync/errgroup"
)

// runParallelTests executes tests across multiple parallel runners on a single node.
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
			return runTestBatchFromFile(ctx, framework, splitFilePath, workerEnvMap, 0, workerIndex)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed to run parallel tests: %w", err)
	}
	return nil
}
