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

const compositeSetup = `inputs:
  node: {default: '22'}
runs:
  using: composite
  steps:
    - uses: actions/setup-node@v6
      with: {node-version: '${{ inputs.node }}'}
    - run: pnpm install
      shell: bash
`

func writeComposite(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, ".github/actions", name, "action.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(data), 0644))
}

func TestCompositeNestingAndPathBoundary(t *testing.T) {
	root := t.TempDir()
	writeComposite(t, root, "outer", "runs:\n  using: composite\n  steps:\n    - uses: ./.github/actions/inner\n      with: {node: '24'}\n")
	writeComposite(t, root, "inner", compositeSetup)
	steps := expandCompositeSteps(root, "workflow", []runtimeStep{{Uses: "./.github/actions/outer"}})
	require.Len(t, steps, 2)
	require.Equal(t, "24", steps[0].With["node-version"])
	require.Equal(t, ".github/actions/inner/action.yml / step 1", steps[0].Source)
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "action.yml"), []byte(compositeSetup), 0644))
	require.NoError(t, os.Symlink(out, filepath.Join(root, "outside")))
	_, err := localActionPath(root, "./outside")
	require.ErrorContains(t, err, "escapes")
	_, err = localActionPath(root, "./../outside")
	require.Error(t, err)
}
