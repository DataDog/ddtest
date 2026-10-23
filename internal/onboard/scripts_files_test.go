// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiteralFileOperationsDoNotHideUnknownPrograms(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{
		"rimraf ./dist", "CI=true rimraf ./dist", "rm build/index.html.bak", "rsync -a docs/ build/docs",
		"sed -i.bak 's#</body>#<script>example</script></body>#g' build/index.html",
		`sed -i.bak 's/<\/body>/<script src="\..\/global\/luxon.js"><\/script><\/body>/g' build/api-docs/index.html`,
	} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason, command)
		require.False(t, result.Matched, command)
	}
	for _, command := range []string{
		"sed 'e jest' file", "sed 's/a/jest/e' file", "sed 's/a/b/;e jest' file", "sed -f script file",
		"rsync -e wrapper docs/ build", "rsync -a docs/ host:dir", "rimraf --glob './*'", "rm $(node pick.js)",
		"unknown-cleaner dist", "rimraf ../outside",
	} {
		result := resolveJestCommand(root, command+" && jest", nil)
		require.NotEmpty(t, result.Reason, command)
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries, "discovery must not execute file operations")
}
