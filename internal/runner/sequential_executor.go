package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
)

// runSequentialTests executes tests in a single sequential runner.
func runSequentialTests(ctx context.Context, framework framework.Framework, workerEnvMap map[string]string) error {
	slog.Info("Running all tests in a single process")

	if err := runTestBatchFromFile(ctx, framework, constants.TestFilesOutputPath, workerEnvMap, 0, 0); err != nil {
		return fmt.Errorf("failed to run tests: %w", err)
	}
	return nil
}
