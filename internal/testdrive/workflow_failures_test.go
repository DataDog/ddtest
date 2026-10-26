package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		{"local runtime", "local_runtime_mismatch", "request_capability", fmt.Errorf("cypress API preflight: %w", &localRuntimeError{Source: "The Cypress action API", Required: "20", Local: "v24.14.1"})},
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
			if tc.code == "local_runtime_mismatch" {
				require.Equal(t, []string{"Node 20 runtime first on PATH"}, result.Capabilities)
				require.Contains(t, result.Reason, "Ask the user before downloading a runtime")
				require.NotEmpty(t, result.ResumeCommand)
			}
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
				// A prescribed install: run it as given, never stop to ask.
				require.Contains(t, result.Instruction, onboard.PrescribedEditInstruction)
				require.Contains(t, result.RequiredInput[0], "as given and without asking")
				require.NotContains(t, result.Needs+strings.Join(result.RequiredInput, " "), "approval")
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
				require.Contains(t, result.ResumeCommand, "onboard")
				require.Contains(t, result.ResumeCommand, "--apply")
				require.Contains(t, result.ResumeCommand, "--allow-dirty")
				checkpoint, err := readCheckpoint(result.RunID)
				require.NoError(t, err)
				require.True(t, checkpoint.Apply)
				require.NotEmpty(t, result.SetupDiff)
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

func TestReviewedBuildBlockerReturnsFlagRatherThanNewFacts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"private":true}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Cucumber{}, language: "javascript", tracerVersion: "5.132.0", projectTracer: "fixture", executor: workflowCheckExecutor{}, nodeVersion: currentNodeVersion}
	drive.workflowRun = func(_ context.Context, out io.Writer) error {
		r := completeValidationFixture("cucumber")
		r.Compatibility = verdict{Status: "not exercised"}
		r.Configurations = []configurationResult{{TestCommand: onboard.TestCommand{Command: "npm test"}, Status: "blocked", Preparation: []buildResult{{BuildCommand: onboard.BuildCommand{Command: "npm run build"}, Status: "blocked", Code: "preparation_review_required", Diagnostic: "package build lifecycle scripts"}}}}
		r.Runs = nil
		return finishValidation(out, root, r)
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "preparation_review_required", result.Code)
	require.Contains(t, result.ResumeCommand, "--allow-reviewed-builds")
	require.Nil(t, result.InputTemplate)
	require.Contains(t, result.Location, "npm run build")
	require.Equal(t, "needs_input", result.Status)
	// The agent learns what to read and when the decision belongs to the user.
	require.Contains(t, result.RequiredInput[0], "npm run build (review package build lifecycle scripts)")
	require.Contains(t, result.RequiredInput[0], "ask the user first")
}

func TestFocusedRunKeepsPreparationReceiptsOnItsConfiguration(t *testing.T) {
	build := onboard.BuildCommand{Directory: ".", Command: "pnpm run build", Kind: onboard.PreparationPackageBuild}
	executed := onboard.TestCommand{Command: "npm test", Directory: ".", Prerequisites: []onboard.BuildCommand{build}}
	other := onboard.TestCommand{Command: "npm run test:other", Directory: "."}
	result := validationResult{Framework: "cucumber", Compatibility: verdict{Status: "not exercised"}}
	setSelectedConfigurationScope(&result, onboard.ValidationScope{Commands: []onboard.TestCommand{executed, other}}, []onboard.TestCommand{executed}, nil)
	receipt := buildResult{BuildCommand: build, Status: "blocked", Code: "preparation_review_required"}
	creditPreparation(&result, configurationResult{TestCommand: executed, Preparation: []buildResult{receipt}})
	require.Equal(t, []buildResult{receipt}, result.Configurations[0].Preparation)
	require.Empty(t, result.Configurations[1].Preparation, "an uncredited configuration did not run this preparation")
}

func TestFocusedCucumberRunIsCreditedThroughItsRetainedPlan(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		"package.json":             `{"scripts":{"build":"tsc -p .","bdd":"cucumber-js"},"devDependencies":{"@cucumber/cucumber":"10.9.0","typescript":"5.9.3"}}`,
		".github/workflows/ci.yml": "jobs:\n  bdd:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm run build\n      - run: npm run bdd\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(value), 0600))
	}
	scope, err := onboard.DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Commands[0].Prerequisites, "the CI build is the test's prerequisite")
	plan, err := onboard.CucumberExecutionCommand(root, "npm run bdd")
	require.NoError(t, err)
	require.NotEmpty(t, plan.Prerequisites, "the focused plan retains the CI build")
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewCucumber(platform.NewJavaScript()), selectedPlan: plan, command: "npm", args: []string{"run", "bdd"}}
	result := completeValidationFixture("cucumber")
	drive.setConfigurationScope(&result, scope)
	require.Equal(t, "passed", result.Configurations[0].Status, "the focused run executed the CI build with the test")
}
