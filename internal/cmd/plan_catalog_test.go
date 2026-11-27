package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/telemetry"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func catalogTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "plan", Args: validatePlanArguments, RunE: func(cmd *cobra.Command, _ []string) error {
		return runCatalogPlan(cmd, telemetry.NoopClient())
	}}
	addCatalogPlanFlags(cmd)
	return cmd
}

func TestPlanCatalogUsesWorkerContextsWithoutChangingSavedPlan(t *testing.T) {
	t.Chdir(t.TempDir())
	viper.Reset()
	settings.Init()
	environment.ResetCITags()
	t.Cleanup(func() { viper.Reset(); settings.Init(); environment.ResetCITags() })
	for _, args := range [][]string{
		{"init", "-q"}, {"remote", "add", "origin", "https://example.com/org/repo.git"},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-qm", "initial"},
	} {
		output, err := exec.Command("git", args...).CombinedOutput()
		require.NoError(t, err, string(output))
	}
	describeTestFile(t, "tests/feature/test_case.py", "raise AssertionError('must not collect')\n")
	describeTestFile(t, "generated.yml", `feature:
  script: [scripts/run-tests]
  variables:
    TEST_SUITE: feature
    TEST_ENVIRONMENTS_1: "py312 py313 missing mismatch"
support:
  script: [build]
`)
	tags := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &tags))
	tags["test.configuration.variant"] = "first"
	other := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &other))
	other["runtime.version"] = "3.13.1"
	metadata := map[string]any{"suites": []any{map[string]any{"name": "feature", "venvs": []any{
		map[string]any{"hash": "py312", "python_version": "3.12", "command": "pytest tests/feature {cmdargs}", "configurations": tags},
		map[string]any{"hash": "py313", "python_version": "3.13", "command": "pytest tests/feature {cmdargs}", "configurations": other},
		map[string]any{"hash": "missing", "python_version": "3.12", "command": "pytest tests/feature {cmdargs}"},
		map[string]any{"hash": "mismatch", "python_version": "3.12", "command": "pytest tests/feature {cmdargs}", "configurations": other},
	}}}}
	data, err := json.Marshal(metadata)
	require.NoError(t, err)
	describeTestFile(t, "environments.json", string(data))
	sentinel := ".testoptimization/cache/http/settings.json"
	describeTestFile(t, sentinel, "existing cache")
	describeTestFile(t, ".testoptimization/runner/test-files.txt", "existing worker plan")
	var mu sync.Mutex
	configurations := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/libraries/tests/services/setting":
			_, _ = w.Write([]byte(`{"data":{"attributes":{"itr_enabled":true,"tests_skipping":true}}}`))
		case "/api/v2/ci/tests/skippable":
			var body struct {
				Data struct {
					Attributes struct {
						Level          string         `json:"test_level"`
						Configurations map[string]any `json:"configurations"`
					} `json:"attributes"`
				} `json:"data"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Data.Attributes.Level != "suite" {
				t.Error("expected suite-level request")
			}
			mu.Lock()
			configurations = append(configurations, body.Data.Attributes.Configurations)
			mu.Unlock()
			if body.Data.Attributes.Configurations["runtime.version"] == "3.12.9" {
				_, _ = w.Write([]byte(`{"data":[{"type":"suite","attributes":{"suite":"test_case.py","configurations":{"test.bundle":"tests.feature"}}}]}`))
			} else {
				_, _ = w.Write([]byte(`{"data":[]}`))
			}
		case "/api/v2/ci/ddtest/test_suite_durations":
			_, _ = w.Write([]byte(`{"data":{"attributes":{"test_suites":{"tests.feature":{"test_case.py":{"source_file":"tests/feature/test_case.py"}}}}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	}))
	defer server.Close()
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_ENABLED", "true")
	t.Setenv("DD_CIVISIBILITY_AGENTLESS_URL", server.URL)
	t.Setenv("DD_API_KEY", "fixture")
	t.Setenv("DD_TAGS", "test.configuration.host:must-not-leak")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	guard := t.TempDir()
	gitBinary, err := exec.LookPath("git")
	require.NoError(t, err)
	require.NoError(t, os.Symlink(gitBinary, filepath.Join(guard, "git")))
	t.Setenv("PATH", guard)
	command := catalogTestCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--ci-provider", "gitlab", "--ci-config", "generated.yml", "--command-environments", "environments.json", "--output", "selection.json"})
	require.NoError(t, command.Execute())
	var result catalogSelection
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Len(t, result.Commands, 4)
	require.Equal(t, "skip", result.Commands[0].Decision)
	require.Empty(t, result.Commands[0].RunnableFiles)
	require.NotNil(t, result.Commands[0].RunnableFiles)
	require.Equal(t, "run", result.Commands[1].Decision)
	require.Equal(t, []string{"tests/feature/test_case.py"}, result.Commands[1].RunnableFiles)
	require.Contains(t, result.Commands[2].Reason, "missing worker configuration")
	require.Contains(t, result.Commands[3].Reason, "worker Python version differs")
	require.Equal(t, "run", result.Jobs[0].Decision)
	require.Equal(t, "run", result.Jobs[1].Decision)
	mu.Lock()
	require.Len(t, configurations, 2)
	require.Equal(t, map[string]any{"variant": "first"}, configurations[0]["custom"])
	require.NotContains(t, configurations[1], "custom")
	mu.Unlock()
	existing, err := os.ReadFile(sentinel)
	require.NoError(t, err)
	require.Equal(t, "existing cache", string(existing))
	existing, err = os.ReadFile(".testoptimization/runner/test-files.txt")
	require.NoError(t, err)
	require.Equal(t, "existing worker plan", string(existing))
	written, err := os.ReadFile("selection.json")
	require.NoError(t, err)
	require.Equal(t, output.Bytes(), written)
	config, err := os.ReadFile("generated.yml")
	require.NoError(t, err)
	describeTestFile(t, "generated.yml", strings.ReplaceAll(string(config), "py312 py313 missing mismatch", "py312"))
	command = catalogTestCommand()
	output.Reset()
	command.SetOut(&output)
	command.SetArgs([]string{"--ci-provider", "gitlab", "--ci-config", "generated.yml", "--command-environments", "environments.json", "--output", "selection.json"})
	require.NoError(t, command.Execute())
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "skip", result.Jobs[0].Decision)
	require.Equal(t, "run", result.Jobs[1].Decision)
}

