package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/datadog-test-runner/internal/constants"
)

func TestDistributeTestFiles(t *testing.T) {
	t.Run("empty test files", func(t *testing.T) {
		result := DistributeTestFiles(map[string]int{}, 3)
		if len(result) != 3 {
			t.Errorf("Expected 3 runners, got %d", len(result))
		}
		for i, runner := range result {
			if len(runner) != 0 {
				t.Errorf("Runner %d should be empty, got %v", i, runner)
			}
		}
	})

	t.Run("single runner", func(t *testing.T) {
		testFiles := map[string]int{
			"test1.rb": 5,
			"test2.rb": 3,
			"test3.rb": 2,
		}
		result := DistributeTestFiles(testFiles, 1)

		if len(result) != 1 {
			t.Errorf("Expected 1 runner, got %d", len(result))
		}

		if len(result[0]) != 3 {
			t.Errorf("Expected all 3 files in single runner, got %d", len(result[0]))
		}

		// Check all files are present
		expectedFiles := map[string]bool{"test1.rb": true, "test2.rb": true, "test3.rb": true}
		for _, file := range result[0] {
			if !expectedFiles[file] {
				t.Errorf("Unexpected file in runner: %s", file)
			}
			delete(expectedFiles, file)
		}
		if len(expectedFiles) != 0 {
			t.Errorf("Missing files: %v", expectedFiles)
		}
	})

	t.Run("zero or negative runners defaults to 1", func(t *testing.T) {
		testFiles := map[string]int{"test1.rb": 1}

		result := DistributeTestFiles(testFiles, 0)
		if len(result) != 1 {
			t.Errorf("Expected 1 runner for parallelRunners=0, got %d", len(result))
		}

		result = DistributeTestFiles(testFiles, -1)
		if len(result) != 1 {
			t.Errorf("Expected 1 runner for parallelRunners=-1, got %d", len(result))
		}
	})

	t.Run("balanced distribution", func(t *testing.T) {
		testFiles := map[string]int{
			"test1.rb": 2,
			"test2.rb": 10,
			"test3.rb": 6,
			"test4.rb": 8,
			"test5.rb": 4,
		}
		result := DistributeTestFiles(testFiles, 3)

		if len(result) != 3 {
			t.Errorf("Expected 3 runners, got %d", len(result))
		}

		// With First Fit Decreasing algorithm, files are sorted by count (descending):
		// test2.rb: 10, test4.rb: 8, test3.rb: 6, test5.rb: 4, test1.rb: 2
		// Expected distribution (always picking bin with minimum load):
		// Runner 0: test2.rb (10)
		// Runner 1: test4.rb (8) + test1.rb (2) = 10
		// Runner 2: test3.rb (6) + test5.rb (4) = 10
		expectedDistribution := [][]string{
			{"test2.rb"},
			{"test4.rb", "test1.rb"},
			{"test3.rb", "test5.rb"},
		}

		// Verify exact distribution
		for i, expectedRunner := range expectedDistribution {
			if len(result[i]) != len(expectedRunner) {
				t.Errorf("Runner %d should have %d files, got %d", i, len(expectedRunner), len(result[i]))
				continue
			}

			// Convert to sets for comparison (order within runner doesn't matter)
			expected := make(map[string]bool)
			actual := make(map[string]bool)

			for _, file := range expectedRunner {
				expected[file] = true
			}
			for _, file := range result[i] {
				actual[file] = true
			}

			for file := range expected {
				if !actual[file] {
					t.Errorf("Runner %d missing expected file %s, got files: %v", i, file, result[i])
				}
			}
			for file := range actual {
				if !expected[file] {
					t.Errorf("Runner %d has unexpected file %s, expected files: %v", i, file, expectedRunner)
				}
			}
		}

		// Verify all runners have load of 10
		for i, runner := range result {
			load := 0
			for _, file := range runner {
				load += testFiles[file]
			}
			if load != 10 {
				t.Errorf("Runner %d has load %d, expected 10", i, load)
			}
		}
	})

	t.Run("more runners than files", func(t *testing.T) {
		testFiles := map[string]int{
			"test1.rb": 5,
			"test2.rb": 3,
		}
		result := DistributeTestFiles(testFiles, 5)

		if len(result) != 5 {
			t.Errorf("Expected 5 runners, got %d", len(result))
		}

		// Count non-empty runners
		nonEmptyRunners := 0
		totalFiles := 0
		for _, runner := range result {
			if len(runner) > 0 {
				nonEmptyRunners++
			}
			totalFiles += len(runner)
		}

		if nonEmptyRunners != 2 {
			t.Errorf("Expected 2 non-empty runners, got %d", nonEmptyRunners)
		}

		if totalFiles != 2 {
			t.Errorf("Expected 2 total files, got %d", totalFiles)
		}
	})

	t.Run("files sorted by test count descending", func(t *testing.T) {
		testFiles := map[string]int{
			"small.rb":  1,
			"large.rb":  100,
			"medium.rb": 50,
		}
		result := DistributeTestFiles(testFiles, 3)

		// The largest file should go to the first runner
		// Find which runner has the large.rb file
		var largeFileRunner = -1
		for i, runner := range result {
			if slices.Contains(runner, "large.rb") {
				largeFileRunner = i
				break
			}
		}

		if largeFileRunner == -1 {
			t.Error("large.rb file should be assigned to a runner")
		}

		// Verify the large file ended up in a runner
		largeFileLoad := 0
		for _, file := range result[largeFileRunner] {
			largeFileLoad += testFiles[file]
		}

		if largeFileLoad < 100 {
			t.Errorf("Runner with large.rb should have at least 100 load, got %d", largeFileLoad)
		}
	})

	t.Run("deterministic output", func(t *testing.T) {
		testFiles := map[string]int{
			"test1.rb": 5,
			"test2.rb": 3,
			"test3.rb": 7,
		}

		// Run multiple times and check results are consistent
		result1 := DistributeTestFiles(testFiles, 2)
		result2 := DistributeTestFiles(testFiles, 2)

		// Results should be identical (same distribution)
		if len(result1) != len(result2) {
			t.Error("Results should have same number of runners")
		}

		for i := range result1 {
			if len(result1[i]) != len(result2[i]) {
				t.Errorf("Runner %d should have same number of files in both results", i)
			}

			// Convert to sets and compare
			files1 := make(map[string]bool)
			files2 := make(map[string]bool)

			for _, file := range result1[i] {
				files1[file] = true
			}
			for _, file := range result2[i] {
				files2[file] = true
			}

			if len(files1) != len(files2) {
				t.Errorf("Runner %d should have same files in both results", i)
				continue
			}

			for file := range files1 {
				if !files2[file] {
					t.Errorf("Runner %d missing file %s in second result", i, file)
				}
			}
		}
	})
}

