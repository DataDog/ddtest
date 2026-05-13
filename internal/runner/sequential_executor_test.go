package runner

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestRunSequentialTests_Success(t *testing.T) {
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

	err := runSequentialTests(context.Background(), mockFramework, map[string]string{})
	if err != nil {
		t.Fatalf("runSequentialTests() should not return error, got: %v", err)
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
