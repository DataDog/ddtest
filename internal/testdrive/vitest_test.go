// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestPrepareVitestEntrypointAndCommandPolicy(t *testing.T) {
	oldCommand, oldConfig := settings.Get().Command, settings.Get().VitestConfig
	t.Cleanup(func() {
		settings.Get().Command, settings.Get().VitestConfig = oldCommand, oldConfig
	})
	settings.Get().Command, settings.Get().VitestConfig = "", "config with spaces/vitest.ts"
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vitest":"4"}}`)
	drive, err := Prepare(PrepareOptions{TracerVersion: "latest"})
	require.NoError(t, err)
	require.Equal(t, "node", drive.command)
	require.Equal(t, []string{filepath.Join(drive.session.Directory(), "vitest.mjs"), "--config", "config with spaces/vitest.ts"}, drive.args)
	var preview bytes.Buffer
	drive.Preview(&preview)
	require.Contains(t, preview.String(), "vitest.mjs")
	require.Contains(t, preview.String(), "config with spaces/vitest.ts")
	_, err = os.Stat(drive.session.Directory())
	require.True(t, os.IsNotExist(err), "prepare/preview must not create the entrypoint")
	settings.Get().Command = "pnpm exec vitest run"
	_, err = Prepare(PrepareOptions{TracerVersion: "latest"})
	require.ErrorContains(t, err, "does not support --command")
	require.ErrorContains(t, err, "--vitest-config")
}

func TestVitestTestdriveMissingDependencyProducesActionableLog(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is unavailable")
	}
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{}`)
	require.NoError(t, writeVitestEntrypoint(root))
	output, err := (&ext.DefaultCommandExecutor{}).CombinedOutput(t.Context(), "node", []string{filepath.Join(root, "vitest.mjs")}, map[string]string{"NODE_OPTIONS": ""})
	require.Error(t, err)
	require.Contains(t, string(output), "Vitest is not resolvable from this project")
	require.Contains(t, string(output), "Install project dependencies")
}

func TestVitestTestdriveConfiguredSuiteIntegration(t *testing.T) {
	nodeModules := os.Getenv("DDTEST_VITEST_NODE_MODULES")
	if nodeModules == "" {
		t.Skip("DDTEST_VITEST_NODE_MODULES is not set")
	}
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Symlink(nodeModules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"type":"module"}`)
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), `throw new Error('default config must not be used')`)
	config := filepath.Join(root, "config with spaces.mjs")
	requireWriteFile(t, config, `export default { test: {
  include: ['checks/*.check.js'], exclude: ['checks/excluded.check.js'],
  setupFiles: ['./setup.js'], reporters: ['json'], outputFile: './results.json',
} }`)
	requireWriteFile(t, filepath.Join(root, "setup.js"), `globalThis.configuredSetup = true`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "checks"), 0755))
	for _, name := range []string{"one", "two"} {
		requireWriteFile(t, filepath.Join(root, "checks", name+".check.js"), `import { expect, test } from 'vitest'; test('configured test', () => expect(globalThis.configuredSetup).toBe(true))`)
	}
	requireWriteFile(t, filepath.Join(root, "checks", "excluded.check.js"), `throw new Error('excluded file must not run')`)
	oldCommand, oldConfig := settings.Get().Command, settings.Get().VitestConfig
	t.Cleanup(func() {
		settings.Get().Command, settings.Get().VitestConfig = oldCommand, oldConfig
	})
	settings.Get().Command, settings.Get().VitestConfig = "", config
	session := t.TempDir()
	require.NoError(t, writeVitestEntrypoint(session))
	command, args, err := vitestTestdriveCommand(session)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := (&ext.DefaultCommandExecutor{}).CombinedOutput(ctx, command, args, map[string]string{"NODE_OPTIONS": ""})
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "JSON report written")
	result, err := os.ReadFile(filepath.Join(root, "results.json"))
	require.NoError(t, err)
	require.Contains(t, string(result), `"numPassedTests":2`)
	require.NotContains(t, string(result), "excluded.check.js")
}
