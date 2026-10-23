// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSessionUsesTemporaryStorage(t *testing.T) {
	repositoryRoot := t.TempDir()
	t.Chdir(repositoryRoot)
	session, err := NewSession()
	require.NoError(t, err)
	require.NotEqual(t, session.ID(), filepath.Base(session.Directory()), "report IDs must not make native temporary paths too long")
	require.DirExists(t, session.Directory())
	files, err := os.ReadDir(repositoryRoot)
	require.NoError(t, err)
	require.Empty(t, files, "creating a session must not write into the customer repository")
	require.NoError(t, session.Close())
	require.NoDirExists(t, session.Directory())
	require.NoError(t, session.Close(), "cleanup is idempotent")
}

func TestNewSessionSupportsConcurrentRuns(t *testing.T) {
	const runCount = 32
	sessions := make(chan *Session, runCount)
	failures := make(chan error, runCount)
	var group sync.WaitGroup
	for range runCount {
		group.Go(func() {
			session, err := NewSession()
			if err != nil {
				failures <- err
				return
			}
			sessions <- session
		})
	}
	group.Wait()
	close(sessions)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	seen := make(map[string]bool, runCount)
	for session := range sessions {
		require.False(t, seen[session.Directory()])
		seen[session.Directory()] = true
		require.NoError(t, session.Close())
	}
	require.Len(t, seen, runCount)
}

func TestSessionRecoveryDeletesOnlyMarkedStorageForLockedRepository(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	owned := planSession()
	owned.root = canonicalWorkflowRoot(root)
	require.NoError(t, owned.create())
	other := planSession()
	other.root = canonicalWorkflowRoot(t.TempDir())
	require.NoError(t, other.create())
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	unmarked, err := os.MkdirTemp(os.TempDir(), "ddtest-customer-")
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(unmarked, "customer data"), "preserve")
	var output bytes.Buffer
	require.NoError(t, recoverSessions(root, &output))
	require.NoDirExists(t, owned.Directory())
	require.DirExists(t, other.Directory())
	require.FileExists(t, filepath.Join(unmarked, "customer data"))
	require.Contains(t, output.String(), "Recovered abandoned testdrive session")
}
