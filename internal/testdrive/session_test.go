// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
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
	require.Equal(t, session.ID(), filepath.Base(session.Directory()))
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
