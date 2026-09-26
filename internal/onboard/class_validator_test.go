// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

const nodeDistributionFixture = `[
 {"version":"v26.2.0","files":["linux-x64","win-x64-exe"]},
 {"version":"v26.4.0","files":["linux-x64"]},
 {"version":"v24.8.0","files":["linux-arm64","osx-arm64-tar"]},
 {"version":"v28.0.0-rc.1","files":["linux-x64"]}
]`

func TestClassValidatorCIRuntimesAndCommand(t *testing.T) {
	root := newJestRepository(t, classValidatorWorkflow)
	writeScripts(t, root, map[string]string{"test": "jest", "test:ci": "jest --runInBand --no-cache --coverage --verbose"})
	calls := map[string]int{}
	client := &http.Client{Transport: runtimeTransport(func(r *http.Request) (*http.Response, error) {
		calls[r.URL.String()]++
		data := nodeDistributionFixture
		if r.URL.String() == nodeVersionsManifest {
			data = ltsManifestFixture
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data))}, nil
	})}
	result := checkCIRuntimes(t.Context(), root, setupNodeResolver(client), func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
	})
	require.Equal(t, "compatible", result.Status, result)
	require.Len(t, result.Jobs, 2)
	require.Len(t, result.Review, 2)
	require.Equal(t, map[string]int{nodeVersionsManifest: 1, nodeDistributionIndex: 1}, calls)
	require.Equal(t, "24", result.Jobs[0].NodeResolution.Version)
	require.Equal(t, "26.4.0", result.Jobs[1].NodeResolution.Version)
	require.Equal(t, "linux-x64", result.Jobs[1].NodeResolution.Platform)
	require.Equal(t, nodeDistributionIndex, result.Jobs[1].NodeResolution.Source)
	require.False(t, result.Jobs[1].NodeResolution.ResolvedAt.IsZero())
	command, err := JestValidationCommand(root)
	require.NoError(t, err)
	require.Equal(t, "npm run test:ci", command)
	discovery, err := findWorkflows(root, "javascript", "jest")
	require.NoError(t, err)
	require.Empty(t, discovery.Unresolved)
}

func TestLatestNodeAliasesAndUnavailableMetadata(t *testing.T) {
	for _, platform := range []string{"linux-x64", "win-x64-exe", "osx-arm64-tar"} {
		calls := 0
		client := &http.Client{Transport: runtimeTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			require.Equal(t, nodeDistributionIndex, r.URL.String())
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(nodeDistributionFixture))}, nil
		})}
		resolve := setupNodeResolver(client)
		for _, alias := range []string{"current", "latest", "node"} {
			result, err := resolve(t.Context(), alias, platform)
			require.NoError(t, err)
			require.Equal(t, map[string]string{"linux-x64": "26.4.0", "win-x64-exe": "26.2.0", "osx-arm64-tar": "24.8.0"}[platform], result.Version)
		}
		require.Equal(t, 1, calls)
	}
	for _, data := range []string{"invalid", "[]", `[{"version":"28.x","files":["linux-x64"]}]`} {
		_, err := resolveLatestNode([]byte(data), "linux-x64")
		require.Error(t, err)
	}
	calls := 0
	resolve := setupNodeResolver(&http.Client{Transport: runtimeTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("offline") })})
	for range 2 {
		_, err := resolve(t.Context(), "current", "linux-x64")
		require.ErrorContains(t, err, "offline")
	}
	require.Equal(t, 1, calls)
	_, err := resolve(t.Context(), "current", "")
	require.ErrorContains(t, err, "platform/architecture")
}

func TestNodeDistributionPlatform(t *testing.T) {
	for _, tc := range []struct {
		runner         any
		arch, expected string
	}{
		{"ubuntu-latest", "", "linux-x64"}, {"ubuntu-24.04-arm", "", "linux-arm64"},
		{"${{ matrix.os }}", "${{ matrix.arch }}", "win-arm64-exe"},
		{"macos-latest", "arm64", "osx-arm64-tar"}, {"macos-latest", "", ""},
		{[]string{"self-hosted", "linux"}, "", ""}, {"self-hosted", "x64", ""},
		{"ubuntu-latest", "${{ inputs.arch }}", ""},
	} {
		require.Equal(t, tc.expected, nodeDistributionPlatform(tc.runner, tc.arch, map[string]any{"os": "windows-2025", "arch": "arm64"}))
	}
}

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
	for _, script := range []string{"jest && echo done", "jest && jest --config other.js", "node setup.js && jest"} {
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

func TestUnavailableCurrentMetadataStaysInconclusive(t *testing.T) {
	resolve := setupNodeResolver(&http.Client{Transport: runtimeTransport(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("metadata unavailable")
	})})
	root := newJestRepository(t, strings.ReplaceAll(classValidatorWorkflow, "'lts/*', ", ""))
	writeScripts(t, root, map[string]string{"test:ci": "jest"})
	result := checkCIRuntimes(t.Context(), root, resolve, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.17.0", Node: ">=22"}, nil
	})
	require.Equal(t, "inconclusive", result.Status)
	require.Len(t, result.Jobs, 1)
	require.Nil(t, result.Jobs[0].NodeResolution)
	require.Contains(t, result.Jobs[0].Reason, "metadata unavailable")
}
