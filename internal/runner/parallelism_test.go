package runner

import "testing"

// Helper function to run calculateParallelRunnersWithParams tests
func testCalculateParallelRunners(skippablePercentage float64, minParallelism, maxParallelism int) int {
	return calculateParallelRunnersWithParams(skippablePercentage, minParallelism, maxParallelism)
}

func TestCalculateParallelRunners_MaxParallelismIsOne(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable", 0.0, 1},
		{"50% skippable", 50.0, 1},
		{"100% skippable", 100.0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 1, 1)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MaxParallelismZeroOrNegative(t *testing.T) {
	tests := []struct {
		name           string
		maxParallelism int
	}{
		{"maxParallelism is 0", 0},
		{"maxParallelism is -1", -1},
		{"maxParallelism is -100", -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should always return 1 regardless of skippable percentage
			result := testCalculateParallelRunners(0.0, 1, tt.maxParallelism)
			if result != 1 {
				t.Errorf("calculateParallelRunners(0.0) with maxParallelism=%d = %d, expected 1", tt.maxParallelism, result)
			}

			result = testCalculateParallelRunners(50.0, 1, tt.maxParallelism)
			if result != 1 {
				t.Errorf("calculateParallelRunners(50.0) with maxParallelism=%d = %d, expected 1", tt.maxParallelism, result)
			}

			result = testCalculateParallelRunners(100.0, 1, tt.maxParallelism)
			if result != 1 {
				t.Errorf("calculateParallelRunners(100.0) with maxParallelism=%d = %d, expected 1", tt.maxParallelism, result)
			}
		})
	}
}

func TestCalculateParallelRunners_MinParallelismLessThanOne(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable with min<1", 0.0, 1},
		{"50% skippable with min<1", 50.0, 1},
		{"100% skippable with min<1", 100.0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 0, 5)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MaxLessThanMin(t *testing.T) {
	// When max < min, min is clamped to max. This ensures that a user who only
	// sets --max-parallelism to a lower value gets the expected behavior.
	result := testCalculateParallelRunners(50.0, 5, 3) // max < min
	expected := 3                                      // Should clamp min to max and return max
	if result != expected {
		t.Errorf("calculateParallelRunners(50.0) = %d, expected %d when max < min", result, expected)
	}
}

func TestCalculateParallelRunners_LinearInterpolation(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable -> max parallelism", 0.0, 8},
		{"25% skippable", 25.0, 7}, // 8 - 0.25 * (8-2) = 8 - 1.5 = 6.5 -> 7
		{"50% skippable", 50.0, 5}, // 8 - 0.5 * (8-2) = 8 - 3 = 5
		{"75% skippable", 75.0, 4}, // 8 - 0.75 * (8-2) = 8 - 4.5 = 3.5 -> 4
		{"100% skippable -> min parallelism", 100.0, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 2, 8)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_EdgeCases(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"Negative percentage", -10.0, 10}, // Should clamp to 0%
		{"Over 100%", 150.0, 3},            // Should clamp to 100%
		{"Exact boundary 0%", 0.0, 10},
		{"Exact boundary 100%", 100.0, 3},
		{"Fractional result rounds", 33.33, 8}, // 10 - 0.3333 * 7 = 7.67 -> 8
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 3, 10)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_MinEqualsMax(t *testing.T) {
	tests := []struct {
		name                string
		skippablePercentage float64
		expected            int
	}{
		{"0% skippable", 0.0, 4},
		{"50% skippable", 50.0, 4},
		{"100% skippable", 100.0, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, 4, 4)
			if result != tt.expected {
				t.Errorf("calculateParallelRunners(%f) = %d, expected %d", tt.skippablePercentage, result, tt.expected)
			}
		})
	}
}

func TestCalculateParallelRunners_RealWorldScenarios(t *testing.T) {
	tests := []struct {
		name                string
		minParallelism      int
		maxParallelism      int
		skippablePercentage float64
		expected            int
		description         string
	}{
		{"Small project", 1, 4, 25.0, 3, "25% skippable in small project"},
		{"Medium project", 2, 12, 60.0, 6, "60% skippable in medium project"},
		{"Large project", 4, 32, 80.0, 10, "80% skippable in large project"},
		{"CI with high parallelism", 8, 64, 90.0, 14, "90% skippable with high parallelism"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := testCalculateParallelRunners(tt.skippablePercentage, tt.minParallelism, tt.maxParallelism)
			if result != tt.expected {
				t.Errorf("%s: calculateParallelRunners(%f) = %d, expected %d",
					tt.description, tt.skippablePercentage, result, tt.expected)
			}

			// Verify result is within bounds
			if result < tt.minParallelism {
				t.Errorf("%s: result %d is less than min_parallelism %d", tt.description, result, tt.minParallelism)
			}
			if result > tt.maxParallelism {
				t.Errorf("%s: result %d is greater than max_parallelism %d", tt.description, result, tt.maxParallelism)
			}
		})
	}
}
