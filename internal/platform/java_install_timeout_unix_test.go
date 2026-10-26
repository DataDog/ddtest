//go:build darwin || linux

package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJavaTracerInstallationHonorsTimeout(t *testing.T) {
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700))
	t.Setenv("PATH", bin)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := time.Now()
	_, err := NewJava().InstallTestdriveTracer(ctx, TracerOptions{Directory: t.TempDir(), Version: "1.67.1", Timeout: 100 * time.Millisecond})
	require.ErrorContains(t, err, "curl exceeded the 100ms per-command limit; raise --timeout")
	require.Less(t, time.Since(started), 10*time.Second)
}
