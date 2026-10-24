package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestWorkflowExecutionBlockersRemainTypedAndNeverCertifySuccess(t *testing.T) {
	for _, tc := range []struct {
		name, code, action string
		failure            error
	}{
		{"metadata", "registry_permission", "request_capability", &platform.MetadataError{Code: "registry_permission", Err: errors.New("permission denied")}},
		{"process capability", "process_unavailable", "request_capability", &processSupervisionError{Code: "process_unavailable", Reason: "process ownership unavailable"}},
		{"detached command", "unsupported_detached_spawn", "report_result", &processSupervisionError{Code: "unsupported_detached_spawn", Reason: "detached command"}},
		{"report conflict", "execution_result_conflict", "report_result", errors.New("execution failed despite a complete report")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
			drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", tracerVersion: "6.20.1", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
			drive.workflowRun = func(_ context.Context, out io.Writer) error {
				require.NoError(t, finishValidation(out, root, completeValidationFixture("vitest")))
				return tc.failure
			}
			var output bytes.Buffer
			require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			require.Equal(t, tc.code, result.Code)
			require.Equal(t, tc.action, result.NextAction)
			require.Empty(t, result.FinalResponse)
			require.NotEqual(t, "complete_locally", result.Status)
		})
	}
}

func TestOnboardNamedSetupBlockersReturnExecutableRecovery(t *testing.T) {
	for _, scenario := range []string{"missing coverage dependency", "custom environment", "metadata permission", "dirty workflow"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			runner := framework.Framework(&framework.Vitest{})
			script := "vitest run"
			step := "      - run: npm test\n"
			if scenario == "missing coverage dependency" {
				runner = &framework.Cucumber{}
				script = "cucumber-js features"
			}
			if scenario == "custom environment" {
				step += "        env: {NODE_OPTIONS: --conditions=customer}\n"
			}
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true,"scripts":{"test":"`+script+`"}}`)
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
			path := filepath.Join(root, ".github/workflows/test.yml")
			workflow := "jobs:\n  test:\n    steps:\n" + step
			requireWriteFile(t, path, workflow)
			if scenario == "dirty workflow" {
				// An untracked workflow in a Git checkout is an uncommitted target.
				output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
				require.NoError(t, err, string(output))
			}
			drive := &Testdrive{repositoryRoot: root, framework: runner, language: "javascript", tracerVersion: "6.20.1", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
			drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) {
				if scenario == "metadata permission" {
					return "", &platform.MetadataError{Code: "registry_permission", Err: errors.New("denied")}
				}
				return "6.20.1", nil
			}
			var output bytes.Buffer
			require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result))
			switch scenario {
			case "missing coverage dependency":
				require.Equal(t, "setup_dependencies_missing", result.Code)
				require.Contains(t, result.NextCommand, "nyc@17.1.0")
				require.Equal(t, "run_command", result.NextAction)
			case "custom environment":
				require.Equal(t, "ci_edit_unsupported", result.Code)
				require.Equal(t, "apply_source_edit", result.NextAction)
				require.Contains(t, result.ResumeCommand, "testdrive")
				require.Contains(t, result.ResumeCommand, "json")
			case "dirty workflow":
				require.Equal(t, "dirty_workflow", result.Code)
				require.Equal(t, "needs_input", result.Status)
				require.Equal(t, "supply_input", result.NextAction)
				require.Contains(t, result.Location, ".github/workflows/test.yml")
				require.Contains(t, result.Reason, "--allow-dirty")
			case "metadata permission":
				require.Equal(t, "registry_permission", result.Code)
				require.Equal(t, []string{"package metadata"}, result.Capabilities)
				require.Equal(t, "request_capability", result.NextAction)
			}
			require.Empty(t, result.Changes)
			require.Equal(t, workflow, readApplyFile(t, root, ".github/workflows/test.yml"))
			require.NotEmpty(t, result.ResumeCommand)
			require.Empty(t, result.FinalResponse)
		})
	}
}

func TestWorkflowWithoutTestCommandsSaysSoAsJSON(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Jest{}, language: "javascript", tracerVersion: "6.20.1", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion, allConfigurations: true}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "agent"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Equal(t, "scope_unresolved", result.Code)
	require.Equal(t, "No Jest test command was found in GitHub Actions workflows or package scripts.", result.Reason)
}
