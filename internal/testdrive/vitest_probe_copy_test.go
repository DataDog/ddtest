// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestVitestCopiedProbeIntegration(t *testing.T) {
	root := vitestIntegrationRoot(t)
	config := `export default {test:{pool:'forks',include:['original.test.js']}}`
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), config)
	source := filepath.Join(root, "original.test.js")
	original := `import {test} from 'vitest'; const ddtestAttempts = 'customer binding'; test('customer test', () => { throw new Error('must be filtered') })`
	requireWriteFile(t, source, original)
	baseline := validationRun{Tests: []jestTest{{File: source, Name: "customer test", Status: "passed"}}}
	probe, err := createProbe(root, baseline, vitestProbeSource)
	require.NoError(t, err)
	drive := &Testdrive{command: "npx", args: []string{"vitest", "run", "--config", "vitest.config.mjs", "original.test.js"}, executor: &ext.DefaultCommandExecutor{}}
	_, _, files, err := drive.inspectVitest(t.Context(), t.TempDir(), probe)
	require.NoError(t, err)
	require.Empty(t, files, "an exact allowlist excludes a new sibling")
	require.NoError(t, removeProbe(root, probe))
	copyRoot := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, workspace.Copy(t.Context(), root, copyRoot, nil))
	copiedProbe, err := appendVitestProbe(root, copyRoot, baseline)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(copyRoot, "original.test.js"), copiedProbe)
	t.Chdir(copyRoot)
	_, projects, files, err := drive.inspectVitest(t.Context(), t.TempDir(), copiedProbe)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	require.Equal(t, []string{copiedProbe}, files)
	for _, mode := range []string{"pass", "fail"} {
		command := exec.CommandContext(t.Context(), "node", filepath.Join(copyRoot, "node_modules/vitest/vitest.mjs"), "run", "--config", "vitest.config.mjs", copiedProbe, "--testNamePattern="+drive.vitestProbeNamePattern())
		command.Env = append(os.Environ(), "DDTEST_PROBE_MODE="+mode)
		data, err := command.CombinedOutput()
		if mode == "pass" {
			require.NoError(t, err, string(data))
		} else {
			require.Error(t, err, string(data))
			require.Contains(t, string(data), "ddtest intentional failure")
		}
		require.NotContains(t, string(data), "must be filtered")
	}
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, original, string(data))
	data, err = os.ReadFile(filepath.Join(root, "vitest.config.mjs"))
	require.NoError(t, err)
	require.Equal(t, config, string(data))
}

func TestAppendVitestProbeRejectsEscapingLink(t *testing.T) {
	source, target, outside := t.TempDir(), t.TempDir(), t.TempDir()
	file := filepath.Join(source, "original.test.js")
	requireWriteFile(t, file, "customer source")
	external := filepath.Join(outside, "external.test.js")
	requireWriteFile(t, external, "external source")
	require.NoError(t, os.Symlink(external, filepath.Join(target, "original.test.js")))
	_, err := appendVitestProbe(source, target, validationRun{Tests: []jestTest{{File: file, Status: "passed"}}})
	require.ErrorContains(t, err, "escapes")
	data, err := os.ReadFile(external)
	require.NoError(t, err)
	require.Equal(t, "external source", string(data))
}
