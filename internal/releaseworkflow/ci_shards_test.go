package releaseworkflow

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

type ciTestJob struct {
	If       string
	Needs    any
	Strategy struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct{ Shard []any }
	}
	Steps []struct {
		Name, Run, Uses string
		Env             map[string]string
	}
}

func ciTestJobs(t *testing.T) map[string]ciTestJob {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	require.NoError(t, err)
	var workflow struct{ Jobs map[string]ciTestJob }
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	return workflow.Jobs
}

func TestRequiredCIChecksRejectFailedCancelledAndSkippedShards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the CI gates run in Ubuntu bash")
	}
	jobs := ciTestJobs(t)
	for _, name := range []string{"test", "test-ddtest-instrument"} {
		job := jobs[name]
		require.Equal(t, "${{ always() }}", job.If)
		require.NotEmpty(t, job.Steps)
		gate := job.Steps[0]
		dependency := "test-shards"
		if name == "test" {
			require.Equal(t, []any{"test-shards", "test-support"}, job.Needs)
			require.Equal(t, "${{ needs.test-support.result }}", gate.Env["SUPPORT_RESULT"])
		} else {
			dependency = "test-ddtest-instrument-shards"
			require.Equal(t, dependency, job.Needs)
		}
		require.Equal(t, "${{ needs."+dependency+".result }}", gate.Env["SHARDS_RESULT"])
		for _, shard := range []string{"success", "failure", "cancelled", "skipped"} {
			for _, support := range []string{"success", "failure", "cancelled", "skipped"} {
				command := exec.CommandContext(t.Context(), "bash", "-eu", "-c", gate.Run)
				command.Env = append(os.Environ(), "SHARDS_RESULT="+shard, "SUPPORT_RESULT="+support)
				err := command.Run()
				passed := shard == "success" && (name != "test" || support == "success")
				require.Equal(t, passed, err == nil, "%s: shards=%s support=%s", name, shard, support)
			}
		}
	}
}

func TestGoCIShardsKeepRaceCoverageAndBothInstrumentationPaths(t *testing.T) {
	jobs := ciTestJobs(t)
	for _, name := range []string{"test-shards", "test-ddtest-instrument-shards"} {
		job := jobs[name]
		require.Equal(t, []any{1, 2, 3}, job.Strategy.Matrix.Shard)
		require.False(t, job.Strategy.FailFast)
		command := ""
		for _, step := range job.Steps {
			if step.Name == "Run tests" {
				command = step.Run
			}
		}
		for _, flag := range []string{"-race", "-coverpkg=./...", "-coverprofile=coverage.out", "-covermode=atomic", "-run"} {
			require.Contains(t, command, flag, name)
		}
		require.Equal(t, name == "test-ddtest-instrument-shards", strings.HasPrefix(command, "ddtest instrument go test"))
	}
}
