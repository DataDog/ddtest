// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func pytestDrive(root string) *Testdrive {
	return &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), allConfigurations: true, tracerVersion: "4.16.1"}
}

// testdrive --agent for pytest asks the user the coordinator's questions,
// with the same results, before anything runs: the agents' Python, then the
// Jenkins plugin's facts.
func TestStandaloneWorkflowAsksTheUserFirst(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	requireWriteFile(t, filepath.Join(root, "Jenkinsfile"), "pipeline {\n  agent any\n  options { datadog(testOptimization: [enabled: true, serviceName: 'app', languages: ['PYTHON']]) }\n  stages { stage('Test') { steps { sh 'python -m pytest' } } }\n}\n")
	onboard.UseCIProvider("jenkins")
	t.Cleanup(func() { onboard.UseCIProvider("") })
	drive := pytestDrive(root)
	drive.workflowRun = func(context.Context, io.Writer) error {
		t.Fatal("nothing runs before the user answers")
		return nil
	}
	ask := func(code string) onboard.AgentResult {
		t.Helper()
		var output bytes.Buffer
		require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "agent"}), code)
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
		require.Equal(t, "needs_input", result.Status)
		require.Equal(t, code, result.Code)
		require.Equal(t, "ask_user", result.NextAction)
		require.Empty(t, result.NextCommand)
		require.Empty(t, result.ResumeCommand)
		return result
	}
	result := ask("ci_runtime_required")
	require.Equal(t, []string{"ci_runtime:Jenkinsfile / Test"}, result.RequiredInput)
	require.Contains(t, result.Instruction, "--ci-runtime python=<version>")

	stateCIRuntimes(t, "python=3.12")
	result = ask("ci_setup_confirmation_required")
	require.Equal(t, []string{"ci_confirm:jenkins-plugin", "ci_confirm:jenkins-plugin-mode=agentless:<site>|agent"}, result.RequiredInput)
	require.NoFileExists(t, validationPath(root), "nothing ran")
}

// A pytest command whose inline PYTHONPATH drops the setup's tracer path
// gets the exact edit before anything runs, then this same command again.
func TestStandaloneWorkflowPrescribesTheCIEditFirst(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755))
	workflow := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.1'}\n      - run: PYTHONPATH=src pytest tests\n"
	requireWriteFile(t, filepath.Join(root, ".github", "workflows", "test.yml"), workflow)
	onboard.UseCIProvider("github")
	t.Cleanup(func() { onboard.UseCIProvider("") })
	drive := pytestDrive(root)
	drive.workflowRun = func(context.Context, io.Writer) error {
		t.Fatal("nothing runs before the edit")
		return nil
	}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "agent"}), onboard.CodeCIEditRequired)
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, onboard.CodeCIEditRequired, result.Code)
	require.Equal(t, "apply_source_edit", result.NextAction)
	require.Empty(t, result.NextCommand)
	require.Equal(t, []string{"testdrive", "--framework", "pytest", "--agent", "--yes", "--ci-provider", "github", "--all"}, result.ResumeCommand[1:])
	require.Equal(t, ".github/workflows/test.yml / test / step 3", result.Location)
	require.Contains(t, result.Reason, "Run `pytest tests` instead, and add PYTHONPATH: ${{ env.PYTHONPATH }}:src to the step's env")
	require.NoFileExists(t, validationPath(root), "nothing ran")

	requireWriteFile(t, filepath.Join(root, ".github", "workflows", "test.yml"), strings.Replace(workflow, "      - run: PYTHONPATH=src pytest tests\n", "      - run: pytest tests\n        env: {PYTHONPATH: '${{ env.PYTHONPATH }}:src'}\n", 1))
	ran := false
	drive.workflowRun = func(context.Context, io.Writer) error { ran = true; return nil }
	_ = drive.Workflow(t.Context(), io.Discard, WorkflowOptions{Format: "agent"})
	require.True(t, ran, "the edited form validates")
}

// Standalone validation records no workflow checks, so its verdict asks
// for the Git exclusion its handoffs prescribe when Git does not ignore the
// reports, with the coordinator's check: /.testoptimization/ satisfies it.
func TestStandaloneWorkflowChecksTheGitExclusion(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	gitInit(t, root)
	verdict := func() onboard.AgentResult {
		t.Helper()
		drive := pytestDrive(root)
		drive.executor = &ext.DefaultCommandExecutor{}
		fixture := completeValidationFixture("pytest")
		drive.workflowRun = func(_ context.Context, out io.Writer) error { return finishValidation(out, root, fixture) }
		var output bytes.Buffer
		require.NoError(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "agent"}))
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
		require.Equal(t, "passed", result.Status)
		return result
	}
	result := verdict()
	// The reason names the retained reports in its own words; the
	// prescribed edit is what this check pins.
	require.Contains(t, result.Instruction, "retained report")
	require.Contains(t, result.Instruction, "Add /.testoptimization/ to the repository .gitignore: every retained report must be ignored")
	require.Contains(t, result.Instruction, "). That ignore-file edit changes no test input")

	requireWriteFile(t, filepath.Join(root, ".gitignore"), "/.testoptimization/\n")
	result = verdict()
	require.NotContains(t, result.Instruction, ".gitignore")
}

