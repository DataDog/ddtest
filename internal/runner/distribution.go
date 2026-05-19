package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
)

// DistributeTestFiles distributes test files across parallel runners using weighted list scheduling.
func DistributeTestFiles(testFiles map[string]int, parallelRunners int) [][]string {
	builder := newTestSplitBuilder(parallelRunners)

	result := make([][]string, builder.parallelRunners)
	for i := range result {
		result[i] = []string{}
	}

	for _, file := range sortedWeightedTestFiles(testFiles) {
		runnerIndex := builder.addFile(file.weight)
		result[runnerIndex] = append(result[runnerIndex], file.path)
	}

	return result
}

// CreateTestSplits creates test split files for parallel runners
// For multiple runners: distributes files using weighted list scheduling and writes to separate runner files
// For single runner: copies test-files.txt content to runner-0
func CreateTestSplits(testFiles map[string]int, parallelRunners int, testFilesOutputPath string) error {
	testsSplitDirs := []string{constants.TestsSplitDir, constants.LegacyTestsSplitDir}

	if parallelRunners > 1 {
		// Distribute test files across parallel runners using weighted list scheduling.
		distribution := DistributeTestFiles(testFiles, parallelRunners)
		for _, testsSplitDir := range testsSplitDirs {
			if err := writeDistributedTestSplits(distribution, testsSplitDir); err != nil {
				return err
			}
		}
	} else {
		// For single runner, copy test-files.txt to runner-0
		testFilesData, err := os.ReadFile(testFilesOutputPath)
		if err != nil {
			return fmt.Errorf("failed to read test files from %s: %w", testFilesOutputPath, err)
		}

		for _, testsSplitDir := range testsSplitDirs {
			if err := writeRunnerSplit(testsSplitDir, 0, testFilesData); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeDistributedTestSplits(distribution [][]string, testsSplitDir string) error {
	for i, runnerFiles := range distribution {
		runnerContent := strings.Join(runnerFiles, "\n")
		if len(runnerFiles) > 0 {
			runnerContent += "\n"
		}

		if err := writeRunnerSplit(testsSplitDir, i, []byte(runnerContent)); err != nil {
			return err
		}
	}

	return nil
}

func writeRunnerSplit(testsSplitDir string, runnerIndex int, content []byte) error {
	if err := os.MkdirAll(testsSplitDir, 0755); err != nil {
		return fmt.Errorf("failed to create tests-split directory %s: %w", testsSplitDir, err)
	}

	runnerFilePath := filepath.Join(testsSplitDir, fmt.Sprintf("runner-%d", runnerIndex))
	if err := os.WriteFile(runnerFilePath, content, 0644); err != nil {
		return fmt.Errorf("failed to write runner-%d files to %s: %w", runnerIndex, runnerFilePath, err)
	}

	return nil
}
