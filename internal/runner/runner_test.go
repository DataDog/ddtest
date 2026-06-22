package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/planner"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

type fakePlanner struct {
	planCalls             int
	loadCalls             int
	distributeCalls       int
	planFunc              func(context.Context) error
	distributeFunc        func([]string, int) [][]string
	plan                  planner.PlanInfo
	loadErr               error
	distributedTestFiles  [][]string
	distributedWorkerNums []int
}

func (f *fakePlanner) Plan(ctx context.Context) error {
	f.planCalls++
	if f.planFunc != nil {
		return f.planFunc(ctx)
	}
	return nil
}

func (f *fakePlanner) LoadPlan() (planner.PlanInfo, error) {
	f.loadCalls++
	return f.plan, f.loadErr
}

func (f *fakePlanner) DistributeTestFiles(testFiles []string, parallelRunners int) [][]string {
	f.distributeCalls++
	f.distributedTestFiles = append(f.distributedTestFiles, slices.Clone(testFiles))
	f.distributedWorkerNums = append(f.distributedWorkerNums, parallelRunners)
	if f.distributeFunc != nil {
		return f.distributeFunc(testFiles, parallelRunners)
	}
	return distributeRoundRobin(testFiles, parallelRunners)
}

func TestNew(t *testing.T) {
	if runner := New(); runner == nil {
		t.Fatal("New() returned nil")
	}
}

func TestTestRunner_Run_PlansThroughPublicClientWhenArtifactsMissing(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)

	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}
	testPlanner := &fakePlanner{
		planFunc: func(ctx context.Context) error {
			writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
			writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/a_spec.rb\n")
			return nil
		},
		plan: planner.PlanInfo{
			Platform:  "ruby",
			Framework: "rspec",
		},
	}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if testPlanner.planCalls != 1 {
		t.Fatalf("expected planner Plan() to be called once, got %d", testPlanner.planCalls)
	}
	if testPlanner.loadCalls != 1 {
		t.Fatalf("expected LoadPlan() to be called once, got %d", testPlanner.loadCalls)
	}
	if testPlanner.distributeCalls != 0 {
		t.Fatalf("expected DistributeTestFiles() not to be called outside CI-node worker mode, got %d", testPlanner.distributeCalls)
	}
	calls := framework.GetRunTestsCalls()
	if len(calls) != 1 || !slices.Equal(calls[0].TestFiles, []string{"spec/a_spec.rb"}) {
		t.Fatalf("expected runner to execute planned test file, got %+v", calls)
	}
}

func TestTestRunner_Run_UsesExistingArtifactsWithoutPlanning(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/existing_spec.rb\n")

	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}
	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if testPlanner.planCalls != 0 {
		t.Fatalf("expected planner Plan() not to be called, got %d calls", testPlanner.planCalls)
	}
	if testPlanner.loadCalls != 1 {
		t.Fatalf("expected LoadPlan() to be called once, got %d", testPlanner.loadCalls)
	}
	if testPlanner.distributeCalls != 0 {
		t.Fatalf("expected DistributeTestFiles() not to be called outside CI-node worker mode, got %d", testPlanner.distributeCalls)
	}
	calls := framework.GetRunTestsCalls()
	if len(calls) != 1 || !slices.Equal(calls[0].TestFiles, []string{"spec/existing_spec.rb"}) {
		t.Fatalf("expected runner to execute existing artifact test file, got %+v", calls)
	}
}

func TestTestRunner_Run_ReturnsErrorWhenPlanUnavailable(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/existing_spec.rb\n")
	logs := captureLogs(t)

	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}
	loadErr := errors.New("plan cache missing")
	testPlanner := &fakePlanner{loadErr: loadErr}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)

	err := runner.Run(context.Background())
	if !errors.Is(err, loadErr) {
		t.Fatalf("expected Run() to return LoadPlan() error, got %v", err)
	}
	if testPlanner.loadCalls != 1 {
		t.Fatalf("expected LoadPlan() to be called once, got %d", testPlanner.loadCalls)
	}
	if framework.GetRunTestsCallsCount() != 0 {
		t.Fatalf("expected runner not to execute tests when plan is unavailable, got %d calls", framework.GetRunTestsCallsCount())
	}
	if !strings.Contains(logs.String(), "level=ERROR") ||
		!strings.Contains(logs.String(), "Test optimization plan is not available") {
		t.Fatalf("expected error log for unavailable plan, got logs: %s", logs.String())
	}
}

