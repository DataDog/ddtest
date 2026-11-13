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
	If          string
	Needs       any
	Uses        string
	Env         map[string]string
	Permissions map[string]string
	Strategy    struct {
		FailFast bool `yaml:"fail-fast"`
		Matrix   struct{ Shard []any }
	}
	Steps []struct {
		Name, Run, Uses string
		ID              string `yaml:"id"`
		Env             map[string]string
		With            map[string]string
	}
}

func TestCompilerCacheRefreshesForEachRevision(t *testing.T) {
	job := ciTestJobs(t)["test-shards"]
	key := ""
	for _, step := range job.Steps {
		if step.Name == "Set up Go" {
			require.Equal(t, "go", step.ID)
			require.Equal(t, "false", step.With["cache"])
		}
		if step.Name == "Cache Go modules" {
			key = step.With["key"]
			for _, input := range []string{"runner.os", "runner.arch", "steps.go.outputs.go-version", "github.sha"} {
				require.Contains(t, key, "${{ "+input+" }}")
			}
			require.Contains(t, step.With["path"], "~/.cache/go-build")
			require.NotEmpty(t, step.With["restore-keys"])
			require.Equal(t, strings.TrimSuffix(key, "${{ github.sha }}"), strings.Split(strings.TrimSpace(step.With["restore-keys"]), "\n")[0])
		}
	}
	require.NotEmpty(t, key)
}

