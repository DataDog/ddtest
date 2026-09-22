// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package framework

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestTestdriveUsesPackageManagerAndProjectScript(t *testing.T) {
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = "" })
	for _, manager := range []struct {
		lock, command string
		args          []string
	}{
		{"", "npm", []string{"run", "test:unit", "--", "--run"}},
		{"yarn.lock", "yarn", []string{"run", "test:unit", "--run"}},
		{"pnpm-lock.yaml", "pnpm", []string{"run", "test:unit", "--run"}},
		{"bun.lock", "bun", []string{"run", "test:unit", "--run"}},
	} {
		t.Run(manager.command, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test:unit":"vitest --config custom.ts"}}`), 0644))
			if manager.lock != "" {
				require.NoError(t, os.WriteFile(filepath.Join(root, manager.lock), nil, 0644))
			}
			command, args, err := TestdriveCommand(root, NewVitest())
			require.NoError(t, err)
			require.Equal(t, manager.command, command)
			require.Equal(t, manager.args, args)
		})
	}
}

func TestTestdrivePreservesExplicitCommandArguments(t *testing.T) {
	t.Cleanup(func() { settings.Get().Command = "" })
	settings.Get().Command = `npm run smoke -- --config "config with spaces.js"`
	command, args, err := TestdriveCommand(t.TempDir(), NewMocha())
	require.NoError(t, err)
	require.Equal(t, "npm", command)
	require.Equal(t, []string{"run", "smoke", "--", "--config", "config with spaces.js"}, args)
	settings.Get().Command = `npm "`
	_, _, err = TestdriveCommand(t.TempDir(), NewMocha())
	require.ErrorContains(t, err, "parse testdrive")
}

func TestTestdriveDoesNotGuessBetweenScripts(t *testing.T) {
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = "" })
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"unit":"mocha test/unit","integration":"mocha test/integration"}}`), 0644))
	_, _, err := TestdriveCommand(root, NewMocha())
	require.ErrorContains(t, err, "--command")
}

func TestJestDetectionSupportsNamedTestScripts(t *testing.T) {
	settings.Get().Command = ""
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test:unit":"jest"},"devDependencies":{"jest":"30.5.1"}}`), 0644))
	jest := NewJest()
	found, err := jest.Detect(root)
	require.NoError(t, err)
	require.True(t, found)
	command, args, err := TestdriveCommand(root, jest)
	require.NoError(t, err)
	require.Equal(t, "npm", command)
	require.Equal(t, []string{"run", "test:unit", "--", "--runInBand"}, args)
}
