package testdrive

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

// certifiedWorkflowReport writes a workflow report with a plan and the private
// receipt the coordinator records after its own verification.
func certifiedWorkflowReport(t *testing.T, root, name string) *validationResult {
	t.Helper()
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	report := completeValidationFixture(name)
	report.Workflow = &WorkflowEvidence{Fingerprint: fingerprint, Node: currentNodeVersion(), Checks: []onboard.WorkflowCheck{{Name: "source", Status: "passed"}},
		Plan: &onboard.ExecutionPlan{SchemaVersion: 1, Fingerprint: fingerprint, Framework: name, Tracer: "6.20.1"}}
	require.NoError(t, finishValidation(io.Discard, root, report))
	public, err := readPreviousValidation(root)
	require.NoError(t, err)
	require.NoError(t, saveWorkflowReceipt(root, *public))
	return public
}

func TestPlanDetailFromTheReportRequiresItsReceipt(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "source.js"), "original")
	public := certifiedWorkflowReport(t, root, "jest")
	var out bytes.Buffer
	require.NoError(t, RenderWorkflowDetail(t.Context(), &out, "", "plan"))
	require.Contains(t, out.String(), `"tracer":"6.20.1"`)

	// A check-only run keeps the full run's plan and binding as history.
	checkOnly := validationResult{Framework: "jest", CheckOnly: true, Retained: &retainedExecution{Status: "passed", Result: public}}
	data, err := marshalValidation(checkOnly)
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), data))
	out.Reset()
	require.NoError(t, RenderWorkflowDetail(t.Context(), &out, "", "plan"))
	require.Contains(t, out.String(), `"tracer":"6.20.1"`)

	public.Workflow.Plan.Tracer = "9.9.9"
	data, err = marshalValidation(*public)
	require.NoError(t, err)
	require.NoError(t, writeValidation(validationPath(root), data))
	out.Reset()
	require.ErrorContains(t, RenderWorkflowDetail(t.Context(), &out, "", "plan"), "not certified")
	require.NotContains(t, out.String(), "9.9.9", "an edited public plan must not be presented")
}

func TestAlternatingFailureCodesShareOneAttemptBound(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	checkpoint, err := openCheckpoint("", root, "jest", "", true, true)
	require.NoError(t, err)
	for attempt, code := range []string{"metadata_network", "validation_preparation", "metadata_network"} {
		require.NoError(t, checkpoint.recordFailure("fingerprint", "facts", code))
		require.Equal(t, attempt+1, checkpoint.Attempts, "a flaky cause alternating codes must not reset the counter (%s)", code)
	}
	require.Equal(t, maxWorkflowAttempts, checkpoint.Attempts)
	require.Equal(t, []string{"metadata_network", "validation_preparation", "metadata_network"}, checkpoint.Failures, "the history survives for the final report")
	path, err := checkpointPath(checkpoint.RunID)
	require.NoError(t, err)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestRejectedWorkflowInputsKeepTheSavedReport(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: npx vitest run\n")
	certifiedWorkflowReport(t, root, "vitest")
	original, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	for name, options := range map[string]WorkflowOptions{
		"facts path typo": {Apply: true, Format: "json", FactsFile: filepath.Join(t.TempDir(), "missing.json")},
		"invalid resume":  {Apply: true, Format: "json", Resume: "0123456789abcdef0123456789abcdef"},
	} {
		t.Run(name, func(t *testing.T) {
			drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
			var out bytes.Buffer
			require.Error(t, drive.Workflow(t.Context(), &out, options))
			var result onboard.AgentResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result))
			require.Contains(t, []string{"facts_invalid", "resume_invalid", "resume_not_found"}, result.Code)
			current, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			require.Equal(t, string(original), string(current), "a rejected input runs no tests and must not replace the report")
		})
	}
	var out bytes.Buffer
	require.NoError(t, RenderReport(t.Context(), &out, "json"))
}

func TestReportOfAPlainRunSaysTheWorkflowDidNotProduceIt(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "source.js"), "original")
	require.NoError(t, finishValidation(io.Discard, root, completeValidationFixture("jest")))
	var out bytes.Buffer
	require.Error(t, RenderReport(t.Context(), &out, "json"))
	var state onboard.AgentResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &state))
	require.Equal(t, "plain_report", state.Code)
	require.NotContains(t, state.Reason, "historical success")
	require.Equal(t, onboard.DDTestCommand("testdrive")[0], state.NextCommand[0])
	out.Reset()
	require.ErrorContains(t, RenderWorkflowDetail(t.Context(), &out, "", "plan"), "plain testdrive run")
}
