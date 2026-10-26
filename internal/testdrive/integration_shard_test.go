package testdrive

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Each integration scenario runs a full paired validation with real
// frameworks, so CI spreads a test's scenarios over several runners.
// DDTEST_TEST_SHARD is "index/total" (1-based); unset runs every scenario.
const testShardVariable = "DDTEST_TEST_SHARD"

// defaultScenarioWeight is an ordinary scenario's relative duration. Weights
// keep shards balanced when a few scenarios take several times as long.
const defaultScenarioWeight = 10

type integrationScenarios struct {
	names   []string
	weights map[string]int
}

// selected returns the scenarios assigned to this process's shard, in their
// declared order.
func (s integrationScenarios) selected(t *testing.T) []string {
	t.Helper()
	index, total, err := parseTestShard(os.Getenv(testShardVariable))
	require.NoError(t, err)
	return s.assign(total)[index-1]
}

// assign places every scenario in exactly one of total shards: heaviest
// first, each to the least loaded shard (lowest index on ties).
func (s integrationScenarios) assign(total int) [][]string {
	order := make([]int, len(s.names))
	for i := range order {
		order[i] = i
	}
	weight := func(i int) int {
		if value, ok := s.weights[s.names[i]]; ok {
			return value
		}
		return defaultScenarioWeight
	}
	slices.SortStableFunc(order, func(a, b int) int { return weight(b) - weight(a) })
	loads := make([]int, total)
	shardOf := make([]int, len(s.names))
	for _, i := range order {
		shard := 0
		for candidate := range loads {
			if loads[candidate] < loads[shard] {
				shard = candidate
			}
		}
		loads[shard] += weight(i)
		shardOf[i] = shard
	}
	shards := make([][]string, total)
	for i, name := range s.names {
		shards[shardOf[i]] = append(shards[shardOf[i]], name)
	}
	return shards
}

func parseTestShard(value string) (int, int, error) {
	if value == "" {
		return 1, 1, nil
	}
	index, total, found := strings.Cut(value, "/")
	i, indexErr := strconv.Atoi(index)
	n, totalErr := strconv.Atoi(total)
	if !found || indexErr != nil || totalErr != nil || n < 1 || i < 1 || i > n {
		return 0, 0, fmt.Errorf("%s must be index/total with 1 <= index <= total, got %q", testShardVariable, value)
	}
	return i, n, nil
}

// TestPublicJestTestdrive lives in the external test package. TypeScript
// scenarios run only where ts-jest is installed, after sharding the full list.
var jestWorkflowScenarios = integrationScenarios{
	names:   []string{"passing", "existing failure", "script selection", "multiple selected projects", "sharded threshold script", "native retries", "temporary tracer", "missing Jest", "script startup override", "controlled mismatch", "CJS disabled globals", "ESM disabled globals", "TypeScript", "TypeScript isolated modules", "TypeScript disabled globals"},
	weights: map[string]int{"multiple selected projects": 18, "TypeScript": 15, "temporary tracer": 12, "controlled mismatch": 12, "missing Jest": 1, "script startup override": 1},
}

// JestWorkflowScenarios returns the Jest workflow scenarios for this shard.
func JestWorkflowScenarios(t *testing.T) []string {
	t.Helper()
	return jestWorkflowScenarios.selected(t)
}

var shardedIntegrationScenarios = map[string]integrationScenarios{
	"cypress":    cypressWorkflowScenarios,
	"jest":       jestWorkflowScenarios,
	"cucumber":   cucumberWorkflowScenarios,
	"mocha":      mochaWorkflowScenarios,
	"playwright": playwrightWorkflowScenarios,
}

func TestIntegrationShardsRunEveryScenarioExactlyOnce(t *testing.T) {
	for framework, scenarios := range shardedIntegrationScenarios {
		for name := range scenarios.weights {
			require.Contains(t, scenarios.names, name, "%s weight names a scenario that doesn't exist", framework)
		}
		for total := 1; total <= 6; total++ {
			var all []string
			for _, shard := range scenarios.assign(total) {
				all = append(all, shard...)
			}
			require.ElementsMatch(t, scenarios.names, all, "%s over %d shards", framework, total)
		}
	}
}

func TestIntegrationShardsBalanceWeightedScenarios(t *testing.T) {
	scenarios := integrationScenarios{names: []string{"recovery", "a", "b", "c", "d"}, weights: map[string]int{"recovery": 30}}
	// Ties go to the lower shard: after a, b and c both shards weigh 30.
	require.Equal(t, [][]string{{"recovery", "d"}, {"a", "b", "c"}}, scenarios.assign(2))
	require.Equal(t, [][]string{{"recovery"}, {"a", "c"}, {"b", "d"}}, scenarios.assign(3))
}

func TestTestShardVariableIsValidated(t *testing.T) {
	for _, value := range []string{"0/2", "3/2", "1", "a/b", "1/0"} {
		_, _, err := parseTestShard(value)
		require.Error(t, err, value)
	}
	index, total, err := parseTestShard("2/5")
	require.NoError(t, err)
	require.Equal(t, []int{2, 5}, []int{index, total})
}

// TestPrintIntegrationShards lists each sharded test's scenarios per shard,
// for reviewing the CI split without running any framework:
// DDTEST_PRINT_SHARDS=5 go test ./internal/testdrive -run TestPrintIntegrationShards -v
func TestPrintIntegrationShards(t *testing.T) {
	total, err := strconv.Atoi(os.Getenv("DDTEST_PRINT_SHARDS"))
	if err != nil || total < 1 {
		t.Skip("set DDTEST_PRINT_SHARDS to a shard count")
	}
	for _, framework := range slices.Sorted(maps.Keys(shardedIntegrationScenarios)) {
		for i, shard := range shardedIntegrationScenarios[framework].assign(total) {
			t.Logf("%s %d/%d: %s", framework, i+1, total, strings.Join(shard, ", "))
		}
	}
}
