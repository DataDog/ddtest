package onboard

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCucumberLiteralAliasesPreserveCoverageAndProfiles(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "nyc -- cucumber-js features --profile ci"})
	commands := ValidationCommands(root, "npm test", "cucumber")
	require.Len(t, commands, 1)
	require.Empty(t, commands[0].UnvalidatedSetup)
	require.Equal(t, "npm test", commands[0].Command)
	require.True(t, CucumberUsesNYC(root, "npm test"))
	probe, err := CucumberProbeCommand(root, "npm test")
	require.NoError(t, err)
	require.Equal(t, "nyc --check-coverage=false -- cucumber-js features --profile ci", probe)
	_, err = CucumberValidationCommand(root, "npm test")
	require.NoError(t, err)
}
func TestCucumberAmbiguousAliasesStayUnvalidated(t *testing.T) {
	for _, script := range []string{"MODE=inner cucumber-js features", "cd nested && cucumber-js features", `DEBUG="any,$DEBUG" cucumber-js features`, "run-s test:*", "cucumber-js one && cucumber-js two"} {
		t.Run(script, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0700))
			writeScripts(t, root, map[string]string{"test": script, "test:one": "cucumber-js features"})
			_, err := CucumberValidationCommand(root, "npm test")
			require.Error(t, err)
		})
	}
}
func TestCucumberCIRequiresCoverageAndEarlyInitialization(t *testing.T) {
	for _, coverage := range []bool{false, true} {
		t.Run(map[bool]string{false: "coverage missing", true: "coverage configured"}[coverage], func(t *testing.T) {
			root := t.TempDir()
			script := "cucumber-js features"
			if coverage {
				script = "nyc -- " + script
			}
			writeScripts(t, root, map[string]string{"test": script})
			path := filepath.Join(root, ".github/workflows/ci.yml")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			require.NoError(t, os.WriteFile(path, []byte(`jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`), 0600))
			check := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
				return tracerRequirement{Version: "6.20.1", Node: ">=22"}, nil
			}, "cucumber")
			if coverage {
				require.Equal(t, "compatible", check.Status, check.Jobs)
			} else {
				require.Equal(t, "inconclusive", check.Status, check.Jobs)
				require.Contains(t, check.Jobs[0].Reason, "coverage")
			}
		})
	}
}

func TestCucumberScopedBootstrapUsesOnlySelectedLeaves(t *testing.T) {
	root := t.TempDir()
	scripts := map[string]string{"test:ci": cucumberScopedOptions + " nyc -- cucumber-js", "bdd": "npm run test:ci", "cucumber:watch": "cucumber-js --watch"}
	writeScripts(t, root, scripts)
	step := runtimeStep{Run: "npm run bdd", Env: map[string]string{"DD_CUCUMBER_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, step, "javascript", "cucumber")
	require.Empty(t, resolution.Reason)
	require.Empty(t, checkCucumberBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil, resolution.Tests))
	scripts["test:ci"] = "nyc -- cucumber-js"
	writeScripts(t, root, scripts)
	resolution = resolveTestStep(root, ciWorkflow{}, runtimeJob{}, step, "javascript", "cucumber")
	require.Contains(t, checkCucumberBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil, resolution.Tests), "Selected Cucumber leaf")
}
func TestCucumberESMLoaderUsesEnvironmentPrecedence(t *testing.T) {
	for _, scope := range []bool{false, true} {
		root := t.TempDir()
		script := "nyc -- cucumber-js"
		if scope {
			script = cucumberScopedOptions + " " + script
		}
		data := `{"type":"module","scripts":{"test":` + strconv.Quote(script) + `}}`
		require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(data), 0600))
		preload := "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"
		workflow := ciWorkflow{Env: map[string]string{"NODE_OPTIONS": preload + " --experimental-loader @istanbuljs/esm-loader-hook"}}
		step := runtimeStep{Run: "npm test", Env: map[string]string{"NODE_OPTIONS": preload}}
		if scope {
			step.Env["DD_CUCUMBER_NODE_OPTIONS"] = preload
		}
		resolution := resolveTestStep(root, workflow, runtimeJob{}, step, "javascript", "cucumber")
		require.Contains(t, checkCucumberBootstrap(root, workflow, runtimeJob{}, step, nil, resolution.Tests), "Istanbul")
		step.Env["NODE_OPTIONS"] += " --experimental-loader @istanbuljs/esm-loader-hook"
		require.Empty(t, checkCucumberBootstrap(root, workflow, runtimeJob{}, step, nil, resolution.Tests))
	}
}
func TestCucumberCompoundAliasesCannotMisrouteFormatter(t *testing.T) {
	for _, script := range []string{"nyc -- cucumber-js && echo done", "echo begin && nyc -- cucumber-js", "npm run bdd && echo done"} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"test": script, "bdd": "nyc -- cucumber-js"})
		remaining := 256
		resolution := resolveJestSequence(root, root, "npm test", nil, &remaining, "cucumber")
		require.Contains(t, resolution.Reason, "formatter options")
		_, err := CucumberValidationCommand(root, "npm test")
		require.Error(t, err)
	}
}
