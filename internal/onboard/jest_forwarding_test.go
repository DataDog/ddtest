// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestCompoundJestForwardsOnlyToJestAndPreservesSetup(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "NODE_ENV=test jest --config unit.js && npm run lint", "lint": "eslint ."})
	bin := filepath.Join(root, "node_modules", ".bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	for name, script := range map[string]string{
		"jest": "#!/bin/sh\nprintf '%s\\n' \"$NODE_ENV:$*\" > jest-args\n",
		"npm":  "#!/bin/sh\nprintf '%s\\n' \"$*\" > setup-args\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0755))
	}
	selected, err := ForwardJestCommand(root, "npm test")
	require.NoError(t, err)
	words, err := shellquote.Split(selected)
	require.NoError(t, err)
	cmd := exec.CommandContext(t.Context(), words[0], append(words[1:], "--listTests", "--json", "--runTestsByPath", "probe file.js")...)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	require.NoError(t, err, string(data))
	data, err = os.ReadFile(filepath.Join(root, "jest-args"))
	require.NoError(t, err)
	require.Equal(t, "test:--config unit.js --listTests --json --runTestsByPath probe file.js\n", string(data))
	data, err = os.ReadFile(filepath.Join(root, "setup-args"))
	require.NoError(t, err)
	require.Equal(t, "run lint\n", string(data))
}

func TestForwardJestRejectsMultipleRunnersAndPreservesUnknownCommands(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest && jest"})
	_, err := ForwardJestCommand(root, "npm test")
	require.ErrorContains(t, err, "Jest invocations: 2")
	selected, err := ForwardJestCommand(root, "node custom-wrapper.js")
	require.NoError(t, err)
	require.Equal(t, "node custom-wrapper.js", selected)
}

func TestSetupFormsAndMissingReleaseInstrumentation(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22'}
      - run: npm -g i yarn
      - run: yarn
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js}
      - run: yarn test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
      - run: npm run site
  release:
    steps:
      - run: yarn test
`)
	writeScripts(t, root, map[string]string{"test": "jest", "site": "documentation build src/index.js"})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
	})
	require.Equal(t, "incompatible", result.Status)
	require.Len(t, result.Jobs, 2)
	require.Len(t, result.Review, 1)
	for _, job := range result.Jobs {
		if job.Job == "release" {
			require.Equal(t, "missing_instrumentation", job.Code)
			require.Contains(t, job.Reason, "release before step")
		} else {
			require.Equal(t, "compatible", job.Status)
		}
	}
}

func TestProbeSkipsOnlyRecognizedLintAndPreservesFullSuiteFailure(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "yarn lint && jest", "lint": "eslint ./src"})
	bin := filepath.Join(root, "node_modules", ".bin")
	require.NoError(t, os.MkdirAll(bin, 0755))
	// A failing linter models the real TSyringe probe's prettier errors.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "yarn"), []byte("#!/bin/sh\nexit 7\n"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bin, "jest"), []byte("#!/bin/sh\nprintf '%s' \"$*\"\n"), 0755))
	selected, err := ForwardJestCommand(root, "yarn test")
	require.NoError(t, err)
	words, err := shellquote.Split(selected)
	require.NoError(t, err)
	for _, probe := range []string{"0", "1"} {
		cmd := exec.CommandContext(t.Context(), words[0], append(words[1:], "--runTestsByPath", "probe.test.ts")...)
		cmd.Dir, cmd.Env = root, append(os.Environ(), "DDTEST_JEST_PROBE="+probe)
		output, err := cmd.CombinedOutput()
		if probe == "0" {
			require.Error(t, err, "real suite must retain lint failure")
		} else {
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "--runTestsByPath probe.test.ts")
		}
	}
	writeScripts(t, root, map[string]string{"lint": "node setup.js"})
	require.False(t, lintOnly(root, "yarn lint", 0), "a script name is not proof of lint-only setup")
}
