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

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/stretchr/testify/require"
)

func TestNewSession(t *testing.T) {
	repositoryRoot := t.TempDir()

	session, err := NewSession(repositoryRoot)
	require.NoError(t, err)

	require.Equal(t, session.ID(), filepath.Base(session.Directory()))
	require.Equal(
		t,
		filepath.Join(repositoryRoot, constants.PlanDirectory, "testdrive"),
		filepath.Dir(session.Directory()),
	)
	info, err := os.Stat(session.Directory())
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestNewSessionSupportsConcurrentRuns(t *testing.T) {
	const runCount = 32

	repositoryRoot := t.TempDir()
	start := make(chan struct{})
	sessions := make(chan *Session, runCount)
	errors := make(chan error, runCount)

	var group sync.WaitGroup
	for range runCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start

			session, err := NewSession(repositoryRoot)
			if err != nil {
				errors <- err
				return
			}
			sessions <- session
		}()
	}

	close(start)
	group.Wait()
	close(sessions)
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}

	seen := make(map[string]struct{}, runCount)
	for session := range sessions {
		_, exists := seen[session.Directory()]
		require.False(t, exists, "session directory was reused: %s", session.Directory())
		seen[session.Directory()] = struct{}{}
	}
	require.Len(t, seen, runCount)
}
