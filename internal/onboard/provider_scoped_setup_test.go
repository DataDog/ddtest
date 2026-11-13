// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

// scopedProvider writes one test job on a provider without the shared
// workflow syntax and says how its setup persists a command.
type scopedProvider struct {
	name, file string
	// config is a job running commands in order.
	config func(commands ...string) string
	// persisted is how the setup writes a command that follows it.
	persisted func(command string) string
}

var scopedProviders = []scopedProvider{
	{
		name: gitLabCIProvider, file: gitLabCIFile,
		config: func(commands ...string) string {
			return "test:\n  image: node:22\n  script:\n    - " + strings.Join(commands, "\n    - ") + "\n"
		},
		persisted: func(command string) string { return command },
	},
	{
		name: circleCIProvider, file: circleCIFile,
		config: func(commands ...string) string {
			return "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - run: " + strings.Join(commands, "\n      - run: ") + "\n" + circleWorkflow("      - test\n")
		},
		persisted: circlePersist,
	},
	{
		name: buildkiteProvider, file: buildkiteFile,
		config: func(commands ...string) string {
			return "steps:\n  - label: test\n    command:\n      - " + strings.Join(commands, "\n      - ") + "\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n"
		},
		persisted: buildkiteEscape,
	},
}

// scopedProvidersNamed is the scoped provider of name, for a test whose
// behavior doesn't depend on the provider.
func scopedProvidersNamed(name string) []scopedProvider {
	for _, provider := range scopedProviders {
		if provider.name == name {
			return []scopedProvider{provider}
		}
	}
	panic("no scoped provider " + name)
}

// scopedRepository is a repository whose only CI is provider's config,
// selected for the test, with the given package.json and files.
func scopedRepository(t *testing.T, provider scopedProvider, config string, pkg map[string]any, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	data, err := json.MarshalIndent(pkg, "", "  ")
	require.NoError(t, err)
	writeFile(t, root, "package.json", string(data)+"\n")
	writeFile(t, root, provider.file, config)
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	t.Chdir(root)
	useCIProvider(t, provider.name)
	return root
}

func scopedRequirement(context.Context, string, string) (tracerRequirement, error) {
	return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
}

func appliedScripts(t *testing.T, root string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	require.NoError(t, err)
	var pkg struct{ Scripts map[string]string }
	require.NoError(t, json.Unmarshal(data, &pkg))
	return pkg.Scripts
}

// scopedFramework is a Cucumber or WebdriverIO project and the package
// script onboard --apply writes for its test leaf.
type scopedFramework struct {
	name    string
	pkg     map[string]any
	files   map[string]string
	applied string
}

var scopedFrameworks = []scopedFramework{
	{
		name:    "cucumber",
		pkg:     map[string]any{"scripts": map[string]string{"build": "echo built", "test": "cucumber-js"}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0"}},
		applied: cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- cucumber-js",
	},
	{
		name:    "webdriverio",
		pkg:     map[string]any{"scripts": map[string]string{"build": "echo built", "test": "wdio run wdio.conf.js"}, "devDependencies": map[string]string{"@wdio/cli": "9.4.1", "@wdio/local-runner": "9.4.1", "@wdio/mocha-framework": "9.4.1"}},
		files:   map[string]string{"wdio.conf.js": "exports.config = {\n  runner: 'local',\n  framework: 'mocha',\n  specs: ['./test/*.js'],\n}\n"},
		applied: webdriverIOScopedOptions + " wdio run wdio.conf.js",
	},
}

// onboard --apply writes the provider's setup with the command that moves
// the preload into the framework's scoped variable, and scopes the package
// scripts as on GitHub Actions; the result is complete and a second apply
// changes nothing.
func TestProviderApplyScopesCucumberAndWebdriverIO(t *testing.T) {
	for _, provider := range scopedProviders {
		for _, framework := range scopedFrameworks {
			t.Run(provider.name+"/"+framework.name, func(t *testing.T) {
				root := scopedRepository(t, provider, provider.config("npm run build", "npm test"), framework.pkg, framework.files)
				plan, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				require.NoError(t, err)
				config := plannedFile(t, plan, provider.file)
				scoped := provider.persisted(providerScopedCommand(framework.name, ""))
				require.Contains(t, config, scoped)
				require.Less(t, strings.Index(config, "npm run build"), strings.Index(config, scoped), "the setup follows the build")
				require.Less(t, strings.Index(config, scoped), strings.Index(config, "npm test"))
				require.NotContains(t, config, esmImportCommand, "the scoped variable carries the ESM loader")
				ignored := plannedFile(t, plan, ".gitignore")
				require.Equal(t, framework.name == "cucumber", strings.Contains(ignored, ".nyc_output/"))
				_, err = plan.apply(true, os.Rename)
				require.NoError(t, err)
				scripts := appliedScripts(t, root)
				require.Equal(t, framework.applied, scripts["test"])
				require.Equal(t, "echo built", scripts["build"])

				check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, framework.name)
				require.Equal(t, StatusCompatible, check.Status, check.Jobs)
				require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, framework.name, "datadoghq.com"))
				again, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				require.NoError(t, err)
				require.True(t, again.Configured(), "a second apply changes nothing")
			})
		}
	}
}

// Native ESM Cucumber also gets the Istanbul ESM loader in the scoped
// variable, as on GitHub Actions, after its dependency is installed.
func TestProviderApplyScopesNativeESMCucumber(t *testing.T) {
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			pkg := map[string]any{"type": "module", "scripts": map[string]string{"test": "cucumber-js"}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0"}}
			root := scopedRepository(t, provider, provider.config("npm test"), pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var dependency *SetupDependencyGap
			require.ErrorAs(t, err, &dependency)
			require.Equal(t, []string{"@istanbuljs/esm-loader-hook"}, dependency.Packages)

			pkg["devDependencies"].(map[string]string)["@istanbuljs/esm-loader-hook"] = "0.3.0"
			data, err := json.Marshal(pkg)
			require.NoError(t, err)
			writeFile(t, root, "package.json", string(data))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, provider.file), provider.persisted(providerScopedCommand("cucumber", cucumberESMLoaderHook)))
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "cucumber")
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
		})
	}
}

// The scoped variable keeps the preload out of standalone automation that
// runs in the same script as the WDIO runner; the setup's job-wide preload
// alone would reach it.
func TestProviderScopedPreloadSkipsStandaloneAutomation(t *testing.T) {
	pkg := map[string]any{"scripts": map[string]string{"e2e": "run-s e2e:standalone e2e:runner", "e2e:standalone": "node standalone.js", "e2e:runner": "wdio run wdio.conf.js"}, "devDependencies": map[string]string{"@wdio/cli": "9.4.1", "npm-run-all": "4.1.5"}}
	files := map[string]string{"standalone.js": "import { remote } from 'webdriverio'\nawait remote({})\n", "wdio.conf.js": "exports.config = {\n  runner: 'local',\n  framework: 'mocha',\n}\n"}
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := scopedRepository(t, provider, provider.config("npm run e2e"), pkg, files)
			_, err := ApplyJavaScriptSetup(root, "webdriverio", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			scripts := appliedScripts(t, root)
			require.Equal(t, webdriverIOScopedOptions+" wdio run wdio.conf.js", scripts["e2e:runner"])
			require.Equal(t, "node standalone.js", scripts["e2e:standalone"])
			check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "webdriverio")
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
		})
	}
	// The setup script alone leaves the preload in NODE_OPTIONS for the
	// standalone script too.
	root := scopedRepository(t, scopedProviders[0], "test:\n  image: node:22\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - "+esmImportCommand+"\n    - npm run e2e\n", pkg, files)
	writeScripts(t, root, map[string]string{"e2e": "run-s e2e:standalone e2e:runner", "e2e:standalone": "node standalone.js", "e2e:runner": webdriverIOScopedOptions + " wdio run wdio.conf.js"})
	check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "webdriverio")
	require.Equal(t, StatusInconclusive, check.Status, check.Jobs)
	require.Contains(t, check.Jobs[0].Reason, "also runs standalone automation")
}

// A provider setup a job already has keeps working when it leaves the
// preload in NODE_OPTIONS: onboard --apply scopes only the package scripts.
// Native ESM Cucumber needs the scoped variable's Istanbul loader, which
// apply cannot add next to an existing setup, so it names the command.
func TestProviderApplyKeepsAnUnscopedExistingSetup(t *testing.T) {
	provider := scopedProviders[0]
	config := "test:\n  image: node:22\n  script:\n" + gitLabSetupEntry(gitLabJestSetup) + "    - " + esmImportCommand + "\n    - npm test\n"
	framework := scopedFrameworks[0]
	root := scopedRepository(t, provider, config, framework.pkg, nil)
	plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	for _, edit := range plan.edits {
		require.NotEqual(t, gitLabCIFile, edit.path, "the existing setup is kept")
	}
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)
	require.Equal(t, framework.applied, appliedScripts(t, root)["test"])
	check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "cucumber")
	require.Equal(t, StatusCompatible, check.Status, check.Jobs)

	pkg := map[string]any{"type": "module", "scripts": map[string]string{"test": "cucumber-js"}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0"}}
	root = scopedRepository(t, provider, config, pkg, nil)
	_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, providerScopedCommand("cucumber", cucumberESMLoaderHook))

	// A scoped variable the job sets itself is custom instrumentation.
	root = scopedRepository(t, provider, "test:\n  image: node:22\n  variables:\n    DD_CUCUMBER_NODE_OPTIONS: --trace-warnings\n  script:\n    - npm test\n", framework.pkg, nil)
	_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Custom inherited instrumentation requires review: DD_CUCUMBER_NODE_OPTIONS")
}

// Every test step's own variables are checked, not only the first one's: a
// later step that replaces the scoped variable gets no preload from it, so
// onboard --apply neither adds a setup beside it nor accepts the existing
// one.
func TestProviderApplyChecksEveryTestStepsScopedVariable(t *testing.T) {
	framework := scopedFrameworks[0]
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := scopedRepository(t, provider, provider.config("npm test", "DD_CUCUMBER_NODE_OPTIONS= npm test"), framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "Custom inherited instrumentation requires review: DD_CUCUMBER_NODE_OPTIONS")
		})
	}
	provider := scopedProviders[0]
	setup := "test:\n  image: node:22\n  script:\n" + gitLabSetupEntry(gitLabJestSetup) + "    - " + providerScopedCommand("cucumber", "") + "\n    - npm test\n"
	for _, later := range []string{"DD_CUCUMBER_NODE_OPTIONS= npm test", "export DD_CUCUMBER_NODE_OPTIONS=--trace-warnings", "npm test"} {
		root := scopedRepository(t, provider, setup+"    - "+later+"\n    - npm test\n", framework.pkg, nil)
		plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
		if later == "npm test" {
			require.NoError(t, err, "every test step reads the exported preload")
			for _, edit := range plan.edits {
				require.NotEqual(t, gitLabCIFile, edit.path, "the existing setup is kept")
			}
			continue
		}
		var gap *SetupGap
		require.ErrorAs(t, err, &gap, later)
		require.Contains(t, gap.Reason, "DD_CUCUMBER_NODE_OPTIONS no longer carries the tracer's preload", later)
	}
}

// The instructions show the scoped command after the setup and the package
// script prefix that consumes it.
func TestProviderScopedSetupInstructions(t *testing.T) {
	for _, provider := range []ciProvider{gitLabCI{}, circleCI{}, buildkite{}} {
		for _, framework := range []string{"cucumber", "webdriverio"} {
			t.Run(provider.name()+"/"+framework, func(t *testing.T) {
				text := providerSetupInstructions(t.TempDir(), provider, "javascript", framework, "", true)
				command := providerScopedCommand(framework, "")
				switch provider.(type) {
				case circleCI:
					command = circlePersist(command)
				case buildkite:
					command = buildkiteEscape(command)
				}
				require.Contains(t, text, command)
				require.Contains(t, text, "Start each ")
				require.Contains(t, text, "directly, not through a package script, starts with "+scopedPrefix(scopedEnvironment(framework)))
				require.Contains(t, text, scopedEnvironment(framework)+":-}")
				require.NotContains(t, text, esmImportCommand)
			})
		}
	}
}

