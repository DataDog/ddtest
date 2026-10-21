package onboard

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestMochaProbeCoverageOverrideAndRunnerScope(t *testing.T) {
	for _, tool := range []string{"nyc", "c8"} {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/unit"), 0700))
		writeScripts(t, root, map[string]string{"test": "cd packages/unit && npm test"})
		writeScripts(t, filepath.Join(root, "packages/unit"), map[string]string{"test": "MODE=unit " + tool + " --all --check-coverage --functions=95 -- mocha test.cjs --retries 2"})
		plan := MochaProbePlan(root, "npm test")
		require.Equal(t, "packages/unit", plan.Directory)
		require.Equal(t, map[string]string{"MODE": "unit"}, plan.Environment)
		require.Equal(t, tool+" --all --functions=95 --check-coverage=false -- mocha test.cjs --retries 2", plan.Command)
		require.Equal(t, filepath.Join(root, "packages/unit"), MochaRunnerDirectory(root, "npm test"))
		require.Equal(t, []string{"test.cjs", "--retries", "2"}, MochaRunnerArguments(root, "npm test"))
		commands := ValidationCommands(root, "npm test", "mocha")
		require.Len(t, commands, 1)
		require.Equal(t, ".", commands[0].Directory)
		require.Empty(t, commands[0].Environment)
		require.Equal(t, "npm test", commands[0].Command)
	}
}

func TestMochaWatchRejectedDuringDiscovery(t *testing.T) {
	for _, command := range []string{"mocha --watch", "mocha -w", "mocha --watch=true", "npm test", "mocha --config watch.cjs"} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"test": "mocha"})
		if command == "npm test" {
			requireWrite := os.WriteFile(filepath.Join(root, ".mocharc.json"), []byte(`{"watch":true}`), 0600)
			require.NoError(t, requireWrite)
		}
		if command == "mocha --config watch.cjs" {
			require.NoError(t, os.WriteFile(filepath.Join(root, "watch.cjs"), []byte(`module.exports={watch:true}`), 0600))
		}
		_, err := MochaValidationCommand(root, command)
		require.ErrorContains(t, err, "watch mode is not supported")
	}
}

func TestMochaBabelIstanbulAvoidsDuplicateCoverageAdvice(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.21.0'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
	writeScripts(t, root, map[string]string{"test": "mocha"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "babel.config.cjs"), []byte(`module.exports={plugins:['istanbul']}`), 0600))
	require.True(t, MochaBabelIstanbulConfigured(root))
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Empty(t, scope.Commands[0].CICoverageCommand)
}

func TestMochaLifecyclePreviewListsHooksAndSideEffects(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"pretest": "node prepare.cjs", "test": "mocha one.cjs", "posttest": "codecov"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "prepare.cjs"), []byte(`console.log('prepare')`), 0600))
	entries := ValidationCommands(root, "npm test", "mocha")
	require.Len(t, entries, 1)
	var output bytes.Buffer
	WriteLifecycleHooks(&output, entries[0])
	require.Contains(t, output.String(), "pretest -> node prepare.cjs")
	require.Contains(t, output.String(), "posttest -> codecov")
	require.Contains(t, output.String(), "may upload coverage or publish")
	require.Equal(t, "npm test", entries[0].Command)
}
