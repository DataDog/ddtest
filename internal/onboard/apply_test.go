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
	"go.yaml.in/yaml/v3"
)

const ordinaryVitestCI = `name: CI # keep this comment
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22'}
      - run: npm ci
      - run: npm run build # preserve prerequisites
      - name: Unit tests
        run: npx vitest run --coverage # retain flags and comment
      - uses: codecov/codecov-action@v5
  other:
    steps:
      - run: echo unrelated
`

func TestApplyVitestSetupPreservesCIAndIsIdempotent(t *testing.T) {
	root := newJestRepository(t, ordinaryVitestCI)
	writeScripts(t, root, map[string]string{"build": "tsc"})
	changes, err := applyVitestSetup(root, "6.21.0")
	require.NoError(t, err)
	require.Len(t, changes, 2)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var workflow ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Len(t, workflow.Jobs["test"].Steps, 6)
	require.Equal(t, "npm run build", workflow.Jobs["test"].Steps[2].Run)
	require.Equal(t, "npx vitest run --coverage", workflow.Jobs["test"].Steps[4].Run)
	require.Empty(t, checkJestBootstrap(workflow, workflow.Jobs["test"], workflow.Jobs["test"].Steps[4], nil, true))
	require.Equal(t, "6.21.0", workflow.Jobs["test"].Steps[3].With["js-tracer-version"])
	require.Contains(t, string(data), "# preserve prerequisites")
	require.Contains(t, string(data), "# retain flags and comment")
	require.Contains(t, string(data), "  other:\n    steps:\n      - run: echo unrelated\n")
	_, err = os.Stat(filepath.Join(root, ".npmignore"))
	require.True(t, os.IsNotExist(err), "do not disable npm's gitignore fallback")
	changes, err = applyVitestSetup(root, "6.21.0")
	require.NoError(t, err)
	require.Empty(t, changes)
	_, err = applyVitestSetup(root, "6.22.0")
	require.ErrorContains(t, err, "Existing Datadog setup differs")
	after, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, data, after, "do not silently upgrade an existing CI tracer")
}

func TestApplyVitestSetupPlansAllFilesBeforeWriting(t *testing.T) {
	for _, replacement := range []string{
		"        run: |\n          npx vitest run --coverage\n",
		"        run: npx vitest run --coverage\n        env: {NODE_OPTIONS: --max-old-space-size=4096}\n",
		"        run: npx vitest run --coverage\n        if: github.event_name == 'push'\n",
	} {
		t.Run(replacement, func(t *testing.T) {
			root := newJestRepository(t, ordinaryVitestCI)
			second := strings.Replace(ordinaryVitestCI, "        run: npx vitest run --coverage # retain flags and comment\n", replacement, 1)
			path := filepath.Join(root, ".github/workflows/z-custom.yml")
			require.NoError(t, os.WriteFile(path, []byte(second), 0644))
			changes, err := applyVitestSetup(root, "6.21.0")
			require.Error(t, err)
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Location, "z-custom.yml / test")
			require.Empty(t, changes)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Equal(t, ordinaryVitestCI, string(data), "earlier ordinary files must remain untouched")
		})
	}
}

func TestApplyVitestSetupRefusesSymlinkAndUnsafeVersion(t *testing.T) {
	root := newJestRepository(t, ordinaryVitestCI)
	external := filepath.Join(t.TempDir(), "ignore")
	require.NoError(t, os.WriteFile(external, []byte("owned elsewhere\n"), 0644))
	require.NoError(t, os.Symlink(external, filepath.Join(root, ".gitignore")))
	changes, err := applyVitestSetup(root, "6.21.0")
	require.ErrorContains(t, err, "symlink")
	require.Empty(t, changes)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, ordinaryVitestCI, string(data))
	_, err = applyVitestSetup(root, "6.21.0\n    api_key: injected")
	require.ErrorContains(t, err, "exact numeric tracer release")
}
