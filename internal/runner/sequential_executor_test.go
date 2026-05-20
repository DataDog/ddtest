package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runSequential()
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

func TestRunSequential_DoesNotReadLegacyTestFiles(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(filepath.Dir(constants.LegacyTestFilesOutputPath), 0755)
	_ = os.WriteFile(constants.LegacyTestFilesOutputPath, []byte("test/file1_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runSequential()
	err := result.err
	if err == nil {
		t.Fatal("runSequential() should return error when only the legacy test files list exists")
	}

	if !strings.Contains(err.Error(), constants.TestFilesOutputPath) {
		t.Errorf("Error should reference new test files path %s, got: %v", constants.TestFilesOutputPath, err)
	}
}
