package onboard

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebdriverIONpmParentAndMatrixSelection(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "click"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"click":"cross-env EXAMPLE_RECIPE=click wdio run ./wdio.conf.js --spec ./click/example.js"}}`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yml"), []byte("jobs:\n  test:\n    strategy:\n      matrix: {recipe: [click]}\n    steps:\n      - run: npm ci\n      - run: npm run ${{ matrix.recipe }}\n        working-directory: ${{ matrix.recipe }}\n"), 0600))
	scope, err := DiscoverValidationScope(root, "webdriverio")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "npm run click", scope.Commands[0].Command)
	require.Equal(t, ".", scope.Commands[0].Directory)
	command, err := WebdriverIOValidationCommand(root, "npm run click")
	require.NoError(t, err)
	require.Equal(t, "npm run click", command)
	_, err = WebdriverIOValidationCommand(root, "node standalone.js")
	require.Error(t, err)
}

func TestWebdriverIORuntimeVerifiesScopedMixedCI(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "standalone.js"), []byte("import { remote } from 'webdriverio'\nawait remote({})\n"), 0600))
	data, err := json.Marshal(map[string]any{"scripts": map[string]string{"setup": "run-s setup:standalone setup:runner", "setup:standalone": "node standalone.js", "setup:runner": webdriverIOScopedOptions + " wdio run wdio.conf.js"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	workflow := `jobs:
  test:
    steps:
      - uses: actions/setup-node@v5
        with: {node-version: '20'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: 5.132.0}
      - run: npm run setup
        env:
          DD_WDIO_NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/ci.yml"), []byte(workflow), 0600))
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "webdriverio")
	require.Equal(t, "compatible", check.Status, check.Jobs)
	require.Len(t, check.Jobs, 1)
	scope, err := DiscoverValidationScope(root, "webdriverio")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	require.Equal(t, "npm run setup:runner", scope.Commands[0].Command)
}

func TestWebdriverIOMixedStandaloneAndScopedLeaf(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "setup"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "setup", "standalone.js"), []byte("import { remote } from 'webdriverio'\nawait remote({})\n"), 0600))
	writeScripts := func(scoped bool) {
		leaf := "wdio run wdio.conf.js --spec setup/runner.js"
		if scoped {
			leaf = webdriverIOScopedOptions + " " + leaf
		}
		data, err := json.Marshal(map[string]any{"scripts": map[string]string{"setup": "run-s setup:standalone setup:runner", "setup:standalone": "node setup/standalone.js", "setup:runner": leaf}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	}
	writeScripts(false)
	job := runtimeJob{}
	step := runtimeStep{Run: "npm run setup", Env: map[string]string{"NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	resolution := resolveTestStep(root, ciWorkflow{}, job, step, "javascript", "webdriverio")
	require.Empty(t, resolution.Reason)
	require.True(t, resolution.OtherFramework)
	require.Len(t, resolution.Tests, 1)
	require.Equal(t, "npm run setup:runner", resolution.Tests[0].Command)
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, job, step, nil), "also runs standalone")
	step.Env = map[string]string{"DD_WDIO_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, job, step, nil), "must start with")
	writeScripts(true)
	require.Empty(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, job, step, nil))
	step.Env["NODE_OPTIONS"] = "-r ${{ env.DD_TRACE_PACKAGE }}"
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, job, step, nil), "also runs standalone")
	delete(step.Env, "NODE_OPTIONS")
	resolution = resolveTestStep(root, ciWorkflow{}, job, step, "javascript", "webdriverio")
	require.Empty(t, resolution.Reason)
	require.Len(t, resolution.Tests, 1)
	require.Empty(t, resolution.Tests[0].UnvalidatedSetup)
	standalone := resolveTestStep(root, ciWorkflow{}, job, runtimeStep{Run: "npm run setup:standalone"}, "javascript", "webdriverio")
	require.True(t, standalone.Review)
	require.Contains(t, standalone.Reason, "Standalone WebDriver")
}

func TestWebdriverIOUnknownNodeWrapperStillBlocks(t *testing.T) {
	root := t.TempDir()
	for _, source := range []string{"console.log('helper')", "import { Launcher } from '@wdio/cli'\n", "import { remote } from 'webdriverio'\nimport 'mocha'\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "wrapper.js"), []byte(source), 0600))
		result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "node wrapper.js"}, "javascript", "webdriverio")
		require.NotEmpty(t, result.Reason)
		require.False(t, result.Review)
	}
	result := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "node " + strings.Repeat("../", 2) + "outside.js"}, "javascript", "webdriverio")
	require.False(t, result.Review)
}

