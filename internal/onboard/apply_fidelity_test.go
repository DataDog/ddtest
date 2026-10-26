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
)

// applySetupSettles applies the setup, checks that the second and third runs
// change nothing, and returns the written workflow.
func applySetupSettles(t *testing.T, root, framework, version string) string {
	t.Helper()
	path := filepath.Join(root, ".github/workflows/test.yml")
	changes, err := ApplyJavaScriptSetup(root, framework, version, "datadoghq.com", "")
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	for range 2 {
		changes, err = ApplyJavaScriptSetup(root, framework, version, "datadoghq.com", "")
		require.NoError(t, err)
		require.Empty(t, changes)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(first), string(data))
	}
	for _, line := range strings.Split(string(first), "\n") {
		require.False(t, line != "" && strings.TrimSpace(line) == "", "whitespace-only line in\n%s", first)
	}
	return string(first)
}

func TestApplyKeepsStepCommentsOnceAndSettles(t *testing.T) {
	for name, workflow := range map[string]string{
		"foot comment":         "jobs:\n  test:\n    steps:\n      - name: Tests\n        run: npx vitest run\n        # trailing comment\n      - run: echo done\n",
		"separated head":       "jobs:\n  test:\n    steps:\n      - run: npm ci\n      # trailing comment\n\n      - run: npx vitest run\n",
		"existing action foot": "jobs:\n  test:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: js\n          api_key: ${{ secrets.DD_API_KEY }}\n          site: datadoghq.com\n          js-tracer-version: 6.21.0\n        # trailing comment\n      - run: npx vitest run\n        env:\n          NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n",
	} {
		t.Run(name, func(t *testing.T) {
			data := applySetupSettles(t, newJestRepository(t, workflow), "vitest", "6.21.0")
			require.Equal(t, 1, strings.Count(data, "# trailing comment"), data)
		})
	}
}

func TestApplyFoldedScalarAddsNoWhitespaceOnlyLine(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: >\n          npm\n          test\n      - run: echo preserved\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
	applySetupSettles(t, root, "mocha", "6.20.1")
}

func TestApplyCRLFKeepsTheCommentAboveTheTestStep(t *testing.T) {
	workflow := "jobs:\r\n  test:\r\n    steps:\r\n      - run: npm ci\r\n      # describes tests\r\n      - run: npx vitest run\r\n"
	data := applySetupSettles(t, newJestRepository(t, workflow), "vitest", "6.21.0")
	require.Equal(t, 1, strings.Count(data, "# describes tests"), data)
	require.Contains(t, data, "js-tracer-version: 6.21.0\r\n      # describes tests\r\n      - run: npx vitest run\r\n", "the comment keeps labelling the test step, not the action")
	require.NotContains(t, strings.ReplaceAll(data, "\r\n", ""), "\n", "every line keeps its CRLF ending")
}

func TestApplyKeepsFourSpaceNestedMappings(t *testing.T) {
	workflow := "jobs:\n    test:\n        steps:\n            - name: Tests\n              run: npx vitest run\n              env:\n                  FOO: bar\n"
	data := applySetupSettles(t, newJestRepository(t, workflow), "vitest", "6.21.0")
	require.Contains(t, data, "\n              env:\n                  FOO: bar\n                  NODE_OPTIONS: ", data)
	require.Contains(t, data, "\n              with:\n                  languages: js\n", "the new action follows the file's nested indentation")
}

func TestApplyKeepsFourSpaceActionInputsWhenGuardingIt(t *testing.T) {
	workflow := "jobs:\n    test:\n        steps:\n            - uses: datadog/test-visibility-github-action@v3\n              with:\n                  languages: js\n                  api_key: ${{ secrets.DD_API_KEY }}\n                  site: datadoghq.com\n                  js-tracer-version: 6.21.0\n            - run: npx vitest run\n              env:\n                  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n"
	data := applySetupSettles(t, newJestRepository(t, workflow), "vitest", "6.21.0")
	require.Contains(t, data, "\n              with:\n                  languages: js\n                  api_key: ${{ secrets.DD_API_KEY }}\n", data)
	require.Contains(t, data, "\n              continue-on-error: true\n", data)
}

func TestSetupValidationRejectsDuplicatedOrDroppedComments(t *testing.T) {
	before := []byte("steps:\n  # kept\n  - run: a\n")
	require.NoError(t, validateSetupComments(before, []byte("steps:\n  - name: x # trailing\n  # kept\n  - run: a\n")))
	require.ErrorContains(t, validateSetupComments(before, []byte("steps:\n  # kept\n  - run: a\n    # kept\n")), "appears 2 times")
	require.ErrorContains(t, validateSetupComments(before, []byte("steps:\n  - run: a\n")), "appears 0 times")
	require.ErrorContains(t, validateSetupComments(before, []byte("steps:\n  # kept\n  - run: a\n    \n")), "whitespace-only")
}
