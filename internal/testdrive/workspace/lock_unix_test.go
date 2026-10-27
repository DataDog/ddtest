//go:build linux || darwin

package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// usePrivateLocks keeps tests from changing the shared /tmp lock folder.
func usePrivateLocks(t *testing.T) {
	t.Helper()
	previous := LockParent
	LockParent = t.TempDir()
	t.Cleanup(func() { LockParent = previous })
}

func TestWorkflowLockRejectsSharedDirectoryAndSymlink(t *testing.T) {
	root := t.TempDir()
	usePrivateLocks(t)
	directory := lockDirectory(root)
	require.NoError(t, os.Mkdir(directory, 0777))
	require.NoError(t, os.Chmod(directory, 0777))
	_, err := AcquireLock(root)
	require.Error(t, err)
	require.NoError(t, os.Chmod(directory, 0700))
	lock, err := AcquireLock(root)
	require.NoError(t, err)
	path := lock.Name()
	require.NoError(t, lock.Close())
	require.NoFileExists(t, path)
	canary := filepath.Join(t.TempDir(), "canary")
	writeFile(t, canary, "customer")
	require.NoError(t, os.Symlink(canary, path))
	_, err = AcquireLock(root)
	require.Error(t, err)
	data, err := os.ReadFile(canary)
	require.NoError(t, err)
	require.Equal(t, "customer", string(data))
}

func TestClosingAnOldLockCannotRemoveTheNextLock(t *testing.T) {
	root := t.TempDir()
	usePrivateLocks(t)
	first, err := AcquireLock(root)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	second, err := AcquireLock(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, second.Close()) }()
	require.NoError(t, first.Close())
	_, err = AcquireLock(root)
	require.ErrorIs(t, err, ErrBusy)
}

func TestWorkflowLockIgnoresTMPDIR(t *testing.T) {
	root := t.TempDir()
	usePrivateLocks(t)
	t.Setenv("TMPDIR", t.TempDir())
	first, err := AcquireLock(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, first.Close()) }()
	// A second invocation with another TMPDIR would otherwise recover the
	// first one's live /tmp process-owner storage as abandoned.
	t.Setenv("TMPDIR", t.TempDir())
	_, err = AcquireLock(root)
	require.ErrorIs(t, err, ErrBusy)
}
