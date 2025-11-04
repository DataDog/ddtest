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

func TestRunCINodeTests_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-1"), []byte("test/file2_test.rb\ntest/file3_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	err := runCINodeTests(context.Background(), mockFramework, map[string]string{}, 1)
	if err != nil {
		t.Fatalf("runCINodeTests() should not return error, got: %v", err)
	}

	// Verify RunTests was called exactly once
	if mockFramework.GetRunTestsCallsCount() != 1 {
		t.Fatalf("Expected RunTests to be called once, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	calls := mockFramework.GetRunTestsCalls()
	call := calls[0]
	expectedFiles := []string{"test/file2_test.rb", "test/file3_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v for ci-node=1, got %v", expectedFiles, call.TestFiles)
	}
}

func TestRunCINodeTests_FileNotFound(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	// Don't create runner-2 file

	mockFramework := &MockFramework{FrameworkName: "rspec"}

	err := runCINodeTests(context.Background(), mockFramework, map[string]string{}, 2)
	if err == nil {
		t.Error("runCINodeTests() should return error when runner file doesn't exist")
	}

	expectedMsg := "runner file for ci-node 2 does not exist"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestRunParallelTests_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-0"), []byte("test/file1_test.rb\n"), 0644)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-1"), []byte("test/file2_test.rb\n"), 0644)

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

func TestRunSequentialTests_Success(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test files
	_ = os.MkdirAll(constants.PlanDirectory, 0755)
	testFiles := "test/file1_test.rb\ntest/file2_test.rb\n"
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "test-files.txt"), []byte(testFiles), 0644)

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

func TestRunTestsFromFile_WithWorkerEnv(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Create test file
	testFile := "test/file1_test.rb\n"
	_ = os.WriteFile("test-list.txt", []byte(testFile), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	workerEnvMap := map[string]string{
		"NODE_INDEX": "{{nodeIndex}}",
		"BUILD_ID":   "123",
	}

	err := runTestsFromFile(context.Background(), mockFramework, "test-list.txt", workerEnvMap, 5)
	if err != nil {
		t.Fatalf("runTestsFromFile() should not return error, got: %v", err)
	}

	// Verify RunTests was called
	if mockFramework.GetRunTestsCallsCount() != 1 {
		t.Fatalf("Expected RunTests to be called once, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	calls := mockFramework.GetRunTestsCalls()
	call := calls[0]

	// Verify nodeIndex placeholder was replaced
	if call.EnvMap["NODE_INDEX"] != "5" {
		t.Errorf("Expected NODE_INDEX=5, got %s", call.EnvMap["NODE_INDEX"])
	}

	// Verify other env vars preserved
	if call.EnvMap["BUILD_ID"] != "123" {
		t.Errorf("Expected BUILD_ID=123, got %s", call.EnvMap["BUILD_ID"])
	}
}

func TestReadTestFilesFromFile_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.WriteFile("empty.txt", []byte(""), 0644)

	files, err := readTestFilesFromFile("empty.txt")
	if err != nil {
		t.Fatalf("readTestFilesFromFile() should not return error for empty file, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files for empty file, got %d", len(files))
	}
}

func TestReadTestFilesFromFile_WithContent(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	content := "test/file1_test.rb\n  test/file2_test.rb  \n\ntest/file3_test.rb\n"
	_ = os.WriteFile("tests.txt", []byte(content), 0644)

	files, err := readTestFilesFromFile("tests.txt")
	if err != nil {
		t.Fatalf("readTestFilesFromFile() should not return error, got: %v", err)
	}

	expected := []string{"test/file1_test.rb", "test/file2_test.rb", "test/file3_test.rb"}
	if !slices.Equal(files, expected) {
		t.Errorf("Expected files %v, got %v", expected, files)
	}
}
