// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiteralFileOperationsDoNotHideUnknownPrograms(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{
		"rimraf ./dist", "CI=true rimraf ./dist", "rm build/index.html.bak", "rsync -a docs/ build/docs",
		"sed -i.bak 's#</body>#<script>example</script></body>#g' build/index.html",
		`sed -i.bak 's/<\/body>/<script src="\..\/global\/luxon.js"><\/script><\/body>/g' build/api-docs/index.html`,
	} {
		result := resolveJestCommand(root, command, nil)
		require.Empty(t, result.Reason, command)
		require.False(t, result.Matched, command)
	}
	for _, command := range []string{
		"sed 'e jest' file", "sed 's/a/jest/e' file", "sed 's/a/b/;e jest' file", "sed -f script file",
		"rsync -e wrapper docs/ build", "rsync -a docs/ host:dir", "rimraf --glob './*'", "rm $(node pick.js)",
		"unknown-cleaner dist", "rimraf ../outside",
	} {
		result := resolveJestCommand(root, command+" && jest", nil)
		require.NotEmpty(t, result.Reason, command)
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries, "discovery must not execute file operations")
}

func TestLuxonAuxiliaryStepsDoNotBlockIdentifiedJest(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: 22}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '5.128.0'}
      - run: npm test
        env: {NODE_OPTIONS: "-r ${{ env.DD_TRACE_PACKAGE }}"}
      - run: npm run site
      - run: bash <(curl -s https://codecov.io/bash)
`)
	writeScripts(t, root, map[string]string{
		"test":      "jest --coverage",
		"api-docs":  `mkdir -p build && documentation build src/luxon.js -f html -o build/api-docs && sed -i.bak 's/<\/body>/<script src="\..\/global\/luxon.js"><\/script><script>console.log("You can try Luxon right here using the ` + "`" + `luxon` + "`" + ` global, like ` + "`" + `luxon.DateTime.now()` + "`" + `");<\/script><\/body>/g' build/api-docs/index.html && rm build/api-docs/index.html.bak`,
		"copy-site": "mkdir -p build && rsync -a docs/ build/docs && rsync -a site/ build",
		"site":      "npm run api-docs && npm run copy-site",
	})
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.128.0", Node: ">=18"}, nil
	})
	require.Equal(t, "compatible", result.Status, result)
	require.Len(t, result.Jobs, 1)
	require.Len(t, result.Review, 2)
	require.Contains(t, result.Review[1].Reason, "not inspected or validated")
	for _, command := range []string{"bash <(curl -s https://example.com/script)", "bash <(curl -s https://codecov.io/bash) && jest"} {
		resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command}, "javascript", "jest")
		require.False(t, resolution.Review)
		require.NotEmpty(t, resolution.Reason)
	}
}
