package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestAgentHandoffDoesNotRepeatLargeMatrix(t *testing.T) {
	plan := &ExecutionPlan{Tracer: "5.132.0", Site: "datadoghq.com", Framework: "mocha", Initialization: "preserve hooks"}
	for range 25 {
		plan.Scope.Commands = append(plan.Scope.Commands, TestCommand{Command: strings.Repeat("configuration detail ", 100)})
	}
	var output bytes.Buffer
	require.NoError(t, RenderAgentResult(&output, AgentResult{RunID: "abcdefghijklmnopqrstuv", Status: "needs_input", Plan: plan, Reason: "one missing prerequisite", ResumeCommand: []string{"ddtest", "onboard", "--resume", "abcdefghijklmnopqrstuv"}}, "json"))
	require.Less(t, output.Len(), 8192)
	var result AgentResult
	require.NoError(t, json.Unmarshal(output.Bytes(), &result))
	require.Nil(t, result.Plan)
	require.Equal(t, 25, result.PlanSummary.Configurations)
	require.Equal(t, "5.132.0", result.PlanSummary.Tracer)
	require.Contains(t, result.DetailsCommand, "--resume")
	require.NotEmpty(t, result.ResumeCommand)
}

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
		_, err := ApplyVitestSetup(root, "6.21.0")
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
		require.NoError(t, err)
		var result ciWorkflow
		require.NoError(t, yaml.Unmarshal(data, &result))
		step := result.Jobs["test"].Steps[4]
		require.Empty(t, checkJestBootstrap(result, result.Jobs["test"], step, nil, "vitest"))
		require.Contains(t, string(data), "LANG: en_US.utf8")
		require.Contains(t, string(data), "--max-old-space-size=2048")
		changes, err := ApplyVitestSetup(root, "6.21.0")
		require.NoError(t, err)
		require.Empty(t, changes)
	}
}

func TestApplyCookieParserPartitionsWithoutDroppingLegacyRows(t *testing.T) {
	root := cookieParserRepository(t)
	_, err := ApplyJavaScriptSetup(root, "mocha", "5.132.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/test.yml"))
	require.NoError(t, err)
	var result ciWorkflow
	require.NoError(t, yaml.Unmarshal(data, &result))
	rows, err := runtimeMatrix(result.Jobs["test"].Strategy.Matrix)
	require.NoError(t, err)
	require.Len(t, rows, 25)
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
	root := octokitCucumberFixture(t)
	before, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	_, err = ApplyJavaScriptSetup(root, "cucumber", "5.132.0", "datadoghq.com")
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
        with: {node-version: '20'}
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
	root := cookieParserRepository(t)
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
