// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoveryRetainsEveryReviewedTool(t *testing.T) {
	result := resolveJestCommand(t.TempDir(), "eslint && docusaurus && jest", nil)
	require.Contains(t, result.ReviewReason, "eslint")
	require.Contains(t, result.ReviewReason, "docusaurus")
}

func TestNpmExecConsumesItsSeparator(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"npm exec jest -- --runInBand", "npm exec -- jest --runInBand"} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason)
		require.Equal(t, []string{"jest --runInBand"}, commandNames(result.Tests))
	}
	result := resolveJestCommand(root, "pnpm exec jest -- --runInBand", nil)
	require.Equal(t, []string{"jest -- --runInBand"}, commandNames(result.Tests))
}

func TestUnresolvedPackageSetupStaysOnDependentTests(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest", "pretest": "node setup.js"})
	result := resolveJestCommand(root, "npm test", nil)
	require.Len(t, result.Tests, 1)
	require.Contains(t, strings.Join(result.Tests[0].UnvalidatedSetup, "\n"), "pretest")
	writeScripts(t, root, map[string]string{"build": "pnpm run '/^build:/'", "build:lib": "tsc", "prebuild": "jest"})
	result = resolveJestCommand(root, "npm run build", nil)
	require.Len(t, result.Builds, 1)
	require.Contains(t, result.Reason, "prebuild")
}

func TestMetadataCannotLoseSetupOrDirectoryState(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"test": "jest"})
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `echo "$(node setup.js)" && jest`}, "javascript", "jest")
	require.Len(t, result.Tests, 1)
	require.Contains(t, strings.Join(result.Tests[0].UnvalidatedSetup, "\n"), "node setup.js")
	result = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: `echo "dir=$(npm config get cache)" >> "$GITHUB_OUTPUT" && cd app && jest`}, "javascript", "jest")
	require.NotEmpty(t, result.Reason)
	if len(result.Tests) > 0 {
		require.Len(t, result.Tests, 1)
		require.Equal(t, "app", result.Tests[0].Directory)
		require.NotEmpty(t, result.Tests[0].UnvalidatedSetup)
	}
}

func TestTildeAndPrintfStateStayUnresolved(t *testing.T) {
	root := t.TempDir()
	result := resolveJestCommand(root, "jest --config ~/jest.config.js", nil)
	require.Empty(t, result.Tests)
	require.NotEmpty(t, result.Reason)
	result = resolveJestCommand(root, `jest --config '~/jest.config.js'`, nil)
	require.Empty(t, result.Reason, "quoted tilde is a literal path")
	result = resolveJestCommand(root, `printf -v PATH '%s' ./custom-bin && jest`, nil)
	require.Len(t, result.Tests, 1)
	require.Contains(t, strings.Join(result.Tests[0].UnvalidatedSetup, "\n"), "variable")
}

func TestScopeRetainsMutationsAndUnresolvedBuildInputs(t *testing.T) {
	for _, command := range []string{"rsync -a fixtures/ src/", "rimraf src"} {
		root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: "+command+"\n      - run: jest\n")
		scope, err := DiscoverValidationScope(root, "jest")
		require.NoError(t, err)
		require.Len(t, scope.Commands, 1)
		require.Contains(t, strings.Join(scope.Commands[0].UnvalidatedSetup, "\n"), command)
	}
	// A build step whose condition only CI decides stays unresolved.
	root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: rollup -c\n        if: steps.setup.outputs.ready\n      - run: jest\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.NotEmpty(t, scope.Unresolved)
	require.NotEmpty(t, scope.Commands[0].UnvalidatedSetup)
	// One that reads a value only CI provides runs without it, reported.
	root = newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: rollup -c\n        env: {MODE: '${{ secrets.MODE }}'}\n      - run: jest\n")
	scope, err = DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Unresolved)
	require.Equal(t, []string{"MODE"}, scope.Commands[0].WithoutCIValues)
}

func TestCompositeSubstitutesExecutableInputFields(t *testing.T) {
	root := newJestRepository(t, `jobs:
  tests:
    steps:
      - uses: ./.github/actions/test
        with: {script: check, dir: app, mode: ci}
`)
	writeScripts(t, filepath.Join(root, "app"), map[string]string{"check": "jest"})
	writeComposite(t, root, "test", `inputs:
  script: {default: test}
  dir: {default: src}
  mode: {default: default}
runs:
  using: composite
  steps:
    - run: npm run ${{ inputs.script }}
      shell: bash
      working-directory: ${{ inputs.dir }}
      env: {MODE: '${{ inputs.mode }}'}
`)
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "app", scope.Commands[0].Directory)
	require.Equal(t, "ci", scope.Commands[0].Environment["MODE"])
}

func TestNodeOptionsRemainValidForNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for NODE_OPTIONS parser verification")
	}
	for _, value := range []string{`--title="unit tests" --experimental-vm-modules`, `--title=C:\repo\tests --experimental-vm-modules`} {
		options, err := ordinaryNodeOptions(value + " -r dd-trace/ci/init")
		require.NoError(t, err)
		cmd := exec.Command(node, "-e", `console.log(JSON.stringify({title:process.title,vm:require('node:vm').SourceTextModule!==undefined}))`)
		cmd.Env = append(cmd.Environ(), "NODE_OPTIONS="+options)
		data, err := cmd.CombinedOutput()
		require.NoError(t, err, string(data))
		var actual struct {
			Title string
			VM    bool
		}
		require.NoError(t, json.Unmarshal(data, &actual), string(data))
		want := "unit tests"
		if strings.Contains(value, `C:`) {
			want = `C:\repo\tests`
		}
		require.Equal(t, want, actual.Title, fmt.Sprintf("options=%s", options))
		require.True(t, actual.VM)
	}
}
