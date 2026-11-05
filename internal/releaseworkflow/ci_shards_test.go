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
	Uses     string
	Strategy struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct{ Shard []any }
	}
	Steps []struct {
		Name, Run, Uses string
		Env             map[string]string
	}
}

func TestReleaseUsesTheSameCIAndRetainsTheInstrumentationGate(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	require.NoError(t, err)
	var workflow struct{ Jobs map[string]ciTestJob }
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Equal(t, "./.github/workflows/ci.yml", workflow.Jobs["ci"].Uses)
	require.Equal(t, "./.github/workflows/go-instrumentation.yml", workflow.Jobs["compatibility"].Uses)
	require.Equal(t, []any{"ci", "compatibility"}, workflow.Jobs["release"].Needs)
}

func TestPublicCLIJobsShareOneExecutable(t *testing.T) {
	jobs := ciTestJobs(t)
	for _, name := range []string{"jest-testdrive-workflow", "testdrive-language-workflow", "ruby-testdrive",
		"java-testdrive", "testdrive-vitest-workflow", "onboard-apply-workflow", "testdrive-mocha-workflow",
		"testdrive-playwright-workflow", "testdrive-cypress-workflow", "testdrive-cucumber-workflow", "testdrive-webdriverio-workflow"} {
		job := jobs[name]
		switch dependencies := job.Needs.(type) {
		case string:
			require.Equal(t, "test-binary", dependencies, name)
		case []any:
			require.Contains(t, dependencies, "test-binary", name)
		default:
			t.Fatalf("%s has no shared binary dependency", name)
		}
		artifact, executable := false, false
		for _, step := range job.Steps {
			artifact = artifact || strings.HasPrefix(step.Uses, "actions/download-artifact@")
			executable = executable || strings.Contains(step.Run, "DDTEST_TEST_BINARY=")
		}
		require.True(t, artifact, name)
		require.True(t, executable, name)
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
