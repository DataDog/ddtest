// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// After a prescribed install, ddtest's own setup edits extend the manifest and
// lockfile without --allow-dirty; any other uncommitted change still needs it.
func TestOnlyPrescribedDependencyChanges(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
	}
	packages := []string{"nyc", "@istanbuljs/esm-loader-hook"}
	write("package.json", `{"devDependencies":{"nyc":"17.1.0","@istanbuljs/esm-loader-hook":"0.3.0"}}`)
	require.True(t, OnlyPrescribedDependencyChanges(root, []string{"package.json", "pnpm-lock.yaml"}, packages))

	require.False(t, OnlyPrescribedDependencyChanges(root, []string{"package.json", ".github/workflows/ci.yml"}, packages), "a dirty workflow still needs approval")
	require.False(t, OnlyPrescribedDependencyChanges(root, []string{"package.json"}, nil), "no prescribed install, nothing to extend")
	write("package.json", `{"devDependencies":{"nyc":"17.1.0"}}`)
	require.False(t, OnlyPrescribedDependencyChanges(root, []string{"package.json"}, packages), "the manifest must declare every prescribed package")
}
