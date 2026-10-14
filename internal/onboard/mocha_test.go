// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMochaCoverageCommandsPreserveCIEntryPoints(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"c8 --reporter=lcov --reporter=text mocha test/test.js","test-esm":"mocha test/test.mjs","opaque":"node test.js"}}`), 0600))
	for _, command := range []string{"npm test", "npm run test-esm", "npx --no-install nyc -- npm test", "nyc --reporter lcov c8 --check-coverage mocha test/test.js"} {
		t.Run(command, func(t *testing.T) {
			commands := ValidationCommands(root, command, "mocha")
			require.Len(t, commands, 1)
			require.Equal(t, strings.TrimPrefix(command, "npx --no-install "), commands[0].Command)
			actual, err := MochaValidationCommand(root, command)
			require.NoError(t, err)
			require.Equal(t, command, actual)
		})
	}
	for _, command := range []string{"npm test && npm run test-esm", "c8 --unknown value mocha", "npm run opaque", "c8 --reporter"} {
		_, err := MochaValidationCommand(root, command)
		require.Error(t, err, command)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yml"), []byte("jobs:\n  test:\n    steps:\n      - run: npm test\n      - run: npm run test-esm\n"), 0600))
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	_, err = MochaValidationCommand(root, "")
	require.ErrorContains(t, err, "--all")
}

func TestMochaCoverageFollowsPackageAliases(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"npm run covered","covered":"npx --no-install nyc -- c8 --reporter=text mocha test.js","plain":"c8 mocha test.js","test-esm":"npx --no-install nyc -- mocha test.mjs"}}`), 0600))
	for _, command := range []string{"npm test", "npm run test-esm", "c8 -- npm test"} {
		require.True(t, MochaUsesNYC(root, command), command)
	}
	require.False(t, MochaUsesNYC(root, "npm run plain"))
	require.False(t, MochaUsesNYC(root, "npm test && npm run plain"))
}