// Its verdict carries the report's final_response and the contract for it:
// PASSED is validated locally, never COMPLETE LOCALLY; an incomplete report
// starts with NOT COMPLETE and ends the turn.
func TestStandaloneWorkflowReturnsTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		fail               bool
	}{
		{"passed", "passed", "standalone_validation", false},
		{"incomplete", "validation_failed", "validation_incomplete", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
			fixture := completeValidationFixture("pytest")
			fixture.Success = !tc.fail
			if tc.fail {
				fixture.Features[0].Status = "failed"
			}
			drive := pytestDrive(root)
			drive.workflowRun = func(_ context.Context, out io.Writer) error { return finishValidation(out, root, fixture) }
			var output bytes.Buffer
			err := drive.Workflow(t.Context(), &output, WorkflowOptions{Format: "agent"})
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
			report := readValidationReport(t, root)
			require.Equal(t, tc.status, result.Status, result.Reason)
			require.Equal(t, tc.code, result.Code)
			require.Equal(t, "report_result", result.NextAction)
			require.Empty(t, result.NextCommand)
			require.Equal(t, report.Summary.FinalResponse, result.FinalResponse)
			require.True(t, strings.HasPrefix(result.Instruction, onboard.FinalResponseInstruction), result.Instruction)
			require.Contains(t, result.Instruction, "never COMPLETE LOCALLY")
			require.Equal(t, validationPath(workspace.CanonicalRoot(root)), result.JSONReport)
			if tc.fail {
				require.Error(t, err)
				require.True(t, strings.HasPrefix(result.FinalResponse, "NOT COMPLETE"), result.FinalResponse)
				require.Contains(t, result.RequiredInput, "/auto-retries: failed")
				require.Equal(t, []string{"testdrive", "--framework", "pytest", "--agent", "--yes", "--all"}, result.ResumeCommand[1:])
				return
			}
			require.NoError(t, err)
			require.Equal(t, "PASSED", report.Summary.Status)
			require.Contains(t, result.Reason, "not COMPLETE LOCALLY")
			require.Empty(t, result.ResumeCommand)
		})
	}
}

// Plain pytest output is what an agent reads: it carries final_response
// with the same contract, and never a bare PASSED beside an incomplete
// verdict. Coordinated languages keep their plain output.
func TestStandalonePlainOutputCarriesTheFinalResponse(t *testing.T) {
	fixture := completeValidationFixture("pytest")
	fixture.Success = false
	fixture.LocalSuccess = true
	fixture.Scope = &onboard.ValidationScope{Unresolved: []string{"Jenkinsfile / Test: unresolved"}}
	var output bytes.Buffer
	_ = finishValidation(&output, t.TempDir(), fixture)
	text := output.String()
	require.Contains(t, text, "Validation verdict: INCOMPLETE\nLocal validation: PASSED (local checks only; they do not complete onboarding)\n")
	require.Contains(t, text, "Coding agents: "+onboard.FinalResponseInstruction)
	require.Contains(t, text, "never COMPLETE LOCALLY")
	require.Contains(t, text, "final_response:\nNOT COMPLETE: onboarding validation is INCOMPLETE.")

	fixture = completeValidationFixture("jest")
	fixture.Success = false
	fixture.LocalSuccess = true
	fixture.Scope = &onboard.ValidationScope{Unresolved: []string{".github/workflows/test.yml / test: unresolved"}}
	output.Reset()
	_ = finishValidation(&output, t.TempDir(), fixture)
	require.Contains(t, output.String(), "Validation verdict: INCOMPLETE\nLocal validation: PASSED\n")
	require.NotContains(t, output.String(), "Coding agents:")
}

// The handoff cannot name the release a CI setup pins, so pytest validation
// selects it; a setup that installs the latest release pins none.
func TestPythonCIPinnedTracer(t *testing.T) {
	language := pythonLanguage{}
	require.Equal(t, "4.16.1", language.ciPinnedTracer(onboard.RuntimeFinding{Tracer: "ddtrace@4.16.1", TracerRequested: "4.16.1"}))
	require.Empty(t, language.ciPinnedTracer(onboard.RuntimeFinding{Tracer: "ddtrace@4.16.1", TracerRequested: "latest", TracerFloating: true}))
	require.Empty(t, language.ciPinnedTracer(onboard.RuntimeFinding{Tracer: "ddtrace@4.16.1"}), "no setup selected it")
}
