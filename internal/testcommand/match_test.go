// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testcommand

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchesPackageScripts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"npm run unit","unit":"jest --ci","cycle":"npm run cycle","lint":"eslint ."}}`), 0600))
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"npm test", true},
		{"pnpm run unit", true},
		{"npm run lint", false},
		{"npm run cycle", false},
		{"npm install jest", false},
		{"npx --no-install jest --ci", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			require.Equal(t, tc.want, Matches(root, tc.command, "javascript", "jest"))
		})
	}
}

func TestMatchesToxEnvironments(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(`[tool.tox]
env_list = ["tests"]
[tool.tox.env.tests]
commands = [["python", "-m", "pytest"]]
[tool.tox.env.lint]
commands = [["ruff", "check"]]
`), 0600))
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{"tox", true},
		{"tox -e lint", false},
		{"tox --environment=lint,tests", true},
		{"tox -etests", true},
		{"tox -e unknown", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			require.Equal(t, tc.want, Matches(root, tc.command, "python", "pytest"))
		})
	}
}