func TestTestRunner_Run_ReturnsPlanErrorWhenArtifactsAreMissing(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)

	planErr := errors.New("planning failed")
	testPlanner := &fakePlanner{
		planFunc: func(ctx context.Context) error {
			return planErr
		},
	}
	runner := NewWithDependencies(&MockPlatformDetector{}, testPlanner)

	err := runner.Run(context.Background())
	if !errors.Is(err, planErr) {
		t.Fatalf("expected Run() to return planning error, got %v", err)
	}
	if testPlanner.planCalls != 1 {
		t.Fatalf("expected Plan() to be called once, got %d", testPlanner.planCalls)
	}
	if testPlanner.loadCalls != 0 {
		t.Fatalf("expected LoadPlan() not to be called after planning failure, got %d", testPlanner.loadCalls)
	}
}

func TestTestRunner_Run_ReturnsStatErrorForBrokenRunnerArtifactsPath(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)

	if err := os.WriteFile(constants.PlanDirectory, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("failed to create broken plan path: %v", err)
	}
	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{}, testPlanner)

	err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to check parallel runners count") {
		t.Fatalf("Run() error = %v, want stat failure", err)
	}
	if testPlanner.planCalls != 0 {
		t.Fatalf("expected Plan() not to be called for stat failure, got %d", testPlanner.planCalls)
	}
}

func TestTestRunner_Run_ReturnsErrorForInvalidParallelRunnerCount(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "many")

	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{}, testPlanner)

	err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to parse parallel runners count") {
		t.Fatalf("Run() error = %v, want parse failure", err)
	}
}

func TestTestRunner_Run_ReturnsPlatformDetectionError(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")

	detectErr := errors.New("no platform")
	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{Err: detectErr}, testPlanner)

	err := runner.Run(context.Background())
	if !errors.Is(err, detectErr) {
		t.Fatalf("expected Run() to return platform detection error, got %v", err)
	}
}

func TestTestRunner_Run_ReturnsFrameworkDetectionError(t *testing.T) {
	withRunnerTestSettings(t)
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")

	frameworkErr := errors.New("no framework")
	platform := &MockPlatform{PlatformName: "ruby", FrameworkErr: frameworkErr}
	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)

	err := runner.Run(context.Background())
	if !errors.Is(err, frameworkErr) {
		t.Fatalf("expected Run() to return framework detection error, got %v", err)
	}
}

func TestTestRunner_Run_WritesReportWhenEnabled(t *testing.T) {
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "true")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "-1")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS", "1")
	viper.Reset()
	settings.Init()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/report_spec.rb\n")

	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}
	testPlanner := &fakePlanner{
		plan: planner.PlanInfo{
			Platform:  "ruby",
			Framework: "rspec",
		},
	}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)
	var report strings.Builder
	runner.reportWriter = &report

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if report.Len() == 0 {
		t.Fatal("expected run report output")
	}
}

func TestRunSequentialMissingTestFile(t *testing.T) {
	chdirTemp(t)

	executor := newTestExecutor(context.Background(), &MockFramework{}, nil, roundRobinTestPlanner{})
	result := executor.runSequential()

	if result.err == nil || !strings.Contains(result.err.Error(), "failed to read test files") {
		t.Fatalf("runSequential() error = %v, want missing test file error", result.err)
	}
}

