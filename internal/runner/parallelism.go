package runner

import "log/slog"

// calculateParallelRunners determines the number of parallel runners by
// estimating splits between the configured min and max parallelism.
func calculateParallelRunners(testFileWeights map[string]int, minParallelism, maxParallelism int) int {
	// maxParallelism could be 0 or negative!
	if maxParallelism <= 1 {
		return 1
	}

	if minParallelism < 1 {
		slog.Warn("min_parallelism is less than 1, setting to 1", "min_parallelism", minParallelism)
		minParallelism = 1
	}

	if maxParallelism < minParallelism {
		slog.Warn("max_parallelism is less than min_parallelism, clamping min to max",
			"max_parallelism", maxParallelism, "min_parallelism", minParallelism)
		minParallelism = maxParallelism
	}

	files := sortedWeightedTestFiles(testFileWeights)
	if len(files) == 0 {
		return minParallelism
	}

	candidateMax := maxUsefulParallelism(minParallelism, maxParallelism, len(files))

	best := scoreSortedSplit(files, minParallelism)
	for parallelRunners := minParallelism + 1; parallelRunners <= candidateMax; parallelRunners++ {
		score := scoreSortedSplit(files, parallelRunners)
		if betterSplit(score, best) {
			best = score
		}
	}

	return best.parallelRunners
}

func maxUsefulParallelism(minParallelism, maxParallelism, filesCount int) int {
	if filesCount < minParallelism {
		return minParallelism
	}
	if filesCount < maxParallelism {
		return filesCount
	}
	return maxParallelism
}

func betterSplit(candidate, currentBest splitScore) bool {
	return candidate.wallTime < currentBest.wallTime ||
		(candidate.wallTime == currentBest.wallTime && candidate.imbalance < currentBest.imbalance)
}
