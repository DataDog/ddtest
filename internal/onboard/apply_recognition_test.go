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

func TestApplyAcceptsAnExistingActionWithoutSite(t *testing.T) {
	workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: " + setupActionV3 + "\n        with: {languages: js, api_key: '${{ secrets.DD_API_KEY }}', js-tracer-version: '6.20.1'}\n      - run: npx vitest run\n"
	root := newJestRepository(t, workflow)
	_, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err, "site is optional and defaults to datadoghq.com")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(data), "datadog/test-visibility-github-action@"), "no second action")
	require.Contains(t, string(data), "NODE_OPTIONS", "the missing preload is added to the test step")
}

func TestApplyNamesTheExistingActionInputThatDiffers(t *testing.T) {
	for with, want := range map[string]string{
		"{languages: js, api_key: '${{ secrets.DD_API_KEY }}'}":                              "js-tracer-version is missing; pin 6.20.1",
		"{languages: js, api_key: '${{ secrets.DD_API_KEY }}', js-tracer-version: '6.21.0'}": "js-tracer-version is 6.21.0, not 6.20.1",
		"{languages: js, js-tracer-version: '6.20.1'}":                                       "api_key is missing",
	} {
		workflow := "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: " + setupActionV3 + "\n        with: " + with + "\n      - run: npx vitest run\n"
		root := newJestRepository(t, workflow)
		_, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
		require.ErrorContains(t, err, want, with)
		require.NotContains(t, err.Error(), "use a recognized v3 release")
	}
}

func TestNpmOwnOptionsAreNotScriptArguments(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "jest", "build": "tsc"})
	writeScripts(t, filepath.Join(root, "packages/a"), map[string]string{"test": "jest"})
	for command, want := range map[string]string{
		"npm run build --if-present":          "",
		"npm run missing --if-present":        "",
		"npm test --silent":                   "jest",
		"npm test -w packages/a":              "jest",
		"npm run test --workspace=packages/a": "jest",
		"npm --workspace packages/a test":     "jest",
		"npm test --prefix packages/a":        "jest",
		"npm test --loglevel=warn -- --ci":    "jest --ci",
	} {
		remaining := 256
		resolution := resolveJestSequence(root, root, command, nil, &remaining, "jest")
		require.Empty(t, resolution.Reason, command)
		if want == "" {
			require.Empty(t, resolution.Tests, command)
			continue
		}
		require.Len(t, resolution.Tests, 1, command)
		require.Equal(t, want, resolution.Tests[0].Command, command)
	}
	for command, want := range map[string]string{
		"npm test -w packages/a -w packages/b": "multiple npm package selections",
		"npm test --workspaces":                "npm script arguments without --",
		"npm test packages/a":                  "npm script arguments without --",
	} {
		remaining := 256
		require.Contains(t, resolveJestSequence(root, root, command, nil, &remaining, "jest").Reason, want, command)
	}
	remaining := 256
	resolution := resolveJestSequence(root, root, "npm test -w packages/a", nil, &remaining, "jest")
	require.Equal(t, "packages/a", resolution.Tests[0].Directory)
}

func TestRetainedNpmSelectionAliasRunsFromCaller(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, filepath.Join(root, "packages/a"), map[string]string{"test": "nyc mocha"})
	for _, command := range []string{"npm test --workspace packages/a", "npm test --prefix packages/a"} {
		remaining := 256
		resolution := resolveJestSequence(root, root, command, nil, &remaining, "mocha")
		require.Empty(t, resolution.Reason, command)
		require.Len(t, resolution.Tests, 1, command)
		// npm resolves the selection from the caller; replaying the retained
		// alias from packages/a would look for packages/a/packages/a.
		require.Equal(t, command, resolution.Tests[0].Command)
		require.Equal(t, ".", resolution.Tests[0].Directory, command)
		require.Equal(t, "packages/a", resolution.Tests[0].mochaProbeDirectory, command)
	}
}

