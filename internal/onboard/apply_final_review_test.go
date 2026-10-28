package onboard

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPreservesBlockEnvironmentHeaderAndQuotedInheritedValues(t *testing.T) {
	for _, env := range []string{"", "        env:\n          LANG: en_US.utf8\n", "        env:\n          NODE_OPTIONS: --conditions=step\n          LANG: en_US.utf8\n"} {
		workflow := "jobs:\n  test:\n    env:\n      NODE_OPTIONS: '--conditions=\"foo: bar\"'\n    steps:\n      - run: npx vitest run\n" + env
		root := newJestRepository(t, workflow)
		_, err := applyVitestSetup(root, "6.20.1")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		var parsed ciWorkflow
		require.NoError(t, yaml.Unmarshal(data, &parsed))
		step := parsed.Jobs["test"].Steps[1]
		require.Contains(t, step.Env["NODE_OPTIONS"], "DD_TRACE_PACKAGE")
		if env == "" {
			require.Contains(t, step.Env["NODE_OPTIONS"], `--conditions="foo: bar"`)
		} else {
			require.Equal(t, "en_US.utf8", step.Env["LANG"])
		}
		if strings.Contains(env, "--conditions=step") {
			require.Contains(t, step.Env["NODE_OPTIONS"], "--conditions=step")
			require.NotContains(t, step.Env["NODE_OPTIONS"], "foo: bar")
		}
		changes, err := applyVitestSetup(root, "6.20.1")
		require.NoError(t, err)
		require.Empty(t, changes)
	}
}

func scopedReviewRepository(t *testing.T, framework, workflow string) string {
	t.Helper()
	root := newJestRepository(t, workflow)
	binary, command := "wdio", "wdio run wdio.conf.cjs"
	if framework == "cucumber" {
		binary, command = "cucumber-js", "cucumber-js features"
	}
	pkg := map[string]any{"scripts": map[string]string{"test": "npm run " + binary, binary: command, "alternate": "npm run other", "other": command}, "devDependencies": map[string]string{"nyc": "17.1.0"}}
	data, err := json.MarshalIndent(pkg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.conf.cjs"), []byte("exports.config={\n runner: 'local',\n framework: 'mocha',\n}"), 0600))
	return root
}

func TestApplyUsesResolvedLeavesAndPreservesForwardingAliases(t *testing.T) {
	for _, framework := range []string{"cucumber", "webdriverio"} {
		root := scopedReviewRepository(t, framework, "jobs:\n  test:\n    steps:\n      - run: npm test\n      - run: npm run alternate\n")
		_, err := ApplyJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", "")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, "package.json"))
		require.NoError(t, err)
		var pkg struct{ Scripts map[string]string }
		require.NoError(t, json.Unmarshal(data, &pkg))
		binary := "wdio"
		if framework == "cucumber" {
			binary = "cucumber-js"
		}
		require.Equal(t, "npm run "+binary, pkg.Scripts["test"])
		require.Equal(t, "npm run other", pkg.Scripts["alternate"])
		require.Contains(t, pkg.Scripts[binary], "NODE_OPTIONS=")
		require.Contains(t, pkg.Scripts["other"], "NODE_OPTIONS=", "deduplicated commands still retain every leaf consumer")
		changes, err := ApplyJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", "")
		require.NoError(t, err)
		require.Empty(t, changes)
	}
}

func TestApplyPreservesInheritedScopedOptionsAndRejectsDynamicValues(t *testing.T) {
	for _, framework := range []string{"cucumber", "webdriverio"} {
		for _, value := range []string{"--conditions=customer", "$CUSTOM"} {
			key := scopedEnvironment(framework)
			workflow := "jobs:\n  test:\n    env:\n      NODE_OPTIONS: --max-old-space-size=4096\n      " + key + ": '" + value + "'\n    steps:\n      - run: npm test\n"
			root := scopedReviewRepository(t, framework, workflow)
			changes, err := ApplyJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", "")
			if value == "$CUSTOM" {
				require.ErrorContains(t, err, "Dynamic inherited "+key)
				require.Empty(t, changes)
				continue
			}
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			var parsed ciWorkflow
			require.NoError(t, yaml.Unmarshal(data, &parsed))
			options := parsed.Jobs["test"].Steps[1].Env[key]
			step := parsed.Jobs["test"].Steps[1]
			step.Env["NODE_OPTIONS"] = options
			resolved, resolveErr := effectiveBootstrapOptions(parsed, parsed.Jobs["test"], step, nil, true)
			require.NoError(t, resolveErr)
			require.Equal(t, 1, strings.Count(resolved, value))
			require.NotContains(t, options, "--max-old-space-size")
		}
	}
}

func TestApplyRejectsWindowsScopedNpmScriptsBeforeWriting(t *testing.T) {
	for _, runner := range []string{"windows-latest", "'${{ matrix.os }}'", "[self-hosted, Windows]"} {
		workflow := "jobs:\n  test:\n    runs-on: " + runner + "\n    strategy:\n      matrix: {os: [ubuntu-latest, windows-latest]}\n    steps:\n      - run: npm test\n        shell: bash\n"
		root := scopedReviewRepository(t, "webdriverio", workflow)
		before, err := os.ReadFile(filepath.Join(root, "package.json"))
		require.NoError(t, err)
		changes, err := ApplyJavaScriptSetup(root, "webdriverio", "6.20.1", "datadoghq.com", "")
		require.ErrorContains(t, err, "Windows npm scripts require portable scoped initialization")
		require.Empty(t, changes)
		after, err := os.ReadFile(filepath.Join(root, "package.json"))
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	}
}

func TestApplyLegacyOnlyPartitionReturnsGapInsteadOfPanicking(t *testing.T) {
	root := legacySetupRepository(t)
	_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	path := filepath.Join(root, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var parsed ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &document))
	steps := setupMember(setupMember(setupMember(document.Content[0], "jobs"), "test"), "steps")
	require.NotNil(t, steps)
	// Remove only the supported test copy, preserving the action and legacy step.
	for i, step := range steps.Content {
		run := setupMember(step, "run")
		condition := setupMember(step, "if")
		if run != nil && condition != nil && !strings.Contains(condition.Value, "!(") {
			steps.Content = append(steps.Content[:i], steps.Content[i+1:]...)
			break
		}
	}
	data, err = yaml.Marshal(&document)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	changes, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "No supported test step remains")
	require.Empty(t, changes)
}
