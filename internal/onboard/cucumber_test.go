// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func octokitCucumberFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for destination, source := range map[string]string{"package.json": "octokit-auth-netrc-package.json", ".github/workflows/ci.yml": "octokit-auth-netrc-ci.yml"} {
		data, err := os.ReadFile(filepath.Join("testdata", source))
		require.NoError(t, err)
		file := filepath.Join(root, destination)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, data, 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nvmrc"), []byte("24"), 0600))
	return root
}

func TestCucumberDiscoversMixedNpmScriptsProfilesAndBuilds(t *testing.T) {
	root := octokitCucumberFixture(t)
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	command := scope.Commands[0]
	require.Equal(t, "npm run test:integration:base -- --profile noWip", command.Command)
	require.Len(t, command.Locations, 4)
	require.NotEmpty(t, command.Prerequisites)
	require.Empty(t, command.UnvalidatedSetup)
	for _, build := range command.Prerequisites {
		require.Equal(t, "npm run build", build.Command)
	}
	probe, err := CucumberProbeCommand(root, command.Command)
	require.NoError(t, err)
	require.Equal(t, "cucumber-js test/integration --profile noWip", probe)
	_, err = CucumberValidationCommand(root, "")
	require.ErrorContains(t, err, "--all")
	_, err = CucumberValidationCommand(root, "npm run test:integration:base -- --profile noWip")
	require.NoError(t, err)
	_, err = CucumberValidationCommand(root, "npm run missing")
	require.Error(t, err)
	remaining := 256
	resolution := resolveJestSequence(root, root, "npm test", nil, &remaining, "cucumber")
	require.True(t, resolution.OtherFramework, "Vitest must remain visible when selecting Cucumber-only instrumentation")
}

func TestCucumberCIRuntimeAndScopeAreChecked(t *testing.T) {
	for _, tc := range []struct {
		name, version, requirement, options string
		scoped                              bool
		want                                string
	}{
		{"Node20 rejected", "6.21.0", ">=22", "", false, "incompatible"},
		{"mixed global preload rejected", "5.132.0", ">=18", "", false, "inconclusive"},
		{"scoped leaf accepted", "5.132.0", ">=18", "", true, "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := octokitCucumberFixture(t)
			workflow := filepath.Join(root, ".github/workflows/ci.yml")
			data, err := os.ReadFile(workflow)
			require.NoError(t, err)
			preload := "NODE_OPTIONS"
			if tc.scoped {
				preload = "DD_CUCUMBER_NODE_OPTIONS"
			}
			action := "      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '" + tc.version + "'}\n      - run: npm test\n        env: {" + preload + ": '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }} --experimental-loader @istanbuljs/esm-loader-hook'}"
			require.NoError(t, os.WriteFile(workflow, []byte(strings.ReplaceAll(string(data), "      - run: npm test", action)), 0600))
			pkg := filepath.Join(root, "package.json")
			data, err = os.ReadFile(pkg)
			require.NoError(t, err)
			old := `DEBUG=\"any,$DEBUG\" cucumber-js`
			replacement := `DEBUG=\"any,$DEBUG\" `
			if tc.scoped {
				replacement += `NODE_OPTIONS=\"${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}\" `
			}
			replacement += "nyc cucumber-js"
			require.Contains(t, string(data), old)
			require.NoError(t, os.WriteFile(pkg, []byte(strings.ReplaceAll(string(data), old, replacement)), 0600))
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: tc.version, Node: tc.requirement}, nil
			}, "cucumber")
			require.Equal(t, tc.want, check.Status, "%+v", check.Jobs)
			if tc.scoped {
				require.Len(t, check.Jobs, 4)
				for _, job := range check.Jobs {
					require.NotEmpty(t, job.Node)
				}
			}
		})
	}
}