// ${NAME%pattern} and the like trim a known value as the shell does; a
// bracket pattern or an unknown value stays dynamic.
func TestCIShellPatternRemoval(t *testing.T) {
	known := map[string]string{"NODE_OPTIONS": "--max-old-space-size=4096 -r ./x.js -r ${{ env.DD_TRACE_PACKAGE }}", "SUFFIX": "-r ./x.js", "GLOB": "*"}
	word := func(text string) *syntax.Word {
		file, err := syntax.NewParser().Parse(strings.NewReader("x="+text), "")
		require.NoError(t, err)
		return file.Stmts[0].Cmd.(*syntax.CallExpr).Assigns[0].Value
	}
	for _, tc := range []struct {
		text, value string
		literal     bool
	}{
		{`"${NODE_OPTIONS%-r *}"`, "--max-old-space-size=4096 -r ./x.js ", true},
		{`"${NODE_OPTIONS%%-r *}"`, "--max-old-space-size=4096 ", true},
		{`"${NODE_OPTIONS##*-r }"`, "${{ env.DD_TRACE_PACKAGE }}", true},
		{`"${NODE_OPTIONS#*-r }"`, "./x.js -r ${{ env.DD_TRACE_PACKAGE }}", true},
		{`"${NODE_OPTIONS%"-r *"}"`, known["NODE_OPTIONS"], true},
		{`"${NODE_OPTIONS%--other}"`, known["NODE_OPTIONS"], true},
		{`"${NODE_OPTIONS% "$SUFFIX"*}"`, "--max-old-space-size=4096", true},
		{`"${NODE_OPTIONS%$GLOB}"`, "${NODE_OPTIONS...}", false},
		{`"${NODE_OPTIONS%[-]r *}"`, "${NODE_OPTIONS...}", false},
		{`"${UNKNOWN%-r *}"`, "${UNKNOWN...}", false},
	} {
		value, literal := ciShellWord(word(tc.text), known, "", "")
		require.Equal(t, tc.value, value, tc.text)
		require.Equal(t, tc.literal, literal, tc.text)
	}
	// The scoped command reads the preload the setup appended, then removes it.
	for _, before := range []string{"", "--max-old-space-size=4096 -r ./x.js"} {
		state := ciShellState{env: map[string]string{"NODE_OPTIONS": strings.TrimSpace(before + " -r ${{ env.DD_TRACE_PACKAGE }}")}, references: map[string]string{"DD_TRACE_ESM_IMPORT": "${{ env.DD_TRACE_ESM_IMPORT }}"}, job: map[string]string{}}
		state.apply(providerScopedCommand("cucumber", ""), map[string]string{})
		require.Equal(t, "-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}", state.env["DD_CUCUMBER_NODE_OPTIONS"])
		require.Equal(t, before, strings.TrimSpace(state.env["NODE_OPTIONS"]))
	}
}

// ${NAME:+word} and ${NAME+word} add word only after a set variable: the
// PYTHONPATH rewrite stays "src" when the setup skipped its export, where
// "$PYTHONPATH:src" gave ":src", whose empty entry is the working directory.
func TestCIShellAlternateValue(t *testing.T) {
	known := map[string]string{"PYTHONPATH": "${{ env.DD_TRACE_PYTHON_PATH }}", "EMPTY": ""}
	word := func(text string) *syntax.Word {
		file, err := syntax.NewParser().Parse(strings.NewReader("x="+text), "")
		require.NoError(t, err)
		return file.Stmts[0].Cmd.(*syntax.CallExpr).Assigns[0].Value
	}
	for _, tc := range []struct {
		text, self, value string
		literal           bool
	}{
		{`"${PYTHONPATH:+$PYTHONPATH:}src"`, "PYTHONPATH", "${{ env.DD_TRACE_PYTHON_PATH }}:src", true},
		{`"${PYTHONPATH:+$PYTHONPATH:}"'my src'`, "PYTHONPATH", "${{ env.DD_TRACE_PYTHON_PATH }}:my src", true},
		{`"${EMPTY:+$EMPTY:}src"`, "EMPTY", "src", true},
		{`"${EMPTY+set}"`, "", "set", true},
		{`"${UNSET:+$UNSET:}src"`, "UNSET", "src", true},
		{`"${UNSET:+$UNSET:}src"`, "", "${UNSET...}src", false},
		{`"${PYTHONPATH:+$(pwd):}src"`, "PYTHONPATH", "$(…):src", false},
	} {
		value, literal := ciShellWord(word(tc.text), known, tc.self, "")
		require.Equal(t, tc.value, value, tc.text)
		require.Equal(t, tc.literal, literal, tc.text)
	}
	state := ciShellState{env: map[string]string{}, references: map[string]string{}, job: map[string]string{}}
	file, err := syntax.NewParser().Parse(strings.NewReader(`export PYTHONPATH="${PYTHONPATH:+$PYTHONPATH:}src"`), "")
	require.NoError(t, err)
	state.statement(file.Stmts[0], map[string]string{})
	require.Equal(t, "src", state.env["PYTHONPATH"])
}

// Every provider fixture for Cucumber and WebdriverIO resolves its scope
// and plans the scoped setup.
func TestProviderScopedFixtures(t *testing.T) {
	for _, tc := range []struct{ fixture, framework, file string }{
		{"gitlab-cucumber", "cucumber", gitLabCIFile},
		{"gitlab-webdriverio", "webdriverio", gitLabCIFile},
		{"circleci-cucumber", "cucumber", circleCIFile},
		{"circleci-webdriverio", "webdriverio", circleCIFile},
		{"buildkite-cucumber", "cucumber", buildkiteFile},
		{"buildkite-webdriverio", "webdriverio", buildkiteFile},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			root := copyFixture(t, tc.fixture)
			t.Chdir(root)
			useCIProvider(t, "")
			scope, err := DiscoverValidationScope(root, tc.framework)
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.NotEmpty(t, scope.Commands)
			plan, err := PlanJavaScriptSetup(root, tc.framework, "5.133.0", "datadoghq.com", ">=18")
			if dependency := (*SetupDependencyGap)(nil); errors.As(err, &dependency) {
				t.Fatalf("fixture misses dev dependencies: %v", dependency)
			}
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, tc.file), scopedEnvironment(tc.framework)+`="-r `)
			require.Contains(t, plannedFile(t, plan, "package.json"), "${"+scopedEnvironment(tc.framework)+":-}")
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, tc.framework)
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
		})
	}
}

// A CommonJS package whose Cucumber run selects native ESM inputs, such as
// --import features/steps.mjs, needs the Istanbul ESM loader as a "type":
// "module" package does: its dependency, and the loader in the scoped
// variable on every provider and on GitHub Actions.
func TestScopedCucumberLoaderFollowsSelectedESMInputs(t *testing.T) {
	pkg := map[string]any{"scripts": map[string]string{"test": "cucumber-js --import features/steps.mjs"}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0"}}
	files := map[string]string{"features/steps.mjs": "export {}\n"}
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := scopedRepository(t, provider, provider.config("npm test"), pkg, files)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var dependency *SetupDependencyGap
			require.ErrorAs(t, err, &dependency)
			require.Equal(t, []string{"@istanbuljs/esm-loader-hook"}, dependency.Packages)

			installed := map[string]any{"scripts": pkg["scripts"], "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0"}}
			data, err := json.Marshal(installed)
			require.NoError(t, err)
			writeFile(t, root, "package.json", string(data))
			require.Equal(t, cucumberESMLoaderHook, scopedProviderOptions(root, "cucumber"))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, provider.file), provider.persisted(providerScopedCommand("cucumber", cucumberESMLoaderHook)))
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "cucumber")
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
		})
	}

	root := t.TempDir()
	data, err := json.Marshal(map[string]any{"scripts": pkg["scripts"], "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0"}})
	require.NoError(t, err)
	writeFile(t, root, "package.json", string(data))
	writeFile(t, root, "features/steps.mjs", "export {}\n")
	writeFile(t, root, ".github/workflows/test.yml", "jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: npm test\n")
	_, options, err := scopedSetupEdits(root, "cucumber", false)
	require.NoError(t, err)
	require.Contains(t, options, cucumberESMLoaderHook, "GitHub Actions' scoped options carry the loader too")
}

// An existing provider setup that leaves the preload in NODE_OPTIONS is
// kept for native ESM Cucumber when the selected scripts already carry the
// Istanbul ESM loader in their scoped prefix, or the step's NODE_OPTIONS
// loads it, as validation accepts.
func TestProviderApplyKeepsAnExistingSetupWithItsIstanbulLoader(t *testing.T) {
	provider := scopedProviders[0]
	inline := cucumberInlineIstanbulOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- cucumber-js"
	for name, setup := range map[string]struct{ script, extra string }{
		"inline":       {inline, ""},
		"NODE_OPTIONS": {"cucumber-js", `    - export NODE_OPTIONS="$NODE_OPTIONS ` + cucumberESMLoaderHook + `"` + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			config := "test:\n  image: node:22\n  script:\n" + gitLabSetupEntry(gitLabJestSetup) + "    - " + esmImportCommand + "\n" + setup.extra + "    - npm test\n"
			pkg := map[string]any{"type": "module", "scripts": map[string]string{"test": setup.script}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0"}}
			root := scopedRepository(t, provider, config, pkg, nil)
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			for _, edit := range plan.edits {
				require.NotEqual(t, gitLabCIFile, edit.path, "the existing setup is kept")
			}
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "cucumber")
			require.Equal(t, StatusCompatible, check.Status, check.Jobs)
		})
	}
}

// A job that runs both Cucumber and WebdriverIO: once Cucumber's setup
// moves the preload into DD_CUCUMBER_NODE_OPTIONS, the setup no longer
// reaches WebdriverIO through NODE_OPTIONS, so apply stops instead of
// scoping WebdriverIO's scripts to a variable nothing exports.
func TestProviderApplyRefusesASetupWhosePreloadAnotherFrameworkMoved(t *testing.T) {
	provider := scopedProviders[0]
	pkg := map[string]any{"scripts": map[string]string{"features": "cucumber-js", "e2e": "wdio run wdio.conf.js"}, "devDependencies": map[string]string{"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@wdio/cli": "9.4.1", "@wdio/local-runner": "9.4.1", "@wdio/mocha-framework": "9.4.1"}}
	root := scopedRepository(t, provider, provider.config("npm run features", "npm run e2e"), pkg, scopedFrameworks[1].files)
	plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	_, err = plan.apply(true, os.Rename)
	require.NoError(t, err)

	_, err = PlanJavaScriptSetup(root, "webdriverio", "5.133.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "no longer leaves the tracer's preload in NODE_OPTIONS")
	require.Contains(t, gap.Reason, "DD_WDIO_NODE_OPTIONS")
	require.Equal(t, "wdio run wdio.conf.js", appliedScripts(t, root)["e2e"], "WebdriverIO's scripts stay unscoped")
}

// A CI command that runs Cucumber or WebdriverIO directly, without a package
// script, gets the exact scoped command to write; onboard --apply then adds
// the setup before it, reads that command as consuming the scoped variable,
// and a second apply changes nothing. The same command without the prefix,
// after a setup that exports the scoped variable, gets it prescribed again.
func TestProviderDirectInvocationGetsTheScopedCommand(t *testing.T) {
	for _, provider := range scopedProviders {
		for _, framework := range scopedFrameworks {
			t.Run(provider.name+"/"+framework.name, func(t *testing.T) {
				direct, scoped := "./node_modules/.bin/cucumber-js ./features --format json",
					cucumberScopedOptions+" "+cucumberImpactedWorkaround+" npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json"
				if framework.name == "webdriverio" {
					direct, scoped = "npx wdio run wdio.conf.js", webdriverIOScopedOptions+" npx wdio run wdio.conf.js"
				}
				written := scoped
				if provider.name == buildkiteProvider {
					written = buildkiteEscape(scoped)
				}
				root := scopedRepository(t, provider, provider.config("npm ci", direct), framework.pkg, framework.files)
				_, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				var gap *EditThenApplyGap
				require.ErrorAs(t, err, &gap)
				require.Contains(t, gap.Reason, "Change this CI command to exactly: "+written+". Then rerun onboard --apply: it adds the")
				require.Contains(t, gap.Location, provider.file+":")

				writeFile(t, root, provider.file, provider.config("npm ci", written))
				scope, err := DiscoverValidationScope(root, framework.name)
				require.NoError(t, err)
				require.Empty(t, scope.Unresolved)
				require.Len(t, scope.Commands, 1)
				require.Equal(t, framework.name == "cucumber", scope.Commands[0].cucumberScoped)
				require.Equal(t, framework.name == "webdriverio", scope.Commands[0].webdriverIOScoped)
				plan, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				require.NoError(t, err)
				config := plannedFile(t, plan, provider.file)
				require.Less(t, strings.Index(config, provider.persisted(providerScopedCommand(framework.name, ""))), strings.Index(config, written))
				_, err = plan.apply(true, os.Rename)
				require.NoError(t, err)
				require.Equal(t, framework.pkg["scripts"], appliedScripts(t, root), "no package script runs these tests")
				check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, framework.name)
				require.Equal(t, StatusCompatible, check.Status, check.Jobs)
				require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, framework.name, "datadoghq.com"))
				again, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				require.NoError(t, err)
				require.True(t, again.Configured(), "a second apply changes nothing")

				// The setup alone does not reach a command without the prefix.
				data, err := os.ReadFile(filepath.Join(root, provider.file))
				require.NoError(t, err)
				writeFile(t, root, provider.file, strings.Replace(string(data), written, direct, 1))
				check = checkCIRuntimes(t.Context(), root, nil, scopedRequirement, framework.name)
				require.Equal(t, StatusInconclusive, check.Status, check.Jobs)
				if framework.name == "cucumber" {
					require.Contains(t, check.Jobs[0].Reason, "runs directly, not through a package script")
				}
				_, err = PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				require.ErrorAs(t, err, &gap)
				require.Contains(t, gap.Reason, "Change this CI command to exactly: "+written+". Then rerun onboard --apply: the job's ")
			})
		}
	}
}