func TestApplyAcceptsNpmOptionsAndWorkingDirectories(t *testing.T) {
	for name, steps := range map[string]string{
		"if-present":        "      - run: npm run build --if-present\n      - run: npm test\n",
		"workspace":         "      - run: npm test -w packages/a\n",
		"workspace equals":  "      - run: npm run test --workspace=packages/a\n",
		"working-directory": "      - run: npm test\n        working-directory: packages/a\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := newJestRepository(t, "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '22'}\n"+steps)
			writeScripts(t, root, map[string]string{"test": "jest", "build": "tsc"})
			writeScripts(t, filepath.Join(root, "packages/a"), map[string]string{"test": "jest"})
			scope, err := DiscoverValidationScope(root, "jest")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			_, err = ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Contains(t, string(data), "format('-r {0}', env.DD_TRACE_PACKAGE)")
		})
	}
}

func TestApplyRefusesWindowsRunnersForEveryFramework(t *testing.T) {
	for name, runner := range map[string]string{
		"label":  "    runs-on: windows-latest\n",
		"matrix": "    strategy: {matrix: {os: [ubuntu-latest, windows-latest]}}\n    runs-on: ${{ matrix.os }}\n",
		"group":  "    runs-on: {group: windows-runners}\n",
		"labels": "    runs-on: {group: large-runners, labels: Windows}\n",
	} {
		t.Run(name, func(t *testing.T) {
			workflow := "jobs:\n  test:\n" + runner + "    defaults: {run: {shell: bash}}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '22'}\n      - run: npm test\n"
			root := newJestRepository(t, workflow)
			_, err := ApplyJavaScriptSetup(root, "jest", "5.132.0", "datadoghq.com", ">=18")
			require.ErrorContains(t, err, "Windows runners are not supported by automatic setup")
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			require.Equal(t, workflow, string(data))
		})
	}
}

func TestMissingPreloadIsReportedSeparatelyFromCustomInitialization(t *testing.T) {
	for env, code := range map[string]string{"": CodeMissingInitialization, "        env: {NODE_OPTIONS: '--max-old-space-size=4096'}\n": CodeUnverifiedInitialization} {
		workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24.14.1'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: js, js-tracer-version: '6.20.1'}\n      - run: npm test\n" + env
		result := checkCIRuntimes(t.Context(), newJestRepository(t, workflow), nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{Version: "6.20.1", Node: ">=18"}, nil
		})
		require.Len(t, result.Jobs, 1)
		require.Equal(t, code, result.Jobs[0].Code, env)
		if code == CodeMissingInitialization {
			require.Contains(t, result.Jobs[0].Reason, "sets no NODE_OPTIONS")
			require.NotContains(t, result.Jobs[0].Reason, "Preserve custom initialization")
		}
	}
}

func TestNpmWorkspaceSelectorsResolveDeclaredPackageNames(t *testing.T) {
	for name, workspaces := range map[string]string{"array": `["packages/*"]`, "object": `{"packages":["packages/*"]}`} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"private":true,"workspaces":`+workspaces+`}`), 0644))
		for directory, manifest := range map[string]string{
			"packages/a": `{"name":"@scope/app","scripts":{"test":"jest --selected"}}`,
			"packages/b": `{"name":"app","scripts":{"test":"jest --named"}}`,
			// npm selects the workspace named app, not this unrelated folder.
			"app": `{"scripts":{"test":"jest --unrelated"}}`,
		} {
			require.NoError(t, os.MkdirAll(filepath.Join(root, directory), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(root, directory, "package.json"), []byte(manifest), 0644))
		}
		for command, want := range map[string]string{
			"npm test -w @scope/app":          "jest --selected",
			"npm test --workspace=app":        "jest --named",
			"npm test --workspace packages/a": "jest --selected",
			"npm test --prefix app":           "jest --unrelated",
		} {
			remaining := 256
			resolution := resolveJestSequence(root, root, command, nil, &remaining, "jest")
			require.Empty(t, resolution.Reason, "%s: %s", name, command)
			require.Len(t, resolution.Tests, 1, "%s: %s", name, command)
			require.Equal(t, want, resolution.Tests[0].Command, "%s: %s", name, command)
		}
	}
}
