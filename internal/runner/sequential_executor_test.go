package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestRunSequential_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test files
	_ = os.MkdirAll(filepath.Dir(constants.TestFilesOutputPath), 0755)
	testFiles := "test/file1_test.rb\ntest/file2_test.rb\n"
	_ = os.WriteFile(constants.TestFilesOutputPath, []byte(testFiles), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}, roundRobinTestPlanner{}).runSequential()
	report, err := result.report, result.err
	if err != nil {
		t.Fatalf("runSequential() should not return error, got: %v", err)
	}
	if report.TestFilesRun != 2 {
		t.Errorf("Expected report to count 2 test files, got %d", report.TestFilesRun)
	}

	// Verify RunTests was called exactly once
	if mockFramework.GetRunTestsCallsCount() != 1 {
		t.Fatalf("Expected RunTests to be called once, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	calls := mockFramework.GetRunTestsCalls()
	call := calls[0]
	expectedFiles := []string{"test/file1_test.rb", "test/file2_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v, got %v", expectedFiles, call.TestFiles)
	}
}

func TestRunSequential_ReportsTIASkippedSuitesWithoutRunnableTests(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(filepath.Dir(constants.TestFilesOutputPath), 0755)
	_ = os.WriteFile(constants.TestFilesOutputPath, nil, 0644)
	_ = os.MkdirAll(constants.TIASkippedTestSuitesDir, 0755)
	artifactPath := filepath.Join(constants.TIASkippedTestSuitesDir, "runner-0.json")
	_ = os.WriteFile(artifactPath, []byte("{\"version\":1,\"test_suites\":[\"src/skipped.test.js\"]}\n"), 0644)

	mockFramework := &MockFramework{FrameworkName: "jest"}
	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}, roundRobinTestPlanner{}).runSequential()
	if result.err != nil {
		t.Fatalf("runSequential() returned error: %v", result.err)
	}

	calls := mockFramework.GetRunTestsCalls()
	if len(calls) != 1 {
		t.Fatalf("RunTests calls = %d, want 1", len(calls))
	}
	if len(calls[0].TestFiles) != 0 {
		t.Fatalf("RunTests test files = %v, want none", calls[0].TestFiles)
	}
	wantArtifactPath, _ := filepath.Abs(artifactPath)
	if got := calls[0].EnvMap[constants.TestOptimizationTIASkippedTestSuitesFileEnvVar]; got != wantArtifactPath {
		t.Errorf("skipped suites artifact env = %q, want %q", got, wantArtifactPath)
	}
}
