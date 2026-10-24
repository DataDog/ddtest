package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/stretchr/testify/require"
)

func TestRepeatedFreshWorkflowStopsWithoutPilingUpCheckpoints(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6"}}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: custom-setup && npx vitest run\n")
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var id string
	for i := 0; i < maxWorkflowAttempts; i++ {
		var out bytes.Buffer
		require.Error(t, drive.Workflow(t.Context(), &out, WorkflowOptions{Apply: true, Format: "json"}))
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &result))
		require.NotContains(t, result.NextCommand, "--apply", "unresolved scope cannot progress by repeating apply")
		require.Empty(t, result.ResumeCommand)
		if i < maxWorkflowAttempts-1 {
			require.Contains(t, result.NextCommand, "--instructions", "only the read-only fallback can progress")
		} else {
			require.Empty(t, result.NextCommand)
		}
		if i == 0 {
			id = result.RunID
		}
		require.Equal(t, id, result.RunID)
		if i == maxWorkflowAttempts-1 {
			require.Equal(t, "report_result", result.NextAction)
			require.Empty(t, result.ResumeCommand)
		}
	}
	path, err := checkpointPath(id)
	require.NoError(t, err)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	checkpoint, err := readCheckpoint(id)
	require.NoError(t, err)
	require.Equal(t, maxWorkflowAttempts, checkpoint.Attempts)
}

func TestCheckpointStartupCleanupPreservesLiveAndUnrecognizedFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := t.TempDir()
	expired := &workflowCheckpoint{RunID: strings.Repeat("a", 32), Root: root, Expires: time.Now().Add(-time.Hour)}
	live := &workflowCheckpoint{RunID: strings.Repeat("b", 32), Root: root, Expires: time.Now().Add(time.Hour)}
	require.NoError(t, expired.save())
	require.NoError(t, live.save())
	expiredPath, err := checkpointPath(expired.RunID)
	require.NoError(t, err)
	livePath, err := checkpointPath(live.RunID)
	require.NoError(t, err)
	canary := filepath.Join(filepath.Dir(livePath), "customer.json")
	requireWriteFile(t, canary, "keep")
	partial := filepath.Join(filepath.Dir(livePath), ".checkpoint-abandoned")
	require.NoError(t, os.WriteFile(partial, []byte("partial"), 0600))
	old := time.Now().Add(-2 * workflowCheckpointLifetime)
	require.NoError(t, os.Chtimes(partial, old, old))
	require.NoError(t, cleanupExpiredCheckpoints())
	require.NoFileExists(t, expiredPath)
	require.NoFileExists(t, partial)
	require.FileExists(t, livePath)
	require.FileExists(t, canary)
}

func TestWorkflowReclaimsAbandonedSessionBeforeDiscovery(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	root := canonicalWorkflowRoot(t.TempDir())
	owned := planSession()
	owned.root = root
	require.NoError(t, owned.create())
	other := planSession()
	other.root = canonicalWorkflowRoot(t.TempDir())
	require.NoError(t, other.create())
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run"}}`)
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	var out bytes.Buffer
	require.Error(t, drive.Workflow(t.Context(), &out, WorkflowOptions{Apply: true, Format: "json"}))
	require.NoDirExists(t, owned.Directory())
	require.DirExists(t, other.Directory(), "another repository's storage is not owned by this workflow")
}
