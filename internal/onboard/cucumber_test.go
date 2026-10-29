package onboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	require.Equal(t, "nyc", CucumberCoverageWrapper(root, "npm test"))
	writeScripts(t, root, map[string]string{"test": "c8 cucumber-js features"})
	require.Equal(t, "c8", CucumberCoverageWrapper(root, "npm test"))
	writeScripts(t, root, map[string]string{"test": "nyc -- cucumber-js features --profile ci"})
	probe, err := CucumberProbeCommand(root, "npm test")
	require.NoError(t, err)
	// Probes take the relaxed coverage wrapper from the leaf; testdrive
	// replaces the leaf's paths and --profile with its owned probe config.
	require.True(t, strings.HasPrefix(probe, "nyc --check-coverage=false -- cucumber-js "), probe)
	_, err = CucumberValidationCommand(root, "npm test")
	require.NoError(t, err)
}
func TestCucumberAmbiguousAliasesStayUnvalidated(t *testing.T) {
	for _, script := range []string{"MODE=inner cucumber-js features", "cd nested && cucumber-js features", `DEBUG="any,$OTHER" cucumber-js features`, "run-s missing:*", "cucumber-js one && cucumber-js two"} {
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
func TestCucumberESMLoaderAcceptsEveryLoaderOptionForm(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","scripts":{"test":"nyc -- cucumber-js"}}`), 0600))
	preload := "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"
	for _, loader := range []string{"--experimental-loader @istanbuljs/esm-loader-hook", "--loader @istanbuljs/esm-loader-hook", "--experimental-loader=@istanbuljs/esm-loader-hook", "--loader=@istanbuljs/esm-loader-hook"} {
		step := runtimeStep{Run: "npm test", Env: map[string]string{"NODE_OPTIONS": preload + " " + loader}}
		resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, step, "javascript", "cucumber")
		require.Empty(t, checkCucumberBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil, resolution.Tests), loader)
	}
	require.False(t, nodeOptionsLoad("--loader=@istanbuljs/esm-loader-hook-fork", "@istanbuljs/esm-loader-hook"))
	require.False(t, nodeOptionsLoad("--require @istanbuljs/esm-loader-hook", "@istanbuljs/esm-loader-hook"))
}
func TestCucumberDefaultCommandAcceptsACIEnvironment(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test": "nyc -- cucumber-js"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte("jobs:\n  test:\n    steps:\n      - run: npm test\n        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}', TZ: UTC}\n"), 0644))
	command, err := CucumberValidationCommand(root, "")
	require.NoError(t, err, "a CI environment alone does not need --all")
	require.Equal(t, "npm test", command)
}
func TestCucumberAliasNamesItsUnresolvedVariable(t *testing.T) {
	for _, script := range []string{`nyc -- cucumber-js "$FEATURES"`, `nyc -- cucumber-js ${FEATURES}`} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"test": script})
		remaining := &scriptBudget{left: 256}
		resolution := resolveJestSequence(root, root, "npm test", nil, remaining, "cucumber")
		require.Contains(t, resolution.Reason, "unresolved environment variable $FEATURES", script)
		require.NotContains(t, resolution.Reason, "compound scripts")
	}
}
func TestCucumberCompoundAliasesCannotMisrouteFormatter(t *testing.T) {
	for _, script := range []string{"nyc -- cucumber-js && echo done", "npm run bdd && echo done", "npm run bdd && npm run bdd", "echo begin; nyc -- cucumber-js"} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"test": script, "bdd": "nyc -- cucumber-js"})
		remaining := &scriptBudget{left: 256}
		resolution := resolveJestSequence(root, root, "npm test", nil, remaining, "cucumber")
		require.Contains(t, resolution.Reason, "formatter options", script)
		_, err := CucumberValidationCommand(root, "npm test")
		require.Error(t, err, script)
	}
}