// A direct Cucumber command with the scoped prefix and NYC is accepted only
// with the impacted-tests workaround, inline after the prefix or among the
// command's own assignments, as onboard --apply writes package scripts.
func TestProviderDirectCucumberNeedsTheImpactedWorkaround(t *testing.T) {
	provider, framework := scopedProviders[0], scopedFrameworks[0]
	leaf := "npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features"
	root := scopedRepository(t, provider, provider.config("npm ci", cucumberScopedOptions+" "+leaf), framework.pkg, nil)
	_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Change this CI command to exactly: "+cucumberScopedOptions+" "+cucumberImpactedWorkaround+" "+leaf+".")

	for _, command := range []string{
		cucumberScopedOptions + " " + cucumberImpactedWorkaround + " " + leaf,
		cucumberScopedOptions + " DD_ENV=ci " + cucumberImpactedWorkaround + " " + leaf,
	} {
		writeFile(t, root, provider.file, provider.config("npm ci", command))
		_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
		require.NoError(t, err, command)
	}
}

// A direct invocation that assigns variables inline loses them when ddtest
// rebuilds the command from what remains, so ddtest prescribes no exact
// command for it: they move out of the command first.
func TestProviderDirectInvocationKeepsInlineAssignments(t *testing.T) {
	for _, framework := range scopedFrameworks {
		t.Run(framework.name, func(t *testing.T) {
			provider := scopedProviders[0]
			direct := "DD_ENV=ci ./node_modules/.bin/cucumber-js ./features"
			if framework.name == "webdriverio" {
				direct = "DD_ENV=ci npx wdio run wdio.conf.js"
			}
			root := scopedRepository(t, provider, provider.config("npm ci", direct), framework.pkg, framework.files)
			_, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			var edit *EditThenApplyGap
			require.False(t, errors.As(err, &edit), "no exact command drops DD_ENV")
			require.Contains(t, gap.Reason, "with inline variable assignments (DD_ENV), which ddtest does not rewrite")
		})
	}
	// A hand-written preload written inline is never dropped from the
	// prescribed NYC command either.
	provider := handWrittenProviders[0]
	pkg := `{"scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "dd-trace": "5.40.0"}}`
	command := `NODE_OPTIONS='-r dd-trace/ci/init --import dd-trace/register.js' DD_CIVISIBILITY_AGENTLESS_ENABLED=true npx cucumber-js`
	root := handWrittenRepository(t, provider, pkg, provider.config(map[string]string{"DD_SITE": "datadoghq.com"}, []string{"npm ci"}, command), nil)
	_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	var gap *SetupGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "inline variable assignments (DD_CIVISIBILITY_AGENTLESS_ENABLED, NODE_OPTIONS)")
	require.NotContains(t, gap.Reason, "Change this CI command to exactly")
}

// An existing provider setup that leaves the preload in NODE_OPTIONS
// reaches a direct Cucumber command, but no package script carries NYC for
// it: onboard --apply prescribes the NYC-wrapped command and keeps the
// setup, and the result validates.
func TestProviderApplyPrescribesNYCForDirectCucumberAfterAnExistingSetup(t *testing.T) {
	provider, framework := scopedProviders[0], scopedFrameworks[0]
	config := func(command string) string {
		return "test:\n  image: node:22\n  script:\n" + gitLabSetupEntry(gitLabJestSetup) + "    - " + esmImportCommand + "\n    - " + command + "\n"
	}
	root := scopedRepository(t, provider, config("./node_modules/.bin/cucumber-js ./features"), framework.pkg, nil)
	_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, CucumberCoverageGap)
	require.Contains(t, gap.Reason, "the job's Datadog setup script stays.")
	_, after, ok := strings.Cut(gap.Reason, "Change this CI command to exactly: ")
	require.True(t, ok, gap.Reason)
	written, _, _ := strings.Cut(after, ". Then rerun onboard --apply")
	require.Contains(t, written, "npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features")

	writeFile(t, root, provider.file, config(written))
	plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	for _, edit := range plan.edits {
		require.NotEqual(t, gitLabCIFile, edit.path, "the existing setup is kept")
	}
	check := checkCIRuntimes(t.Context(), root, nil, scopedRequirement, "cucumber")
	require.Equal(t, StatusCompatible, check.Status, check.Jobs)
}

// A direct invocation inside a compound command gets no prescribed command:
// the scoped prefix would reach only the command before it.
func TestProviderDirectInvocationInACompoundCommand(t *testing.T) {
	for _, provider := range scopedProviders {
		for _, framework := range scopedFrameworks {
			t.Run(provider.name+"/"+framework.name, func(t *testing.T) {
				direct := "npm run build && ./node_modules/.bin/cucumber-js ./features"
				if framework.name == "webdriverio" {
					direct = "npm run build && npx wdio run wdio.conf.js"
				}
				root := scopedRepository(t, provider, provider.config("npm ci", direct), framework.pkg, framework.files)
				_, err := PlanJavaScriptSetup(root, framework.name, "5.133.0", "datadoghq.com", ">=18")
				var gap *SetupGap
				require.ErrorAs(t, err, &gap)
				var edit *EditThenApplyGap
				require.False(t, errors.As(err, &edit), "no exact command: %v", err)
				require.Contains(t, gap.Reason, "as part of a compound shell command")
			})
		}
	}
}

// A direct WebdriverIO invocation reads its configuration from the
// directory it runs in, not the checkout root's.
func TestProviderDirectWebdriverIOReadsItsDirectorysConfig(t *testing.T) {
	provider, framework := scopedProviders[0], scopedFrameworks[1]
	files := map[string]string{
		"e2e/wdio.conf.js": framework.files["wdio.conf.js"],
		// The checkout root's runs in the browser, outside instrumentation.
		"wdio.conf.js": "exports.config = {\n  runner: 'browser',\n  framework: 'mocha',\n  specs: ['./test/*.js'],\n}\n",
	}
	root := scopedRepository(t, provider, provider.config("npm ci", "cd e2e", "npx wdio run wdio.conf.js"), framework.pkg, files)
	_, err := PlanJavaScriptSetup(root, "webdriverio", "5.133.0", "datadoghq.com", ">=18")
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	require.Contains(t, gap.Reason, "Change this CI command to exactly: "+webdriverIOScopedOptions+" npx wdio run wdio.conf.js.")

	// Without one there, the root's is not read in its place.
	require.NoError(t, os.Remove(filepath.Join(root, "e2e", "wdio.conf.js")))
	_, err = PlanJavaScriptSetup(root, "webdriverio", "5.133.0", "datadoghq.com", ">=18")
	require.Error(t, err)
	require.False(t, errors.As(err, &gap), "%v", err)
}

// A direct Cucumber invocation that CI pipes into a report converter gets
// the exact replacement: the tests write the same report to a file with the
// scoped setup, and the same command converts the file whatever their result
// and exits with their status, or the conversion's when only it fails.
// onboard --apply then accepts it, reading the conversion as outside the
// tests.
func TestProviderSplitsACucumberReportPipe(t *testing.T) {
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			framework := scopedFrameworks[0]
			pipe := "./node_modules/.bin/cucumber-js ./features --format json | ./node_modules/.bin/cucumber-junit > junit.xml"
			root := scopedRepository(t, provider, provider.config("npm ci", pipe), framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			_, after, ok := strings.Cut(gap.Reason, "or the conversion's when only the conversion fails: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". Then rerun onboard --apply")
			require.True(t, ok, gap.Reason)
			tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
			conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
			want := `(status=0; rm -f cucumber-report.json; ` + tests + ` || status=$?; ` + conversion + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
			if provider.name == buildkiteProvider {
				want = buildkiteEscape(want) // As the pipeline holds it.
			}
			require.Equal(t, want, command)

			writeFile(t, root, provider.file, provider.config("npm ci", command, "echo later"))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, provider.file), command)
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json", scope.Commands[0].Command, "the tests are the configuration")
			require.Contains(t, scope.Commands[0].StepScopeNotes, "This CI step runs "+conversion+" after its cucumber tests, whether they pass or fail, then exits with the tests' status, or with the status of a command after them that fails when they pass.")
		})
	}
}

// The status subshell converts the report whatever the tests' result, and
// exits with the tests' status, or the conversion's when only the
// conversion fails, as the original pipe did then: with errexit on, as CI
// scripts run, and off, and with pipefail for the conversion's own pipe.
// Only a passing run continues with the script's later commands under
// errexit. sh runs every combination; bash, also with pipefail, and dash run
// those where the tests, the conversion or both fail, but for the
// three-stage conversion, which only sh needs to show the last stage's
// status counts.
func TestStatusSubshellConvertsAndKeepsTheTestsStatus(t *testing.T) {
	shells := realShells(t, "sh", "bash", "dash")
	tests := map[string]int{"(echo report > report.json; exit 3)": 3, "echo report > report.json": 0}
	conversions := map[string]int{
		"cat report.json | tr a-z A-Z > junit.xml":                       0,
		"cat report.json | sh -c 'tr a-z A-Z > junit.xml; exit 5'":       5,
		"cat report.json | tr a-z A-Z | sh -c 'cat > junit.xml; exit 6'": 6,
	}
	for _, shell := range shells {
		options := []string{"set -e", "set +e"}
		if shell == "bash" {
			options = append(options, "set -e -o pipefail")
		}
		var cases []shellCase
		var labels []string
		var wants []int
		var errexit []bool
		for _, option := range options {
			for tests, testStatus := range tests {
				for conversion, conversionStatus := range conversions {
					if shell != "sh" && (conversionStatus == 6 || testStatus == 0 && conversionStatus == 0) {
						continue
					}
					want := testStatus
					if want == 0 {
						want = conversionStatus
					}
					cases = append(cases, shellCase{script: option + "\n" + statusSubshell("report.json", tests, conversion) + "\nstatus=$?\necho later > later.txt\nexit \"$status\"\n"})
					labels = append(labels, shell+" "+option+" "+tests+" "+conversion)
					wants = append(wants, want)
					errexit = append(errexit, strings.HasPrefix(option, "set -e"))
				}
			}
		}
		for i, result := range runShellCases(t, shell, cases) {
			label, want := labels[i], wants[i]
			assert.Equal(t, want, result.status, "the tests' status, or the conversion's when only it fails: %s", label)
			converted, readErr := os.ReadFile(filepath.Join(result.dir, "junit.xml"))
			if assert.NoError(t, readErr, "the report is converted whatever the tests' result: %s", label) {
				assert.Equal(t, "REPORT\n", string(converted), label)
			}
			_, later := os.Stat(filepath.Join(result.dir, "later.txt"))
			if errexit[i] {
				assert.Equal(t, want == 0, later == nil, "only a passing run continues with the script: %s", label)
			} else {
				assert.NoError(t, later, "without errexit, the script continues: %s", label)
			}
		}
	}
}

