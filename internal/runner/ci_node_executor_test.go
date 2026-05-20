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

func TestRunCINode_SingleWorker(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-1"), []byte("test/file2_test.rb\ntest/file3_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	// Test with single worker (ciNodeWorkers=1)
	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runCINode(1, 1, nil)
	report, err := result.report, result.err
	if err != nil {
		t.Fatalf("runCINode() should not return error, got: %v", err)
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
	expectedFiles := []string{"test/file2_test.rb", "test/file3_test.rb"}
	if !slices.Equal(call.TestFiles, expectedFiles) {
		t.Errorf("Expected test files %v for ci-node=1, got %v", expectedFiles, call.TestFiles)
	}
}

func TestRunCINode_MultipleWorkers(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)
	logs := captureLogs(t)

	// Setup test split directory and files - 4 test files for ci-node 1
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-1"),
		[]byte("test/file1_test.rb\ntest/file2_test.rb\ntest/file3_test.rb\ntest/file4_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	// Test with 2 workers on ci-node 1
	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runCINode(1, 2, nil)
	report, err := result.report, result.err
	if err != nil {
		t.Fatalf("runCINode() should not return error, got: %v", err)
	}
	if report.LocalWorkers != 2 {
		t.Errorf("Expected report to count 2 local workers, got %d", report.LocalWorkers)
	}
	if report.TestFilesRun != 4 {
		t.Errorf("Expected report to count 4 test files, got %d", report.TestFilesRun)
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

	logOutput := logs.String()
	if strings.Contains(logOutput, "Assigned tests to CI node worker") {
		t.Errorf("Expected no INFO logs for CI node worker assignments, got logs: %s", logOutput)
	}
	if strings.Count(logOutput, "Assigned test files to CI node worker") != 2 ||
		!strings.Contains(logOutput, "ciNode=1") ||
		!strings.Contains(logOutput, "workerIndex=0") ||
		!strings.Contains(logOutput, "workerIndex=1") ||
		!strings.Contains(logOutput, "test/file1_test.rb") ||
		!strings.Contains(logOutput, "test/file2_test.rb") ||
		!strings.Contains(logOutput, "test/file3_test.rb") ||
		!strings.Contains(logOutput, "test/file4_test.rb") {
		t.Errorf("Expected DEBUG logs with assigned CI node worker test files, got logs: %s", logOutput)
	}
}

func TestRunCINode_NodeIndexMatchesCINode(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files - 2 test files for ci-node 1
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-1"),
		[]byte("test/file1_test.rb\ntest/file2_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	workerEnvMap := map[string]string{
		"NODE_INDEX":   "{{nodeIndex}}",
		"WORKER_INDEX": "{{workerIndex}}",
	}

	// Test with 2 workers on ci-node 1
	result := newTestExecutor(context.Background(), mockFramework, workerEnvMap).runCINode(1, 2, nil)
	err := result.err
	if err != nil {
		t.Fatalf("runCINode() should not return error, got: %v", err)
	}

	// Verify RunTests was called twice
	if mockFramework.GetRunTestsCallsCount() != 2 {
		t.Fatalf("Expected RunTests to be called twice, got %d calls", mockFramework.GetRunTestsCallsCount())
	}

	// Collect all NODE_INDEX values
	calls := mockFramework.GetRunTestsCalls()
	nodeIndices := make([]string, 0)
	workerIndices := make([]string, 0)
	for _, call := range calls {
		nodeIndices = append(nodeIndices, call.EnvMap["NODE_INDEX"])
		workerIndices = append(workerIndices, call.EnvMap["WORKER_INDEX"])
	}
	slices.Sort(nodeIndices)
	slices.Sort(workerIndices)

	expectedIndices := []string{"1", "1"}
	if !slices.Equal(nodeIndices, expectedIndices) {
		t.Errorf("Expected node indices %v, got %v", expectedIndices, nodeIndices)
	}

	expectedWorkerIndices := []string{"0", "1"}
	if !slices.Equal(workerIndices, expectedWorkerIndices) {
		t.Errorf("Expected worker indices %v, got %v", expectedWorkerIndices, workerIndices)
	}
}

func TestRunCINode_SingleWorkerNodeIndex(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory and files
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-2"),
		[]byte("test/file1_test.rb\n"), 0644)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	workerEnvMap := map[string]string{
		"NODE_INDEX":   "{{nodeIndex}}",
		"WORKER_INDEX": "{{workerIndex}}",
	}

	result := newTestExecutor(context.Background(), mockFramework, workerEnvMap).runCINode(2, 1, nil)
	err := result.err
	if err != nil {
		t.Fatalf("runCINode() should not return error, got: %v", err)
	}

	calls := mockFramework.GetRunTestsCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].EnvMap["NODE_INDEX"] != "2" {
		t.Errorf("Expected NODE_INDEX=2 for single worker on ci-node 2, got %s", calls[0].EnvMap["NODE_INDEX"])
	}
	if calls[0].EnvMap["WORKER_INDEX"] != "0" {
		t.Errorf("Expected WORKER_INDEX=0 for single worker on ci-node 2, got %s", calls[0].EnvMap["WORKER_INDEX"])
	}
}

