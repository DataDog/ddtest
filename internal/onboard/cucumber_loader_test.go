package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberSourceLoadersAreRecognizedInScripts(t *testing.T) {
	rest, loaders, ok := cucumberLoaderOverride(`cross-env NODE_OPTIONS="--import tsx" cucumber-js`)
	require.True(t, ok)
	require.Equal(t, "cucumber-js", rest)
	require.Equal(t, "--import tsx", loaders)

	rest, loaders, ok = cucumberLoaderOverride(`cross-env FORCE_COLOR=0 NODE_OPTIONS=--require=ts-node/register cucumber-js --profile ci`)
	require.True(t, ok)
	require.Equal(t, "cross-env FORCE_COLOR=0 cucumber-js --profile ci", rest)
	require.Equal(t, "--require ts-node/register", loaders)

	_, _, ok = cucumberLoaderOverride(`NODE_OPTIONS="--import tsx" cucumber-js`)
	require.True(t, ok, "plain shell assignments are the same override")
	for _, script := range []string{
		`cross-env NODE_OPTIONS="--require ./setup.js" cucumber-js`,      // repository code may replace instrumentation
		`cross-env NODE_OPTIONS="-r dd-trace/ci/init" cucumber-js`,       // a tracer preload is not a source loader
		`cross-env NODE_OPTIONS="--max-old-space-size=4096" cucumber-js`, // ordinary options keep their existing review
		`cucumber-js`,
		`NODE_OPTIONS="--import tsx" cucumber-js && echo done`,       // a compound script would be re-quoted into literal arguments
		`NODE_OPTIONS="--import tsx" cucumber-js --profile $PROFILE`, // an expansion would become literal
	} {
		_, _, ok := cucumberLoaderOverride(script)
		require.False(t, ok, script)
	}

	scoped := `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx --experimental-loader @istanbuljs/esm-loader-hook" cucumber-js`
	require.True(t, cucumberScopedScript(scoped))
	require.True(t, cucumberInlineIstanbulScript(scoped))
	require.Equal(t, "cucumber-js", cucumberDiscoveryScript(scoped))
	require.True(t, cucumberScopedScript(cucumberScopedOptions+" cucumber-js"))
	require.False(t, cucumberInlineIstanbulScript(cucumberScopedOptions+" cucumber-js"))
	require.False(t, cucumberScopedScript(`NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --require ./setup.js" cucumber-js`))
}

// Modeled on morphatic/astrologyjs: `cross-env NODE_OPTIONS="--import tsx" cucumber-js`.
func TestApplyCucumberKeepsAnInScriptSourceLoader(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm run test:bdd\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test:bdd":"cross-env NODE_OPTIONS=\"--import tsx\" cucumber-js"},"devDependencies":{"nyc":"17.1.0","tsx":"4.23.15"}}`), 0600))

	reason := CucumberLoaderOverride(root, "npm run test:bdd")
	require.Contains(t, reason, `NODE_OPTIONS="--import tsx", replacing inherited tracer preloads`)
	require.Contains(t, reason, `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx" cucumber-js`)

	_, err := ApplyJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	var pkg struct{ Scripts map[string]string }
	require.NoError(t, json.Unmarshal(data, &pkg))
	require.Equal(t, `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx" `+cucumberImpactedWorkaround+` npx --no-install nyc -- cucumber-js`, pkg.Scripts["test:bdd"])
	require.Empty(t, CucumberLoaderOverride(root, "npm run test:bdd"), "the rewritten leaf keeps inherited preloads")
	commands := ValidationCommands(root, "npm run test:bdd", "cucumber")
	require.Len(t, commands, 1)
	require.True(t, commands[0].cucumberScoped)
}

// Modeled on astrologyjs: Cucumber 13 needs Node 22+, so the step skips the
// Node 20 row of a matrix the rest of the job still runs.
func TestRuntimeRowWhoseTestStepIsSkippedIsExcluded(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy:\n      matrix: {node: ['20', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n        if: matrix.node != '20'\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"cucumber-js"},"devDependencies":{"@cucumber/cucumber":"13.2.1"}}`), 0600))
	check := CheckCIRuntimes(t.Context(), root, "cucumber")
	var excluded, unresolved int
	for _, job := range check.Jobs {
		if job.Status == StatusExcluded && job.Reason == "Test steps are skipped by their conditions on this matrix row." {
			excluded++
		}
		if job.Reason == "No active test step could be checked." {
			unresolved++
		}
	}
	require.Equal(t, 1, excluded, "%+v", check.Jobs)
	require.Zero(t, unresolved, "%+v", check.Jobs)
}

// The generated step-level preload replaces an inherited NODE_OPTIONS, so it
// must carry the inherited options in both branches, with and without a tracer.
func TestApplyKeepsInheritedNodeOptionsWithAndWithoutTheTracer(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    env: {NODE_OPTIONS: '--conditions=customer'}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n")
	_, err := ApplyJavaScriptSetup(root, "jest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Contains(t, string(data), `format('--conditions=customer -r {0}', env.DD_TRACE_PACKAGE) || '--conditions=customer'`)
}

// Probes call cucumber-js directly, so they need the loaders the scoped
// script keeps after the inherited options, in their original order.
func TestCucumberProbeLoadersKeepTheScriptsSourceLoaders(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{
		"test:bdd":"NODE_OPTIONS=\"${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx --experimental-loader @istanbuljs/esm-loader-hook\" nyc -- cucumber-js",
		"bdd":"npm run test:bdd",
		"plain":"NODE_OPTIONS=\"${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}\" cucumber-js"}}`), 0600))
	for _, command := range []string{"npm run test:bdd", "npm run bdd"} {
		require.Equal(t, "--import tsx --experimental-loader @istanbuljs/esm-loader-hook", CucumberProbeLoaders(root, command), command)
	}
	require.Empty(t, CucumberProbeLoaders(root, "npm run plain"))
	require.Empty(t, CucumberProbeLoaders(root, "cucumber-js"))

	require.True(t, CucumberSourceLoaderActive("-r dd-trace/ci/init --import tsx"))
	require.True(t, CucumberSourceLoaderActive("--require=ts-node/register"))
	require.False(t, CucumberSourceLoaderActive("-r dd-trace/ci/init --experimental-loader @istanbuljs/esm-loader-hook"))
	require.False(t, CucumberSourceLoaderActive(""))
}