// realShells is those of names on PATH; the test skips without any.
func realShells(t *testing.T, names ...string) []string {
	t.Helper()
	var shells []string
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			shells = append(shells, name)
		}
	}
	if len(shells) == 0 {
		t.Skip("no sh")
	}
	return shells
}

// shellCase is a script a real shell runs in a directory of its own, which
// holds files first.
type shellCase struct {
	script string
	files  map[string]string
}

// shellResult is the status a case's script exited with and the directory
// it ran in.
type shellResult struct {
	status int
	dir    string
}

// runShellCases runs each case's script in a fresh process of shell, in the
// case's own directory, all from one process of shell that records every
// status: a matrix of cases costs the test one process, not one per case.
func runShellCases(t *testing.T, shell string, cases []shellCase) []shellResult {
	t.Helper()
	root := t.TempDir()
	results := make([]shellResult, len(cases))
	for i, c := range cases {
		name := fmt.Sprintf("%03d", i)
		results[i].dir = filepath.Join(root, name)
		require.NoError(t, os.Mkdir(results[i].dir, 0o755))
		for path, contents := range c.files {
			require.NoError(t, os.WriteFile(filepath.Join(results[i].dir, path), []byte(contents), 0o644))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, name+".sh"), []byte(c.script), 0o644))
	}
	driver := `for script in *.sh; do name=${script%.sh}; (cd "$name" && exec "$1" "../$script"); echo "$?" > "$name.status"; done`
	command := exec.Command(shell, "-c", driver, "_", shell)
	command.Dir = root
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	for i := range results {
		data, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("%03d.status", i)))
		require.NoError(t, err, "%s", output)
		results[i].status, err = strconv.Atoi(strings.TrimSpace(string(data)))
		require.NoError(t, err)
	}
	return results
}

// The status subshell removes the intermediate report before the tests, so
// tests that fail before writing it, in a reused workspace or after another
// command that wrote it, never get an earlier run's report converted: cat
// finds no file, and the command exits with the tests' status whatever the
// converter does with no input. sh runs it with errexit on and off, bash
// with pipefail, where cat's failure is the conversion's status, and dash
// with errexit.
func TestStatusSubshellNeverConvertsAStaleReport(t *testing.T) {
	conversions := []string{
		"cat report.json | tr a-z A-Z > junit.xml",
		`cat report.json | sh -c 'input=$(cat); [ -n "$input" ] || exit 7; echo "$input" > junit.xml'`,
	}
	for _, shell := range realShells(t, "sh", "bash", "dash") {
		options := map[string][]string{"sh": {"set -e", "set +e"}, "bash": {"set -e -o pipefail", "set +e -o pipefail"}, "dash": {"set -e"}}[shell]
		var cases []shellCase
		var labels []string
		for _, option := range options {
			for _, conversion := range conversions {
				cases = append(cases, shellCase{script: option + "\n" + statusSubshell("report.json", "sh -c 'exit 4'", conversion) + "\n", files: map[string]string{"report.json": "stale\n"}})
				labels = append(labels, shell+" "+option+" "+conversion)
			}
		}
		for i, result := range runShellCases(t, shell, cases) {
			label := labels[i]
			assert.Equal(t, 4, result.status, "the tests' status: %s", label)
			_, statErr := os.Stat(filepath.Join(result.dir, "report.json"))
			assert.ErrorIs(t, statErr, fs.ErrNotExist, "the stale report is removed: %s", label)
			converted, readErr := os.ReadFile(filepath.Join(result.dir, "junit.xml"))
			if strings.Contains(label, "exit 7") {
				assert.ErrorIs(t, readErr, fs.ErrNotExist, "the converter got no input: %s", label)
			} else if assert.NoError(t, readErr, "the conversion ran after the failing tests: %s", label) {
				assert.Empty(t, string(converted), "no earlier run's report is converted: %s", label)
			}
		}
	}
}

// Every provider whose scripts stop at the first failing command reads only
// the prescribed removal before a status subshell's tests: any other, or
// one of another file, leaves the command unresolved.
func TestEveryProviderReadsOnlyThePrescribedRemoval(t *testing.T) {
	tests := "./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
	conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
	guard := ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
	for name, write := range statusSubshellProviders {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1","cucumber-junit":"1.7.1"}}`)
			t.Chdir(root)
			useCIProvider(t, name)
			write(t, root, "npm ci", statusSubshell("cucumber-report.json", tests, conversion))
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "cucumber-js ./features --format json:cucumber-report.json", scope.Commands[0].Command)

			// TestStatusSubshellReadsOnlyThePrescribedForm rejects every other
			// removal at the reader; each provider reaches it with one of
			// another file and one that Buildkite escapes.
			for _, removal := range []string{"rm -f other.json", "rm -f $REPORT"} {
				write(t, root, "npm ci", `(status=0; `+removal+`; `+tests+` || status=$?; `+conversion+guard)
				scope, err := DiscoverValidationScope(root, "cucumber")
				require.NoError(t, err)
				require.Empty(t, scope.Commands, removal)
				require.NotEmpty(t, scope.Unresolved, removal)
			}
		})
	}
}

// Every status subshell ddtest prescribes for a report pipe is one ddtest
// reads back as the same tests and conversion on the next onboard --apply,
// and one the shell parses: a pipe whose conversion the reader can't read,
// such as one through a { ...; } group or one that reads the status
// variable, keeps the general advice instead, and a ; or comment after the
// pipe stays out of the prescription.
func TestEveryReportPipePrescriptionReadsBack(t *testing.T) {
	root := t.TempDir()
	var prescriptions []string
	for pipe, prescribed := range map[string]bool{
		"cucumber-js --format json | cucumber-junit > junit.xml":                                                        true,
		"cucumber-js --format json | cucumber-junit > junit.xml;":                                                       true,
		"cucumber-js --format json | cucumber-junit > junit.xml ;  ":                                                    true,
		"cucumber-js --format json | cucumber-junit > junit.xml # convert":                                              true,
		"cucumber-js --format json | cucumber-junit > junit.xml; # convert":                                             true,
		"cucumber-js --format json |\n  cucumber-junit > junit.xml":                                                     true,
		"npx cucumber-js './features/a b' --format \"json\" | cucumber-junit --name 'My suite' > \"out dir/junit.xml\"": true,
		"cucumber-js --format json | tee raw.json | cucumber-junit > junit.xml":                                         true,
		"cucumber-js --format json | cucumber-junit 2>&1 > junit.xml":                                                   true,
		"cucumber-js --format json | FORCE_COLOR=0 cucumber-junit > junit.xml":                                          true,
		"cucumber-js --format=message | npx @cucumber/junit-xml-formatter > junit.xml":                                  false,
		"cucumber-js --format json | { cucumber-junit; }":                                                               false,
		"cucumber-js --format json | { cucumber-junit > junit.xml; } # convert":                                         false,
		"cucumber-js --format json | (cucumber-junit > junit.xml)":                                                      false,
		// The subshell's variables are ones the pipe doesn't reference.
		"cucumber-js --format json | cucumber-junit > \"$status.xml\"":                          true,
		"cucumber-js --format json | cucumber-junit --name \"$converted\"":                      true,
		"cucumber-js \"$status\" --format json | cucumber-junit":                                true,
		"cucumber-js \"$converted\" --format json | cucumber-junit":                             true,
		"cucumber-js ${status:-a} --format json | cucumber-junit --name \"$datadog_converted\"": true,
		"cucumber-js --format json | cucumber-junit | exit 0":                                   false,
		"CI=1 cucumber-js --format json | cucumber-junit":                                       false,
		"cucumber-js --format json | cucumber-junit > junit.xml; echo done":                     false,
		"cucumber-js --format json | cucumber-junit > junit.xml && echo done":                   false,
	} {
		t.Run(pipe, func(t *testing.T) {
			_, err := directScopedCommand(root, runtimeStep{Run: pipe}, TestCommand{}, "cucumber")
			require.Error(t, err)
			var split *ReportPipeSplit
			if !prescribed {
				require.False(t, errors.As(err, &split), "%v", err)
				require.Contains(t, err.Error(), "as part of a compound shell command")
				return
			}
			require.ErrorAs(t, err, &split)
			read := runtimeStep{Run: split.Command}
			read.readStatusSubshell()
			require.Len(t, read.afterTests, 1, split.Command)
			require.Contains(t, read.Run, "cucumber-js", split.Command)
			require.Equal(t, split.Command, read.statusWrapper[0]+read.Run+read.statusWrapper[1])
			original, ok := parseCIShell(pipe)
			require.True(t, ok)
			status, converted := statusVariables(read.Run, read.afterTests[0])
			require.True(t, strings.HasPrefix(split.Command, "("+status+"=0; "), split.Command)
			require.Contains(t, split.Command, "{ "+converted+"=$?; ")
			require.False(t, referencesVariable(original, status), "the pipe's $%s is not the subshell's", status)
			require.False(t, referencesVariable(original, converted), "the pipe's $%s is not the subshell's", converted)
			require.NotContains(t, split.Command, "#")
			require.NotContains(t, split.Command, ";;")
			prescriptions = append(prescriptions, split.Command)
		})
	}
	requireShellsParse(t, prescriptions, "bash", "dash")
}

// requireShellsParse checks that each shell of names on PATH parses every
// command, with one shell -n over a script holding each command as the
// body of a function of its own, where an unclosed quote or group fails the
// script.
func requireShellsParse(t *testing.T, commands []string, names ...string) {
	t.Helper()
	var script strings.Builder
	for i, command := range commands {
		fmt.Fprintf(&script, "prescription_%d() {\n%s\n}\n", i, command)
	}
	path := filepath.Join(t.TempDir(), "prescriptions.sh")
	require.NoError(t, os.WriteFile(path, []byte(script.String()), 0o644))
	for _, shell := range names {
		if _, err := exec.LookPath(shell); err != nil {
			continue
		}
		output, err := exec.Command(shell, "-n", path).CombinedOutput()
		require.NoError(t, err, "%s -n:\n%s\n%s", shell, script.String(), output)
	}
}