func TestWebdriverIOScopedBootstrapChecksConditionalBranches(t *testing.T) {
	root := t.TempDir()
	step := runtimeStep{Run: "if npm -ps ls @wdio/cli | grep -q @wdio/cli; then npm test; else npm test; fi", Env: map[string]string{"DD_WDIO_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	writeScripts(t, root, map[string]string{"test": "wdio run wdio.conf.cjs"})
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil), "must start with")
	writeScripts(t, root, map[string]string{"test": webdriverIOScopedOptions + " wdio run wdio.conf.cjs"})
	require.Empty(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil))
	// A scoped preload no resolved leaf consumes cannot certify the step.
	step.Run = "node unknown-wrapper.js"
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil), "no resolved WDIO leaf consumes it")
}

func TestWebdriverIOBootstrapAcceptsNpmTestShorthand(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": webdriverIOScopedOptions + " wdio run wdio.conf.cjs"})
	reason := checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npm test", Env: map[string]string{"DD_WDIO_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}, nil)
	require.Empty(t, reason)
}

func TestWebdriverIOFocusedAliasRejectsUnresolvedSuffixAndStandalone(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "standalone.js"), []byte("import { remote } from 'webdriverio';\nawait remote({});"), 0600))
	for _, script := range []string{"wdio run wdio.conf.cjs && node unknown-wrapper.js", "node standalone.js && wdio run wdio.conf.cjs"} {
		data, err := json.Marshal(map[string]any{"scripts": map[string]string{"test": script}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
		_, err = WebdriverIOValidationCommand(root, "npm test")
		require.Error(t, err, script)
	}
}

func TestWebdriverIOScopedBootstrapUsesOwningPackage(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(pkg, 0700))
	write := func(path, script string) {
		data, err := json.Marshal(map[string]any{"scripts": map[string]string{"test": script}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, data, 0600))
	}
	write(filepath.Join(root, "package.json"), "wdio run root.cjs")
	write(filepath.Join(pkg, "package.json"), webdriverIOScopedOptions+" wdio run wdio.conf.cjs")
	step := runtimeStep{Run: "npm test", WorkingDirectory: "packages/app", Env: map[string]string{"DD_WDIO_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	require.Empty(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil))
	write(filepath.Join(root, "package.json"), webdriverIOScopedOptions+" wdio run root.cjs")
	write(filepath.Join(pkg, "package.json"), "wdio run wdio.conf.cjs")
	require.Contains(t, checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil), "must start with")
}

func TestWebdriverIOShortCommandsAndOtherFrameworkClassification(t *testing.T) {
	root := t.TempDir()
	for _, command := range []string{"wdio", "wdio wdio.conf.ts", "npx wdio wdio.conf.mjs", "wdio wdio.ci.conf.js", "wdio config/wdio.local.conf.ts", "wdio run config/wdio.local.conf.ts"} {
		remaining := &scriptBudget{left: 256}
		resolution := resolveJestSequence(root, root, command, nil, remaining, "webdriverio")
		require.Empty(t, resolution.Reason, command)
		require.Empty(t, resolution.ReviewReason, command)
		require.Len(t, resolution.Tests, 1, command)
	}
	for _, command := range []string{"wdio config", "wdio install service sauce", "npx wdio repl chrome"} {
		remaining := &scriptBudget{left: 256}
		resolution := resolveJestSequence(root, root, command, nil, remaining, "webdriverio")
		require.Contains(t, resolution.ReviewReason, "WebdriverIO setup/tool command is outside test execution", command)
		require.Empty(t, resolution.Tests, command)
	}
	resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: "npx wdio run wdio.conf.js"}, "javascript", "jest")
	require.True(t, resolution.OtherFramework)
	require.Empty(t, resolution.Reason)
	require.Empty(t, resolution.Tests)
}

func TestWebdriverIOBootstrapFollowsInitializedForwardingLeaves(t *testing.T) {
	root := newJestRepository(t, "")
	step := runtimeStep{Run: "npm test", Env: map[string]string{"DD_WDIO_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	for _, initialized := range []bool{false, true} {
		leaf := "wdio run wdio.conf.cjs"
		if initialized {
			leaf = webdriverIOScopedOptions + " " + leaf
		}
		writeScripts(t, root, map[string]string{"test": "npm run browser-tests", "browser-tests": "npm run wdio", "wdio": leaf})
		reason := checkWebdriverIOBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil)
		if initialized {
			require.Empty(t, reason)
		} else {
			require.Contains(t, reason, "must start with")
		}
	}
}
