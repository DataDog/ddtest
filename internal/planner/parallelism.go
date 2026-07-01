package planner

import (
	"log/slog"
	"time"
)

// calculateParallelRunnerSplit determines the selected runner split by
// estimating candidates between the configured min and max parallelism.
func calculateParallelRunnerSplit(testFileWeights map[string]int, minParallelism, maxParallelism int, parallelRunnerOverhead, targetTime time.Duration) splitScore {
	files := sortedWeightedTestFiles(testFileWeights)
	selector := splitSelector{
		parallelRunnerOverhead: parallelRunnerOverhead,
		targetTime:             targetTime,
	}

	// maxParallelism could be 0 or negative!
	if maxParallelism <= 1 {
		score := scoreSortedWeightedRunnerSplit(files, 1)
		selector.logCandidate(score)
		selector.maybeWarnTargetTimeUnreachable(score, 1, 1)
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
		selector.logCandidate(score)
		selector.maybeWarnTargetTimeUnreachable(score, minParallelism, maxParallelism)
		return score
	}

	candidateMax := maxUsefulParallelism(minParallelism, maxParallelism, len(files))

	best := scoreSortedWeightedRunnerSplit(files, minParallelism)
	targetBest := best
	targetBestFound := selector.meetsTargetTime(best)
	selector.logCandidate(best)
	for parallelRunners := minParallelism + 1; parallelRunners <= candidateMax; parallelRunners++ {
		score := scoreSortedWeightedRunnerSplit(files, parallelRunners)
		selector.logCandidate(score)
		if selector.better(score, best) {
			best = score
		}
		if selector.meetsTargetTime(score) && (!targetBestFound || selector.better(score, targetBest)) {
			targetBest = score
			targetBestFound = true
		}
	}

	if targetBestFound {
		return targetBest
	}

	selector.maybeWarnTargetTimeUnreachable(best, minParallelism, maxParallelism)
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

type splitSelector struct {
	parallelRunnerOverhead time.Duration
	targetTime             time.Duration
}

func (s splitSelector) better(candidate, currentBest splitScore) bool {
	candidateScore := s.selectionScore(candidate)
	currentBestScore := s.selectionScore(currentBest)
	if candidateScore != currentBestScore {
		return candidateScore < currentBestScore
	}
	if candidate.parallelRunners != currentBest.parallelRunners {
		return candidate.parallelRunners < currentBest.parallelRunners
	}
	if candidate.wallTime != currentBest.wallTime {
		return candidate.wallTime < currentBest.wallTime
	}
	return candidate.imbalance < currentBest.imbalance
}

// selectionScore models each candidate as wallTime + runners * overhead. When
// scores tie, the selector intentionally prefers fewer runners before comparing
// wall time and imbalance.
func (s splitSelector) selectionScore(score splitScore) int {
	return score.wallTime + score.parallelRunners*s.parallelRunnerOverheadMillis()
}

func (s splitSelector) parallelRunnerOverheadMillis() int {
	if s.parallelRunnerOverhead <= 0 {
		return 0
	}
	return int(s.parallelRunnerOverhead / time.Millisecond)
}

func (s splitSelector) meetsTargetTime(score splitScore) bool {
	return s.targetTime > 0 && score.wallTimeDuration() <= s.targetTime
}

func (s splitSelector) maybeWarnTargetTimeUnreachable(best splitScore, minParallelism, maxParallelism int) {
	if s.targetTime <= 0 || s.meetsTargetTime(best) {
		return
	}

	slog.Warn("No parallel runner split meets target time; selecting best split from all candidates",
		"targetTime", s.targetTime,
		"minParallelism", minParallelism,
		"maxParallelism", maxParallelism,
		"selectedParallelRunners", best.parallelRunners,
		"selectedExpectedWallTime", best.wallTimeDuration())
}

func (s splitSelector) logCandidate(score splitScore) {
	slog.Debug("Considered parallel runner split",
		"parallelRunners", score.parallelRunners,
		"expectedWallTime", score.wallTimeDuration(),
		"imbalance", score.imbalanceDuration(),
		"expectedTotalRuntime", score.totalRuntimeDuration(),
		"parallelRunnerOverhead", s.parallelRunnerOverhead,
		"targetTime", s.targetTime,
		"meetsTargetTime", s.meetsTargetTime(score),
		"selectionScore", time.Duration(s.selectionScore(score))*time.Millisecond)
}