func TestRunCINode_FileNotFound(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	// Don't create runner-2 file

	mockFramework := &MockFramework{FrameworkName: "rspec"}

	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runCINode(2, 1, nil)
	err := result.err
	if err == nil {
		t.Error("runCINode() should return error when runner file doesn't exist")
	}

	expectedMsg := "runner file for ci-node 2 does not exist"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestRunCINode_DoesNotReadLegacySplitFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.MkdirAll(constants.LegacyTestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.LegacyTestsSplitDir, "runner-2"), []byte("test/file1_test.rb\n"), 0644)

	mockFramework := &MockFramework{FrameworkName: "rspec"}

	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runCINode(2, 1, nil)
	err := result.err
	if err == nil {
		t.Error("runCINode() should return error when only the legacy runner file exists")
	}

	expectedMsg := "runner file for ci-node 2 does not exist"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestRunCINode_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	// Setup test split directory with empty runner file
	_ = os.MkdirAll(constants.TestsSplitDir, 0755)
	_ = os.WriteFile(filepath.Join(constants.TestsSplitDir, "runner-0"), []byte(""), 0644)

	mockFramework := &MockFramework{FrameworkName: "rspec"}

	// Should not error for empty file, just not run any tests
	result := newTestExecutor(context.Background(), mockFramework, map[string]string{}).runCINode(0, 2, nil)
	report, err := result.report, result.err
	if err != nil {
		t.Fatalf("runCINode() should not return error for empty file, got: %v", err)
	}
	if report.TestFilesRun != 0 {
		t.Errorf("Expected report to count 0 test files, got %d", report.TestFilesRun)
	}

	// Verify no tests were run
	if mockFramework.GetRunTestsCallsCount() != 0 {
		t.Errorf("Expected no RunTests calls for empty file, got %d", mockFramework.GetRunTestsCallsCount())
	}
}

func TestSubsplitTestsBetweenWorkers(t *testing.T) {
	t.Run("even split", func(t *testing.T) {
		files := []string{"a", "b", "c", "d"}
		result := subsplitTestsBetweenWorkers(files, 2, map[string]int{})

		if len(result) != 2 {
			t.Fatalf("Expected 2 groups, got %d", len(result))
		}

		// Equal default weights keep this balanced as a->0, b->1, c->0, d->1.
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
		result := subsplitTestsBetweenWorkers(files, 2, map[string]int{})

		if len(result) != 2 {
			t.Fatalf("Expected 2 groups, got %d", len(result))
		}

		// Equal default weights keep this balanced as a->0, b->1, c->0, d->1, e->0.
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
		result := subsplitTestsBetweenWorkers(files, 4, map[string]int{})

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
		result := subsplitTestsBetweenWorkers(files, 1, map[string]int{})

		if len(result) != 1 {
			t.Fatalf("Expected 1 group, got %d", len(result))
		}

		if !slices.Equal(result[0], files) {
			t.Errorf("Expected group 0 to be %v, got %v", files, result[0])
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := subsplitTestsBetweenWorkers([]string{}, 3, map[string]int{})

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
		result := subsplitTestsBetweenWorkers(files, 0, map[string]int{})

		if len(result) != 1 {
			t.Fatalf("Expected 1 group for n=0, got %d", len(result))
		}

		if !slices.Equal(result[0], files) {
			t.Errorf("Expected all files in single group, got %v", result[0])
		}
	})

	t.Run("weighted split keeps heavy file isolated", func(t *testing.T) {
		files := []string{"a", "b", "c", "d"}
		weights := map[string]int{
			"a": 100,
			"b": 1,
			"c": 1,
			"d": 1,
		}

		result := subsplitTestsBetweenWorkers(files, 2, weights)

		if len(result) != 2 {
			t.Fatalf("Expected 2 groups, got %d", len(result))
		}

		if !slices.Equal(result[0], []string{"a"}) {
			t.Errorf("Expected heavy file to be alone in group 0, got %v", result[0])
		}
		if !slices.Equal(result[1], []string{"b", "c", "d"}) {
			t.Errorf("Expected light files in group 1, got %v", result[1])
		}
	})
}
