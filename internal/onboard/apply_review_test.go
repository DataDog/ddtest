package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestApplyMochaYAMLShapesPreserveCommandSteps(t *testing.T) {
	for _, command := range []string{"npm test", "CI=true npm test", "|\n          npx nyc -- mocha", "|\n          CI=true npm test\n          echo done"} {
		t.Run(command, func(t *testing.T) {
			root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: "+command+"\n")
			require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
			_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			var result ciWorkflow
			require.NoError(t, yaml.Unmarshal(data, &result))
			steps := result.Jobs["test"].Steps
			require.Len(t, steps, 3)
			require.NotEmpty(t, steps[1].Uses)
			require.Empty(t, steps[1].Run, "action cannot also be a run step")
			require.Empty(t, steps[2].Uses)
			require.Contains(t, steps[2].Run, "nyc --")
			if strings.Contains(command, "CI=true") {
				require.Contains(t, steps[2].Run, "CI=true npx --no-install nyc -- npm test")
			}
			require.NotContains(t, steps[2].Run, "env:", "environment must not become shell source")
			require.Contains(t, steps[2].Env["NODE_OPTIONS"], "DD_TRACE_PACKAGE")
			changes, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
			require.NoError(t, err)
			require.Empty(t, changes)
		})
	}
}

func TestApplyMochaESMInitializesImportLoader(t *testing.T) {
	for _, command := range []string{"npx nyc -- mocha test.mjs", "npm test"} {
		root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: "+command+"\n")
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","scripts":{"test":"nyc -- mocha test.mjs"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
		_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		var generated ciWorkflow
		require.NoError(t, yaml.Unmarshal(data, &generated))
		require.Empty(t, checkJestBootstrap(generated, generated.Jobs["test"], generated.Jobs["test"].Steps[2], nil, true))
	}
}

func TestApplyScopedPartitionPreservesOtherFramework(t *testing.T) {
	root := mixedCucumberSetupRepository(t)
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = []byte(strings.Replace(string(data), `"vitest":"4.1.11"`, `"vitest":"4.1.11","nyc":"17.1.0","@istanbuljs/esm-loader-hook":"0.3.0"`, 1))
	require.NoError(t, os.WriteFile(path, data, 0600))
	path = filepath.Join(root, ".github/workflows/test.yml")
	require.NoError(t, os.WriteFile(path, []byte("jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '${{ matrix.node }}'}\n      - run: npm test\n"), 0600))
	_, err = ApplyJavaScriptSetup(root, "cucumber", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	var workflow ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	steps := workflow.Jobs["test"].Steps
	require.Len(t, steps, 4)
	require.Contains(t, steps[2].Env["DD_CUCUMBER_NODE_OPTIONS"], "DD_TRACE_PACKAGE")
	require.Empty(t, steps[2].Env["NODE_OPTIONS"])
	check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
	}, "cucumber")
	require.Equal(t, "compatible", check.Status, check.Jobs)
}

func TestApplyPartitionsRuntimeAtTestsRatherThanLaterSetup(t *testing.T) {
	root := legacySetupRepository(t)
	path := filepath.Join(root, ".github/workflows/test.yml")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = append(data, []byte("    - uses: actions/setup-node@v4\n      with: {node-version: '24'}\n")...)
	require.NoError(t, os.WriteFile(path, data, 0600))
	_, err = ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "matrix.node == '24'")
	var workflow ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.Empty(t, workflow.Jobs["test"].Steps[3].Env)
}

