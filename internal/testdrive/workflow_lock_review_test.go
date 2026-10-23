//go:build linux || darwin

package testdrive

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
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