func TestRunSequentialWithEmptyTestFile(t *testing.T) {
	chdirTemp(t)
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "\n")
	framework := &MockFramework{}

	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})
	result := executor.runSequential()

	if result.err != nil {
		t.Fatalf("runSequential() returned error: %v", result.err)
	}
	if result.report.TestFilesRun != 0 {
		t.Fatalf("expected no test files to run, got %d", result.report.TestFilesRun)
	}
	if framework.GetRunTestsCallsCount() != 0 {
		t.Fatalf("expected no framework calls, got %d", framework.GetRunTestsCallsCount())
	}
}

func TestRunSequentialReturnsWorkerError(t *testing.T) {
	chdirTemp(t)
	writeRunnerTestFile(t, constants.TestFilesOutputPath, "spec/failing_spec.rb\n")
	framework := &MockFramework{Err: errors.New("worker failed")}

	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})
	result := executor.runSequential()

	if result.err == nil || !strings.Contains(result.err.Error(), "failed to run tests") {
		t.Fatalf("runSequential() error = %v, want worker error", result.err)
	}
}

func TestRunParallelMissingSplitDirectory(t *testing.T) {
	chdirTemp(t)

	executor := newTestExecutor(context.Background(), &MockFramework{}, nil, roundRobinTestPlanner{})
	result := executor.runParallel()

	if result.err == nil || !strings.Contains(result.err.Error(), "failed to read tests split directory") {
		t.Fatalf("runParallel() error = %v, want missing split directory error", result.err)
	}
}

func TestRunParallelSkipsDirectoriesAndEmptyBatches(t *testing.T) {
	chdirTemp(t)
	if err := os.MkdirAll(filepath.Join(constants.TestsSplitDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create split subdir: %v", err)
	}
	writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-0"), "\n")
	framework := &MockFramework{}

	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})
	result := executor.runParallel()

	if result.err != nil {
		t.Fatalf("runParallel() returned error: %v", result.err)
	}
	if result.report.LocalWorkers != 1 {
		t.Fatalf("expected one file worker, got %d", result.report.LocalWorkers)
	}
	if framework.GetRunTestsCallsCount() != 0 {
		t.Fatalf("expected no framework calls for empty batch, got %d", framework.GetRunTestsCallsCount())
	}
}

func TestRunParallelReturnsWorkerError(t *testing.T) {
	chdirTemp(t)
	writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-0"), "spec/failing_spec.rb\n")
	framework := &MockFramework{Err: errors.New("worker failed")}

	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})
	result := executor.runParallel()

	if result.err == nil || !strings.Contains(result.err.Error(), "failed to run parallel tests") {
		t.Fatalf("runParallel() error = %v, want worker error", result.err)
	}
}

func TestNewCINodeExecutionReportDefaultsWorkers(t *testing.T) {
	report := newCINodeExecutionReport(3, 0)

	if report.LocalWorkers != 1 {
		t.Fatalf("expected ci-node workers to default to 1, got %d", report.LocalWorkers)
	}
	if report.CINode != 3 {
		t.Fatalf("expected ci-node 3, got %d", report.CINode)
	}
}

func TestLoadCINodeTestFilesMissingFile(t *testing.T) {
	chdirTemp(t)

	_, err := loadCINodeTestFiles(7)
	if err == nil || !strings.Contains(err.Error(), "runner file for ci-node 7 does not exist") {
		t.Fatalf("loadCINodeTestFiles() error = %v, want missing runner file", err)
	}
}

func TestRunCINodeSingleWorkerWithEmptyBatch(t *testing.T) {
	framework := &MockFramework{}
	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})

	err := executor.runCINodeSingleWorker(0, nil)
	if err != nil {
		t.Fatalf("runCINodeSingleWorker() returned error: %v", err)
	}
	if framework.GetRunTestsCallsCount() != 0 {
		t.Fatalf("expected no framework calls for empty batch, got %d", framework.GetRunTestsCallsCount())
	}
}

func TestRunCINodeWorkersWithEmptyBatch(t *testing.T) {
	framework := &MockFramework{}
	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})

	err := executor.runCINodeWorkers(0, 2, nil)
	if err != nil {
		t.Fatalf("runCINodeWorkers() returned error: %v", err)
	}
	if framework.GetRunTestsCallsCount() != 0 {
		t.Fatalf("expected no framework calls for empty batch, got %d", framework.GetRunTestsCallsCount())
	}
}

