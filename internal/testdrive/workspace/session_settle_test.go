// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lateWriter replaces removeAll for a test: for the first failures calls,
// each removing an entry of directory, a process still exiting writes its
// compile cache entry into the directory being emptied, so the removal
// ends as unlinkat does in that race, with ENOTEMPTY. Later calls remove
// normally. It returns the number of removal calls.
func lateWriter(t *testing.T, directory string, failures int) *int {
	t.Helper()
	delays := settleRetryDelays
	settleRetryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { removeAll, settleRetryDelays = os.RemoveAll, delays })
	calls := 0
	removeAll = func(path string) error {
		calls++
		if calls > failures || CanonicalRoot(filepath.Dir(path)) != CanonicalRoot(directory) {
			return os.RemoveAll(path)
		}
		cache := filepath.Join(path, "node-compile-cache", "v24.11.1-arm64-efeeb6ce-501")
		require.NoError(t, os.RemoveAll(path))
		require.NoError(t, os.MkdirAll(cache, 0700))
		require.NoError(t, os.WriteFile(filepath.Join(cache, "late"), []byte("cache"), 0600))
		return &os.PathError{Op: "unlinkat", Path: filepath.Dir(cache), Err: syscall.ENOTEMPTY}
	}
	return &calls
}

// The CI failure "recover owned session: unlinkat …/node-compile-cache/v24…:
// directory not empty": a Node process of a killed invocation writes its
// compile cache while the next invocation recovers that invocation's
// session. Recovery waits for the writer to finish instead of failing.
func TestRecoveryOutlastsAProcessWritingItsCompileCacheOnExit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	session := PlanSession()
	session.SetOwnerRoot(root)
	require.NoError(t, session.Create())
	require.NoError(t, os.MkdirAll(filepath.Join(session.Directory(), "tmp-run", "node-compile-cache"), 0700))
	calls := lateWriter(t, session.Directory(), 2)

	var output bytes.Buffer
	require.NoError(t, RecoverSessions(root, &output))
	require.NoDirExists(t, session.Directory())
	require.Contains(t, output.String(), "Recovered abandoned testdrive session")
	require.Equal(t, 4, *calls, "two interrupted removals, the settled one and the directory itself")
}

// A writer that outlasts the retries, or any other failure, still fails
// removal, and the owner marker stays for a later recovery.
func TestRemovalGivesUpOnAWriterThatKeepsWriting(t *testing.T) {
	directory, err := os.MkdirTemp(t.TempDir(), "ddtest-session-")
	require.NoError(t, err)
	require.NoError(t, MarkStorage(directory, t.TempDir()))
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "tmp-run"), 0700))
	calls := lateWriter(t, directory, 100)

	err = RemoveMarkedStorage(directory)
	require.ErrorIs(t, err, syscall.ENOTEMPTY)
	require.Equal(t, 1+len(settleRetryDelays), *calls, "bounded retries")
	require.FileExists(t, filepath.Join(directory, OwnerMarker))

	*calls = 0
	removeAll = func(string) error {
		*calls++
		return &os.PathError{Op: "unlinkat", Path: directory, Err: syscall.EACCES}
	}
	require.ErrorIs(t, RemoveMarkedStorage(directory), syscall.EACCES)
	require.Equal(t, 1, *calls, "other errors are not retried")
}
