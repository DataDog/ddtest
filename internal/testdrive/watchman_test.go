package testdrive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionStopsPrivateWatchmanAndPreservesAnotherDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket lifecycle")
	}
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("Watchman is not installed")
	}
	first, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	other, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, other.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, session := range []*Session{first, other} {
		env := map[string]string{}
		require.NoError(t, session.watchmanEnvironment(env))
		// Exercise the executable Jest's fb-watchman client discovers on PATH.
		output, err := exec.CommandContext(ctx, filepath.Join(session.watchman.directory, "bin/watchman"), "get-pid").CombinedOutput()
		require.NoError(t, err, string(output))
	}
	owned := first.watchman.directory
	require.NoError(t, first.Close())
	require.NoDirExists(t, owned)
	output, err := exec.CommandContext(ctx, other.watchman.binary, append(other.watchman.args(), "--no-spawn", "--no-local", "get-pid")...).CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "pid")
}

func TestWatchmanCleanupDoesNotCertifyUnverifiedStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket lifecycle")
	}
	if _, err := exec.LookPath("watchman"); err != nil {
		t.Skip("Watchman is not installed")
	}
	session, err := NewSession()
	require.NoError(t, err)
	require.NoError(t, session.watchmanEnvironment(map[string]string{}))
	directory := session.watchman.directory
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)); require.NoError(t, session.Close()) })
	// Cancellation can race daemon startup. A missing socket is not proof that
	// an invoked daemon never started or has stopped.
	require.NoError(t, os.WriteFile(filepath.Join(directory, "started"), nil, 0600))
	require.ErrorContains(t, session.Close(), "cannot verify Watchman startup/exit")
	require.DirExists(t, directory, "keep the exact owned path for recovery")
}
