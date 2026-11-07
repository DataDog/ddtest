package testdrive

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestReportDetailReadsBoundedCheckpointAndRejectsChangedSource(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := workspace.CanonicalRoot(t.TempDir())
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "source.js"), "initial\n")
	fingerprint, err := InputFingerprint(t.Context(), root)
	require.NoError(t, err)
	checkpoint, err := openCheckpoint("", root, "vitest", "", true, true)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, checkpoint.remove()) })
	checkpoint.Plan = &onboard.ExecutionPlan{Fingerprint: fingerprint, Tracer: "6.21.0", Scope: onboard.ValidationScope{Commands: []onboard.TestCommand{{ID: "configuration-one", Command: "vitest run"}}}}
	require.NoError(t, checkpoint.save())
	var output validationUsageOutput
	require.NoError(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "configuration-one"))
	require.Equal(t, "succeeded", output.status)
	require.Equal(t, "report", output.phase)
	require.Empty(t, output.code)
	var configuration onboard.TestCommand
	require.NoError(t, json.Unmarshal(output.Bytes(), &configuration))
	require.Equal(t, "vitest run", configuration.Command)
	output = validationUsageOutput{}
	require.NoError(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "plan"))
	require.Equal(t, "succeeded", output.status)
	require.Equal(t, "report", output.phase)
	require.Empty(t, output.code)
	require.Contains(t, output.String(), "6.21.0")
	for _, detail := range []string{"plan", "configuration-one"} {
		var failed failingDetailUsageOutput
		require.ErrorIs(t, RenderWorkflowDetail(t.Context(), &failed, checkpoint.RunID, detail), io.ErrClosedPipe)
		require.Equal(t, "needs_input", failed.status)
		require.Equal(t, "report", failed.phase)
		require.Equal(t, "report_unavailable", failed.code)
	}
	// The same formatting-only edit as the real Playwright trial invalidates
	// evidence; detailed inspection must not accidentally certify old inputs.
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.js"), []byte("initial\n\n"), 0600))
	output = validationUsageOutput{}
	require.ErrorContains(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "plan"), "stale")
	require.Equal(t, "needs_input", output.status)
	require.Equal(t, "report", output.phase)
	require.Equal(t, "report_unavailable", output.code)
	var failure onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &failure))
	require.Equal(t, "report_unavailable", failure.Code)
	require.Nil(t, failure.Plan)
	require.Empty(t, failure.FinalResponse)
}

type failingDetailUsageOutput struct{ validationUsageOutput }

func (*failingDetailUsageOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
