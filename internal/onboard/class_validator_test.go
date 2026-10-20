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

const classValidatorWorkflow = `jobs:
  tests:
    runs-on: ubuntu-latest
    strategy:
      matrix: {node: ['lts/*', 'current']}
    steps:
      - uses: actions/setup-node@v3
        with: {node-version: '${{ matrix.node }}'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.17.0'}
      - run: npm run test:ci
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
      - run: jq 'del(.devDependencies) | del(.scripts)' package.json > build/package.json
      - run: npm publish ./build
`

func TestPackagingCannotHideTestCommandsOrLifecycleHooks(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest"})
	for _, command := range []string{
		`jq 'del(.scripts)' package.json > build/package.json && jest`,
		"jq \"$(node tests.js)\" package.json > build/package.json",
		`jq '.' package.json > /tmp/output.json`,
		`npm publish ./build && jest`,
		`npm publish "${TARGET}"`,
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.False(t, result.Review, command)
		require.True(t, result.Matched || result.Reason != "", command)
	}
	for _, hook := range []string{"prepublishOnly", "prepack", "prepare", "postpack", "publish", "postpublish"} {
		writeScripts(t, filepath.Join(root, "build"), map[string]string{hook: "jest"})
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm publish ./build"}, "javascript", "jest")
		require.False(t, result.Review)
		require.Contains(t, result.Reason, hook)
	}
}

func TestJestCommandSelectionPreservesAliasesAndRejectsAmbiguity(t *testing.T) {
	root := newJestRepository(t, classValidatorWorkflow)
	writeScripts(t, root, map[string]string{"test": "jest", "test:ci": "npm test -- --coverage --runInBand"})
	command, err := JestValidationCommand(root)
	require.NoError(t, err)
	require.Equal(t, "npm run test:ci", command)
	path := filepath.Join(root, ".github/workflows/test.yml")
	require.NoError(t, os.WriteFile(path, []byte(classValidatorWorkflow+"      - run: npm test\n"), 0644))
	_, err = JestValidationCommand(root)
	require.ErrorContains(t, err, "multiple Jest CI commands")
	require.NoError(t, os.WriteFile(path, []byte(classValidatorWorkflow), 0644))
	for _, script := range []string{"jest && jest --config other.js", "node setup.js && jest"} {
		writeScripts(t, root, map[string]string{"test:ci": script})
		_, err = JestValidationCommand(root)
		require.ErrorContains(t, err, "--command", script)
	}
}

func TestJestCommandSelectionFallbackAndUnsafeCI(t *testing.T) {
	root := newJestRepository(t, "jobs: {}")
	writeScripts(t, root, map[string]string{"test": "jest", "test:ci": "jest --coverage"})
	command, err := JestValidationCommand(root)
	require.NoError(t, err)
	require.Equal(t, "npm run test:ci", command)
	require.NoError(t, os.WriteFile(filepath.Join(root, "yarn.lock"), nil, 0644))
	command, err = JestValidationCommand(root)
	require.NoError(t, err)
	require.Equal(t, "yarn run test:ci", command)
	workflow := filepath.Join(root, ".github/workflows/test.yml")
	for _, command := range []string{"npm run test:ci", "node ci-tests.js"} {
		require.NoError(t, os.WriteFile(workflow, []byte("jobs:\n  test:\n    steps:\n      - run: "+command+"\n"), 0644))
		writeScripts(t, root, map[string]string{"test": "jest", "test:ci": "jest --coverage", "pretest:ci": "node setup.js"})
		_, err = JestValidationCommand(root)
		require.ErrorContains(t, err, "--command")
	}
	writeScripts(t, root, map[string]string{"test": "jest"})
	require.NoError(t, os.WriteFile(workflow, []byte("jobs:\n  test:\n    steps:\n      - run: jest\n        working-directory: packages/foo\n"), 0644))
	_, err = JestValidationCommand(root)
	require.ErrorContains(t, err, "working directory")
}