func TestApplyDirectScopedCommandRequiresReviewBeforeEdits(t *testing.T) {
	for _, framework := range []string{"cucumber", "webdriverio"} {
		command := "npx nyc -- cucumber-js features"
		if framework == "webdriverio" {
			command = "npx wdio run wdio.conf.cjs"
		}
		workflow := "jobs:\n  test:\n    steps:\n      - run: " + command + "\n"
		root := newJestRepository(t, workflow)
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"nyc":"17.1.0"}}`), 0600))
		changes, err := ApplyJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", "")
		require.ErrorContains(t, err, "package-script consumer")
		require.Empty(t, changes)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		require.Equal(t, workflow, string(data))
		require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	}
}

func TestScopedSetupDoesNotDuplicateInheritedNodeOptions(t *testing.T) {
	for _, framework := range []string{"cucumber", "webdriverio"} {
		root := newJestRepository(t, "jobs:\n  test:\n    env: {NODE_OPTIONS: '--conditions=customer'}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n")
		script := "cucumber-js features"
		if framework == "webdriverio" {
			script = "wdio run wdio.conf.cjs"
			require.NoError(t, os.WriteFile(filepath.Join(root, "wdio.conf.cjs"), []byte("exports.config={\n runner: 'local',\n framework: 'mocha'\n}"), 0600))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"`+script+`"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
		_, err := ApplyJavaScriptSetup(root, framework, "6.20.1", "datadoghq.com", ">=22")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		var workflow ciWorkflow
		require.NoError(t, yaml.Unmarshal(data, &workflow))
		require.Equal(t, "--conditions=customer", workflow.Jobs["test"].Env["NODE_OPTIONS"])
		scoped := workflow.Jobs["test"].Steps[2].Env[scopedEnvironment(framework)]
		require.Contains(t, scoped, "DD_TRACE_PACKAGE")
		require.NotContains(t, scoped, "--conditions=customer", "the package leaf already consumes inherited NODE_OPTIONS once")
	}
}

func TestApplyMochaPhysicalScalarBoundaries(t *testing.T) {
	for _, command := range []string{">\n          npm\n          test", "\"npm\n          test\"", "|\n          npm test"} {
		for _, version := range []string{"'24'", "'${{ matrix.node }}'"} {
			t.Run(command+version, func(t *testing.T) {
				root := newJestRepository(t, "jobs:\n  test:\n    strategy:\n      matrix: {node: ['16', '24']}\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: "+version+"}\n      - run: "+command+"\n      - run: echo preserved\n")
				require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
				_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
				require.NoError(t, err)
				path := filepath.Join(root, ".github/workflows/test.yml")
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				var workflow ciWorkflow
				require.NoError(t, yaml.Unmarshal(data, &workflow))
				steps := workflow.Jobs["test"].Steps
				require.Equal(t, "echo preserved", steps[len(steps)-1].Run)
				require.Contains(t, steps[2].Run, "npx --no-install nyc -- npm test")
				require.Contains(t, steps[2].Env["NODE_OPTIONS"], "DD_TRACE_PACKAGE")
				if strings.Contains(version, "matrix") {
					require.Equal(t, "npm test", strings.TrimSpace(steps[3].Run))
					require.Contains(t, steps[3].If, "!(matrix.node == '24')")
					require.Empty(t, steps[3].Env)
				}
				changes, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
				require.NoError(t, err)
				require.Empty(t, changes)
			})
		}
	}
}

func TestApplyRejectsStepTracerOverridesBeforeWriting(t *testing.T) {
	for _, key := range []string{"DD_TRACE_PACKAGE", "DD_TRACE_ESM_IMPORT", "DD_CIVISIBILITY_ENABLED", "DD_TRACE_ENABLED"} {
		root := newJestRepository(t, strings.Replace(ordinaryVitestCI, "        run: npx vitest run --coverage # retain flags and comment", "        env:\n          "+key+": custom\n        run: npx vitest run --coverage", 1))
		path := filepath.Join(root, ".github/workflows/test.yml")
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		changes, err := applyVitestSetup(root, "6.20.1")
		require.ErrorContains(t, err, key)
		require.Empty(t, changes)
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.NoFileExists(t, filepath.Join(root, ".gitignore"))
	}
}

func TestApplyResolvesMochaCoverageInTheStepWorkingDirectory(t *testing.T) {
	for name, layout := range map[string]struct{ defaults, jobDefaults, step string }{
		"workflow default": {defaults: "defaults:\n  run: {working-directory: sub}\n", step: "      - run: npm test\n"},
		"job default":      {jobDefaults: "    defaults:\n      run: {working-directory: sub}\n", step: "      - run: npm test\n"},
		"step":             {step: "      - run: npm test\n        working-directory: sub\n"},
		"cd":               {step: "      - run: cd sub && npm test\n"},
	} {
		t.Run(name, func(t *testing.T) {
			workflow := layout.defaults + "jobs:\n  test:\n" + layout.jobDefaults + "    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '22'}\n" + layout.step
			root := newJestRepository(t, workflow)
			require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(root, "sub/package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`), 0600))
			_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=18")
			require.NoError(t, err)
			data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
			require.NoError(t, err)
			var updated ciWorkflow
			require.NoError(t, yaml.Unmarshal(data, &updated))
			steps := updated.Jobs["test"].Steps
			require.Len(t, steps, 3)
			require.Contains(t, steps[2].Run, "npx --no-install nyc -- npm test", "nyc wraps the Mocha leaf of the step's own package")
			require.Empty(t, checkTestBootstrap(updated, updated.Jobs["test"], steps[2], nil, resolveTestStep(root, updated, updated.Jobs["test"], steps[2], "javascript", "mocha").Tests, "mocha"))
		})
	}
}

