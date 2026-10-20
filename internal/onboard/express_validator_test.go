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

const expressValidatorWorkflow = `jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix: {node: [14, 16, 18, 20, 22, 24]}
    steps:
      - uses: actions/setup-node@v3
        with: {node-version: '${{ matrix.node }}'}
      - uses: ./.github/actions/npm-cache
      - run: npm ci
      - run: npm run build
      - uses: datadog/test-visibility-github-action@v3
        if: matrix.node >= 22
        with: {languages: js, js-tracer-version: '6.17.0'}
      - run: npm test
        if: matrix.node >= 22
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
      - run: npm test
        if: matrix.node < 22
  docs:
    steps:
      - uses: ./.github/actions/npm-cache
      - run: npm run docs:regenerate-api
      - run: git diff --quiet --exit-code || echo "Regenerate docs with ` + "`npm run docs:regenerate-api`" + `"
      - run: npm run docs:build
`

func expressValidatorRepository(t *testing.T) string {
	t.Helper()
	root := newJestRepository(t, expressValidatorWorkflow)
	writeScripts(t, root, map[string]string{
		"test": "jest", "build": "tsc", "docs:build": "npm --prefix ./website run build",
		"docs:regenerate-api": "npm --prefix ./website run regenerate-api",
	})
	writeScripts(t, filepath.Join(root, "website"), map[string]string{
		"build": "docusaurus build", "regenerate-api": "node ./scripts/regenerate-api.js",
	})
	action := filepath.Join(root, ".github/actions/npm-cache")
	require.NoError(t, os.MkdirAll(action, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(action, "action.yml"), []byte(`runs:
  using: composite
  steps:
    - run: echo "dir=$(npm config get cache)" >> $GITHUB_OUTPUT
      shell: bash
    - uses: actions/cache@v3
`), 0644))
	return root
}

func TestNpmPrefixFollowsDirectoryAndLifecycle(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "website")
	writeScripts(t, root, map[string]string{"test": "eslint .", "build": "npm --prefix ./website run build"})
	writeScripts(t, child, map[string]string{"test": "jest", "build": "docusaurus build"})
	for _, command := range []string{"npm --prefix ./website test", "npm --prefix=./website run test", "CI=true npm --prefix website test"} {
		result := resolveJestCommand(root, command, nil)
		require.True(t, result.Matched, result)
		require.False(t, result.SingleJest, "subdirectory validation needs explicit working directory")
		require.Empty(t, result.Reason)
	}
	result := resolveJestCommand(root, "npm run build", nil)
	require.Empty(t, result.Reason, "same script name in a different package is not a cycle")
	require.Contains(t, result.ReviewReason, "docusaurus")
	writeScripts(t, root, map[string]string{"test": "jest", "build": "npm --prefix ./website run build"})
	require.True(t, resolveJestCommand(root, "npm --prefix . test", nil).SingleJest)
	writeScripts(t, child, map[string]string{"build": "npm --prefix .. run build"})
	require.Contains(t, resolveJestCommand(root, "npm run build", nil).Reason, "cycle")
	for _, hook := range []string{"pretest", "posttest"} {
		writeScripts(t, child, map[string]string{"test": "jest", hook: "node setup.js"})
		require.Contains(t, resolveJestCommand(root, "npm --prefix website test", nil).Reason, hook)
	}
	outside := t.TempDir()
	writeScripts(t, outside, map[string]string{"test": "jest"})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "outside")))
	for _, prefix := range []string{"../outside", "outside", outside, "missing", "$TARGET", "'~'", "''"} {
		result := resolveJestCommand(root, "npm --prefix "+prefix+" test", nil)
		require.False(t, result.Matched, result)
		require.NotEmpty(t, result.Reason, result)
	}
}

func TestMetadataNeverHidesExecutedTestsOrUnknownCommands(t *testing.T) {
	root := expressValidatorRepository(t)
	for _, command := range []string{
		`echo "dir=$(npm config get cache)" >> $GITHUB_OUTPUT`,
		`printf '%s\n' "dir=$(npm config get cache)" >> "$GITHUB_OUTPUT"`,
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.Empty(t, result.Reason, result)
		require.False(t, result.Matched)
	}
	for _, command := range []string{
		`echo "$(npm test)" >> $GITHUB_OUTPUT`,
		"git diff --quiet --exit-code || echo \"`npm test`\"",
		`echo "dir=$(npm config get cache)" >> $GITHUB_OUTPUT && npm test`,
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.True(t, result.Matched, result)
		require.False(t, result.Review)
		require.False(t, result.SingleJest)
	}
	for _, command := range []string{
		`echo "$(node tests.js)"`, `echo "$(npm config get cache; node tests.js)" >> $GITHUB_OUTPUT`,
		`echo "$(npm run missing)"`, `echo "$(npm run docs:build)" >> $GITHUB_ENV`,
		`echo "${VALUE:-$(npm test)}"`, `echo "$(npm --prefix "$TARGET" test)"`,
		`echo "$(echo $(npm test))"`, `$(npm config get cache)`, `echo okay > $(npm test)`,
		`git diff --quiet --exit-code || echo "$UNKNOWN"`, `git diff --quiet --exit-code || node tests.js`,
	} {
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.NotEmpty(t, result.Reason, command)
		require.False(t, result.Review, command)
	}
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{
		Run: "git diff --quiet --exit-code || echo \"`npm run docs:regenerate-api`\"",
		Env: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init"},
	}, "javascript", "jest")
	require.False(t, result.Review, "an instrumented opaque wrapper must remain unresolved")
	require.NotEmpty(t, result.Reason)
	writeScripts(t, filepath.Join(root, "website"), map[string]string{"regenerate-api": "jest"})
	result = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "git diff --quiet --exit-code || echo \"`npm run docs:regenerate-api`\""}, "javascript", "jest")
	require.True(t, result.Matched, "a docs alias that runs Jest must be identified")
	require.False(t, result.Review)
	writeScripts(t, filepath.Join(root, "website"), map[string]string{"regenerate-api": "node docs.js", "preregenerate-api": "jest"})
	result = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm run docs:regenerate-api"}, "javascript", "jest")
	require.Contains(t, result.Reason, "lifecycle")
	require.False(t, result.Review)
}
