// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/environment"
	"github.com/stretchr/testify/require"
)

func TestSecretOnSetupStepDoesNotBecomeATestCommand(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '22'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.17.0'}
      - run: npm ci
        env: {NPM_TOKEN: '${{ secrets.NPM }}'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.17.0", Node: ">=18"}, nil
	})
	for _, finding := range result.Jobs {
		require.NotEqual(t, "npm ci", finding.Command, finding.Reason)
	}
	require.Equal(t, StatusCompatible, result.Status, result)
}

func TestNpxOptionsDoNotChangeTheResolvedConfiguration(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"npx jest u.test.js", "npx --no-install jest u.test.js", "npx --yes jest u.test.js"} {
		tests := ValidationCommands(root, command, "jest")
		require.Len(t, tests, 1, command)
		require.Equal(t, "jest u.test.js", tests[0].Command, command)
	}
}

func TestExactCITracerUsesInstalledPackageWhenTheRegistryFails(t *testing.T) {
	root := t.TempDir()
	writeTracerManifest := func(version, node string) {
		directory := filepath.Join(root, "node_modules", "dd-trace")
		require.NoError(t, os.MkdirAll(directory, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(directory, "package.json"), []byte(`{"version":"`+version+`","engines":{"node":"`+node+`"}}`), 0644))
	}
	writeTracerManifest("5.132.0", ">=18")
	requirement, ok := installedTracerRequirement(root, "5.132.0")
	require.True(t, ok)
	require.Equal(t, tracerRequirement{Version: "5.132.0", Node: ">=18", Requested: "5.132.0"}, requirement)
	for _, version := range []string{"5", "latest", "5.131.0"} {
		_, ok := installedTracerRequirement(root, version)
		require.False(t, ok, "%s needs registry metadata", version)
	}
}

func TestCIRequirementLookupsAreRememberedIncludingFailures(t *testing.T) {
	key := t.Name()
	calls := 0
	failing := func() (tracerRequirement, error) {
		calls++
		return tracerRequirement{}, errors.New("ENOTFOUND registry")
	}
	for range 3 {
		_, err := cachedCIRequirement(key, failing)
		require.ErrorContains(t, err, "ENOTFOUND")
	}
	require.Equal(t, 1, calls, "a registry outage costs one bounded lookup per selector")
}

func TestWorkflowExpressionIsReportedInsteadOfAShellParseError(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: jest --config ${{ github.workspace }}/jest.config.js\n")
	scope, err := DiscoverValidationScope(root, "jest")
	require.NoError(t, err)
	require.Len(t, scope.Unresolved, 1)
	require.Contains(t, scope.Unresolved[0], "unresolved CI workflow expression requires review: ${{ github.workspace }}")
	require.NotContains(t, scope.Unresolved[0], "cannot parse shell sequence")
	require.Equal(t, 1, strings.Count(scope.Unresolved[0], "unresolved CI workflow expression"))
}

func TestConfiguredOnboardingSuggestsAllForSeveralConfigurations(t *testing.T) {
	for workflow, want := range map[string]string{
		"jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js}\n      - run: npx jest --config a.js\n      - run: npx jest --config b.js\n": "`ddtest testdrive --framework jest --all`",
		"jobs:\n  tests:\n    steps:\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js}\n      - run: npx jest\n":                                                    "`ddtest testdrive --framework jest`",
	} {
		root := newJestRepository(t, workflow)
		t.Chdir(root)
		var output strings.Builder
		require.NoError(t, onboardGitHub(&output, &environment.GitHub{}, root, "javascript", "jest"))
		require.Contains(t, output.String(), "Run "+want+" to check the setup locally.")
	}
}