func TestCreateTestSplits(t *testing.T) {
	t.Run("single runner - copies test-files.txt to runner-0", func(t *testing.T) {
		tempDir := t.TempDir()
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		// Create test-files.txt with content
		_ = os.MkdirAll(constants.PlanDirectory, 0755)
		testContent := "test/file1_test.rb\ntest/file2_test.rb\n"
		_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "test-files.txt"), []byte(testContent), 0644)

		testFiles := map[string]int{
			"test/file1_test.rb": 2,
			"test/file2_test.rb": 1,
		}

		err := CreateTestSplits(testFiles, 1, filepath.Join(constants.PlanDirectory, "test-files.txt"))
		if err != nil {
			t.Fatalf("CreateTestSplits() should not return error, got: %v", err)
		}

		// Verify tests-split directory was created
		if _, err := os.Stat(filepath.Join(constants.PlanDirectory, "tests-split")); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created")
		}

		// Verify runner-0 file was created
		runnerFilePath := filepath.Join(constants.PlanDirectory, "tests-split", "runner-0")
		if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
			t.Error("Expected runner-0 file to be created")
		}

		// Verify content matches test-files.txt
		runnerContent, err := os.ReadFile(runnerFilePath)
		if err != nil {
			t.Fatalf("Failed to read runner-0 file: %v", err)
		}

		if string(runnerContent) != testContent {
			t.Errorf("Expected runner-0 content %q, got %q", testContent, string(runnerContent))
		}
	})

	t.Run("multiple runners - creates distributed split files", func(t *testing.T) {
		tempDir := t.TempDir()
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		testFiles := map[string]int{
			"test/file1_test.rb": 10, // Largest file
			"test/file2_test.rb": 5,
			"test/file3_test.rb": 3,
		}

		err := CreateTestSplits(testFiles, 2, filepath.Join(constants.PlanDirectory, "test-files.txt"))
		if err != nil {
			t.Fatalf("CreateTestSplits() should not return error, got: %v", err)
		}

		// Verify tests-split directory was created
		if _, err := os.Stat(filepath.Join(constants.PlanDirectory, "tests-split")); os.IsNotExist(err) {
			t.Error("Expected tests-split directory to be created")
		}

		// Verify both runner files were created
		for i := 0; i < 2; i++ {
			runnerFilePath := filepath.Join(constants.PlanDirectory, "tests-split", fmt.Sprintf("runner-%d", i))
			if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
				t.Errorf("Expected runner-%d file to be created", i)
			}
		}

		// Verify content distribution
		// With bin packing: runner-0 gets file1 (10), runner-1 gets file2+file3 (8)
		runner0Content, _ := os.ReadFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-0"))
		runner1Content, _ := os.ReadFile(filepath.Join(constants.PlanDirectory, "tests-split", "runner-1"))

		runner0Files := strings.Fields(strings.TrimSpace(string(runner0Content)))
		runner1Files := strings.Fields(strings.TrimSpace(string(runner1Content)))

		// Verify all files are distributed
		allFiles := append(runner0Files, runner1Files...)
		expectedFiles := []string{"test/file1_test.rb", "test/file2_test.rb", "test/file3_test.rb"}

		if len(allFiles) != 3 {
			t.Errorf("Expected 3 total files distributed, got %d", len(allFiles))
		}

		for _, expected := range expectedFiles {
			if !slices.Contains(allFiles, expected) {
				t.Errorf("Expected file %s to be in distribution, but not found", expected)
			}
		}
	})

	t.Run("empty test files", func(t *testing.T) {
		tempDir := t.TempDir()
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		// Create empty test-files.txt
		_ = os.MkdirAll(constants.PlanDirectory, 0755)
		_ = os.WriteFile(filepath.Join(constants.PlanDirectory, "test-files.txt"), []byte(""), 0644)

		err := CreateTestSplits(map[string]int{}, 2, filepath.Join(constants.PlanDirectory, "test-files.txt"))
		if err != nil {
			t.Fatalf("CreateTestSplits() should not return error for empty files, got: %v", err)
		}

		// Verify runner files are created (even if empty)
		for i := 0; i < 2; i++ {
			runnerFilePath := filepath.Join(constants.PlanDirectory, "tests-split", fmt.Sprintf("runner-%d", i))
			if _, err := os.Stat(runnerFilePath); os.IsNotExist(err) {
				t.Errorf("Expected runner-%d file to be created", i)
			}
		}
	})

	t.Run("test-files.txt read error for single runner", func(t *testing.T) {
		tempDir := t.TempDir()
		oldWd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldWd) }()
		_ = os.Chdir(tempDir)

		// Don't create test-files.txt
		testFiles := map[string]int{"test/file1_test.rb": 1}

		err := CreateTestSplits(testFiles, 1, filepath.Join(constants.PlanDirectory, "test-files.txt"))
		if err == nil {
			t.Error("CreateTestSplits() should return error when test-files.txt doesn't exist")
		}

		expectedMsg := "failed to read test files from " + filepath.Join(constants.PlanDirectory, "test-files.txt")
		if !strings.Contains(err.Error(), expectedMsg) {
			t.Errorf("Error should contain '%s', got: %v", expectedMsg, err)
		}
	})
}
