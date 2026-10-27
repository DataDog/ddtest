package testdrive

import (
	"bytes"
	"encoding/json"
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
	var output bytes.Buffer
	require.NoError(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "configuration-one"))
	var configuration onboard.TestCommand
	require.NoError(t, json.Unmarshal(output.Bytes(), &configuration))
	require.Equal(t, "vitest run", configuration.Command)
	output.Reset()
	require.NoError(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "plan"))
	require.Contains(t, output.String(), "6.21.0")
	// The same formatting-only edit as the real Playwright trial invalidates
	// evidence; detailed inspection must not accidentally certify old inputs.
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.js"), []byte("initial\n\n"), 0600))
	output.Reset()
	require.ErrorContains(t, RenderWorkflowDetail(t.Context(), &output, checkpoint.RunID, "plan"), "stale")
	var failure onboard.AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &failure))
	require.Equal(t, "report_unavailable", failure.Code)
	require.Nil(t, failure.Plan)
	require.Empty(t, failure.FinalResponse)
}
