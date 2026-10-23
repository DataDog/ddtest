//go:build linux || darwin

package testdrive

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowLockRejectsSharedDirectoryAndSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
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
	t.Setenv("TMPDIR", t.TempDir())
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
