// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func stateCIRuntimes(t *testing.T, values ...string) {
	t.Helper()
	var statements []onboard.CIRuntimeStatement
	for _, value := range values {
		statement, err := onboard.ParseCIRuntime(value)
		require.NoError(t, err)
		statements = append(statements, statement)
	}
	require.NoError(t, onboard.UseCIStatements(statements, nil))
	t.Cleanup(func() { require.NoError(t, onboard.UseCIStatements(nil, nil)) })
}

// The coordinator asks the user for a self-hosted runner's Node before any
// setup, preflight or test, and offers no command to run without it.
func TestWorkflowAsksForAnUnknownCIRuntimeFirst(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    runs-on: [self-hosted, linux]\n    steps:\n      - run: npx vitest run\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var output bytes.Buffer
	require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: "json"}), "ci_runtime_required")
	var result onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
	require.Equal(t, "needs_input", result.Status)
	require.Equal(t, "ask_user", result.NextAction)
	require.Equal(t, []string{"ci_runtime:.github/workflows/test.yml / test"}, result.RequiredInput)
	require.Empty(t, result.NextCommand)
	require.Empty(t, result.ResumeCommand)
	require.Contains(t, result.Instruction, "Never guess it")
	require.Contains(t, result.Reason, ".github/workflows/test.yml / test (the self-hosted runner self-hosted, linux)")
	require.NoFileExists(t, filepath.Join(root, ".testoptimization", "testdrive.json"), "nothing ran")
}

// final_response lists what the user stated, apart from what ddtest read,
// and a statement nothing used as a caveat; the fingerprint binds them.
func TestValidationSummaryShowsUserStatements(t *testing.T) {
	root := t.TempDir()
	without, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	stateCIRuntimes(t, "node=22", "ruby=3.3")
	with, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	require.NotEqual(t, without, with, "reports are bound to the statements")

	result := completeValidationFixture("jest")
	result.CIRuntime = &onboard.RuntimeCheck{Status: "compatible", Jobs: []onboard.RuntimeFinding{{Workflow: ".github/workflows/test.yml", Job: "test", Node: "22", Status: "compatible", UserStated: true, RuntimeHost: "the self-hosted runner self-hosted"}}}
	summary := summarizeValidation(result)
	require.Equal(t, []string{"Node 22 for .github/workflows/test.yml / test (--ci-runtime). ddtest checked the tracer against it as it checks a CI image's version."}, summary.UserStated)
	require.Contains(t, summary.Caveats, "--ci-runtime ruby=3.3 was not used: no validated Ruby CI job it names has a runtime ddtest could not read.")
	result.Summary = summary
	handoff := validationHandoff(result)
	require.Contains(t, handoff, "Stated by the user, not read from CI configuration or verified: Node 22 for .github/workflows/test.yml / test (--ci-runtime).")
	require.Contains(t, handoff, "Caveat (not a blocker): --ci-runtime ruby=3.3 was not used")
}
