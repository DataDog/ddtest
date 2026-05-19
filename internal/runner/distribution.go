package runner

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DataDog/ddtest/internal/constants"
)

// DistributeTestFiles distributes test files across parallel runners using weighted list scheduling.
func DistributeTestFiles(testFiles map[string]int, parallelRunners int) [][]string {
	builder := newTestSplitBuilder(parallelRunners)
	return builder.distributeFiles(testFiles)
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

type weightedTestFile struct {
	path   string
	weight int
}

func sortedWeightedTestFiles(testFiles map[string]int) []weightedTestFile {
	files := make([]weightedTestFile, 0, len(testFiles))
	for path, weight := range testFiles {
		files = append(files, weightedTestFile{path: path, weight: weight})
	}

	slices.SortFunc(files, func(a, b weightedTestFile) int {
		if a.weight > b.weight {
			return -1
		}
		if a.weight < b.weight {
			return 1
		}
		if a.path < b.path {
			return -1
		}
		if a.path > b.path {
			return 1
		}
		return 0
	})

	return files
}

type splitScore struct {
	parallelRunners int
	wallTime        int
	imbalance       int
}

func scoreSortedWeightedRunnerSplit(files []weightedTestFile, parallelRunners int) splitScore {
	builder := newTestSplitBuilder(parallelRunners)
	for _, file := range files {
		builder.addFile(file.weight)
	}
	return builder.score()
}

type testSplitBuilder struct {
	parallelRunners int
	loads           minLoadHeap
}

func newTestSplitBuilder(parallelRunners int) testSplitBuilder {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	return testSplitBuilder{
		parallelRunners: parallelRunners,
		loads:           makeMinLoadHeap(parallelRunners),
	}
}

func (b *testSplitBuilder) addFile(weight int) int {
	lightestRunner := heap.Pop(&b.loads).(runnerLoad)
	lightestRunner.load += weight
	heap.Push(&b.loads, lightestRunner)
	return lightestRunner.index
}

func (b *testSplitBuilder) distributeFiles(testFiles map[string]int) [][]string {
	return b.distributeSortedFiles(sortedWeightedTestFiles(testFiles))
}

func (b *testSplitBuilder) distributeSortedFiles(files []weightedTestFile) [][]string {
	result := make([][]string, b.parallelRunners)
	for i := range result {
		result[i] = []string{}
	}

	for _, file := range files {
		runnerIndex := b.addFile(file.weight)
		result[runnerIndex] = append(result[runnerIndex], file.path)
	}

	return result
}

func (b testSplitBuilder) score() splitScore {
	minLoad, maxLoad := minMaxLoad(b.loads)
	return splitScore{
		parallelRunners: b.parallelRunners,
		wallTime:        maxLoad,
		imbalance:       maxLoad - minLoad,
	}
}

type runnerLoad struct {
	index int
	load  int
}

type minLoadHeap []runnerLoad

func (h minLoadHeap) Len() int {
	return len(h)
}

func (h minLoadHeap) Less(i, j int) bool {
	if h[i].load == h[j].load {
		return h[i].index < h[j].index
	}
	return h[i].load < h[j].load
}

func (h minLoadHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *minLoadHeap) Push(x any) {
	*h = append(*h, x.(runnerLoad))
}

func (h *minLoadHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func makeMinLoadHeap(parallelRunners int) minLoadHeap {
	loads := make(minLoadHeap, parallelRunners)
	for i := range loads {
		loads[i] = runnerLoad{index: i}
	}
	heap.Init(&loads)
	return loads
}

func minMaxLoad(loads []runnerLoad) (int, int) {
	minLoad := loads[0].load
	maxLoad := loads[0].load
	for _, load := range loads[1:] {
		if load.load < minLoad {
			minLoad = load.load
		}
		if load.load > maxLoad {
			maxLoad = load.load
		}
	}
	return minLoad, maxLoad
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
