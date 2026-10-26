package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

const partialScopeWorkflow = "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n  publish:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: node unknown-wrapper.js\n"

// Unresolved CI elsewhere never blocks setup of the configurations that did
// resolve; it stays unedited and is reported instead.
func TestApplyEditsResolvedConfigurationsDespiteUnresolvedCI(t *testing.T) {
	drive := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	path := filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	requireWriteFile(t, path, partialScopeWorkflow)
	var output bytes.Buffer
	require.NoError(t, drive.Apply(t.Context(), &output, ApplyOptions{Site: "datadoghq.com", Preview: true}))
	require.Contains(t, output.String(), "Unresolved CI is not validated locally")
	require.Contains(t, output.String(), "unknown-wrapper.js")
	diff := output.String()[strings.Index(output.String(), "---"):]
	require.Contains(t, diff, "Configure Datadog Test Optimization")
	require.NotContains(t, diff[strings.Index(diff, "publish:"):], "Configure Datadog Test Optimization", "the unresolved job is not edited")
	require.Equal(t, partialScopeWorkflow, readApplyFile(t, drive.repositoryRoot, ".github/workflows/test.yml"), "a preview writes nothing")
}

// Modeled on express: its legacy job runs only Node 16/17, below the release
// every other row needs. Opting that step out is the exact, complete fix, and
// it is proposed for the user's approval, never applied.
func TestCIRowsNeedingAnotherMajorProposeAnExactOptOut(t *testing.T) {
	selection := platform.JSSelection{Version: "5.132.0", Node: ">=18"}
	ci := onboard.RuntimeCheck{Jobs: []onboard.RuntimeFinding{
		{Workflow: ".github/workflows/ci.yml", Job: "test", Step: 7, Node: "24", Status: onboard.StatusCompatible},
		{Workflow: ".github/workflows/legacy.yml", Job: "test", Step: 6, Node: "16"},
		{Workflow: ".github/workflows/legacy.yml", Job: "test", Step: 6, Node: "17"},
		{Workflow: ".github/workflows/legacy.yml", Job: "test", Step: 6, Node: "14", Code: onboard.CodeExcludedRuntime},
		{Workflow: ".github/workflows/publish.yml", Job: "release", Step: 2, Node: "16", Code: onboard.CodeUnresolvedCommand},
	}}
	err := ciTracerMajorsError("mocha", ci, selection, nil)
	var majors *ciTracerMajors
	require.True(t, errors.As(err, &majors))
	require.Equal(t, []string{".github/workflows/legacy.yml / test / step 6: add `DD_CIVISIBILITY_ENABLED: 'false'` to the step env (Node 16, 17 keep running without the tracer)"}, majors.Exclusions)
	require.Contains(t, err.Error(), "only with the user's approval")
	require.NotContains(t, err.Error(), "publish.yml", "unresolved rows are reported elsewhere and don't constrain setup")

	mixed := ci
	mixed.Jobs = append(slices.Clone(ci.Jobs), onboard.RuntimeFinding{Workflow: ".github/workflows/legacy.yml", Job: "test", Step: 6, Node: "24", Status: onboard.StatusCompatible})
	require.True(t, errors.As(ciTracerMajorsError("mocha", mixed, selection, nil), &majors))
	require.Empty(t, majors.Exclusions, "a step that also runs supported rows needs a per-row condition, not a blanket opt-out")
	require.True(t, errors.As(ciTracerMajorsError("jest", ci, selection, nil), &majors))
	require.Empty(t, majors.Exclusions, "only Mocha discovery recognizes the opt-out")
}

func TestWorkflowAsksTheUserBeforeOptingOutCIRows(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npx vitest run\n")
	edit := ".github/workflows/legacy.yml / test / step 6: add `DD_CIVISIBILITY_ENABLED: 'false'` to the step env (Node 16 keep running without the tracer)"
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", nodeVersion: func() string { return "24.14.1" }}
	drive.workflowSelect = func(context.Context, onboard.ValidationScope, bool) (string, error) {
		return "", &ciTracerMajors{message: "CI rows need different dd-trace majors", Exclusions: []string{edit}}
	}
	var output bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Equal(t, "preflight_unresolved", result.Code)
	require.Contains(t, result.Needs, "Ask the user before excluding any CI row")
	require.NotContains(t, result.Needs, "without excluding CI entries", "the guidance no longer contradicts the proposed opt-out")
	require.Len(t, result.RequiredInput, 1)
	require.Contains(t, result.RequiredInput[0], edit)
	require.Empty(t, result.Changes, "excluding CI rows is never applied automatically")
}

// The agent path proceeds to tracer selection for the resolved configurations
// instead of sending the agent to manual instructions.
func TestWorkflowProceedsWithResolvedConfigurationsAndReportsUnresolvedCI(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), strings.ReplaceAll(partialScopeWorkflow, "npm test", "npx vitest run"))
	for _, all := range []bool{false, true} {
		drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript", allConfigurations: all, nodeVersion: func() string { return "24.14.1" }}
		selections := 0
		drive.workflowSelect = func(_ context.Context, scope onboard.ValidationScope, _ bool) (string, error) {
			selections++
			require.Len(t, scope.Commands, 1)
			require.NotEmpty(t, scope.Unresolved)
			return "", os.ErrPermission // stop after the decision under test
		}
		var output bytes.Buffer
		require.Error(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}))
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result))
		require.NotEqual(t, "scope_unresolved", result.Code, output.String())
		require.Equal(t, 1, selections)
		var scopeCheck *onboard.WorkflowCheck
		for i := range result.Checks {
			if result.Checks[i].Name == "ci_scope" {
				scopeCheck = &result.Checks[i]
			}
		}
		require.NotNil(t, scopeCheck, "%+v", result.Checks)
		require.Contains(t, scopeCheck.Reason, "unknown-wrapper.js")
	}
}
