// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package platform

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestPnPLoaderFindsClosestWorkspaceAndStopsAtCheckout(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(app, 0700))
	loader := filepath.Join(root, ".pnp.cjs")
	require.NoError(t, os.WriteFile(loader, []byte(""), 0600))
	found, err := FindPnPLoader(app)
	require.NoError(t, err)
	require.Equal(t, loader, found)
	closest := filepath.Join(app, ".pnp.js")
	require.NoError(t, os.WriteFile(closest, []byte(""), 0600))
	found, err = FindPnPLoader(app)
	require.NoError(t, err)
	require.Equal(t, closest, found)
	require.NoError(t, os.Remove(closest))
	require.NoError(t, os.WriteFile(filepath.Join(app, ".git"), []byte("gitdir: elsewhere"), 0600))
	found, err = FindPnPLoader(app)
	require.NoError(t, err)
	require.Empty(t, found)
}
