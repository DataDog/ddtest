package runner

import (
	"container/heap"
	"slices"
)

type weightedTestFile struct {
	path   string
	weight int
}

type runnerLoad struct {
	index int
	load  int
}

type weightedRunnerSplit struct {
	parallelRunners int
	loads           minLoadHeap
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

type splitScore struct {
	parallelRunners int
	wallTime        int
	imbalance       int
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

func scoreSortedWeightedRunnerSplit(files []weightedTestFile, parallelRunners int) splitScore {
	split := newWeightedRunnerSplit(parallelRunners)
	for _, file := range files {
		split.addFile(file.weight)
	}
	return split.score()
}

func newWeightedRunnerSplit(parallelRunners int) weightedRunnerSplit {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	return weightedRunnerSplit{
		parallelRunners: parallelRunners,
		loads:           makeMinLoadHeap(parallelRunners),
	}
}

func (s *weightedRunnerSplit) addFile(weight int) int {
	lightestRunner := heap.Pop(&s.loads).(runnerLoad)
	lightestRunner.load += weight
	heap.Push(&s.loads, lightestRunner)
	return lightestRunner.index
}

func (s weightedRunnerSplit) score() splitScore {
	minLoad, maxLoad := minMaxLoad(s.loads)
	return splitScore{
		parallelRunners: s.parallelRunners,
		wallTime:        maxLoad,
		imbalance:       maxLoad - minLoad,
	}
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