func TestCucumberChainEndingInItsOnlyLeafResolves(t *testing.T) {
	// The leaf is the last link of one && chain, so forwarded options and its
	// exit status stay its own; earlier links are other frameworks or setup.
	for _, script := range []string{"echo begin && nyc -- cucumber-js", "pnpm test:unit && pnpm test:bdd"} {
		root := t.TempDir()
		writeScripts(t, root, map[string]string{"test": script, "test:unit": "vitest run", "test:bdd": "nyc cucumber-js"})
		remaining := &scriptBudget{left: 256}
		resolution := resolveJestSequence(root, root, "npm test", nil, remaining, "cucumber")
		require.Empty(t, resolution.Reason, script)
		require.True(t, resolution.Matched, script)
		require.Len(t, resolution.Tests, 1, script)
	}
}

func TestCucumberCompoundScriptWithoutALeafIsNotACucumberAlias(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"build": "rimraf dist && tsup && tsc -p tsconfig.build.json"})
	remaining := &scriptBudget{left: 256}
	resolution := resolveJestSequence(root, root, "pnpm build", nil, remaining, "cucumber")
	require.NotContains(t, resolution.Reason, "formatter options")
	require.False(t, resolution.Matched)
}

func TestCucumberProbeLifecycleListsTheHooksAroundTheLeaf(t *testing.T) {
	for _, tc := range []struct {
		name          string
		scripts       map[string]string
		before, after []string
	}{
		{"direct", map[string]string{"pretest": "cp fixtures/steps.js features/support/steps.js", "test": "cucumber-js", "posttest": "rm features/support/steps.js"}, []string{"pretest"}, []string{"posttest"}},
		{"nested", map[string]string{"pretest": "mkdir out", "test": "npm run scenarios", "prescenarios": "cp fixtures/steps.js features/support/steps.js", "scenarios": "cucumber-js", "postscenarios": "rm features/support/steps.js", "posttest": "rm out/marker"}, []string{"pretest", "prescenarios"}, []string{"postscenarios", "posttest"}},
		{"leaf in a hook", map[string]string{"pretest": "cucumber-js", "test": "echo done", "posttest": "rm features/support/steps.js"}, nil, []string{"posttest"}},
		{"none", map[string]string{"test": "cucumber-js"}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeScripts(t, root, tc.scripts)
			before, after, err := CucumberProbeLifecycle(root, "npm test")
			require.NoError(t, err)
			names := func(scripts []LifecycleScript) []string {
				var result []string
				for _, script := range scripts {
					require.Equal(t, ".", script.Directory)
					result = append(result, script.Name)
				}
				return result
			}
			require.Equal(t, tc.before, names(before))
			require.Equal(t, tc.after, names(after))
		})
	}
}

func TestCucumberInlineIstanbulRemainsScopedToOriginalAlias(t *testing.T) {
	root := t.TempDir()
	script := cucumberInlineIstanbulOptions + ` DEBUG="any,$DEBUG" nyc -- cucumber-js features --profile ci`
	data, err := json.Marshal(map[string]any{"type": "module", "scripts": map[string]string{"test": script}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), data, 0600))
	step := runtimeStep{Run: "npm test", Env: map[string]string{"DD_CUCUMBER_NODE_OPTIONS": "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}"}}
	resolution := resolveTestStep(root, ciWorkflow{}, runtimeJob{}, step, "javascript", "cucumber")
	require.Empty(t, resolution.Reason)
	require.Len(t, resolution.Tests, 1)
	require.True(t, resolution.Tests[0].cucumberScoped)
	require.True(t, resolution.Tests[0].cucumberInlineIstanbul)
	require.Equal(t, "npm test", resolution.Tests[0].Command)
	require.Empty(t, checkCucumberBootstrap(root, ciWorkflow{}, runtimeJob{}, step, nil, resolution.Tests))
	actual, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	require.Equal(t, data, actual)
}

func TestCucumberFocusedCommandNamesWhyItDidNotResolve(t *testing.T) {
	root := t.TempDir()
	writeScripts(t, root, map[string]string{"test:bdd": `cross-env NODE_OPTIONS="--require ./setup.js" cucumber-js`})
	_, err := CucumberExecutionCommand(root, "npm run test:bdd")
	require.ErrorContains(t, err, "command overrides instrumentation or executable selection")
	require.ErrorContains(t, err, "report this blocker rather than rewriting package scripts")
}
