package runner

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// DistributeTestFiles distributes test files across parallel runners using bin packing algorithm
func DistributeTestFiles(testFiles map[string]int, parallelRunners int) [][]string {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	if len(testFiles) == 0 {
		result := make([][]string, parallelRunners)
		for i := range result {
			result[i] = []string{}
		}
		return result
	}

	// Convert map to sorted slice (largest first)
	files := make([]struct {
		path  string
		count int
	}, 0, len(testFiles))
	for path, count := range testFiles {
		files = append(files, struct {
			path  string
			count int
		}{path: path, count: count})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].count > files[j].count
	})

	// loads tracks current test duration assigned to each bin
	loads := make([]int, parallelRunners)
	// result tracks files assigned to each bin (can be returned directly)
	result := make([][]string, parallelRunners)
	for i := range result {
		result[i] = []string{}
	}

	// First Fit Decreasing algorithm for bin packing
	// On each step take the file in decreasing order of load
	// and put it into the bin with minimum load
	//
	// Time complexity is N * M where
	// N - number of bins (estimated about 10^2)
	// M - number of test files (estimated about 10^4)
	for _, file := range files {
		minBin := 0
		for i := 1; i < len(loads); i++ {
			if loads[i] < loads[minBin] {
				minBin = i
			}
		}

		loads[minBin] += file.count
		result[minBin] = append(result[minBin], file.path)
	}

	return result
}

// CreateTestSplits creates test split files for parallel runners
// For multiple runners: distributes files using bin packing and writes to separate runner files
// For single runner: copies test-files.txt content to runner-0
func CreateTestSplits(testFiles map[string]int, parallelRunners int, testFilesOutputPath string) error {
	// Create tests-split directory
	if err := os.MkdirAll(TestsSplitDir, 0755); err != nil {
		return fmt.Errorf("failed to create tests-split directory: %w", err)
	}

	if parallelRunners > 1 {
		// Distribute test files across parallel runners using bin packing
		distribution := DistributeTestFiles(testFiles, parallelRunners)

		// Save each runner's test files to separate files
		for i, runnerFiles := range distribution {
			runnerContent := strings.Join(runnerFiles, "\n")
			if len(runnerFiles) > 0 {
				runnerContent += "\n"
			}

			runnerFilePath := fmt.Sprintf("%s/runner-%d", TestsSplitDir, i)
			if err := os.WriteFile(runnerFilePath, []byte(runnerContent), 0644); err != nil {
				return fmt.Errorf("failed to write runner-%d files to %s: %w", i, runnerFilePath, err)
			}
		}
	} else {
		// For single runner, copy test-files.txt to runner-0
		runnerFilePath := fmt.Sprintf("%s/runner-0", TestsSplitDir)
		testFilesData, err := os.ReadFile(testFilesOutputPath)
		if err != nil {
			return fmt.Errorf("failed to read test files from %s: %w", testFilesOutputPath, err)
		}

		if err := os.WriteFile(runnerFilePath, testFilesData, 0644); err != nil {
			return fmt.Errorf("failed to copy test files to %s: %w", runnerFilePath, err)
		}
	}

	return nil
}