func TestApplyRefusesACdItCannotFollow(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - run: (cd sub && npm test)\n")
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub/package.json"), []byte(`{"scripts":{"test":"mocha"},"devDependencies":{"nyc":"17.1.0"}}`), 0600))
	before, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	_, err = setupMochaCoverage(root, root, "mocha", "(cd sub && npm test)")
	require.ErrorContains(t, err, "cd outside a plain command sequence")
	after, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestApplyConditionalRuntimeSetupUsesActiveMatrixRow(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    strategy:\n      matrix: {lane: [legacy, current]}\n    steps:\n      - uses: actions/setup-node@v4\n        if: matrix.lane == 'legacy'\n        with: {node-version: '16'}\n      - uses: actions/setup-node@v4\n        if: matrix.lane == 'current'\n        with: {node-version: '24'}\n      - run: npx vitest run\n")
	_, err := ApplyJavaScriptSetup(root, "vitest", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var workflow ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	steps := workflow.Jobs["test"].Steps
	require.Equal(t, "matrix.lane == 'current'", steps[2].If)
	require.Equal(t, "matrix.lane == 'current'", steps[3].If)
	require.Equal(t, "${{ !(matrix.lane == 'current') }}", steps[4].If)
}

func TestApplyMochaKeepsRecognizedBabelIstanbulCoverage(t *testing.T) {
	root := newJestRepository(t, "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n")
	writeScripts(t, root, map[string]string{"test": "mocha"})
	require.NoError(t, os.WriteFile(filepath.Join(root, "babel.config.cjs"), []byte(`module.exports={plugins:['istanbul']}`), 0600))
	_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.NoError(t, err, "Babel/Istanbul coverage needs no nyc dependency")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.NotContains(t, string(data), "nyc")
	var generated ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &generated))
	job := generated.Jobs["test"]
	step := job.Steps[len(job.Steps)-1]
	resolution := resolveTestStep(root, generated, job, step, "javascript", "mocha")
	require.Empty(t, checkTestBootstrap(generated, job, step, nil, resolution.Tests, "mocha"))
}

func TestApplyMochaRefusesNYCForNativeESMBeforeWriting(t *testing.T) {
	workflow := "jobs:\n  test:\n    steps:\n      - uses: actions/setup-node@v4\n        with: {node-version: '24'}\n      - run: npm test\n"
	root := newJestRepository(t, workflow)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","scripts":{"test":"mocha test.js"},"devDependencies":{"mocha":"11.7.6","nyc":"17.1.0"}}`), 0600))
	_, err := ApplyJavaScriptSetup(root, "mocha", "6.20.1", "datadoghq.com", ">=22")
	require.ErrorContains(t, err, "native ESM Mocha source coverage can't be set up automatically")
	require.ErrorContains(t, err, "@istanbuljs/esm-loader-hook")
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	require.Equal(t, workflow, string(data), "nothing is written")
}