// Only the exact status-preserving subshell is read as tests followed by
// commands outside them; a command after the tests that may start them, a
// missing status or another exit stays unresolved.
func TestStatusSubshellReadsOnlyThePrescribedForm(t *testing.T) {
	guard := ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }`
	for run, want := range map[string][2]any{
		`(status=0; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit > j.xml` + guard + `; exit "$status")`:                                                                              {"npx cucumber-js --format json:r.json", []string{"cat r.json | cucumber-junit > j.xml"}},
		"(\n  rc=0\n  npx cucumber-js || rc=$?\n  npx junit-merge -d r -o j.xml || { saved=$?; [ \"$rc\" -ne 0 ] || rc=$saved; }\n  allure generate r || { saved=$?; [ $rc -ne 0 ] || rc=\"$saved\"; }\n  exit $rc\n)": {"npx cucumber-js", []string{"npx junit-merge -d r -o j.xml", "allure generate r"}},
		// The prescription removes the report the tests write and the conversion reads first.
		`(status=0; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit > j.xml` + guard + `; exit "$status")`:                             {"npx cucumber-js --format json:r.json", []string{"cat r.json | cucumber-junit > j.xml"}},
		`(status=0; rm -f r.json; npx cucumber-js --format=json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; npx allure generate r` + guard + `; exit "$status")`: {"npx cucumber-js --format=json:r.json", []string{"cat r.json | cucumber-junit", "npx allure generate r"}},
		// Any other removal, or one of another file, stays unresolved.
		`(status=0; rm -f other.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:           {},
		`(status=0; rm -f r.json; npx cucumber-js --format json:other.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:           {},
		`(status=0; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cat other.json | cucumber-junit` + guard + `; exit "$status")`:           {},
		`(status=0; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cucumber-junit < r.json` + guard + `; exit "$status")`:                   {},
		`(status=0; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json` + guard + `; exit "$status")`:                                {},
		`(status=0; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; exit "$status")`:                                                         {},
		`(status=0; rm -rf r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:              {},
		`(status=0; rm -rf .; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:                   {},
		`(status=0; rm r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:                  {},
		`(status=0; rm -f r.json x; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:             {},
		`(status=0; rm -f -- r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:            {},
		`(status=0; rm -f *.json; npx cucumber-js --format json:*.json || status=$?; cat *.json | cucumber-junit` + guard + `; exit "$status")`:               {},
		`(status=0; rm -f "$r"; npx cucumber-js --format json:$r || status=$?; cat "$r" | cucumber-junit` + guard + `; exit "$status")`:                       {},
		`(status=0; rm -f ../r.json; npx cucumber-js --format json:../r.json || status=$?; cat ../r.json | cucumber-junit` + guard + `; exit "$status")`:      {},
		`(status=0; rm -f /r.json; npx cucumber-js --format json:/r.json || status=$?; cat /r.json | cucumber-junit` + guard + `; exit "$status")`:            {},
		`(status=0; rm -f r.json > log; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:         {},
		`(status=0; rm -f r.json & npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:              {},
		`(status=0; F=1 rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:           {},
		`(rm -f r.json; status=0; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:               {},
		`(status=0; rm -f r.json; rm -f r.json; npx cucumber-js --format json:r.json || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`: {},
		// The round-2 form, without the guard, would let a failing conversion pass without errexit.
		`(status=0; npx cucumber-js || status=$?; cat r.json | cucumber-junit > j.xml; exit "$status")`:            {},
		`(status=0; npx cucumber-js || status=$?; cat r.json | cucumber-junit` + guard + `; exit 0)`:               {},
		`(status=1; npx cucumber-js || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:       {},
		`(status=0; npx cucumber-js || other=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:        {},
		`(status=0; npx cucumber-js && status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:       {},
		`(status=0; npx cucumber-js || status=$?; exit "$status")`:                                                 {},
		`(status=0; npx cucumber-js || status=$?; cat r.json | cucumber-junit` + guard + `; exit "${status:-1}")`:  {},
		`(status=0; npx cucumber-js || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status") > log`: {},
		`status=0; npx cucumber-js || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status"`:         {},
		// Guards that lose the tests' status, or the conversion's.
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; status=$converted; }; exit "$status")`:                                   {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; [ "$status" -eq 0 ] && status=$converted; }; exit "$status")`:            {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; [ "$status" -ne 1 ] || status=$converted; }; exit "$status")`:            {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; [ "$other" -ne 0 ] || status=$converted; }; exit "$status")`:             {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; [ "$status" -ne 0 ] || status=$other; }; exit "$status")`:                {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=1; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`:             {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { status=$?; [ "$status" -ne 0 ] || status=$status; }; exit "$status")`:                  {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || { converted=$?; [ "$status" -ne 0 ] || status=$converted; exit 0; }; exit "$status")`:    {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit || ( converted=$?; [ "$status" -ne 0 ] || status=$converted ); exit "$status")`:             {},
		`(status=0; npx cucumber-js || status=$?; cucumber-junit $converted || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`: {},
		`(status=0; npx cucumber-js || status=$?; exit 0` + guard + `; exit "$status")`:                                                                      {},
		// Tests that read either variable would get the subshell's value, not CI's.
		`(status=0; npx cucumber-js "$status" || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:           {},
		`(status=0; npx cucumber-js ${status:-1} || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:        {},
		`(status=0; npx cucumber-js $((status + 1)) || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`:     {},
		`(status=0; npx cucumber-js --name "$converted" || status=$?; cat r.json | cucumber-junit` + guard + `; exit "$status")`: {},
		// Other variable names, as ddtest picks when the pipe references status or converted.
		`(datadog_status=0; rm -f r.json; npx cucumber-js "$status" --format json:r.json || datadog_status=$?; cat r.json | cucumber-junit --name "$converted" || { datadog_converted=$?; [ "$datadog_status" -ne 0 ] || datadog_status=$datadog_converted; }; exit "$datadog_status")`: {"npx cucumber-js \"$status\" --format json:r.json", []string{"cat r.json | cucumber-junit --name \"$converted\""}},
		`(datadog_status=0; npx cucumber-js "$datadog_status" || datadog_status=$?; cat r.json | cucumber-junit || { datadog_converted=$?; [ "$datadog_status" -ne 0 ] || datadog_status=$datadog_converted; }; exit "$datadog_status")`:                                                {},
		`(datadog_status=0; npx cucumber-js "$datadog_converted" || datadog_status=$?; cat r.json | cucumber-junit || { datadog_converted=$?; [ "$datadog_status" -ne 0 ] || datadog_status=$datadog_converted; }; exit "$datadog_status")`:                                             {},
	} {
		step := runtimeStep{Run: run}
		step.readStatusSubshell()
		if want[0] == nil {
			require.Equal(t, run, step.Run, run)
			require.Empty(t, step.afterTests, run)
			continue
		}
		require.Equal(t, want[0], step.Run, run)
		require.Equal(t, want[1], step.afterTests, run)
	}
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1"},"scripts":{"report":"cucumber-js"}}`)
	writeFile(t, root, gitLabCIFile, "acceptance:\n  image: node:24\n  script:\n    - npm install\n    - (status=0; npx cucumber-js || status=$?; npm run report || { converted=$?; [ \"$status\" -ne 0 ] || status=$converted; }; exit \"$status\")\n")
	useCIProvider(t, gitLabCIProvider)
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "CI runs npm run report after the tests in the same command, and it may start cucumber itself")
}

// Only a pipe from one Cucumber formatter on standard output into commands
// that can't start tests is split; any other compound command keeps the
// general advice.
func TestCucumberReportPipeSplitsOnlyAReportConversion(t *testing.T) {
	for command, want := range map[string][2]string{
		"cucumber-js --format json | cucumber-junit > junit.xml":               {"cucumber-js --format json:cucumber-report.json", "cat cucumber-report.json | cucumber-junit > junit.xml"},
		"npx cucumber-js -f message | npx @cucumber/junit-xml-formatter":       {},
		"cucumber-js --format=junit | tee junit.xml":                           {"cucumber-js --format=junit:cucumber-report.xml", "cat cucumber-report.xml | tee junit.xml"},
		"cucumber-js --format json --format html:report.html | cucumber-junit": {"cucumber-js --format json:cucumber-report.json --format html:report.html", "cat cucumber-report.json | cucumber-junit"},
		"cucumber-js --format json --format html | cucumber-junit":             {},
		"cucumber-js --format json | node convert.js":                          {},
		"cucumber-js --format json:out.json | cucumber-junit":                  {},
		"cucumber-js --format pretty | cucumber-junit":                         {},
		"cucumber-js --format json |& cucumber-junit":                          {},
		"npm run build && cucumber-js --format json | cucumber-junit":          {},
		"cat x.json | cucumber-junit":                                          {},
	} {
		tests, conversion, report, ok := cucumberReportPipe(command, noFile)
		require.Equal(t, want[0] != "", ok, command)
		if ok {
			require.Equal(t, "cat "+report+" | ", conversion[:len("cat "+report+" | ")], command)
		}
		require.Equal(t, want, [2]string{tests, conversion}, command)
	}
}

// Every provider whose scripts stop at the first failing command reads the
// status-preserving subshell the same way: its tests are the configuration,
// its conversion a note outside the tests, and the commands after it still
// run in the same shell.
func TestEveryProviderReadsTheStatusSubshell(t *testing.T) {
	tests := "./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
	conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
	command := statusSubshell("cucumber-report.json", tests, conversion)
	require.Contains(t, command, "(status=0; rm -f cucumber-report.json; ./node_modules/.bin/cucumber-js")
	jenkinsfile := "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        sh '''\n          " + command + "\n          npx eslint .\n        '''\n      }\n    }\n  }\n}\n"
	for name, write := range map[string]func(root string){
		gitLabCIProvider: func(root string) {
			writeFile(t, root, gitLabCIFile, "test:\n  image: node:22\n  script:\n    - npm ci\n    - "+command+"\n    - npx eslint .\n")
		},
		circleCIProvider: func(root string) {
			writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - run: npm ci\n      - run: "+command+"\n      - run: npx eslint .\n"+circleWorkflow("      - test\n"))
		},
		buildkiteProvider: func(root string) {
			writeFile(t, root, buildkiteFile, "steps:\n  - label: test\n    command:\n      - npm ci\n      - "+buildkiteEscape(command)+"\n      - npx eslint .\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n")
		},
		jenkinsProvider: func(root string) { writeFile(t, root, "Jenkinsfile", jenkinsfile) },
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1","cucumber-junit":"1.7.1","eslint":"9.0.0"}}`)
			write(root)
			t.Chdir(root)
			useCIProvider(t, name)
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "cucumber-js ./features --format json:cucumber-report.json", scope.Commands[0].Command, "the tests are the configuration")
			require.Contains(t, scope.Commands[0].StepScopeNotes, "This CI step runs "+conversion+" after its cucumber tests, whether they pass or fail, then exits with the tests' status, or with the status of a command after them that fails when they pass.")
		})
	}
}

// statusSubshellProviders writes a job whose script runs commands in order,
// in each provider whose scripts stop at the first failing command, as its
// configuration holds them (Buildkite's with $$).
var statusSubshellProviders = map[string]func(t *testing.T, root string, commands ...string){
	gitLabCIProvider: func(t *testing.T, root string, commands ...string) {
		writeFile(t, root, gitLabCIFile, "test:\n  image: node:22\n  script:\n    - "+strings.Join(commands, "\n    - ")+"\n")
	},
	circleCIProvider: func(t *testing.T, root string, commands ...string) {
		writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n      - checkout\n      - run: "+strings.Join(commands, "\n      - run: ")+"\n"+circleWorkflow("      - test\n"))
	},
	buildkiteProvider: func(t *testing.T, root string, commands ...string) {
		escaped := make([]string, len(commands))
		for i, command := range commands {
			escaped[i] = buildkiteEscape(command)
		}
		writeFile(t, root, buildkiteFile, "steps:\n  - label: test\n    command:\n      - "+strings.Join(escaped, "\n      - ")+"\n    plugins:\n      - docker#v5.12.0:\n          image: node:22\n")
	},
	jenkinsProvider: func(t *testing.T, root string, commands ...string) {
		writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  stages {\n    stage('Test') {\n      steps {\n        sh '''\n          "+strings.Join(commands, "\n          ")+"\n        '''\n      }\n    }\n  }\n}\n")
	},
}

// A command between a status subshell's tests and its exit that can change
// the shell's flow, or the status it exits with, makes the subshell
// unresolved: the saved-status exit might never run, as after exit 0.
func TestStatusSubshellRejectsCommandsThatSkipTheExit(t *testing.T) {
	for _, middle := range append([]string{
		"exit 0", "return 0", "exec cucumber-junit", "break", "continue", "set +e", `\exit 0`, "'exit' 0", "eval exit 0",
		"source ./convert.sh", ". ./convert.sh", "trap 'exit 0' EXIT", "builtin exit 0", "command exit 0",
		"cucumber-junit < r.json & :", "cat r.json | cucumber-junit || true", "cucumber-junit < r.json && exit 0",
		"status=0", "status=0 cucumber-junit", "export status=0", "unset status", ": $((status=0))", "let status=0", "echo ${status:=0}",
		"(exit 0)", "{ exit 0; }", "if true; then exit 0; fi", "f() { exit 0; }", "! cucumber-junit", "$CONVERT r.json",
	}, statusUnsafeBuiltins...) {
		t.Run(middle, func(t *testing.T) {
			run := `(status=0; npx cucumber-js || status=$?; ` + middle + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
			step := runtimeStep{Run: run}
			step.readStatusSubshell()
			require.Equal(t, run, step.Run)
			require.Empty(t, step.afterTests)
		})
	}
	for _, middle := range []string{"cat r.json | cucumber-junit > j.xml", "cucumber-junit < r.json > j.xml 2>&1", "status-report r.json", "echo $other", ": done", "test -f j.xml", "sort -o status r.txt", "/usr/bin/printf -v", "/usr/bin/printf '%n' x"} {
		step := runtimeStep{Run: `(status=0; npx cucumber-js || status=$?; ` + middle + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`}
		step.readStatusSubshell()
		require.Equal(t, []string{middle}, step.afterTests, middle)
	}
	for name, write := range statusSubshellProviders {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1"}}`)
			write(t, root, "npm ci", `(status=0; ./node_modules/.bin/cucumber-js || status=$?; exit 0; exit "$status")`)
			t.Chdir(root)
			useCIProvider(t, name)
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Commands, "an exit 0 before the saved status masks failing tests")
			require.NotEmpty(t, scope.Unresolved)

			// The reader rejects every unsafe builtin above; each provider
			// reaches it with one that quotes, and one that Buildkite escapes.
			for _, middle := range []string{"printf '%n' status", "[[ -n $((status=0)) ]]"} {
				require.Contains(t, statusUnsafeBuiltins, middle)
				write(t, root, "npm ci", `(status=0; ./node_modules/.bin/cucumber-js || status=$?; `+middle+` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`)
				scope, err := DiscoverValidationScope(root, "cucumber")
				require.NoError(t, err)
				require.Empty(t, scope.Commands, "%s overwrites the saved status", middle)
				require.NotEmpty(t, scope.Unresolved, middle)
			}
		})
	}
}