func TestRunCINodeWorkerGroupsSkipsEmptyGroups(t *testing.T) {
	framework := &MockFramework{}
	executor := newTestExecutor(context.Background(), framework, nil, roundRobinTestPlanner{})

	err := executor.runCINodeWorkerGroups(0, [][]string{{}, {"spec/one_spec.rb"}})
	if err != nil {
		t.Fatalf("runCINodeWorkerGroups() returned error: %v", err)
	}
	calls := framework.GetRunTestsCalls()
	if len(calls) != 1 || !slices.Equal(calls[0].TestFiles, []string{"spec/one_spec.rb"}) {
		t.Fatalf("expected one non-empty worker call, got %+v", calls)
	}
}

func TestTestRunner_Run_CINodeWorkersRunWithoutLoadedWeights(t *testing.T) {
	withRunnerTestSettings(t)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "0")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS", "2")
	viper.Reset()
	settings.Init()
	chdirTemp(t)
	writeRunnerTestFile(t, constants.ParallelRunnersOutputPath, "1")
	writeRunnerTestFile(t, filepath.Join(constants.TestsSplitDir, "runner-0"), strings.Join([]string{
		"spec/fast_a_spec.rb",
		"spec/fast_b_spec.rb",
		"spec/slow_spec.rb",
	}, "\n")+"\n")

	framework := &MockFramework{FrameworkName: "rspec"}
	platform := &MockPlatform{PlatformName: "ruby", Framework: framework}
	testPlanner := &fakePlanner{}
	runner := NewWithDependencies(&MockPlatformDetector{Platform: platform}, testPlanner)

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	calls := framework.GetRunTestsCalls()
	if len(calls) != 2 {
		t.Fatalf("expected two CI-node worker calls, got %+v", calls)
	}
	if testPlanner.loadCalls != 1 {
		t.Fatalf("expected LoadPlan() to be called once, got %d", testPlanner.loadCalls)
	}
	if testPlanner.distributeCalls != 1 {
		t.Fatalf("expected DistributeTestFiles() to be called once, got %d", testPlanner.distributeCalls)
	}
	if len(testPlanner.distributedTestFiles) != 1 ||
		!slices.Equal(testPlanner.distributedTestFiles[0], []string{
			"spec/fast_a_spec.rb",
			"spec/fast_b_spec.rb",
			"spec/slow_spec.rb",
		}) {
		t.Fatalf("expected runner to pass only CI-node file list to planner distribution, got %v", testPlanner.distributedTestFiles)
	}
	if len(testPlanner.distributedWorkerNums) != 1 || testPlanner.distributedWorkerNums[0] != 2 {
		t.Fatalf("expected runner to request 2 worker groups, got %v", testPlanner.distributedWorkerNums)
	}
	allFiles := make([]string, 0)
	for _, call := range calls {
		allFiles = append(allFiles, call.TestFiles...)
	}
	slices.Sort(allFiles)
	expectedFiles := []string{"spec/fast_a_spec.rb", "spec/fast_b_spec.rb", "spec/slow_spec.rb"}
	if !slices.Equal(allFiles, expectedFiles) {
		t.Fatalf("expected CI-node workers to run all node files without loaded weights, got %+v", calls)
	}
}

func distributeRoundRobin(testFiles []string, parallelRunners int) [][]string {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}
	groups := make([][]string, parallelRunners)
	for i := range groups {
		groups[i] = []string{}
	}
	for index, testFile := range testFiles {
		groups[index%parallelRunners] = append(groups[index%parallelRunners], testFile)
	}
	return groups
}

func withRunnerTestSettings(t *testing.T) {
	t.Helper()
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_REPORT_ENABLED", "false")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE", "-1")
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_CI_NODE_WORKERS", "1")
	viper.Reset()
	settings.Init()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})
}

func chdirTemp(t *testing.T) {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWd)
	})
}

func writeRunnerTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
