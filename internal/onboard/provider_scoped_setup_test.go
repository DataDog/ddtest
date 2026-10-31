// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
