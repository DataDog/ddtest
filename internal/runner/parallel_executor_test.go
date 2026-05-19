package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
)

func TestRunParallelTests_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-0"), []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-1"), []byte("test/file2_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runParallelTests(context.Background(), mockFramework, map[string]string{})
	if err != nil {
		t.Fatalf("runParallelTests() should not return error, got: %v", err)
	}

	// Verify RunTests was called twice
	if mockFramework.GetRunTestsCallsCount() != 2 {
		t.Fatalf("Expected RunTests to be called twice, got %d calls", mockFramework.GetRunTestsCallsCount())
	}
}

func TestRunParallelTests_MissingSplitDirectory(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Don't create tests-split directory
	mockFramework := &MockFramework{FrameworkName: "rspec"}

	err := runParallelTests(context.Background(), mockFramework, map[string]string{})
	if err == nil {
		t.Error("runParallelTests() should return error when tests-split directory is missing")
	}

	expectedMsg := "failed to read tests split directory"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
	}
}