// Builtins that write a shell variable named by an argument, here the saved
// status, so the subshell's exit would mask failing tests, or change how the
// shell runs later commands; no builtin but those that only print or test
// may run after the tests. bash's printf writes one through -v or a %n in
// its format, so no printf builtin qualifies, even one that only prints;
// /usr/bin/printf, a separate process, can't write the shell's variables.
var statusUnsafeBuiltins = []string{
	"printf '%n' status", "printf 'x'", "printf -v status 0", "printf -vstatus 0", "printf -- -v", `printf "$format" 0`, "read status <<<0", "read -r status < r.txt",
	"mapfile status < r.txt", "readarray -t status < r.txt", "getopts a status", "wait -p status", "compgen -V status -W 0",
	"hash -p /bin/true cucumber-junit", "[[ -n $((status=0)) ]]",
}

// statusSubshellAfter is a status subshell whose tests are followed by
// middle, with the guard that keeps their status.
func statusSubshellAfter(tests, middle string) string {
	return `(status=0; ` + tests + ` || status=$?; ` + middle + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
}

// test and [ after the tests qualify only with operands bash never
// evaluates as arithmetic: a file test, -n or -z on a literal, = or !=
// between literals, or a comparison of literal integers. Any other operand
// set can assign the saved status through an array subscript, as test -v
// 'BASH_VERSINFO[status=0]' does, or is a combination the reader doesn't
// need, so the subshell stays unresolved. The guard's own [ "$status" -ne
// 0 ] is a separate shape that keeps being read.
var (
	statusUnsafeTests = []string{
		"test -v 'BASH_VERSINFO[status=0]'", "[ -v 'a[status=0]' ]", "test -f 'x[status=0]'", `[ -n "$x" ]`,
		"test 1 -eq 'a[status=0]'", "[ 'a[status=0]' -ne 0 ]", "test -v status", "test -f j.xml -o -v 'a[status=0]'", "[ -f j.xml -a -s j.xml ]",
		"[ ! -f j.xml ]", "test j.xml", "test", "[ ]", "[ -f j.xml", "test -f j.xml ]", "test -f '$x'", `test -f x\[1\]`, "test -f j*.xml", "test -t 1",
		"test a == b", "test 1x -eq 1", "[[ -f j.xml ]]",
	}
	statusSafeTests = []string{
		"test -f junit.xml", "[ -s report.xml ]", "test -e j.xml", "[ -d reports ]", "test -L j.xml", "test -n x", "[ -z '' ]",
		"test a = b", `[ "a b" != c ]`, "test 1 -eq 1", "[ -1 -lt 2 ]", "test 0 -ge 0",
	}
)

func TestStatusSubshellAcceptsOnlyPlainTests(t *testing.T) {
	for _, middle := range statusUnsafeTests {
		run := statusSubshellAfter("npx cucumber-js", middle)
		step := runtimeStep{Run: run}
		step.readStatusSubshell()
		require.Equal(t, run, step.Run, middle)
		require.Empty(t, step.afterTests, middle)
	}
	for _, middle := range statusSafeTests {
		step := runtimeStep{Run: statusSubshellAfter("npx cucumber-js", middle)}
		step.readStatusSubshell()
		require.Equal(t, "npx cucumber-js", step.Run, middle)
		require.Equal(t, []string{middle}, step.afterTests, middle)
	}
	// The guard's own test still reads, after a file test.
	step := runtimeStep{Run: `(status=0; npx cucumber-js || status=$?; [ -s j.xml ] || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; cat j.xml || { converted=$?; [ $status -ne 0 ] || status="$converted"; }; exit "$status")`}
	step.readStatusSubshell()
	require.Equal(t, []string{"[ -s j.xml ]", "cat j.xml"}, step.afterTests)

	// Every provider reaches the reader's operand check: two rejected
	// forms, one per program, and one accepted.
	for name, write := range statusSubshellProviders {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1"}}`)
			t.Chdir(root)
			useCIProvider(t, name)
			for _, middle := range []string{"test -v 'BASH_VERSINFO[status=0]'", "[ -v 'a[status=0]' ]"} {
				require.Contains(t, statusUnsafeTests, middle)
				write(t, root, "npm ci", statusSubshellAfter("./node_modules/.bin/cucumber-js", middle))
				scope, err := DiscoverValidationScope(root, "cucumber")
				require.NoError(t, err)
				require.Empty(t, scope.Commands, "%s overwrites the saved status", middle)
				require.NotEmpty(t, scope.Unresolved, middle)
			}
			write(t, root, "npm ci", statusSubshellAfter("./node_modules/.bin/cucumber-js", "test -f junit.xml"))
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Contains(t, scope.Commands[0].StepScopeNotes, "This CI step runs test -f junit.xml after its cucumber tests, whether they pass or fail, then exits with the tests' status, or with the status of a command after them that fails when they pass.")
		})
	}
}

// In a real bash, test -v 'BASH_VERSINFO[status=0]' after failing tests
// resets the saved status through its subscript, so the subshell exits 0,
// with errexit and pipefail on, which is why the reader rejects it; a file
// test, which it accepts, keeps the tests' status.
func TestStatusSubshellTestVariableMasksFailingTestsInBash(t *testing.T) {
	realShells(t, "bash")
	if exec.Command("bash", "-c", "x=1; test -v x").Run() != nil {
		t.Skip("this bash has no test -v")
	}
	masking := statusSubshellAfter("(exit 3)", "test -v 'BASH_VERSINFO[status=0]'")
	plain := statusSubshellAfter("(exit 3)", "test -f junit.xml")
	results := runShellCases(t, "bash", []shellCase{
		{script: "set -e -o pipefail\n" + masking + "\n"},
		{script: "set -e -o pipefail\n" + plain + "\n", files: map[string]string{"junit.xml": "<testsuite/>"}},
	})
	require.Equal(t, 0, results[0].status, "the failing tests' status is masked")
	require.Equal(t, 3, results[1].status, "a file test keeps the tests' status")
	for middle, read := range map[string]bool{"test -v 'BASH_VERSINFO[status=0]'": false, "test -f junit.xml": true} {
		step := runtimeStep{Run: statusSubshellAfter("npx cucumber-js", middle)}
		step.readStatusSubshell()
		require.Equal(t, read, len(step.afterTests) > 0, middle)
	}
}

// The commands after a status subshell's tests are read as the same
// commands after the tests in a plain sequence: a file change stays setup
// for a later test step, and a command ddtest can't resolve is outside the
// tests only until a later test follows it.
func TestStatusSubshellTailsPrepareLaterTests(t *testing.T) {
	for name, write := range statusSubshellProviders {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1","cucumber-junit":"1.7.1"}}`)
			write(t, root, "npm ci", `(status=0; ./node_modules/.bin/cucumber-js features/a || status=$?; cp ci.env .env || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`, "./node_modules/.bin/cucumber-js features/b")
			t.Chdir(root)
			useCIProvider(t, name)
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "CI file or dependency changes require separate validation: cp ci.env .env", "the later tests read the file it changes")

			write(t, root, "npm ci", `(status=0; ./node_modules/.bin/cucumber-js features/a || status=$?; ./node_modules/.bin/cucumber-junit < r.json > j.xml || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`, "./node_modules/.bin/cucumber-js features/b")
			scope, err = DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "but before a later cucumber test step, which it may prepare")
		})
	}
}

// An existing status subshell whose tests still need the scoped prefix and
// NYC gets the whole subshell back with only its tests edited, so following
// the prescription keeps the conversion and the exit with the tests' status;
// onboard --apply then accepts it.
func TestProviderEditsTheTestsInsideAStatusSubshell(t *testing.T) {
	for _, provider := range scopedProviders {
		t.Run(provider.name, func(t *testing.T) {
			framework := scopedFrameworks[0]
			conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
			original := statusSubshell("cucumber-report.json", "./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json", conversion)
			escape := func(command string) string { return command }
			if provider.name == buildkiteProvider {
				escape = buildkiteEscape // As the pipeline holds it.
			}
			root := scopedRepository(t, provider, provider.config("npm ci", escape(original)), framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			_, after, ok := strings.Cut(gap.Reason, "Change this CI command to exactly: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". Then rerun onboard --apply")
			require.True(t, ok, gap.Reason)
			want := statusSubshell("cucumber-report.json", cucumberScopedOptions+" "+cucumberImpactedWorkaround+" npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json", conversion)
			require.Equal(t, escape(want), command)

			writeFile(t, root, provider.file, provider.config("npm ci", command))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, provider.file), command)
		})
	}
}

// jenkinsContainerTestStage is a Jenkinsfile whose Test stage runs in a
// container, where onboard --apply sets the tracer up with the setup
// snippet, with npm ci and then script, an sh step's argument as written.
func jenkinsContainerTestStage(script string) string {
	return "pipeline {\n  agent { docker { image 'node:22' } }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        sh " + script + "\n      }\n    }\n  }\n}\n"
}

// jenkinsWritten is the shell text of the sh step a Jenkinsfile runs last,
// as Groovy reads it without its surrounding blank space, with each ${…}
// it interpolates written back as ${…}, and the expressions it
// interpolates.
func jenkinsWritten(t *testing.T, jenkinsfile string) (string, []string) {
	t.Helper()
	tokens, err := lexGroovy(jenkinsfile)
	require.NoError(t, err)
	var text string
	var interpolated []string
	for _, token := range tokens {
		if token.str == nil {
			continue
		}
		var written strings.Builder
		interpolated = nil
		for _, part := range token.str.parts {
			if part.interpolated {
				written.WriteString("${" + part.expr + "}")
				interpolated = append(interpolated, part.expr)
				continue
			}
			written.WriteString(part.text)
		}
		text = strings.TrimSpace(written.String())
	}
	return text, interpolated
}

