package onboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlaywrightCIResolvesAliasesAndIgnoresBrowserInstallation(t *testing.T) {
	root := newJestRepository(t, `jobs:
  browser:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '20'}
      - run: pnpm install
      - run: npx playwright install --with-deps
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '5.131.1'}
      - run: pnpm test:e2e
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	writeScripts(t, root, map[string]string{"test:e2e": "pnpm playwright test --project=chromium"})
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "playwright test --project=chromium", scope.Commands[0].Command)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.131.1", Node: ">=18"}, nil
	}, "playwright")
	require.Equal(t, "compatible", check.Status)
	require.Len(t, check.Jobs, 1)
	command, err := PlaywrightValidationCommand(root, "pnpm test:e2e")
	require.NoError(t, err)
	require.Equal(t, "pnpm test:e2e", command)
	// An identically named script takes precedence over the installed binary.
	writeScripts(t, root, map[string]string{"playwright": "node custom-wrapper.js"})
	_, err = PlaywrightValidationCommand(root, "pnpm playwright test")
	require.Error(t, err)
}

func TestPlaywrightScopeReviewsOnlyProvenUnrelatedTurboTasks(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: pnpm test\n")
	writeScripts(t, root, map[string]string{"test": "turbo test"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm-workspace.yaml"), []byte("packages: ['packages/*']\n"), 0600))
	dir := filepath.Join(root, "packages", "unit")
	require.NoError(t, os.MkdirAll(dir, 0700))
	writeScripts(t, dir, map[string]string{"test": "vitest run"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "turbo.json"), []byte(`{"pipeline":{"test":{}}}`), 0600))
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	require.Empty(t, scope.Commands)
	require.Empty(t, scope.Unresolved)
	require.NotEmpty(t, scope.Review)
	for _, command := range []string{"playwright test", "node opaque.js"} {
		writeScripts(t, dir, map[string]string{"test": command})
		scope, err = DiscoverValidationScope(root, "playwright")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
	}
	writeScripts(t, dir, map[string]string{"test": "vitest run"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "turbo.json"), []byte(`{"pipeline":{"test":{"dependsOn":["^build"]}}}`), 0600))
	scope, err = DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
}

func TestPlaywrightTurboPackageOverrideRemainsUnresolved(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  tests:\n    steps:\n      - run: turbo test\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "turbo.json"), []byte(`{"tasks":{"test":{},"unit#test":{"dependsOn":["browser#test"]}}}`), 0600))
	scope, err := DiscoverValidationScope(root, "playwright")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
}
