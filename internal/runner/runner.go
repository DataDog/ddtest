package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	ciUtils "github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
)

type Runner interface {
	Run(ctx context.Context) error
}

type Planner interface {
	Plan(ctx context.Context) error
	LoadPlan() (planner.PlanInfo, error)
	DistributeTestFiles(testFiles []string, parallelRunners int) [][]string
}

type TestRunner struct {
	platformDetector platform.PlatformDetector
	planner          Planner
	reportWriter     io.Writer
}

func New() *TestRunner {
	return NewWithDependencies(platform.NewPlatformDetector(), planner.New())
}

func NewWithDependencies(
	platformDetector platform.PlatformDetector,
	testPlanner Planner,
) *TestRunner {
	runner := newTestRunnerWithDefaults()
	runner.platformDetector = platformDetector
	runner.planner = testPlanner
	return runner
}

func newTestRunnerWithDefaults() *TestRunner {
	return &TestRunner{
		planner:      planner.New(),
		reportWriter: os.Stderr,
	}
}

func (tr *TestRunner) Run(ctx context.Context) error {
	// Check if parallel runners output file exists
	if _, err := os.Stat(constants.ParallelRunnersOutputPath); os.IsNotExist(err) {
		slog.Info("Test optimization planning data not found, running planning phase...")

		// Run Setup if the file doesn't exist
		if err := tr.planner.Plan(ctx); err != nil {
			return fmt.Errorf("failed to run planning phase: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to check parallel runners count at %s: %w", constants.ParallelRunnersOutputPath, err)
	}

	planInfo, err := tr.planner.LoadPlan()
	if err != nil {
		slog.Error("Test optimization plan is not available", "error", err)
		return fmt.Errorf("test optimization plan is not available: %w", err)
	}

	parallelRunners, err := readParallelRunnersCount()
	if err != nil {
		return err
	}
	slog.Info("Got parallel runners count", "parallelRunners", parallelRunners)

	// Parse worker environment variables if provided in settings
	workerEnvMap := settings.GetWorkerEnvMap()
	slog.Info("Worker environment variables", "workerEnvKeys", workerEnvKeys(workerEnvMap))

	// Detect platform and framework
	detectedPlatform, err := tr.platformDetector.DetectPlatform()
	if err != nil {
		return fmt.Errorf("failed to detect platform: %w", err)
	}
	slog.Info("Platform detected", "platform", detectedPlatform.Name())

	framework, err := detectedPlatform.DetectFramework()
	if err != nil {
		return fmt.Errorf("failed to detect framework: %w", err)
	}
	slog.Info("Framework detected", "framework", framework.Name())
	runInfo := runmetadata.New(ciUtils.GetCITags())
	if planInfo.IsZero() {
		planInfo = planner.NewPlanInfo(nil, detectedPlatform.Name(), framework.Name())
	}

	ciNode := settings.GetCiNode()
	startTime := time.Now()
	executor := newTestExecutor(ctx, framework, workerEnvMap, tr.planner)
	var executionResult runExecutionResult
	if ciNode >= 0 {
		executionResult = executor.runCINode(ciNode, settings.GetCiNodeWorkers())
	} else if parallelRunners > 1 {
		executionResult = executor.runParallel()
	} else {
		executionResult = executor.runSequential()
	}

	if settings.GetReportEnabled() {
		printRunReport(tr.reportWriter, runReport{
			RunInfo:   runInfo,
			PlanInfo:  planInfo,
			Execution: executionResult.report,
			Duration:  time.Since(startTime),
			Err:       executionResult.err,
		})
	}
	return executionResult.err
}

func readParallelRunnersCount() (int, error) {
	runnersData, err := os.ReadFile(constants.ParallelRunnersOutputPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read parallel runners count from %s: %w", constants.ParallelRunnersOutputPath, err)
	}
	runnersString := strings.TrimSpace(string(runnersData))

	parallelRunners := 0
	if _, err := fmt.Sscanf(runnersString, "%d", &parallelRunners); err != nil {
		return 0, fmt.Errorf("failed to parse parallel runners count from %s: %w", runnersString, err)
	}

	return parallelRunners, nil
}
