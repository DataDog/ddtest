package planner

import (
	"log/slog"
	"time"
)

// calculateParallelRunnerSplit determines the selected runner split by
// estimating candidates between the configured min and max parallelism.
func calculateParallelRunnerSplit(testFileWeights map[string]int, minParallelism, maxParallelism int, parallelRunnerOverhead, targetTime time.Duration) splitScore {
	return calculateParallelRunnerSplitSelection(testFileWeights, minParallelism, maxParallelism, parallelRunnerOverhead, targetTime).selected
}

func calculateParallelRunnerSplitSelection(testFileWeights map[string]int, minParallelism, maxParallelism int, parallelRunnerOverhead, targetTime time.Duration) splitSelection {
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
		return selector.selection(score, score, []splitScore{score})
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
		return selector.selection(score, score, []splitScore{score})
	}

	candidateMax := maxUsefulParallelism(minParallelism, maxParallelism, len(files))

	bestWithoutTarget := scoreSortedWeightedRunnerSplit(files, minParallelism)
	lowestWallTime := bestWithoutTarget
	bestWithinTarget := bestWithoutTarget
	targetBestFound := selector.meetsTargetTime(bestWithoutTarget)
	candidates := []splitScore{bestWithoutTarget}
	selector.logCandidate(bestWithoutTarget)
	for parallelRunners := minParallelism + 1; parallelRunners <= candidateMax; parallelRunners++ {
		score := scoreSortedWeightedRunnerSplit(files, parallelRunners)
		candidates = append(candidates, score)
		selector.logCandidate(score)
		if selector.better(score, bestWithoutTarget) {
			bestWithoutTarget = score
		}
		if selector.betterWallTime(score, lowestWallTime) {
			lowestWallTime = score
		}
		if selector.meetsTargetTime(score) && (!targetBestFound || selector.better(score, bestWithinTarget)) {
			bestWithinTarget = score
			targetBestFound = true
		}
	}

	if targetBestFound {
		return selector.selection(bestWithinTarget, bestWithoutTarget, candidates)
	}
	if selector.targetTime <= 0 {
		return selector.selection(bestWithoutTarget, bestWithoutTarget, candidates)
	}

	selector.maybeWarnTargetTimeUnreachable(lowestWallTime, minParallelism, maxParallelism)
	return selector.selection(lowestWallTime, bestWithoutTarget, candidates)
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

type splitSelection struct {
	selected               splitScore
	bestWithoutTarget      splitScore
	candidates             []splitScore
	parallelRunnerOverhead time.Duration
	targetTime             time.Duration
	available              bool
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

func (s splitSelector) betterWallTime(candidate, currentBest splitScore) bool {
	if candidate.wallTime != currentBest.wallTime {
		return candidate.wallTime < currentBest.wallTime
	}
	if candidate.parallelRunners != currentBest.parallelRunners {
		return candidate.parallelRunners < currentBest.parallelRunners
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

func (s splitSelector) selection(selected, bestWithoutTarget splitScore, candidates []splitScore) splitSelection {
	return splitSelection{
		selected:               selected,
		bestWithoutTarget:      bestWithoutTarget,
		candidates:             candidates,
		parallelRunnerOverhead: s.parallelRunnerOverhead,
		targetTime:             s.targetTime,
		available:              true,
	}
}

func (s splitSelector) meetsTargetTime(score splitScore) bool {
	return s.targetTime > 0 && score.wallTimeDuration() <= s.targetTime
}

func (s splitSelector) maybeWarnTargetTimeUnreachable(best splitScore, minParallelism, maxParallelism int) {
	if s.targetTime <= 0 || s.meetsTargetTime(best) {
		return
	}

	slog.Warn("No parallel runner split meets target time; selecting split with lowest expected wall time",
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
