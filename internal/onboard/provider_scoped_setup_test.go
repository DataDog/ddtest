// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
// scripts run, and off, under sh, bash and dash, and with pipefail on and
// off for the conversion's own pipe. Only a passing run continues with the
// script's later commands under errexit.
func TestStatusSubshellConvertsAndKeepsTheTestsStatus(t *testing.T) {
	type shell struct{ name, options string }
	var shells []shell
	for _, name := range []string{"sh", "bash", "dash"} {
		if _, err := exec.LookPath(name); err != nil {
			continue
		}
		shells = append(shells, shell{name, "set -e"}, shell{name, "set +e"})
		if name == "bash" {
			shells = append(shells, shell{name, "set -e -o pipefail"}, shell{name, "set +e -o pipefail"})
		}
	}
	if len(shells) == 0 {
		t.Skip("no sh")
	}
	tests := map[string]int{"(echo report > report.json; exit 3)": 3, "echo report > report.json": 0}
	conversions := map[string]int{
		"cat report.json | tr a-z A-Z > junit.xml":                       0,
		"cat report.json | sh -c 'tr a-z A-Z > junit.xml; exit 5'":       5,
		"cat report.json | tr a-z A-Z | sh -c 'cat > junit.xml; exit 6'": 6,
	}
	for _, shell := range shells {
		for tests, testStatus := range tests {
			for conversion, conversionStatus := range conversions {
				t.Run(shell.name+" "+shell.options+" "+tests+" "+conversion, func(t *testing.T) {
					want := testStatus
					if want == 0 {
						want = conversionStatus
					}
					directory := t.TempDir()
					script := shell.options + "\n" + statusSubshell("report.json", tests, conversion) + "\nstatus=$?\necho later > later.txt\nexit \"$status\"\n"
					command := exec.Command(shell.name, "-c", script)
					command.Dir = directory
					err := command.Run()
					status := 0
					var exit *exec.ExitError
					if errors.As(err, &exit) {
						status = exit.ExitCode()
					} else {
						require.NoError(t, err)
					}
					require.Equal(t, want, status, "the tests' status, or the conversion's when only it fails")
					converted, readErr := os.ReadFile(filepath.Join(directory, "junit.xml"))
					require.NoError(t, readErr, "the report is converted whatever the tests' result")
					require.Equal(t, "REPORT\n", string(converted))
					_, later := os.Stat(filepath.Join(directory, "later.txt"))
					if strings.HasPrefix(shell.options, "set -e") {
						require.Equal(t, want == 0, later == nil, "only a passing run continues with the script")
					} else {
						require.NoError(t, later, "without errexit, the script continues")
					}
				})
			}
		}
	}
}

// The status subshell removes the intermediate report before the tests, so
// tests that fail before writing it, in a reused workspace or after another
// command that wrote it, never get an earlier run's report converted: cat
// finds no file, and the command exits with the tests' status whatever the
// converter does with no input.
func TestStatusSubshellNeverConvertsAStaleReport(t *testing.T) {
	type shell struct{ name, options string }
	var shells []shell
	for _, name := range []string{"sh", "bash", "dash"} {
		if _, err := exec.LookPath(name); err != nil {
			continue
		}
		shells = append(shells, shell{name, "set -e"}, shell{name, "set +e"})
		if name == "bash" {
			shells = append(shells, shell{name, "set -e -o pipefail"}, shell{name, "set +e -o pipefail"})
		}
	}
	if len(shells) == 0 {
		t.Skip("no sh")
	}
	for _, shell := range shells {
		for _, conversion := range []string{
			"cat report.json | tr a-z A-Z > junit.xml",
			`cat report.json | sh -c 'input=$(cat); [ -n "$input" ] || exit 7; echo "$input" > junit.xml'`,
		} {
			t.Run(shell.name+" "+shell.options+" "+conversion, func(t *testing.T) {
				directory := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(directory, "report.json"), []byte("stale\n"), 0o644))
				script := shell.options + "\n" + statusSubshell("report.json", "sh -c 'exit 4'", conversion) + "\n"
				command := exec.Command(shell.name, "-c", script)
				command.Dir = directory
				err := command.Run()
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 4, exit.ExitCode(), "the tests' status")
				_, statErr := os.Stat(filepath.Join(directory, "report.json"))
				require.ErrorIs(t, statErr, fs.ErrNotExist, "the stale report is removed")
				converted, readErr := os.ReadFile(filepath.Join(directory, "junit.xml"))
				if strings.Contains(conversion, "exit 7") {
					require.ErrorIs(t, readErr, fs.ErrNotExist, "the converter got no input")
				} else {
					require.NoError(t, readErr, "the conversion ran after the failing tests")
					require.Empty(t, string(converted), "no earlier run's report is converted")
				}
			})
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

			for _, removal := range []string{"rm -f other.json", "rm -rf .", "rm -f $REPORT", "rm -f cucumber-report.json other.json"} {
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
	var shells []string
	for _, name := range []string{"bash", "dash"} {
		if _, err := exec.LookPath(name); err == nil {
			shells = append(shells, name)
		}
	}
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
		"cucumber-js --format json | cucumber-junit > \"$status.xml\"":                                                  false,
		"cucumber-js --format json | cucumber-junit --name \"$converted\"":                                              false,
		"cucumber-js --format json | cucumber-junit | exit 0":                                                           false,
		"CI=1 cucumber-js --format json | cucumber-junit":                                                               false,
		"cucumber-js --format json | cucumber-junit > junit.xml; echo done":                                             false,
		"cucumber-js --format json | cucumber-junit > junit.xml && echo done":                                           false,
	} {
		t.Run(pipe, func(t *testing.T) {
			_, err := directScopedCommand(t.TempDir(), runtimeStep{Run: pipe}, TestCommand{}, "cucumber")
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
			require.NotContains(t, split.Command, "#")
			require.NotContains(t, split.Command, ";;")
			for _, shell := range shells {
				output, err := exec.Command(shell, "-n", "-c", split.Command).CombinedOutput()
				require.NoError(t, err, "%s -n: %s\n%s", shell, split.Command, output)
			}
		})
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

			for _, middle := range statusUnsafeBuiltins {
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
	for _, provider := range scopedProviders {
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
	for _, provider := range scopedProviders {
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
