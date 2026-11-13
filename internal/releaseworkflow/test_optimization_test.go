package releaseworkflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestSharedCLIIncludesTheInstrumentationOnLinuxAndWindows(t *testing.T) {
	job := ciTestJobs(t)["test-binary"]
	build := ""
	artifacts := map[string]string{}
	for _, step := range job.Steps {
		if step.Run != "" {
			build = step.Run
		}
		if step.With["name"] != "" {
			artifacts[step.With["name"]] = step.With["path"]
		}
	}
	require.Contains(t, build, "go -C ddto run ./scripts/release -main .. -name ddtest -command instrument")
	require.Contains(t, build, "-target linux/amd64,windows/amd64")
	require.Equal(t, "${{ runner.temp }}/test-binary/ddtest", artifacts["integration-cli"])
	require.Equal(t, "${{ runner.temp }}/test-binary/ddtest.exe", artifacts["integration-cli-windows"])
}

func TestTestOptimizationSetupKeepsCredentialsOutOfExportedSettings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the setup script is exercised with hosted-runner bash")
	}
	data, err := os.ReadFile("../../.github/actions/setup-test-optimization/action.yml")
	require.NoError(t, err)
	var action struct {
		Runs struct {
			Steps []struct {
				Run  string
				With map[string]string
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &action))
	require.Len(t, action.Runs.Steps, 2)
	require.Equal(t, "${{ runner.os == 'Windows' && 'integration-cli-windows' || 'integration-cli' }}", action.Runs.Steps[0].With["name"])
	for _, platform := range []string{"Linux", "Windows"} {
		for _, key := range []string{"", "test-secret-key"} {
			t.Run(platform+"/"+map[bool]string{true: "configured", false: "missing-key"}[key != ""], func(t *testing.T) {
				directory := t.TempDir()
				binary := filepath.Join(directory, "test-binary", "ddtest")
				if platform == "Windows" {
					binary += ".exe"
				}
				require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0700))
				require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n[ \"$1 $2\" = 'instrument --version' ]\n"), 0755))
				environment, path := filepath.Join(directory, "env"), filepath.Join(directory, "path")
				command := exec.CommandContext(t.Context(), "bash", "-eu", "-c", action.Runs.Steps[1].Run)
				command.Env = append(os.Environ(), "RUNNER_TEMP="+directory, "RUNNER_OS="+platform,
					"DD_API_KEY="+key, "SESSION_NAME=cucumber-compatibility (Cucumber 10.9.0)",
					"CHECK_RUN_ID=123", "GITHUB_ENV="+environment, "GITHUB_PATH="+path)
				output, err := command.CombinedOutput()
				require.NoError(t, err, string(output))
				data, err := os.ReadFile(environment)
				require.NoError(t, err)
				settings := string(data)
				enabled := "false"
				if key != "" {
					enabled = "parent"
				}
				require.Contains(t, settings, "DD_CIVISIBILITY_ENABLED="+enabled+"\n")
				require.Contains(t, settings, "DDTEST_TEST_BINARY="+binary+"\n")
				require.Contains(t, settings, "DD_TEST_SESSION_NAME=cucumber-compatibility (Cucumber 10.9.0)\n")
				require.Contains(t, settings, "JOB_CHECK_RUN_ID=123\n")
				require.Contains(t, settings, "DD_CIVISIBILITY_AGENTLESS_ENABLED=true\n")
				require.Contains(t, settings, "DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED=false\n")
				require.NotContains(t, settings, "DD_API_KEY=")
				// Not test-shards' ddtest, where Test Impact Analysis skips tests.
				require.Contains(t, settings, "DD_SERVICE=ddtest-integration\n")
				require.NotContains(t, settings+string(output), "test-secret-key")
				data, err = os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, filepath.Dir(binary)+"\n", string(data))
			})
		}
	}
}
