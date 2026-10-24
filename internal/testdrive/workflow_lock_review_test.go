//go:build linux || darwin

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

// usePrivateWorkflowLocks keeps tests from changing the shared /tmp lock folder.
func usePrivateWorkflowLocks(t *testing.T) {
	t.Helper()
	previous := workflowLockParent
	workflowLockParent = t.TempDir()
	t.Cleanup(func() { workflowLockParent = previous })
}

func TestWorkflowLockRejectsSharedDirectoryAndSymlink(t *testing.T) {
	root := t.TempDir()
	usePrivateWorkflowLocks(t)
	directory := workflowLockDirectory(root)
	require.NoError(t, os.Mkdir(directory, 0777))
	require.NoError(t, os.Chmod(directory, 0777))
	_, err := acquireWorkflowLock(root)
	require.Error(t, err)
	require.NoError(t, os.Chmod(directory, 0700))
	lock, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	path := lock.Name()
	require.NoError(t, lock.Close())
	require.NoFileExists(t, path)
	canary := filepath.Join(t.TempDir(), "canary")
	requireWriteFile(t, canary, "customer")
	require.NoError(t, os.Symlink(canary, path))
	_, err = acquireWorkflowLock(root)
	require.Error(t, err)
	data, err := os.ReadFile(canary)
	require.NoError(t, err)
	require.Equal(t, "customer", string(data))
}

func TestClosingAnOldLockCannotRemoveTheNextLock(t *testing.T) {
	root := t.TempDir()
	usePrivateWorkflowLocks(t)
	first, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	second, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, second.Close()) }()
	require.NoError(t, first.Close())
	_, err = acquireWorkflowLock(root)
	require.ErrorIs(t, err, errWorkflowBusy)
}

func TestWorkflowLockIgnoresTMPDIR(t *testing.T) {
	root := t.TempDir()
	usePrivateWorkflowLocks(t)
	t.Setenv("TMPDIR", t.TempDir())
	first, err := acquireWorkflowLock(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()
	// A second invocation with another TMPDIR would otherwise recover the
	// first one's live /tmp process-owner storage as abandoned.
	t.Setenv("TMPDIR", t.TempDir())
	_, err = acquireWorkflowLock(root)
	require.ErrorIs(t, err, errWorkflowBusy)
}

func TestCoordinationFailureKeepsAnExecutableAgentCommand(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blocked, nil, 0600))
	previous := workflowLockParent
	workflowLockParent = blocked
	t.Cleanup(func() { workflowLockParent = previous })
	root := t.TempDir()
	drive := &Testdrive{repositoryRoot: root, framework: &framework.Vitest{}, language: "javascript"}
	for _, format := range []string{"agent", "json"} {
		var output bytes.Buffer
		require.ErrorContains(t, drive.Workflow(t.Context(), &output, WorkflowOptions{Apply: true, Format: format}), "coordination_unavailable")
		var result onboard.AgentResult
		require.NoError(t, json.Unmarshal(output.Bytes(), &result), output.String())
		require.Equal(t, "request_capability", result.NextAction)
		want := append(onboard.OnboardingCommand("vitest"), "--agent")
		if format == "json" {
			want = append(onboard.OnboardingCommand("vitest"), "--format", "json")
		}
		require.Equal(t, want, result.NextCommand, "the agent retries the same structured onboarding command")
	}
}