func TestPlanCatalogRejectsIncompleteOrMixedArguments(t *testing.T) {
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	for _, args := range [][]string{
		{"--ci-provider", "gitlab"},
		{"--output", "selection.json"},
		{"--ci-provider", "gitlab", "--ci-config", "generated.yml", "--command-environments", "env.json", "--output", "selection.json", "tests"},
	} {
		command := catalogTestCommand()
		command.SetArgs(args)
		require.Error(t, command.Execute())
	}
}

func TestCatalogRequiresDeclaredCustomConfigurations(t *testing.T) {
	t.Setenv("DD_TAGS", "test.configuration.variant:worker")
	job := onboard.PythonCIJob{CICommandJob: onboard.CICommandJob{Variables: map[string]string{"DD_TAGS": os.Getenv("DD_TAGS")}}}
	tags := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &tags))
	command := onboard.PythonCICommand{PythonCatalogCommand: onboard.PythonCatalogCommand{Cwd: "."}, PythonVersion: "3.12", CandidateTestFiles: []string{"tests/test_case.py"}, Configurations: tags}
	require.Contains(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"), "worker custom configuration differs")
	tags["test.configuration.variant"] = "other"
	require.Contains(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"), "worker custom configuration differs")
	tags["test.configuration.variant"] = "worker"
	require.Empty(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"))
}

func TestCatalogKeepsExplicitEmptyWorkerIdentityRunnable(t *testing.T) {
	tags := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &tags))
	command := onboard.PythonCICommand{PythonCatalogCommand: onboard.PythonCatalogCommand{Cwd: "."}, PythonVersion: "3.12", CandidateTestFiles: []string{"tests/test_case.py"}, Configurations: tags}
	for _, key := range []string{"DD_SERVICE", "DD_ENV", "DD_TAGS"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "planner")
			job := onboard.PythonCIJob{CICommandJob: onboard.CICommandJob{Variables: map[string]string{key: ""}}}
			require.Contains(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"), "worker identity override")
			delete(job.Variables, key)
			require.Empty(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"))
		})
	}
}

func TestCatalogUsesEffectiveWorkerIdentityDefaults(t *testing.T) {
	t.Setenv("DD_SERVICE", "")
	t.Setenv("DD_ENV", "")
	tags := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(describeRuntimeTags), &tags))
	command := onboard.PythonCICommand{PythonCatalogCommand: onboard.PythonCatalogCommand{Cwd: "."}, PythonVersion: "3.12", CandidateTestFiles: []string{"tests/test_case.py"}, Configurations: tags}
	job := onboard.PythonCIJob{CICommandJob: onboard.CICommandJob{Variables: map[string]string{"DD_SERVICE": "repo", "DD_ENV": "none"}}}
	require.Empty(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"))
	job.Variables["DD_ENV"] = "ci"
	require.Contains(t, commandPlanningGap(job, command, "https://example.com/org/repo.git"), "worker identity override")
}