func TestReleaseRequiresGreenMainWithoutRerunningMatrices(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	require.NoError(t, err)
	var workflow struct {
		Jobs map[string]ciTestJob
		On   struct {
			Dispatch struct {
				Inputs map[string]struct {
					Type, Default string
					Required      bool
					Options       []string
				}
			} `yaml:"workflow_dispatch"`
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Len(t, workflow.On.Dispatch.Inputs, 1)
	input := workflow.On.Dispatch.Inputs["release_type"]
	require.Equal(t, "choice", input.Type)
	require.Equal(t, "minor", input.Default)
	require.True(t, input.Required)
	require.Equal(t, []string{"patch", "minor"}, input.Options)
	require.Len(t, workflow.Jobs, 1)
	job := workflow.Jobs["release"]
	require.Equal(t, "${{ inputs.release_type }}", job.Env["RELEASE_TYPE"])
	require.NotContains(t, job.Env, "VERSION")
	require.Equal(t, "read", job.Permissions["actions"])
	checks, build, create := []int{}, -1, -1
	for index, step := range job.Steps {
		if strings.Contains(step.Run, "check_release_ci.py") {
			checks = append(checks, index)
			require.Equal(t, "${{ github.token }}", step.Env["GH_TOKEN"])
		}
		if step.Name == "Checkout code" {
			require.Equal(t, "${{ github.sha }}", step.With["ref"])
		}
		if step.Name == "Build binaries" {
			build = index
		}
		if step.Name == "Create Release and Upload Assets" {
			create = index
		}
	}
	require.Len(t, checks, 2)
	require.Less(t, checks[0], build)
	require.Greater(t, checks[1], build)
	require.Less(t, checks[1], create)
}

func TestReleaseCIGate(t *testing.T) {
	cmd := exec.Command("python3", "-B", "-m", "unittest", "discover", "-s", "../../.github/scripts", "-p", "test_check_release_ci.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release CI gate tests failed: %v\n%s", err, output)
	}
}

func TestRootGoTestJobsUseTestOptimization(t *testing.T) {
	jobs := ciTestJobs(t)
	for name, job := range jobs {
		require.NotContains(t, job.Env, "DD_API_KEY", "%s must not expose the reporting key to every step", name)
		for _, step := range job.Steps {
			if step.Env["DD_API_KEY"] == "" {
				continue
			}
			require.True(t, step.Uses == "./.github/actions/setup-test-optimization" || strings.Contains(step.Run, "ddtest instrument go test"), "%s / %s exposes the reporting key outside instrumentation", name, step.Name)
			require.NotRegexp(t, `\b(npm (ci|install)|npx\b.*\binstall|gem install|pip install|go install)\b`, step.Run, "%s / %s installs dependencies with the reporting key", name, step.Name)
		}
		if name == "test-shards" {
			continue // Its instrumenting build and coverage flags are checked below.
		}
		hasTests := false
		for _, step := range job.Steps {
			hasTests = hasTests || strings.Contains(step.Run, "go test")
		}
		if !hasTests {
			continue
		}
		switch dependencies := job.Needs.(type) {
		case string:
			require.Equal(t, "test-binary", dependencies, name)
		case []any:
			require.Contains(t, dependencies, "test-binary", name)
		default:
			t.Fatalf("%s has no shared binary dependency", name)
		}
		configured := false
		for _, step := range job.Steps {
			if step.Uses == "./.github/actions/setup-test-optimization" {
				configured = true
				require.Equal(t, "${{ secrets.DATADOG_API_KEY }}", step.Env["DD_API_KEY"], name)
				require.True(t, strings.HasPrefix(step.With["session-name"], name), name)
			}
			if strings.Contains(step.Run, "go test") {
				require.Equal(t, "${{ secrets.DATADOG_API_KEY }}", step.Env["DD_API_KEY"], name)
				require.True(t, configured, "%s tests run before reporting is configured", name)
				require.NotContains(t, strings.ReplaceAll(step.Run, "ddtest instrument go test", ""), "go test", name)
			}
		}
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
	job := ciTestJobs(t)["test"]
	require.Equal(t, "${{ always() }}", job.If)
	require.Equal(t, []any{"test-shards", "test-support"}, job.Needs)
	require.NotEmpty(t, job.Steps)
	gate := job.Steps[0]
	require.Equal(t, "${{ needs.test-shards.result }}", gate.Env["SHARDS_RESULT"])
	require.Equal(t, "${{ needs.test-support.result }}", gate.Env["SUPPORT_RESULT"])
	for _, shard := range []string{"success", "failure", "cancelled", "skipped"} {
		for _, support := range []string{"success", "failure", "cancelled", "skipped"} {
			command := exec.CommandContext(t.Context(), "bash", "-eu", "-c", gate.Run)
			command.Env = append(os.Environ(), "SHARDS_RESULT="+shard, "SUPPORT_RESULT="+support)
			err := command.Run()
			passed := shard == "success" && support == "success"
			require.Equal(t, passed, err == nil, "shards=%s support=%s", shard, support)
		}
	}
}

func TestGoCIShardsReportThroughDdtestInstrumentWithRaceCoverage(t *testing.T) {
	job := ciTestJobs(t)["test-shards"]
	require.Equal(t, []any{1, 2, 3}, job.Strategy.Matrix.Shard)
	require.False(t, job.Strategy.FailFast)
	command := ""
	for _, step := range job.Steps {
		require.NotContains(t, step.Uses, "test-visibility-github-action", "Orchestrion must not report the suite")
		if step.Name == "Run tests" {
			command = step.Run
		}
	}
	require.True(t, strings.HasPrefix(command, "ddtest instrument go test"), command)
	for _, flag := range []string{"-race", "-coverpkg=" + coveredPackages, "-coverprofile=coverage.out", "-covermode=atomic"} {
		require.Contains(t, command, flag)
	}
	// ddtest instrument backfills the coverage of skipped tests only without
	// flags that narrow the run, so the shards split by package.
	for _, flag := range []string{"-run", "-skip", "-short"} {
		require.NotContains(t, command, " "+flag)
	}
}

// coveredPackages names the module's packages without ./..., which keeps ddtest
// instrument from using its in-memory per-test coverage: ddto's directory lies
// below the module's.
const coveredPackages = "github.com/DataDog/ddtest,github.com/DataDog/ddtest/internal/..."

func TestCoveredPackagesIncludeEveryPackageOfTheModule(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "go", "list", "../../...").Output()
	require.NoError(t, err)
	for _, name := range strings.Fields(string(output)) {
		if name != "github.com/DataDog/ddtest" {
			require.True(t, strings.HasPrefix(name, "github.com/DataDog/ddtest/internal/"), "%s is outside -coverpkg", name)
		}
	}
}
