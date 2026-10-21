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

func TestSymlinkedRootKeepsPrerequisitesRepositoryRelative(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - run: pnpm add -D react@18
      - run: sed -i s/a/b/ config.js
      - run: npm run build
      - run: jest
`)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(root, link))
	writeScripts(t, root, map[string]string{"build": "pnpm run '/^build:/'", "build:lib": "tsc"})
	scope, err := DiscoverValidationScope(link, "jest")
	require.NoError(t, err)
	require.Equal(t, []string{"jest"}, commandNames(scope.Commands))
	require.Equal(t, []string{"pnpm add -D react@18", "sed -i s/a/b/ config.js", "npm run build"}, buildNames(scope.Commands[0].Prerequisites))
	for _, build := range scope.Commands[0].Prerequisites {
		require.Equal(t, ".", build.Directory)
	}
	for _, command := range []string{"pnpm add -D react@18 && jest", "sed -i s/a/b/ config.js && jest", "npm run build && jest"} {
		result := resolveJestCommand(link, command, nil)
		require.Empty(t, result.Reason, command)
		require.Equal(t, []string{"jest"}, commandNames(result.Tests))
		require.NotEmpty(t, result.Tests[0].Prerequisites, command)
		for _, build := range result.Tests[0].Prerequisites {
			require.Equal(t, ".", build.Directory, command)
		}
	}
}
