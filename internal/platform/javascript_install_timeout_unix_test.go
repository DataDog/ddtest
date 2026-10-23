//go:build darwin || linux

package platform

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/stretchr/testify/require"
)

func TestTestdriveTracerInstallationHonorsTheCommandTimeout(t *testing.T) {
	bin := t.TempDir()
	// A stalled registry or lifecycle script; node is absent, so detection
	// finds no project tracer and installation runs.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700))
	t.Setenv("PATH", bin)
	javascript := &JavaScript{executor: &ext.DefaultCommandExecutor{}}

	started := time.Now()
	_, err := javascript.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: "6.20.1", Timeout: 200 * time.Millisecond})
	require.ErrorContains(t, err, "npm exceeded the 200ms per-command limit; raise --timeout")
	require.Less(t, time.Since(started), 10*time.Second)
}
