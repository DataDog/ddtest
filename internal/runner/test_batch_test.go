package runner

import (
	"context"
	"os"
	"slices"
	"testing"
)

func TestLoadTestBatch_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	_ = os.WriteFile("empty.txt", []byte(""), 0644)

	files, err := loadTestBatch("empty.txt")
	if err != nil {
		t.Fatalf("loadTestBatch() should not return error for empty file, got: %v", err)
	}

	if len(files) != 0 {
		t.Errorf("Expected 0 files for empty file, got %d", len(files))
	}
}

func TestLoadTestBatch_WithContent(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(tempDir)

	content := "test/file1_test.rb\n  test/file2_test.rb  \n\ntest/file3_test.rb\n"
	_ = os.WriteFile("tests.txt", []byte(content), 0644)

	files, err := loadTestBatch("tests.txt")
	if err != nil {
		t.Fatalf("loadTestBatch() should not return error, got: %v", err)
	}

	expected := []string{"test/file1_test.rb", "test/file2_test.rb", "test/file3_test.rb"}
	if !slices.Equal(files, expected) {
		t.Errorf("Expected files %v, got %v", expected, files)
	}
}

func TestRunBatch(t *testing.T) {
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		RunTestsCalls: []RunTestsCall{},
	}

	testFiles := []string{"test/file1_test.rb", "test/file2_test.rb"}
	workerEnvMap := map[string]string{
		"NODE_INDEX":       "{{nodeIndex}}",
		"WORKER_INDEX":     "{{workerIndex}}",
		"WORKER_RESOURCES": "node_{{nodeIndex}}_worker_{{workerIndex}}",
		"STATIC":           "value",
	}

	err := newTestExecutor(context.Background(), mockFramework, workerEnvMap).runBatch(testFiles, 5, 3)
	if err != nil {
		t.Fatalf("runBatch() should not return error, got: %v", err)
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

	if call.EnvMap["WORKER_INDEX"] != "3" {
		t.Errorf("Expected WORKER_INDEX=3, got %s", call.EnvMap["WORKER_INDEX"])
	}

	if call.EnvMap["WORKER_RESOURCES"] != "node_5_worker_3" {
		t.Errorf("Expected WORKER_RESOURCES=node_5_worker_3, got %s", call.EnvMap["WORKER_RESOURCES"])
	}

	if call.EnvMap["STATIC"] != "value" {
		t.Errorf("Expected STATIC=value, got %s", call.EnvMap["STATIC"])
	}
}
