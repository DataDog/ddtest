package onboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestApplyInheritedAndStepEnvironmentPreservesOptions(t *testing.T) {
	for _, level := range []string{"job", "step"} {
		workflow := ordinaryVitestCI
		if level == "job" {
			workflow = strings.Replace(workflow, "    runs-on: ubuntu-latest", "    runs-on: ubuntu-latest\n    env:\n      LANG: en_US.utf8\n      NODE_OPTIONS: --max-old-space-size=2048", 1)
		} else {
			workflow = strings.Replace(workflow, "        run: npx", "        env:\n          LANG: en_US.utf8\n          NODE_OPTIONS: --max-old-space-size=2048\n        run: npx", 1)
		}
		root := newJestRepository(t, workflow)
		writeScripts(t, root, map[string]string{"build": "tsc"})
		_, err := applyVitestSetup(root, "6.21.0")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		var result ciWorkflow
		require.NoError(t, yaml.Unmarshal(data, &result))
		step := result.Jobs["test"].Steps[4]
		require.Empty(t, checkJestBootstrap(result, result.Jobs["test"], step, nil, true))
		require.Contains(t, string(data), "LANG: en_US.utf8")
		require.Contains(t, string(data), "--max-old-space-size=2048")
		changes, err := applyVitestSetup(root, "6.21.0")
		require.NoError(t, err)
		require.Empty(t, changes)
	}
}

func TestApplyPartitionsWithoutDroppingLegacyRows(t *testing.T) {
	root := legacySetupRepository(t)
	_, err := ApplyJavaScriptSetup(root, "mocha", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var result ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &result))
	rows, err := runtimeMatrix(result.Jobs["test"].Strategy.Matrix)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "mocha")
	compatible, excluded := 0, 0
	for _, finding := range check.Jobs {
		switch finding.Status {
		case "compatible":
			compatible++
		case "excluded":
			excluded++
		default:
			t.Errorf("unexpected CI finding: %+v", finding)
		}
	}
	require.Positive(t, compatible)
	require.Positive(t, excluded)
	require.Equal(t, "compatible", check.Status)
	scope, err := DiscoverValidationScope(root, "mocha")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	changes, err := ApplyJavaScriptSetup(root, "mocha", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.Empty(t, changes)
}

func TestCucumberRecipeScopesCoverageAndPreservesOtherFramework(t *testing.T) {
	root := mixedCucumberSetupRepository(t)
	before, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	_, err = ApplyJavaScriptSetup(root, "cucumber", "5.132.0", "datadoghq.com", "")
	var dependencies *SetupDependencyGap
	require.ErrorAs(t, err, &dependencies)
	require.Equal(t, []string{"nyc", "@istanbuljs/esm-loader-hook"}, dependencies.Packages)
	after, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	var pkg map[string]any
	require.NoError(t, json.Unmarshal(before, &pkg))
	deps := pkg["devDependencies"].(map[string]any)
	deps["nyc"] = "18.0.0"
	deps["@istanbuljs/esm-loader-hook"] = "0.3.0"
	data, err := json.MarshalIndent(pkg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	_, err = ApplyJavaScriptSetup(root, "cucumber", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "cucumber")
	require.Equal(t, "compatible", check.Status, check.Jobs)
	data, err = os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	var updated struct{ Scripts map[string]string }
	require.NoError(t, json.Unmarshal(data, &updated))
	require.Equal(t, pkg["scripts"].(map[string]any)["test:unit:base"], updated.Scripts["test:unit:base"])
	require.Equal(t, "cucumber-js --watch", updated.Scripts["cucumber:watch"])
	require.Contains(t, updated.Scripts["test:integration:base"], cucumberScopedOptions)
	require.Contains(t, updated.Scripts["test:integration:base"], "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false")
	require.Contains(t, updated.Scripts["test:integration:base"], "nyc -- cucumber-js")
	changes, err := ApplyJavaScriptSetup(root, "cucumber", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.Empty(t, changes)
}

func TestWDIORecipePreservesStandaloneAndBrowserScripts(t *testing.T) {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v5
        with: {node-version: '20.6.0'}
      - run: npm run click
`)
	writeScripts(t, root, map[string]string{"click": "cross-env EXAMPLE_RECIPE=click wdio run wdio.conf.js --spec click.js", "browser": "wdio run wdio.browser.js", "standalone": "node standalone.js"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.conf.js"), []byte("export const config = {\n runner: 'local',\n framework: 'mocha'\n}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.browser.js"), []byte("export const config = {\n runner: ['browser', {}]\n}\n"), 0600))
	_, err := ApplyJavaScriptSetup(root, "webdriverio", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	var updated struct{ Scripts map[string]string }
	require.NoError(t, json.Unmarshal(data, &updated))
	require.True(t, strings.HasPrefix(updated.Scripts["click"], webdriverIOScopedOptions))
	require.Equal(t, "wdio run wdio.browser.js", updated.Scripts["browser"])
	require.Equal(t, "node standalone.js", updated.Scripts["standalone"])
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.132.0", Node: ">=18"}, nil
	}, "webdriverio")
	require.Equal(t, "compatible", check.Status, check.Jobs)
}

func TestRuntimePartitionDoesNotDuplicateReferencedStepID(t *testing.T) {
	root := legacySetupRepository(t)
	path := filepath.Join(root, ".github/workflows/test.yml")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	before = []byte(strings.Replace(string(before), "    - name: Run tests\n", "    - name: Run tests\n      id: existing_tests\n", 1))
	require.NoError(t, os.WriteFile(path, before, 0600))
	changes, err := ApplyJavaScriptSetup(root, "mocha", "5.132.0", "datadoghq.com", ">=18")
	require.ErrorContains(t, err, "without duplicating or changing that id")
	require.Empty(t, changes)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func legacySetupRepository(t *testing.T) string {
	root := newJestRepository(t, `jobs:
  test:
    runs-on: ubuntu-latest
    strategy:
      matrix: {node: ['16', '20', '24']}
    steps:
    - uses: actions/setup-node@v4
      with: {node-version: '${{ matrix.node }}'}
    - name: Run tests
      run: npm test
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
	return root
}
func mixedCucumberSetupRepository(t *testing.T) string {
	root := newJestRepository(t, `jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '20.6.0'}
      - run: npm test
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","scripts":{"test":"run-s test:unit:base test:integration:base","test:unit:base":"vitest run","test:integration:base":"cucumber-js features","cucumber:watch":"cucumber-js --watch"},"devDependencies":{"@cucumber/cucumber":"12.2.0","vitest":"4.1.11"}}`), 0600))
	return root
}
