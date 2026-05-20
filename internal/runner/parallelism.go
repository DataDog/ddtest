package runner

import "log/slog"

// calculateParallelRunnerSplit determines the selected runner split by
// estimating candidates between the configured min and max parallelism.
func calculateParallelRunnerSplit(testFileWeights map[string]int, minParallelism, maxParallelism int) splitScore {
	files := sortedWeightedTestFiles(testFileWeights)

	// maxParallelism could be 0 or negative!
	if maxParallelism <= 1 {
		score := scoreSortedWeightedRunnerSplit(files, 1)
		logCandidateSplit(score)
		return score
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

	if len(files) == 0 {
		score := scoreSortedWeightedRunnerSplit(files, minParallelism)
		logCandidateSplit(score)
		return score
	}

	candidateMax := maxUsefulParallelism(minParallelism, maxParallelism, len(files))

	best := scoreSortedWeightedRunnerSplit(files, minParallelism)
	logCandidateSplit(best)
	for parallelRunners := minParallelism + 1; parallelRunners <= candidateMax; parallelRunners++ {
		score := scoreSortedWeightedRunnerSplit(files, parallelRunners)
		logCandidateSplit(score)
		if betterSplit(score, best) {
			best = score
		}
	}

	return best
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

func logCandidateSplit(score splitScore) {
	slog.Debug("Considered parallel runner split",
		"parallelRunners", score.parallelRunners,
		"expectedWallTime", score.wallTimeDuration(),
		"imbalance", score.imbalanceDuration(),
		"expectedTotalRuntime", score.totalRuntimeDuration())
}
