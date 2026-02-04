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

func TestRunCINodeTests_SingleWorker(t *testing.T) {
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

	// Test with single worker (ciNodeWorkers=1)
	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, map[string]string{}, 1, 1)
	if err != nil {
		t.Fatalf("runCINodeTestsWithWorkers() should not return error, got: %v", err)
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

func TestRunCINodeTests_MultipleWorkers(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files - 4 test files for ci-node 1
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-1"),
		[]byte("test/file1_test.rb\ntest/file2_test.rb\ntest/file3_test.rb\ntest/file4_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	// Test with 2 workers on ci-node 1
	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, map[string]string{}, 1, 2)
	if err != nil {
		t.Fatalf("runCINodeTestsWithWorkers() should not return error, got: %v", err)
	}

	// Verify RunTests was called twice (once per worker)
	if mockFramework.GetRunTestsCallsCount() != 2 {
		t.Fatalf("Expected RunTests to be called twice, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	// Verify all test files were distributed
	calls := mockFramework.GetRunTestsCalls()
	allFiles := make([]string, 0)
	for _, call := range calls {
		allFiles = append(allFiles, call.TestFiles...)
	}
	slices.Sort(allFiles)

	expectedFiles := []string{"test/file1_test.rb", "test/file2_test.rb", "test/file3_test.rb", "test/file4_test.rb"}
	if !slices.Equal(allFiles, expectedFiles) {
		t.Errorf("Expected all test files %v to be distributed, got %v", expectedFiles, allFiles)
	}
}

func TestRunCINodeTests_GlobalIndexCalculation(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files - 2 test files for ci-node 1
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-1"),
		[]byte("test/file1_test.rb\ntest/file2_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	workerEnvMap := map[string]string{
		"NODE_INDEX": "{{nodeIndex}}",
	}

	// Test with 2 workers on ci-node 1
	// Global indices should be: 1*10000+0=10000 and 1*10000+1=10001
	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, workerEnvMap, 1, 2)
	if err != nil {
		t.Fatalf("runCINodeTestsWithWorkers() should not return error, got: %v", err)
	}

	// Verify RunTests was called twice
	if mockFramework.GetRunTestsCallsCount() != 2 {
		t.Fatalf("Expected RunTests to be called twice, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	// Collect all NODE_INDEX values
	calls := mockFramework.GetRunTestsCalls()
	nodeIndices := make([]string, 0)
	for _, call := range calls {
		nodeIndices = append(nodeIndices, call.EnvMap["NODE_INDEX"])
	}
	slices.Sort(nodeIndices)

	// Global indices for ci-node=1 should be 10000 and 10001 (ciNode * 10000 + localIndex)
	expectedIndices := []string{"10000", "10001"}
	if !slices.Equal(nodeIndices, expectedIndices) {
		t.Errorf("Expected global indices %v, got %v", expectedIndices, nodeIndices)
	}
}

func TestRunCINodeTests_SingleWorkerGlobalIndex(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-2"),
		[]byte("test/file1_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	workerEnvMap := map[string]string{
		"NODE_INDEX": "{{nodeIndex}}",
	}

	// Single worker mode on ci-node 2 - global index should be 20000 (ciNode * 10000)
	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, workerEnvMap, 2, 1)
	if err != nil {
		t.Fatalf("runCINodeTestsWithWorkers() should not return error, got: %v", err)
	}

	calls := mockFramework.GetRunTestsCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].EnvMap["NODE_INDEX"] != "20000" {
		t.Errorf("Expected NODE_INDEX=20000 for single worker on ci-node 2, got %s", calls[0].EnvMap["NODE_INDEX"])
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

	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, map[string]string{}, 2, 1)
	if err == nil {
		t.Error("runCINodeTestsWithWorkers() should return error when runner file doesn't exist")
	}

	expectedMsg := "runner file for ci-node 2 does not exist"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestRunCINodeTests_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory with empty runner file
	_ = os.MkdirAll(filepath.Join(constants.PlanDirectory, "tests-split"), 0755)
	_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-0"), []byte(""), 0644)

	mockFramework := &MockFramework{FrameworkName: "rspec"}

	// Should not error for empty file, just not run any tests
	err := runCINodeTestsWithWorkers(context.Background(), mockFramework, map[string]string{}, 0, 2)
	if err != nil {
		t.Fatalf("runCINodeTestsWithWorkers() should not return error for empty file, got: %v", err)
	}

	// Verify no tests were run
	if mockFramework.GetRunTestsCallsCount() != 0 {
		t.Errorf("Expected no RunTests calls for empty file, got %d", mockFramework.GetRunTestsCallsCount())
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

func TestSplitTestFilesIntoGroups(t *testing.T) {
	t.Run("even split", func(t *testing.T) {
		files := []string{"a", "b", "c", "d"}
		result := splitTestFilesIntoGroups(files, 2)

		if len(result) != 2 {
			t.Fatalf("Expected 2 groups, got %d", len(result))
		}

		// Round-robin: a->0, b->1, c->0, d->1
		expected0 := []string{"a", "c"}
		expected1 := []string{"b", "d"}

		if !slices.Equal(result[0], expected0) {
			t.Errorf("Expected group 0 to be %v, got %v", expected0, result[0])
		}
		if !slices.Equal(result[1], expected1) {
			t.Errorf("Expected group 1 to be %v, got %v", expected1, result[1])
		}
	})

	t.Run("uneven split", func(t *testing.T) {
		files := []string{"a", "b", "c", "d", "e"}
		result := splitTestFilesIntoGroups(files, 2)

		if len(result) != 2 {
			t.Fatalf("Expected 2 groups, got %d", len(result))
		}

		// Round-robin: a->0, b->1, c->0, d->1, e->0
		expected0 := []string{"a", "c", "e"}
		expected1 := []string{"b", "d"}

		if !slices.Equal(result[0], expected0) {
			t.Errorf("Expected group 0 to be %v, got %v", expected0, result[0])
		}
		if !slices.Equal(result[1], expected1) {
			t.Errorf("Expected group 1 to be %v, got %v", expected1, result[1])
		}
	})

	t.Run("more groups than files", func(t *testing.T) {
		files := []string{"a", "b"}
		result := splitTestFilesIntoGroups(files, 4)

		if len(result) != 4 {
			t.Fatalf("Expected 4 groups, got %d", len(result))
		}

		// a->0, b->1, groups 2 and 3 are empty
		if !slices.Equal(result[0], []string{"a"}) {
			t.Errorf("Expected group 0 to be [a], got %v", result[0])
		}
		if !slices.Equal(result[1], []string{"b"}) {
			t.Errorf("Expected group 1 to be [b], got %v", result[1])
		}
		if len(result[2]) != 0 {
			t.Errorf("Expected group 2 to be empty, got %v", result[2])
		}
		if len(result[3]) != 0 {
			t.Errorf("Expected group 3 to be empty, got %v", result[3])
		}
	})

	t.Run("single group", func(t *testing.T) {
		files := []string{"a", "b", "c"}
		result := splitTestFilesIntoGroups(files, 1)

		if len(result) != 1 {
			t.Fatalf("Expected 1 group, got %d", len(result))
		}

		if !slices.Equal(result[0], files) {
			t.Errorf("Expected group 0 to be %v, got %v", files, result[0])
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := splitTestFilesIntoGroups([]string{}, 3)

		if len(result) != 3 {
			t.Fatalf("Expected 3 groups, got %d", len(result))
		}

		for i, group := range result {
			if len(group) != 0 {
				t.Errorf("Expected group %d to be empty, got %v", i, group)
			}
		}
	})

	t.Run("zero groups defaults to 1", func(t *testing.T) {
		files := []string{"a", "b"}
		result := splitTestFilesIntoGroups(files, 0)

		if len(result) != 1 {
			t.Fatalf("Expected 1 group for n=0, got %d", len(result))
		}

		if !slices.Equal(result[0], files) {
			t.Errorf("Expected all files in single group, got %v", result[0])
		}
	})
}

func TestRunTestsWithGlobalIndex(t *testing.T) {
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	testFiles := []string{"test/file1_test.rb", "test/file2_test.rb"}
	workerEnvMap := map[string]string{
		"NODE_INDEX": "{{nodeIndex}}",
		"DB_NAME":    "test_db_{{nodeIndex}}",
		"STATIC":     "value",
	}

	err := runTestsWithGlobalIndex(context.Background(), mockFramework, testFiles, workerEnvMap, 5)
	if err != nil {
		t.Fatalf("runTestsWithGlobalIndex() should not return error, got: %v", err)
	}

	if mockFramework.GetRunTestsCallsCount() != 1 {
		t.Fatalf("Expected 1 call, got %d", mockFramework.GetRunTestsCallsCount())
	}

	call := mockFramework.GetRunTestsCalls()[0]

	if !slices.Equal(call.TestFiles, testFiles) {
		t.Errorf("Expected test files %v, got %v", testFiles, call.TestFiles)
	}

	if call.EnvMap["NODE_INDEX"] != "5" {
		t.Errorf("Expected NODE_INDEX=5, got %s", call.EnvMap["NODE_INDEX"])
	}

	if call.EnvMap["DB_NAME"] != "test_db_5" {
		t.Errorf("Expected DB_NAME=test_db_5, got %s", call.EnvMap["DB_NAME"])
	}

	if call.EnvMap["STATIC"] != "value" {
		t.Errorf("Expected STATIC=value, got %s", call.EnvMap["STATIC"])
	}
}