// jenkinsLiterals are the four string literals an sh step's script can be
// written in, each holding a script whose first command reads SUITE, which
// the pipeline's environment sets (see jenkinsSuiteStage), as Groovy (in a
// GString) or the shell (in a single-quoted string) expands it, then a line
// break or ; and the shell command it is given, escaped as that literal
// needs; with what Groovy hands the shell before that command and the
// expressions it interpolates.
var jenkinsLiterals = []struct {
	name, first  string
	interpolated []string
	script       func(command string) string
}{
	{"double-quoted", "echo ${env.SUITE}; ", []string{"env.SUITE"}, func(command string) string {
		return `"echo ${env.SUITE}; ` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`).Replace(command) + `"`
	}},
	{"triple-double-quoted", "echo ${env.SUITE}\n          ", []string{"env.SUITE"}, func(command string) string {
		return "\"\"\"\n          echo ${env.SUITE}\n          " + strings.NewReplacer(`\`, `\\`, "$", `\$`).Replace(command) + "\n        \"\"\""
	}},
	{"single-quoted", `echo "$SUITE"; `, nil, func(command string) string {
		return `'echo "$SUITE"; ` + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(command) + "'"
	}},
	{"triple-single-quoted", "echo \"$SUITE\"\n          ", nil, func(command string) string {
		return "'''\n          echo \"$SUITE\"\n          " + strings.ReplaceAll(command, `\`, `\\`) + "\n        '''"
	}},
}

// jenkinsSuiteStage is jenkinsContainerTestStage with SUITE set in the
// pipeline's environment, which ddtest resolves where a GString
// interpolates it.
func jenkinsSuiteStage(script string) string {
	return strings.Replace(jenkinsContainerTestStage(script), "  stages {", "  environment { SUITE = 'smoke' }\n  stages {", 1)
}

// requireJenkinsKeepsBuildNumber checks that, with the build number in
// place of SUITE, which only the Jenkins build knows and ddtest so reads in
// no script, the edited literal still hands the shell want after the first
// command, with the build number still interpolated in a GString.
func requireJenkinsKeepsBuildNumber(t *testing.T, applied, first string, interpolated []string, want string) {
	t.Helper()
	built := strings.NewReplacer("env.SUITE", "env.BUILD_NUMBER", "$SUITE", "$BUILD_NUMBER").Replace(applied)
	written, got := jenkinsWritten(t, built)
	require.Equal(t, strings.NewReplacer("env.SUITE", "env.BUILD_NUMBER", "$SUITE", "$BUILD_NUMBER").Replace(first)+want, written)
	var expected []string
	for _, expr := range interpolated {
		expected = append(expected, strings.Replace(expr, "SUITE", "BUILD_NUMBER", 1))
	}
	require.Equal(t, expected, got, "Groovy still interpolates the build number")
}

// jenkinsReplaceCommand replaces, in a Jenkinsfile whose test step's script
// is script(old), the old command as that literal holds it with command as
// ddtest prescribes it, leaving the rest of the literal as it is: what a
// user following the prescription does.
func jenkinsReplaceCommand(t *testing.T, jenkinsfile string, script func(string) string, old, command string) string {
	t.Helper()
	before, empty := script(old), script("")
	// The literal without the command shares its start and end with the
	// literal with it: the command is what sits between them.
	at := 0
	for at < len(empty) && before[at] == empty[at] {
		at++
	}
	edited := before[:at] + command + before[len(before)-(len(empty)-at):]
	require.Contains(t, jenkinsfile, before)
	return strings.Replace(jenkinsfile, before, edited, 1)
}

// In a container stage, the snippet's stage, a direct Cucumber invocation
// piped into a report converter gets its replacement escaped for the
// string literal its sh step already uses, so replacing only the old
// command inside it hands the shell the exact status subshell while the
// script's other commands, and what a GString interpolates in them, such
// as ${env.BUILD_NUMBER}, stay as they are: switching a GString to single
// quotes would hand the shell ${env.BUILD_NUMBER}, a bad substitution.
// Quotes in the command are kept, and ddtest reads the result as the tests
// and the conversion after them. A host stage gets the plugin, which
// reaches the command without any change.
func TestJenkinsReportPipePrescriptionKeepsTheStepsGroovyLiteral(t *testing.T) {
	framework := scopedFrameworks[0]
	pipe := `./node_modules/.bin/cucumber-js ./features --format json | ./node_modules/.bin/cucumber-junit --name 'My suite' > junit.xml`
	tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
	conversion := `cat cucumber-report.json | ./node_modules/.bin/cucumber-junit --name 'My suite' > junit.xml`
	want := statusSubshell("cucumber-report.json", tests, conversion)
	for _, literal := range jenkinsLiterals {
		t.Run(literal.name, func(t *testing.T) {
			jenkinsfile := jenkinsSuiteStage(literal.script(pipe))
			written, interpolated := jenkinsWritten(t, jenkinsfile)
			require.Equal(t, literal.first+pipe, written)
			require.Equal(t, literal.interpolated, interpolated)
			root := scopedRepository(t, scopedProvider{name: jenkinsProvider, file: "Jenkinsfile"}, jenkinsfile, framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			require.NotContains(t, gap.Reason, "replace that string with")
			require.Contains(t, gap.Reason, "replace only the old command inside that string")
			_, after, ok := strings.Cut(gap.Reason, "or the conversion's when only the conversion fails: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". Then rerun onboard --apply")
			require.True(t, ok, gap.Reason)

			applied := jenkinsReplaceCommand(t, jenkinsfile, literal.script, pipe, command)
			require.Equal(t, jenkinsSuiteStage(literal.script(want)), applied)
			written, interpolated = jenkinsWritten(t, applied)
			require.Equal(t, literal.first+want, written, "Groovy passes the shell the exact command")
			require.Equal(t, literal.interpolated, interpolated, "the script's other expressions stay interpolated")
			requireJenkinsKeepsBuildNumber(t, applied, literal.first, literal.interpolated, want)
			writeFile(t, root, "Jenkinsfile", applied)
			requireJenkinsReadsTheStatusSubshell(t, root, tests, conversion)
		})
	}

	host := strings.Replace(jenkinsContainerTestStage(groovyQuote(pipe, "'")), "agent { docker { image 'node:22' } }", "agent any", 1)
	root := scopedRepository(t, scopedProvider{name: jenkinsProvider, file: "Jenkinsfile"}, host, framework.pkg, nil)
	_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err, "the plugin needs no change to the command")
}

// The tests inside an existing status subshell in a container stage get
// their scoped command escaped for the string literal of its sh step,
// which ddtest reads back as the same tests and conversion, with the
// script's other expressions still interpolated.
func TestJenkinsStatusSubshellEditKeepsTheStepsGroovyLiteral(t *testing.T) {
	framework := scopedFrameworks[0]
	conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
	original := statusSubshell("cucumber-report.json", "./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json", conversion)
	tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
	want := statusSubshell("cucumber-report.json", tests, conversion)
	for _, literal := range jenkinsLiterals {
		t.Run(literal.name, func(t *testing.T) {
			jenkinsfile := jenkinsSuiteStage(literal.script(original))
			written, _ := jenkinsWritten(t, jenkinsfile)
			require.Equal(t, literal.first+original, written)
			root := scopedRepository(t, scopedProvider{name: jenkinsProvider, file: "Jenkinsfile"}, jenkinsfile, framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			require.NotContains(t, gap.Reason, "replace that string with")
			_, after, ok := strings.Cut(gap.Reason, "Change this CI command to exactly: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". This is the command as the sh step's ")
			require.True(t, ok, gap.Reason)

			applied := jenkinsReplaceCommand(t, jenkinsfile, literal.script, original, command)
			require.Equal(t, jenkinsSuiteStage(literal.script(want)), applied)
			written, interpolated := jenkinsWritten(t, applied)
			require.Equal(t, literal.first+want, written)
			require.Equal(t, literal.interpolated, interpolated, "the script's other expressions stay interpolated")
			requireJenkinsKeepsBuildNumber(t, applied, literal.first, literal.interpolated, want)
			writeFile(t, root, "Jenkinsfile", applied)
			requireJenkinsReadsTheStatusSubshell(t, root, tests, conversion)
		})
	}
}

// requireJenkinsReadsTheStatusSubshell checks that the Jenkinsfile's test
// step is read as the status subshell of tests and conversion, and that
// onboard --apply asks for no further change to its command.
func requireJenkinsReadsTheStatusSubshell(t *testing.T, root, tests, conversion string) {
	t.Helper()
	scope, err := DiscoverValidationScope(root, "cucumber")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 1)
	_, configuration, _ := strings.Cut(tests, "npx --no-install ")
	require.Equal(t, configuration, scope.Commands[0].Command, "the tests are the configuration")
	require.Contains(t, scope.Commands[0].StepScopeNotes, "This CI step runs "+conversion+" after its cucumber tests, whether they pass or fail, then exits with the tests' status, or with the status of a command after them that fails when they pass.")
	_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	if err != nil {
		require.NotContains(t, err.Error(), "CI command", "the command needs no further change")
	}
}

// A command escaped for each Groovy string literal is the exact command
// Groovy hands the shell, with backslashes, $, quotes, runs of three quotes
// and line breaks, while a ${…} the literal interpolates before it stays
// interpolated; a script that is not a literal gets the command as it is,
// and other providers but Buildkite get it unchanged.
func TestJenkinsCommandRoundTripsThroughEachGroovyLiteral(t *testing.T) {
	commands := []string{`echo "$a" 'b'`, `printf '%s\n' x`, `echo '''`, `echo ''''`, `echo """`, `echo """"`, `echo \\`, `exit "$status"`, `echo "a"`, `echo 'a'`, "echo a\necho b", `echo ${NODE_OPTIONS} $?`}
	for _, quote := range []string{`"`, `"""`, "'", "'''"} {
		for _, command := range commands {
			written, note := jenkinsCommand(quote, command)
			require.Contains(t, note, "replace only the old command inside that string")
			prefix := "echo ${env.BUILD_NUMBER}; "
			var interpolated []string
			if strings.HasPrefix(quote, `"`) {
				interpolated = []string{"env.BUILD_NUMBER"}
			}
			text, got := jenkinsWritten(t, "sh "+quote+prefix+written+quote+"\n")
			require.Equal(t, prefix+command, text, "%s %s", quote, written)
			require.Equal(t, interpolated, got, "%s %s", quote, written)
		}
	}
	written, note := jenkinsCommand("", `echo "$a"`)
	require.Equal(t, `echo "$a"`, written)
	require.Contains(t, note, "not a Groovy string literal")
	written, note = providerCommand(gitLabCI{}, runtimeStep{}, `echo "$a"`)
	require.Equal(t, `echo "$a"`, written)
	require.Empty(t, note)
	written, _ = providerCommand(jenkinsContainer{}, runtimeStep{origin: jenkinsShellStep{quote: `"`}}, `echo "$a"`)
	require.Equal(t, `echo \"\$a\"`, written)
}

// The intermediate report is a file the pipeline names nowhere, so the
// conversion never writes to the file it reads; a quoted formatter option
// is replaced by its decoded text, quoted only when it needs to be.
func TestCucumberReportPipeChoosesAFreshReportAndDecodesTheOption(t *testing.T) {
	for command, want := range map[string][2]string{
		"cucumber-js --format junit | tee cucumber-report.xml":                                          {"cucumber-js --format junit:cucumber-report-1.xml", "cat cucumber-report-1.xml | tee cucumber-report.xml"},
		"cucumber-js --format json | cucumber-junit > cucumber-report.json":                             {"cucumber-js --format json:cucumber-report-1.json", "cat cucumber-report-1.json | cucumber-junit > cucumber-report.json"},
		"cucumber-js --format json | cucumber-junit >> ./cucumber-report-1.json > cucumber-report.json": {"cucumber-js --format json:cucumber-report-2.json", "cat cucumber-report-2.json | cucumber-junit >> ./cucumber-report-1.json > cucumber-report.json"},
		"cucumber-js -f message | cucumber-junit -o cucumber-report.ndjson":                             {"cucumber-js -f message:cucumber-report-1.ndjson", "cat cucumber-report-1.ndjson | cucumber-junit -o cucumber-report.ndjson"},
		"cucumber-js -f message | cucumber-junit --output=cucumber-report.ndjson":                       {"cucumber-js -f message:cucumber-report-1.ndjson", "cat cucumber-report-1.ndjson | cucumber-junit --output=cucumber-report.ndjson"},
		"cucumber-js '--format=json' | cucumber-junit":                                                  {"cucumber-js --format=json:cucumber-report.json", "cat cucumber-report.json | cucumber-junit"},
		`cucumber-js "--format=json" | cucumber-junit`:                                                  {"cucumber-js --format=json:cucumber-report.json", "cat cucumber-report.json | cucumber-junit"},
		"cucumber-js --format 'json' | cucumber-junit":                                                  {"cucumber-js --format json:cucumber-report.json", "cat cucumber-report.json | cucumber-junit"},
	} {
		t.Run(command, func(t *testing.T) {
			tests, conversion, report, ok := cucumberReportPipe(command, noFile)
			require.True(t, ok)
			require.Equal(t, want, [2]string{tests, conversion})
			_, parsed := parseCIShell(statusSubshell(report, tests, conversion))
			require.True(t, parsed)
		})
	}
	// The report's name is the same on every provider; Buildkite's is the
	// pipeline that holds the command escaped (see
	// TestProviderSplitsACucumberReportPipe for the others).
	for _, provider := range scopedProvidersNamed(buildkiteProvider) {
		t.Run(provider.name, func(t *testing.T) {
			framework := scopedFrameworks[0]
			pipe := "./node_modules/.bin/cucumber-js ./features '--format=json' | ./node_modules/.bin/cucumber-junit > cucumber-report.json"
			root := scopedRepository(t, provider, provider.config("npm ci", pipe), framework.pkg, nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			_, after, ok := strings.Cut(gap.Reason, "or the conversion's when only the conversion fails: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". Then rerun onboard --apply")
			require.True(t, ok, gap.Reason)
			tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format=json:cucumber-report-1.json"
			want := statusSubshell("cucumber-report-1.json", tests, "cat cucumber-report-1.json | ./node_modules/.bin/cucumber-junit > cucumber-report.json")
			if provider.name == buildkiteProvider {
				want = buildkiteEscape(want) // As the pipeline holds it.
			}
			require.Equal(t, want, command)

			writeFile(t, root, provider.file, provider.config("npm ci", command))
			_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
		})
	}
}

// noFile is a directory with no file of any name.
func noFile(string) bool { return false }

// The intermediate report also names no file of the step's directory, such
// as a tracked fixture the tests would overwrite, and onboard --apply accepts
// the prescription that skips it.
func TestCucumberReportPipeSkipsTheStepDirectorysFiles(t *testing.T) {
	// Every provider reads the step directory the same way, here the
	// repository root (see TestReportNameTakenReadsTheStepDirectory for
	// another).
	for _, provider := range scopedProvidersNamed(circleCIProvider) {
		t.Run(provider.name, func(t *testing.T) {
			framework := scopedFrameworks[0]
			pipe := "./node_modules/.bin/cucumber-js ./features --format json | ./node_modules/.bin/cucumber-junit > junit.xml"
			root := scopedRepository(t, provider, provider.config("npm ci", pipe), framework.pkg, map[string]string{"cucumber-report.json": "{}\n", "cucumber-report-1.json": "{}\n"})
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			_, after, ok := strings.Cut(gap.Reason, "or the conversion's when only the conversion fails: ")
			require.True(t, ok, gap.Reason)
			command, _, ok := strings.Cut(after, ". Then rerun onboard --apply")
			require.True(t, ok, gap.Reason)
			tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report-2.json"
			want := statusSubshell("cucumber-report-2.json", tests, "cat cucumber-report-2.json | ./node_modules/.bin/cucumber-junit > junit.xml")
			if provider.name == buildkiteProvider {
				want = buildkiteEscape(want) // As the pipeline holds it.
			}
			require.Equal(t, want, command)

			writeFile(t, root, provider.file, provider.config("npm ci", command))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.Contains(t, plannedFile(t, plan, provider.file), command)
		})
	}
}

// A report name is taken by a file of the step's directory, or a path git
// tracks there that the checkout lacks, and by no file elsewhere.
func TestReportNameTakenReadsTheStepDirectory(t *testing.T) {
	root := t.TempDir()
	output, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(output))
	writeFile(t, root, "cucumber-report.json", "{}\n")
	writeFile(t, root, "packages/a/cucumber-report.xml", "<x/>\n")
	writeFile(t, root, "packages/a/cucumber-report.html", "<x/>\n")
	output, err = exec.Command("git", "-C", root, "add", "packages/a/cucumber-report.html").CombinedOutput()
	require.NoError(t, err, string(output))
	require.NoError(t, os.Remove(filepath.Join(root, "packages/a/cucumber-report.html")))

	taken := reportNameTaken(root, ".")
	require.True(t, taken("cucumber-report.json"))
	require.False(t, taken("cucumber-report.xml"))
	taken = reportNameTaken(root, "packages/a")
	require.False(t, taken("cucumber-report.json"), "the root's file is not the step's")
	require.True(t, taken("cucumber-report.xml"))
	require.True(t, taken("cucumber-report.html"), "tracked, though missing from the checkout")
}

// The status subshell records the statuses in variables neither its tests
// nor its conversion reference: status and converted, else datadog_status
// and datadog_converted, then those with a numeric suffix.
func TestStatusVariablesAvoidThePipesVariables(t *testing.T) {
	for commands, want := range map[[2]string][2]string{
		{"cucumber-js --format json:r.json", "cat r.json | cucumber-junit"}:                                {"status", "converted"},
		{"cucumber-js status --format json:r.json", "cat r.json | cucumber-junit > converted.xml"}:         {"status", "converted"},
		{`cucumber-js "$status" --format json:r.json`, "cat r.json | cucumber-junit"}:                      {"datadog_status", "datadog_converted"},
		{"cucumber-js --format json:r.json", `cat r.json | cucumber-junit --name "${converted}"`}:          {"datadog_status", "datadog_converted"},
		{"cucumber-js $((status + 1)) --format json:r.json", "cat r.json | cucumber-junit"}:                {"datadog_status", "datadog_converted"},
		{"cucumber-js --format json:r.json", "cat r.json | status=1 cucumber-junit"}:                       {"datadog_status", "datadog_converted"},
		{`cucumber-js "$status" "$datadog_converted" --format json:r.json`, "cat r.json | cucumber-junit"}: {"datadog_status_2", "datadog_converted_2"},
		{`cucumber-js "$status" "$datadog_status_2"`, `cat r.json | cucumber-junit "$datadog_status"`}:     {"datadog_status_3", "datadog_converted_3"},
	} {
		status, converted := statusVariables(commands[0], commands[1])
		require.Equal(t, want, [2]string{status, converted}, commands)
		command := statusSubshell("r.json", commands[0], commands[1])
		require.True(t, strings.HasPrefix(command, "("+want[0]+"=0; rm -f r.json; "), command)
		require.True(t, strings.HasSuffix(command, ` || { `+want[1]+`=$?; [ "$`+want[0]+`" -ne 0 ] || `+want[0]+`=$`+want[1]+`; }; exit "$`+want[0]+`")`), command)
	}
}

// Tests that read the job's status variable keep reading it inside the
// status subshell ddtest writes for them, which records into other
// variables, on every provider whose scripts stop at the first failing
// command (Buildkite's with $$): onboarding reads its tests with the job's
// value. In a subshell that records into status itself, the tests would
// read the subshell's 0 instead, and the command stays unresolved.
func TestEveryProviderReadsTheJobsStatusVariableInsideTheSubshell(t *testing.T) {
	tests := `./node_modules/.bin/cucumber-js ./features --tags "$status" --format json:cucumber-report.json`
	conversion := "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"
	shadowed := `(status=0; rm -f cucumber-report.json; ` + tests + ` || status=$?; ` + conversion + ` || { converted=$?; [ "$status" -ne 0 ] || status=$converted; }; exit "$status")`
	command := statusSubshell("cucumber-report.json", tests, conversion)
	require.Equal(t, `(datadog_status=0; rm -f cucumber-report.json; `+tests+` || datadog_status=$?; `+conversion+` || { datadog_converted=$?; [ "$datadog_status" -ne 0 ] || datadog_status=$datadog_converted; }; exit "$datadog_status")`, command)
	for name, write := range map[string]func(root, command string){
		gitLabCIProvider: func(root, command string) {
			writeFile(t, root, gitLabCIFile, "test:\n  image: node:22\n  variables:\n    status: smoke\n  script:\n    - npm ci\n    - "+command+"\n")
		},
		circleCIProvider: func(root, command string) {
			writeFile(t, root, circleCIFile, "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    environment:\n      status: smoke\n    steps:\n      - checkout\n      - run: npm ci\n      - run: "+command+"\n"+circleWorkflow("      - test\n"))
		},
		buildkiteProvider: func(root, command string) {
			writeFile(t, root, buildkiteFile, "steps:\n  - label: test\n    env:\n      status: smoke\n    command:\n      - npm ci\n      - "+buildkiteEscape(command)+"\n")
		},
		jenkinsProvider: func(root, command string) {
			writeFile(t, root, "Jenkinsfile", "pipeline {\n  agent any\n  environment {\n    status = 'smoke'\n  }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        sh '''\n          "+command+"\n        '''\n      }\n    }\n  }\n}\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "package.json", `{"devDependencies":{"@cucumber/cucumber":"11.0.1","cucumber-junit":"1.7.1"}}`)
			t.Chdir(root)
			useCIProvider(t, name)
			write(root, command)
			scope, err := DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Unresolved)
			require.Len(t, scope.Commands, 1)
			require.Equal(t, "cucumber-js ./features --tags smoke --format json:cucumber-report.json", scope.Commands[0].Command, "the job's status")
			require.Contains(t, scope.Commands[0].StepScopeNotes, "This CI step runs "+conversion+" after its cucumber tests, whether they pass or fail, then exits with the tests' status, or with the status of a command after them that fails when they pass.")

			write(root, shadowed)
			scope, err = DiscoverValidationScope(root, "cucumber")
			require.NoError(t, err)
			require.Empty(t, scope.Commands, "the tests would read the subshell's status")
			require.NotEmpty(t, scope.Unresolved)
		})
	}
}

// In a real shell, the tests and the conversion get the values CI gave
// status and converted, whatever the subshell records, and the command
// still exits with the tests' status, or the conversion's when only it
// fails: in sh with errexit on and off, and in bash and dash with errexit.
func TestStatusSubshellKeepsTheShellsStatusVariable(t *testing.T) {
	conversion := `cat report.json | sh -c 'cat > junit.xml; echo "conversion $1" >> junit.xml; exit 5' _ "$converted"`
	for _, shell := range realShells(t, "sh", "bash", "dash") {
		var cases []shellCase
		var labels []string
		var wants []int
		options := []string{"set -e"}
		if shell == "sh" {
			options = append(options, "set +e")
		}
		for _, options := range options {
			for tests, want := range map[string]int{
				`sh -c 'echo "tests $1" > report.json' _ "$status"`:         5,
				`sh -c 'echo "tests $1" > report.json; exit 3' _ "$status"`: 3,
			} {
				command := statusSubshell("report.json", tests, conversion)
				require.True(t, strings.HasPrefix(command, "(datadog_status=0; "), command)
				cases = append(cases, shellCase{script: options + "\nstatus=ci-status\nconverted=ci-converted\n" + command + "\n"})
				labels = append(labels, shell+" "+options+" "+tests)
				wants = append(wants, want)
			}
		}
		for i, result := range runShellCases(t, shell, cases) {
			assert.Equal(t, wants[i], result.status, labels[i])
			junit, err := os.ReadFile(filepath.Join(result.dir, "junit.xml"))
			if assert.NoError(t, err, labels[i]) {
				assert.Equal(t, "tests ci-status\nconversion ci-converted\n", string(junit), labels[i])
			}
		}
	}
}

func TestJenkinsScopedContainerCommandCompletesSetup(t *testing.T) {
	tests := cucumberScopedOptions + " " + cucumberImpactedWorkaround + " npx --no-install nyc -- ./node_modules/.bin/cucumber-js ./features --format json:cucumber-report.json"
	for name, command := range map[string]string{
		"direct":      tests,
		"report pipe": statusSubshell("cucumber-report.json", tests, "cat cucumber-report.json | ./node_modules/.bin/cucumber-junit > junit.xml"),
	} {
		t.Run(name, func(t *testing.T) {
			root := scopedRepository(t, scopedProvider{name: jenkinsProvider, file: "Jenkinsfile"}, jenkinsSuiteStage(jenkinsLiterals[3].script(command)), scopedFrameworks[0].pkg, nil)
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			changes, err := plan.Apply(true)
			require.NoError(t, err)
			require.NotEmpty(t, changes)
			plan, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			changes, err = plan.Apply(true)
			require.NoError(t, err)
			require.Empty(t, changes)
		})
	}
}

func TestJenkinsSetupInstructionsKeepTheStepsGroovyLiteral(t *testing.T) {
	for _, image := range []string{"node:22", "node:22-alpine"} {
		for _, literal := range jenkinsLiterals {
			t.Run(image+"/"+literal.name, func(t *testing.T) {
				command := "./node_modules/.bin/cucumber-js ./features"
				config := strings.Replace(jenkinsSuiteStage(literal.script(command)), "node:22", image, 1)
				root := scopedRepository(t, scopedProvider{name: jenkinsProvider, file: "Jenkinsfile"}, config, scopedFrameworks[0].pkg, nil)
				text := providerSetupInstructions(root, jenkins{}, "javascript", "cucumber", "datadoghq.com", true)
				require.NotContains(t, text, "Write that script as a single-quoted")
				_, after, found := strings.Cut(text, "```groovy\n")
				require.True(t, found, text)
				fragment, _, found := strings.Cut(after, "\n```")
				require.True(t, found, text)
				quote := map[string]string{"double-quoted": `"`, "triple-double-quoted": `"""`, "single-quoted": "'", "triple-single-quoted": "'''"}[literal.name]
				snippet, expressions := jenkinsWritten(t, "sh "+quote+fragment+quote+"\n")
				require.Empty(t, expressions, "only the shell expands the inserted snippet")
				require.Contains(t, snippet, "DD_API_KEY")
				ending, _ := jenkinsCommand(quote, "\n"+command)
				applied := jenkinsReplaceCommand(t, config, literal.script, command, fragment+ending)
				written, expressions := jenkinsWritten(t, applied)
				require.Equal(t, literal.first+snippet+"\n"+command, written)
				require.Equal(t, literal.interpolated, expressions)
				requireJenkinsKeepsBuildNumber(t, applied, literal.first, literal.interpolated, snippet+"\n"+command)
			})
		}
	}
}
