package runner

import (
	"log/slog"
	"math"

	"github.com/DataDog/ddtest/internal/settings"
)

// calculateParallelRunners determines the number of parallel runners based on skippable percentage
// and parallelism configuration
func calculateParallelRunners(skippablePercentage float64) int {
	return calculateParallelRunnersWithParams(skippablePercentage, settings.GetMinParallelism(), settings.GetMaxParallelism())
}

func calculateParallelRunnersWithParams(skippablePercentage float64, minParallelism, maxParallelism int) int {
	// maxParallelism could be 0 or negative!
	if maxParallelism <= 1 {
		return 1
	}

	if minParallelism < 1 {
		slog.Warn("min_parallelism is less than 1, setting to 1", "min_parallelism", minParallelism)
		return 1
	}

	if maxParallelism < minParallelism {
		slog.Warn("max_parallelism is less than min_parallelism, clamping min to max",
			"max_parallelism", maxParallelism, "min_parallelism", minParallelism)
		minParallelism = maxParallelism
	}

	percentage := math.Max(0.0, math.Min(100.0, skippablePercentage)) // Clamp to [0, 100]
	runners := float64(maxParallelism) - (percentage/100.0)*float64(maxParallelism-minParallelism)

	return int(math.Round(runners))
}
