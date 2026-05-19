package runner

import (
	"fmt"
	"testing"
)

func testCalculateParallelRunners(testFileWeights map[string]int, minParallelism, maxParallelism int) int {
	return calculateParallelRunnersWithParams(testFileWeights, minParallelism, maxParallelism)
}

func TestCalculateParallelRunners_MaxParallelismIsOne(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
	}

	for _, maxParallelism := range []int{1, 0, -1, -100} {
		t.Run(fmt.Sprintf("maxParallelism=%d", maxParallelism), func(t *testing.T) {
			result := testCalculateParallelRunners(testFileWeights, 1, maxParallelism)
			if result != 1 {
				t.Errorf("calculateParallelRunners() with maxParallelism=%d = %d, expected 1", maxParallelism, result)
			}
		})
	}
}

func TestCalculateParallelRunners_MinParallelismLessThanOne(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
		"test4.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 0, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 when min_parallelism is normalized to 1", result)
	}
}

func TestCalculateParallelRunners_MaxLessThanMin(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
		"test4.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 5, 3)
	if result != 3 {
		t.Errorf("calculateParallelRunners() = %d, expected 3 when max_parallelism is clamped", result)
	}
}

func TestCalculateParallelRunners_MinEqualsMax(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 4, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 when min and max match", result)
	}
}

func TestCalculateParallelRunners_EmptyTestFiles(t *testing.T) {
	result := testCalculateParallelRunners(map[string]int{}, 2, 8)
	if result != 2 {
		t.Errorf("calculateParallelRunners() = %d, expected normalized min parallelism 2 for empty tests", result)
	}
}

func TestCalculateParallelRunners_WallTimeWins(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 10,
		"test3.rb": 10,
		"test4.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 1, 4)
	if result != 4 {
		t.Errorf("calculateParallelRunners() = %d, expected 4 to minimize slowest runner time", result)
	}
}

func TestCalculateParallelRunners_ImbalanceBreaksWallTimeTie(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
		"test2.rb": 6,
		"test3.rb": 6,
		"test4.rb": 6,
		"test5.rb": 6,
	}

	result := testCalculateParallelRunners(testFileWeights, 3, 4)
	if result != 3 {
		t.Errorf("calculateParallelRunners() = %d, expected 3 because it keeps the same wall time with lower imbalance", result)
	}
}

func TestCalculateParallelRunners_OverParallelizedInputsPreserveMinimum(t *testing.T) {
	testFileWeights := map[string]int{
		"test1.rb": 10,
	}

	result := testCalculateParallelRunners(testFileWeights, 2, 20)
	if result != 2 {
		t.Errorf("calculateParallelRunners() = %d, expected 2 because min_parallelism is unavoidable", result)
	}
}

func BenchmarkCalculateParallelRunners20000TestFiles(b *testing.B) {
	testFileWeights := make(map[string]int, 20000)
	for i := range 20000 {
		testFileWeights[fmt.Sprintf("test/%05d_test.rb", i)] = (i % 1000) + 1
	}

	b.ResetTimer()
	for range b.N {
		_ = calculateParallelRunnersWithParams(testFileWeights, 1, 256)
	}
}
