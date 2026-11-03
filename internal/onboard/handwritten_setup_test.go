// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// handWrittenPackage is a Jest project that depends on dd-trace, which a
// hand-written setup preloads.
const handWrittenPackage = `{"scripts": {"test": "jest"}, "devDependencies": {"jest": "30.0.0", "vitest": "3.2.0", "dd-trace": "5.40.0"}}`

// handWrittenProvider writes one provider's job running command with env as
// its variables, in the provider's own syntax. A preload (NODE_OPTIONS or
// RUBYOPT) in env is set for the commands after before instead, as a job
// that installs its dependencies there must: the job's variables would
// reach the install, before the tracer exists.
type handWrittenProvider struct {
	name, file string
	config     func(env map[string]string, before []string, command string) string
}

// handWrittenPreloadEntries moves env's preload into entries for the
// commands after before, written as provider runs them: an export in the
// job's shell, or for CircleCI, whose run steps start their own shells, in
// $BASH_ENV.
func handWrittenPreloadEntries(provider string, env map[string]string, before []string) (map[string]string, []string) {
	entries := slices.Clone(before)
	for _, key := range []string{"NODE_OPTIONS", "RUBYOPT"} {
		value, set := env[key]
		if !set {
			continue
		}
		env = with(env, key, "")
		switch provider {
		case "circleci":
			entries = append(entries, `echo 'export `+key+`="`+value+`"' >> "$BASH_ENV"`)
		case "buildkite":
			entries = append(entries, `export `+key+`="`+strings.ReplaceAll(value, "$", "$$")+`"`)
		default:
			entries = append(entries, `export `+key+`="`+value+`"`)
		}
	}
	return env, entries
}

var handWrittenProviders = []handWrittenProvider{
	{"gitlab", ".gitlab-ci.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("gitlab", env, before)
		text := "test:\n  image: node:22\n" + yamlVariables("  variables:\n", "    ", env) + "  script:\n"
		for _, entry := range append(before, command) {
			text += "    - " + entry + "\n"
		}
		return text
	}},
	{"circleci", ".circleci/config.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("circleci", env, before)
		text := "version: 2.1\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n" + yamlVariables("    environment:\n", "      ", env) + "    steps:\n      - checkout\n"
		for _, entry := range append(before, command) {
			text += "      - run: " + entry + "\n"
		}
		return text + "workflows:\n  main:\n    jobs:\n      - test\n"
	}},
	{"buildkite", ".buildkite/pipeline.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("buildkite", env, before)
		text := "steps:\n  - label: test\n" + yamlVariables("    env:\n", "      ", env) + "    commands:\n"
		for _, entry := range append(before, command) {
			text += "      - " + entry + "\n"
		}
		text += "    plugins:\n      - docker#v5.12.0:\n          image: node:22\n          propagate-environment: true\n          environment:\n            - DD_API_KEY\n"
		return text
	}},
}

// preloadFirst is an entry that sets a preload for the provider's later
// commands, to place before a dependency install.
func preloadFirst(provider handWrittenProvider, key, value string) []string {
	_, entries := handWrittenPreloadEntries(provider.name, map[string]string{key: value}, nil)
	return entries
}

func yamlVariables(header, indent string, env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	text := header
	for _, key := range sortedKeys(env) {
		text += indent + key + ": " + "'" + env[key] + "'\n"
	}
	return text
}

func sortedKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// handWrittenAgentless is the configuration dd-trace's documentation
// prescribes for agentless reporting, with the key from the provider.
func handWrittenAgentless(options string) map[string]string {
	return map[string]string{"NODE_OPTIONS": options, "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
}

func with(env map[string]string, key, value string) map[string]string {
	copied := map[string]string{}
	for k, v := range env {
		copied[k] = v
	}
	if value == "" {
		delete(copied, key)
	} else {
		copied[key] = value
	}
	return copied
}

func handWrittenRepository(t *testing.T, provider handWrittenProvider, pkg, config string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "package.json", pkg)
	writeFile(t, root, provider.file, config)
	for path, contents := range files {
		writeFile(t, root, path, contents)
	}
	t.Chdir(root)
	useCIProvider(t, provider.name)
	return root
}

func handWrittenRuntime(t *testing.T, root, framework string) RuntimeCheck {
	t.Helper()
	return checkCIRuntimes(t.Context(), root, nil, func(_ context.Context, action, version string) (tracerRequirement, error) {
		require.Equal(t, "5.40.0", version, action)
		return tracerRequirement{Version: version, Node: ">=18"}, nil
	}, framework)
}

// A job that sets up dd-trace by hand is certified with the rules the
// provider's setup follows, on every provider without the shared syntax:
// an exact release, the key from the provider's secrets, the selected site,
// a preload that reaches the tests and agentless reporting.
func TestHandWrittenJavaScriptSetups(t *testing.T) {
	agentless := handWrittenAgentless("-r dd-trace/ci/init")
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, framework, pkg string
				env                  map[string]string
				before               []string
				command, site        string
				files                map[string]string
				// configured is ciSetupConfigured with site; status the runtime check's.
				configured bool
				status     string
				reason     string
			}{
				{name: "complete", env: agentless, configured: true, status: StatusCompatible},
				{name: "selected site", env: agentless, site: "datadoghq.com", configured: true, status: StatusCompatible},
				{name: "default site", env: with(agentless, "DD_SITE", ""), site: "datadoghq.com", configured: true, status: StatusCompatible},
				{name: "other site", env: agentless, site: "datadoghq.eu", reason: "set DD_SITE=datadoghq.eu", status: StatusCompatible},
				{name: "ESM", framework: "vitest", command: "npx vitest run", env: handWrittenAgentless("-r dd-trace/ci/init --import dd-trace/register.js"), configured: true, status: StatusCompatible},
				{name: "ESM without the loader", framework: "vitest", command: "npx vitest run", env: agentless, status: StatusInconclusive, reason: "add --import dd-trace/register.js"},
				{name: "other options", env: handWrittenAgentless("--max-old-space-size=4096 --require=dd-trace/ci/init"), configured: true, status: StatusCompatible},
				{name: "lockfile pin", pkg: `{"devDependencies": {"jest": "30.0.0", "dd-trace": "^5.0.0"}}`, files: map[string]string{"package-lock.json": `{"lockfileVersion": 3, "packages": {"node_modules/dd-trace": {"version": "5.40.0"}}}`}, env: agentless, configured: true, status: StatusCompatible},
				{name: "yarn lockfile pin", pkg: `{"devDependencies": {"jest": "30.0.0", "dd-trace": "^5.0.0"}}`, files: map[string]string{"yarn.lock": "dd-trace@^5.0.0:\n  version \"5.40.0\"\n"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "explicit install", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install --no-save dd-trace@5.40.0"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "floating range", pkg: `{"devDependencies": {"jest": "30.0.0", "dd-trace": "^5.0.0"}}`, env: agentless, status: StatusInconclusive, reason: "which is not an exact release, and no lockfile pins it"},
				{name: "floating install", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm install dd-trace"}, env: agentless, status: StatusInconclusive, reason: "installs dd-trace without an exact release"},
				{name: "global install", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm install -g dd-trace@5.40.0"}, env: agentless, status: StatusInconclusive, reason: "installs dd-trace globally"},
				{name: "not a dependency", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, env: agentless, status: StatusInconclusive, reason: "depends on dd-trace"},
				{name: "production install", before: []string{"npm ci --omit=dev"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
				{name: "optional dependency", pkg: `{"devDependencies": {"jest": "30.0.0"}, "optionalDependencies": {"dd-trace": "5.40.0"}}`, before: []string{"npm ci --omit=dev"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "optional dependency omitted", pkg: `{"devDependencies": {"jest": "30.0.0"}, "optionalDependencies": {"dd-trace": "5.40.0"}}`, before: []string{"npm ci --omit=optional"}, env: agentless, status: StatusInconclusive, reason: "installs the dependencies without the optional ones"},
				{name: "optional dependency omitted, then a full install", pkg: `{"devDependencies": {"jest": "30.0.0"}, "optionalDependencies": {"dd-trace": "5.40.0"}}`, before: []string{"npm ci --omit=optional", "npm ci"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "saved optional dependency pruned", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install --save-optional --save-exact dd-trace@5.40.0", "npm prune --omit=optional"}, env: agentless, status: StatusInconclusive, reason: "The job removes dd-trace (npm prune --omit=optional at"},
				{name: "production install including the devDependencies", before: []string{"NODE_ENV=production npm ci --include=dev"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "install removed by npm ci", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm install --no-save dd-trace@5.40.0", "npm ci"}, env: agentless, status: StatusInconclusive, reason: "which npm ci at"},
				{name: "saved install before npm ci", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm install --save-exact dd-trace@5.40.0", "npm ci"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "install in another package", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install --no-save --prefix tools dd-trace@5.40.0"}, env: agentless, status: StatusInconclusive, reason: "outside the checkout root, where the tests run"},
				{name: "framework disabled", env: with(agentless, "DD_TRACE_JEST_ENABLED", "false"), status: StatusInconclusive, reason: "DD_TRACE_JEST_ENABLED=false turns the tracer's jest integration off"},
				{name: "literal key", env: with(agentless, "DD_API_KEY", "0123456789abcdef"), status: StatusInconclusive, reason: "never commit the key"},
				{name: "agent mode", env: with(agentless, "DD_CIVISIBILITY_AGENTLESS_ENABLED", ""), status: StatusInconclusive, reason: "reports through a Datadog Agent"},
				{name: "disabled", env: with(agentless, "DD_CIVISIBILITY_ENABLED", "false"), status: StatusInconclusive, reason: "DD_CIVISIBILITY_ENABLED=false turns the tracer off"},
				{name: "APM preload", env: handWrittenAgentless("-r dd-trace/init"), status: StatusInconclusive, reason: "does not set up Test Optimization"},
				{name: "action variable", env: handWrittenAgentless("-r $DD_TRACE_PACKAGE"), status: StatusInconclusive, reason: "only the Datadog GitHub Actions setup exports"},
				{name: "no setup", env: map[string]string{"DD_SITE": "datadoghq.com"}, status: StatusIncompatible, reason: "selecting JavaScript runs before this test command"},
				// npm runs on Node too: a preload that reaches npm ci, before
				// dd-trace is installed, stops the job on a fresh checkout.
				{name: "preload before the install", env: with(agentless, "NODE_OPTIONS", ""), before: append(preloadFirst(provider, "NODE_OPTIONS", "-r dd-trace/ci/init"), "npm ci"), status: StatusInconclusive, reason: "NODE_OPTIONS preloads dd-trace for npm ci"},
				{name: "inline preload on the install", env: agentless, before: []string{"NODE_OPTIONS='-r dd-trace/ci/init' npm ci"}, status: StatusInconclusive, reason: "NODE_OPTIONS preloads dd-trace for npm ci"},
				// Command resolution and the validation scope accept only the
				// documented spelling.
				{name: "node_modules path preload", env: handWrittenAgentless("-r ./node_modules/dd-trace/ci/init"), status: StatusInconclusive, reason: "a spelling ddtest does not validate"},
				{name: "unknown tracer switch", env: with(agentless, "DD_TRACE_ENABLED", "$TRACE_TESTS"), status: StatusInconclusive, reason: "DD_TRACE_ENABLED comes from"},
				{name: "unknown Test Optimization switch", env: with(agentless, "DD_CIVISIBILITY_ENABLED", "$TRACE_TESTS"), status: StatusInconclusive, reason: "DD_CIVISIBILITY_ENABLED comes from"},
				{name: "manifest tracer uninstalled", before: []string{"npm ci", "npm uninstall dd-trace"}, env: agentless, status: StatusInconclusive, reason: "The job removes dd-trace (npm uninstall dd-trace at"},
				{name: "reinstall after an uninstall", before: []string{"npm ci", "npm uninstall --no-save dd-trace", "npm ci"}, env: agentless, status: StatusInconclusive, reason: "cannot tell whether the later npm ci"},
				{name: "uninstall then exact install", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm uninstall dd-trace", "npm install --no-save dd-trace@5.40.0"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "production install, split option", before: []string{"npm ci --omit dev"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
				{name: "production install, split only", before: []string{"npm install --only production"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
				{name: "production install, its own NODE_ENV", before: []string{"NODE_ENV=production npm ci"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
				// Pruning the devDependencies removes a tracer the job saved
				// as one, and the last install decides what the tests find.
				{name: "saved devDependency pruned", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install --save-dev --save-exact dd-trace@5.40.0", "npm prune --omit=dev"}, env: agentless, status: StatusInconclusive, reason: "The job removes dd-trace (npm prune --omit=dev at"},
				{name: "saved devDependency, production prune", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install -D --save-exact dd-trace@5.40.0", "npm prune --production"}, env: agentless, status: StatusInconclusive, reason: "The job removes dd-trace (npm prune --production at"},
				{name: "saved dependency survives a production prune", pkg: `{"devDependencies": {"jest": "30.0.0"}}`, before: []string{"npm ci", "npm install --save-exact dd-trace@5.40.0", "npm prune --omit=dev"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "declared devDependency pruned", before: []string{"npm ci", "npm prune --production"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
				{name: "production install, then a full one", before: []string{"npm ci --omit=dev", "npm ci"}, env: agentless, configured: true, status: StatusCompatible},
				{name: "full install, then a production one", before: []string{"npm ci", "npm ci --omit=dev"}, env: agentless, status: StatusInconclusive, reason: "installs production dependencies only"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					framework, command, pkg := tc.framework, tc.command, tc.pkg
					if framework == "" {
						framework, command = "jest", "npx jest"
					}
					if pkg == "" {
						pkg = handWrittenPackage
					}
					before := tc.before
					if before == nil {
						before = []string{"npm ci"}
					}
					root := handWrittenRepository(t, provider, pkg, provider.config(tc.env, before, command), tc.files)
					require.Equal(t, tc.configured, javascriptDiscovery{}.ciSetupConfigured(root, framework, tc.site))
					result := handWrittenRuntime(t, root, framework)
					require.Equal(t, tc.status, result.Status, "%+v", result)
					var reasons []string
					for _, job := range result.Jobs {
						reasons = append(reasons, job.Reason)
					}
					problems := strings.Join(existingSetupProblems(root, "javascript", framework, tc.site), "\n")
					if tc.reason != "" {
						require.Contains(t, strings.Join(reasons, "\n")+"\n"+problems, tc.reason)
					}
					if tc.configured {
						require.Empty(t, problems)
						require.Contains(t, result.Jobs[0].Action, "hand-written dd-trace@5.40.0 setup")
						require.Equal(t, "dd-trace@5.40.0", result.Jobs[0].Tracer)
					}
				})
			}
		})
	}
}

// onboard --apply keeps a hand-written setup: a complete one is configured
// and gets no second setup; an incomplete one is reported with what it lacks.
func TestHandWrittenJavaScriptSetupApply(t *testing.T) {
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			agentless := handWrittenAgentless("-r dd-trace/ci/init")
			root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(agentless, []string{"npm ci"}, "npx jest"), nil)
			plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.True(t, plan.Configured(), "a complete hand-written setup needs no edit")

			writeFile(t, root, provider.file, provider.config(with(agentless, "DD_SITE", "datadoghq.eu"), []string{"npm ci"}, "npx jest"))
			_, err = PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "sets up Test Optimization by hand")
			require.Contains(t, gap.Reason, "keep it, without adding a second setup")
			require.Contains(t, gap.Reason, "set DD_SITE=datadoghq.com")
			data, err := os.ReadFile(filepath.Join(root, provider.file))
			require.NoError(t, err)
			require.NotContains(t, string(data), "install_test_visibility")
		})
	}
}

// onboard --apply adds NYC and the Istanbul ESM loader only to the selected
// Cucumber package scripts, so a job with a complete hand-written setup is
// not configured while a direct Cucumber command lacks NYC, or native ESM
// Cucumber lacks the loader: apply prescribes each edit, keeps the
// hand-written setup, and the result validates.
func TestHandWrittenCucumberSetupApply(t *testing.T) {
	const cucumberPackage = `{%s"scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0", "dd-trace": "5.40.0"}}`
	agentless := handWrittenAgentless("-r dd-trace/ci/init --import dd-trace/register.js")
	for _, provider := range handWrittenProviders {
		t.Run(provider.name+"/direct", func(t *testing.T) {
			root := handWrittenRepository(t, provider, fmt.Sprintf(cucumberPackage, ""), provider.config(agentless, []string{"npm ci"}, "npx cucumber-js"), nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *EditThenApplyGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, CucumberCoverageGap)
			require.Contains(t, gap.Reason, "the job's hand-written setup stays")
			_, after, ok := strings.Cut(gap.Reason, "Change this CI command to exactly: ")
			require.True(t, ok, gap.Reason)
			written, _, _ := strings.Cut(after, ". Then rerun onboard --apply")
			require.Contains(t, written, "npx --no-install nyc -- cucumber-js")

			writeFile(t, root, provider.file, provider.config(agentless, []string{"npm ci"}, written))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.True(t, plan.Configured(), "the hand-written setup stays")
			require.Equal(t, StatusCompatible, handWrittenRuntime(t, root, "cucumber").Status)

			// NYC alone is not enough: the impacted-tests workaround must
			// reach the command too, written on it or in its variables.
			writeFile(t, root, provider.file, provider.config(agentless, []string{"npm ci"}, "npx --no-install nyc -- cucumber-js"))
			_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "without "+cucumberImpactedWorkaround)
			require.Contains(t, gap.Reason, "the job's hand-written setup stays")
			_, after, ok = strings.Cut(gap.Reason, "Change this CI command to exactly: ")
			require.True(t, ok, gap.Reason)
			require.Contains(t, after, cucumberImpactedWorkaround+" npx --no-install nyc -- cucumber-js")
			writeFile(t, root, provider.file, provider.config(with(agentless, "DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED", "false"), []string{"npm ci"}, "npx --no-install nyc -- cucumber-js"))
			plan, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.True(t, plan.Configured(), "the job's variables carry the workaround")
		})
		t.Run(provider.name+"/native ESM", func(t *testing.T) {
			pkg := fmt.Sprintf(cucumberPackage, `"type": "module", `)
			root := handWrittenRepository(t, provider, pkg, provider.config(agentless, []string{"npm ci"}, "npm test"), nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "hand-written setup leaves the tracer's preload in NODE_OPTIONS without the "+cucumberESMLoaderHook)
			data, err := os.ReadFile(filepath.Join(root, "package.json"))
			require.NoError(t, err)
			require.Equal(t, pkg, string(data), "apply stops before any edit")

			loaded := with(agentless, "NODE_OPTIONS", agentless["NODE_OPTIONS"]+" "+cucumberESMLoaderHook)
			writeFile(t, root, provider.file, provider.config(loaded, []string{"npm ci"}, "npm test"))
			plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			_, err = plan.apply(true, os.Rename)
			require.NoError(t, err)
			require.Equal(t, StatusCompatible, handWrittenRuntime(t, root, "cucumber").Status)
			config, err := os.ReadFile(filepath.Join(root, provider.file))
			require.NoError(t, err)
			require.NotContains(t, string(config), "DD_CUCUMBER_NODE_OPTIONS=", "no provider setup is added")
		})
		// A later test command that sets its own NODE_OPTIONS without the
		// loader gets incomplete coverage, whatever the first one loads.
		t.Run(provider.name+"/native ESM, later step without the loader", func(t *testing.T) {
			pkg := fmt.Sprintf(cucumberPackage, `"type": "module", `)
			loaded := with(agentless, "NODE_OPTIONS", agentless["NODE_OPTIONS"]+" "+cucumberESMLoaderHook)
			env, before := handWrittenPreloadEntries(provider.name, loaded, []string{"npm ci"})
			later := `NODE_OPTIONS='` + agentless["NODE_OPTIONS"] + `' npm test`
			root := handWrittenRepository(t, provider, pkg, provider.config(env, append(before, "npm test"), later), nil)
			_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "hand-written setup leaves the tracer's preload in NODE_OPTIONS without the "+cucumberESMLoaderHook)

			writeFile(t, root, provider.file, provider.config(env, append(before, "npm test"), `NODE_OPTIONS='`+loaded["NODE_OPTIONS"]+`' npm test`))
			_, err = PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err, "every test step loads it")
		})
	}
}

// Outside GitHub Actions the installer the GitLab setup script, the CircleCI
// orb and the Buildkite snippet run appends its preload to NODE_OPTIONS and
// exports DD_TRACE_ESM_IMPORT, never DD_TRACE_PACKAGE. A test command that
// adds -r $DD_TRACE_PACKAGE after the setup would start Node with -r and no
// module, so it is never certified; extending NODE_OPTIONS otherwise is.
func TestProviderSetupsExportNoDDTracePackage(t *testing.T) {
	const reads = `export NODE_OPTIONS="$NODE_OPTIONS -r $DD_TRACE_PACKAGE"`
	const extends = `export NODE_OPTIONS="$NODE_OPTIONS --max-old-space-size=4096"`
	for _, tc := range []struct {
		provider, file string
		config         func(extra string) string
	}{
		{"gitlab", ".gitlab-ci.yml", func(extra string) string {
			return "test:\n  image: node:22\n  script:\n" + gitLabSetupEntry(gitLabJestSetup) + "    - " + extra + "\n    - npx jest\n"
		}},
		{"circleci", ".circleci/config.yml", func(extra string) string {
			return "version: 2.1\norbs:\n  test-optimization-circleci-orb: datadog/test-optimization-circleci-orb@2\njobs:\n  test:\n    docker:\n      - image: cimg/node:22.11\n    steps:\n" + circleOrbStep + "      - run: |\n          echo '" + extra + "' >> \"$BASH_ENV\"\n      - run: npx jest\n" + circleWorkflow("      - test\n")
		}},
		{"buildkite", ".buildkite/pipeline.yml", func(extra string) string {
			return "steps:\n" + buildkiteDockerStep("test", "node:22", buildkiteSnippetText("js", "5.133.0", "datadoghq.com", "      ")+"      - "+strings.ReplaceAll(extra, "$", "$$")+"\n      - npx jest\n")
		}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			check := func(extra string) RuntimeCheck {
				root := t.TempDir()
				writeFile(t, root, "package.json", `{"devDependencies": {"jest": "30.0.0"}}`)
				writeFile(t, root, tc.file, tc.config(extra))
				t.Chdir(root)
				useCIProvider(t, tc.provider)
				workflows, err := readProviderWorkflows(ciProviderNamed(tc.provider), root)
				require.NoError(t, err)
				for _, workflow := range workflows {
					for _, job := range workflow.Jobs {
						for _, step := range job.Steps {
							require.NotContains(t, step.Env, "DD_TRACE_PACKAGE", "the installer exports no DD_TRACE_PACKAGE outside GitHub Actions")
						}
					}
				}
				return checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
					return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
				}, "jest")
			}
			result := check(reads)
			require.Equal(t, StatusInconclusive, result.Status, "%+v", result)
			require.Contains(t, result.Jobs[len(result.Jobs)-1].Reason, "reads $DD_TRACE_PACKAGE, which only the Datadog GitHub Actions setup exports")
			scope, err := DiscoverValidationScope(".", "jest")
			require.NoError(t, err)
			unresolved := strings.Join(scope.Unresolved, "\n")
			require.Contains(t, unresolved, "reads $DD_TRACE_PACKAGE, which only the Datadog GitHub Actions setup exports", "discovery names the cause too")
			// It quotes what the configuration writes, never the model's
			// GitHub Actions expression for the setup's preload.
			require.Contains(t, unresolved, "its CI value -r $DD_TRACE_PACKAGE reads")
			require.NotContains(t, unresolved, "${{")
			result = check(extends)
			require.Equal(t, StatusCompatible, result.Status, "%+v", result)
		})
	}
}

// rubyHandWrittenProviders write one provider's RSpec job.
var rubyHandWrittenProviders = []handWrittenProvider{
	{"gitlab", ".gitlab-ci.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("gitlab", env, before)
		text := "rspec:\n  image: ruby:3.3\n" + yamlVariables("  variables:\n", "    ", env) + "  script:\n"
		for _, entry := range append(before, command) {
			text += "    - " + entry + "\n"
		}
		return text
	}},
	{"circleci", ".circleci/config.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("circleci", env, before)
		text := "version: 2.1\njobs:\n  rspec:\n    docker:\n      - image: cimg/ruby:3.3\n" + yamlVariables("    environment:\n", "      ", env) + "    steps:\n      - checkout\n"
		for _, entry := range append(before, command) {
			text += "      - run: " + entry + "\n"
		}
		return text + "workflows:\n  main:\n    jobs:\n      - rspec\n"
	}},
	{"buildkite", ".buildkite/pipeline.yml", func(env map[string]string, before []string, command string) string {
		env, before = handWrittenPreloadEntries("buildkite", env, before)
		text := "steps:\n  - label: rspec\n" + yamlVariables("    env:\n", "      ", env) + "    commands:\n"
		for _, entry := range append(before, command) {
			text += "      - " + entry + "\n"
		}
		return text + "    plugins:\n      - docker#v5.12.0:\n          image: ruby:3.3\n          propagate-environment: true\n          environment:\n            - DD_API_KEY\n"
	}},
}

const (
	handWrittenGemfile     = "source 'https://rubygems.org'\ngem 'rspec'\ngem 'datadog-ci', '1.42.0'\n"
	handWrittenGemfileLock = "GEM\n  remote: https://rubygems.org/\n  specs:\n    datadog-ci (1.42.0)\n    rspec (3.13.0)\n\nDEPENDENCIES\n  datadog-ci (= 1.42.0)\n  rspec\n"
	handWrittenSpecHelper  = "require \"datadog/ci\"\n\nDatadog.configure do |c|\n  c.service = \"app\"\n  c.ci.enabled = true\n  c.ci.agentless_mode_enabled = true\n  c.ci.instrument :rspec\nend\n"
)

// A job that sets up datadog-ci by hand, with RUBYOPT or an RSpec helper,
// is certified with the same rules; what is missing is reported, and what
// ddtest cannot read stays unresolved.
func TestHandWrittenRubySetups(t *testing.T) {
	auto := map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument", "DD_CIVISIBILITY_ENABLED": "true", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	helperFiles := map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": handWrittenSpecHelper}
	for _, provider := range rubyHandWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				env        map[string]string
				before     []string
				command    string
				lock       string
				files      map[string]string
				site       string
				configured bool
				reason     string
			}{
				{name: "RUBYOPT", env: auto, configured: true},
				// Ruby reads RUBYOPT when it starts bundle exec too, before
				// Bundler sets up a bundle that may hold datadog-ci alone.
				{name: "RUBYOPT with bundle exec", env: with(auto, "RUBYOPT", "-rdatadog/ci/auto_instrument"), reason: "preload -rbundler/setup before it"},
				{name: "RUBYOPT with -r bundler/setup", env: with(auto, "RUBYOPT", "-r bundler/setup -r datadog/ci/auto_instrument"), configured: true},
				{name: "RUBYOPT without Bundler", env: with(auto, "RUBYOPT", "-rdatadog/ci/auto_instrument"), command: "rspec", reason: "before Bundler sets up the bundle"},
				{name: "RUBYOPT with a binstub", env: with(auto, "RUBYOPT", "-rdatadog/ci/auto_instrument"), command: "bin/rspec", files: map[string]string{"bin/rspec": "#!/usr/bin/env ruby\nrequire 'bundler/setup'\nload Gem.bin_path('rspec-core', 'rspec')\n"}, reason: "a binstub such as bin/rspec too"},
				{name: "RUBYOPT with a binstub after Bundler", env: auto, command: "bin/rspec", files: map[string]string{"bin/rspec": "#!/usr/bin/env ruby\nrequire 'bundler/setup'\nload Gem.bin_path('rspec-core', 'rspec')\n"}, configured: true},
				{name: "framework disabled", env: with(auto, "DD_TRACE_RSPEC_ENABLED", "false"), reason: "DD_TRACE_RSPEC_ENABLED=false turns the tracer's rspec integration off"},
				{name: "conditional instrument", env: map[string]string{"DD_SITE": "datadoghq.com"}, files: map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": strings.Replace(handWrittenSpecHelper, "  c.ci.instrument :rspec\n", "  c.ci.instrument :rspec if ENV[\"TRACE_TESTS\"] == \"true\"\n", 1)}, reason: "calls c.ci.instrument :rspec if ENV[\"TRACE_TESTS\"] == \"true\", which ddtest does not evaluate"},
				{name: "not enabled", env: with(auto, "DD_CIVISIBILITY_ENABLED", ""), reason: "set DD_CIVISIBILITY_ENABLED=true"},
				{name: "not locked", env: auto, lock: "GEM\n  specs:\n    rspec (3.13.0)\n", reason: "does not lock datadog-ci"},
				{name: "other site", env: auto, site: "datadoghq.eu", reason: "set DD_SITE=datadoghq.eu"},
				{name: "agent mode", env: with(auto, "DD_CIVISIBILITY_AGENTLESS_ENABLED", ""), reason: "reports through a Datadog Agent"},
				{name: "literal key", env: with(auto, "DD_API_KEY", "0123456789abcdef"), reason: "never commit the key"},
				{name: "spec helper", env: map[string]string{"DD_SITE": "datadoghq.com"}, files: helperFiles, configured: true},
				{name: "spec helper without instrument", files: map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": strings.Replace(handWrittenSpecHelper, "  c.ci.instrument :rspec\n", "", 1)}, reason: "add c.ci.instrument :rspec"},
				{name: "conditional spec helper", files: map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": strings.Replace(handWrittenSpecHelper, "  c.ci.enabled = true\n", "  c.ci.enabled = ENV[\"CI\"] == \"true\"\n", 1)}, reason: "which ddtest does not evaluate"},
				{name: "no setup", env: map[string]string{"DD_SITE": "datadoghq.com"}, reason: "before this test command"},
				{name: "RUBYOPT before bundle install", env: with(auto, "RUBYOPT", ""), before: append(preloadFirst(provider, "RUBYOPT", auto["RUBYOPT"]), "bundle install"), reason: "RUBYOPT preloads datadog-ci for bundle install"},
				{name: "unknown tracer switch", env: with(auto, "DD_TRACE_ENABLED", "$TRACE_TESTS"), reason: "DD_TRACE_ENABLED comes from"},
				// --options replaces .rspec.
				{name: "selected options file", env: map[string]string{"DD_SITE": "datadoghq.com"}, command: "bundle exec rspec --options .rspec-ci", files: map[string]string{".rspec": "--format progress\n", ".rspec-ci": "--require spec_helper\n", "spec/spec_helper.rb": handWrittenSpecHelper}, configured: true},
				{name: "options file without the helper", env: map[string]string{"DD_SITE": "datadoghq.com"}, command: "bundle exec rspec --options .rspec-ci", files: map[string]string{".rspec": "--require spec_helper\n", ".rspec-ci": "--format progress\n", "spec/spec_helper.rb": handWrittenSpecHelper}, reason: "before this test command"},
				// RSpec requires ./spec/spec_helper.rb from the working
				// directory, a bare name from spec/ or lib/.
				{name: "path-qualified helper", env: map[string]string{"DD_SITE": "datadoghq.com"}, files: map[string]string{".rspec": "--require ./spec/spec_helper.rb\n", "spec/spec_helper.rb": handWrittenSpecHelper}, configured: true},
				{name: "helper on the load path", env: map[string]string{"DD_SITE": "datadoghq.com"}, files: map[string]string{".rspec": "--require support/datadog\n", "spec/support/datadog.rb": handWrittenSpecHelper}, configured: true},
				// The helper's setting replaces DD_CIVISIBILITY_AGENTLESS_ENABLED.
				{name: "helper turns agentless mode off", env: map[string]string{"DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}, files: map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": strings.Replace(handWrittenSpecHelper, "agentless_mode_enabled = true", "agentless_mode_enabled = false", 1)}, reason: "reports through a Datadog Agent (localhost:8126) because the test helper's Datadog configuration turns agentless mode off"},
				{name: "helper selects the site", env: map[string]string{"DD_SITE": "datadoghq.com"}, files: map[string]string{".rspec": "--require spec_helper\n", "spec/spec_helper.rb": strings.Replace(handWrittenSpecHelper, "  c.service = \"app\"\n", "  c.service = \"app\"\n  c.site = \"datadoghq.eu\"\n", 1)}, reason: "the helper sets c.site = \"datadoghq.eu\""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					command := tc.command
					if command == "" {
						command = "bundle exec rspec"
					}
					lock := tc.lock
					if lock == "" {
						lock = handWrittenGemfileLock
					}
					files := map[string]string{"Gemfile": handWrittenGemfile, "Gemfile.lock": lock}
					for path, contents := range tc.files {
						files[path] = contents
					}
					root := t.TempDir()
					for path, contents := range files {
						writeFile(t, root, path, contents)
					}
					before := tc.before
					if before == nil {
						before = []string{"bundle install"}
					}
					writeFile(t, root, provider.file, provider.config(tc.env, before, command))
					t.Chdir(root)
					useCIProvider(t, provider.name)
					require.Equal(t, tc.configured, rubyDiscovery{}.ciSetupConfigured(root, "rspec", tc.site))
					check := RubyCIConfigurationForSite(root, "rspec", tc.site)
					require.Len(t, check.Jobs, 1, "%+v", check)
					job := check.Jobs[0]
					if tc.configured {
						require.Equal(t, "not checked", job.Status, job.Reason)
						require.Equal(t, "1.42.0", job.TracerRequested)
						require.Contains(t, job.Action, "hand-written datadog-ci 1.42.0 setup")
						require.Empty(t, existingSetupProblems(root, "ruby", "rspec", tc.site))
						return
					}
					require.Equal(t, "inconclusive", job.Status)
					require.Contains(t, job.Reason, tc.reason)
				})
			}
		})
	}
}

// A Jenkins stage in a container, which the Datadog Jenkins plugin cannot
// instrument, is set up by hand there: ddtest then checks that setup, with
// the key from a Jenkins credential, instead of reporting it unsupported.
func TestHandWrittenJenkinsContainerSetup(t *testing.T) {
	// The preload reaches the tests only, after npm ci installs dd-trace.
	const scopedTest = "withEnv(['NODE_OPTIONS=-r dd-trace/ci/init']) {\n          sh 'npx jest'\n        }"
	pipeline := func(environment string, test ...string) string {
		if len(test) == 0 {
			test = []string{scopedTest}
		}
		return "pipeline {\n  agent { docker { image 'node:22' } }\n  environment {\n" + environment + "  }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        " + test[0] + "\n      }\n    }\n  }\n}\n"
	}
	complete := "    DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n    DD_API_KEY = credentials('datadog-api-key')\n"
	for _, tc := range []struct {
		name, environment string
		configured        bool
		status, reason    string
	}{
		{"complete", complete, true, StatusCompatible, ""},
		{"literal key", strings.Replace(complete, "credentials('datadog-api-key')", "'0123456789abcdef'", 1), false, StatusInconclusive, "never commit the key"},
		// A Jenkins credential reaches a stage only where it is bound.
		{"unbound key", strings.Replace(complete, "    DD_API_KEY = credentials('datadog-api-key')\n", "", 1), false, StatusInconclusive, "No DD_API_KEY reaches these tests"},
		{"agent mode", strings.Replace(complete, "    DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n", "", 1), false, StatusInconclusive, "reports through a Datadog Agent"},
		// The pipeline's environment reaches npm ci too, before dd-trace is
		// installed.
		{"preload before the install", complete + "    NODE_OPTIONS = '-r dd-trace/ci/init'\n", false, StatusInconclusive, "NODE_OPTIONS preloads dd-trace for npm ci"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := pipeline(tc.environment)
			if strings.Contains(tc.environment, "NODE_OPTIONS") {
				source = pipeline(tc.environment, "sh 'npx jest'")
			}
			root := jenkinsRepository(t, source)
			writeFile(t, root, "package.json", handWrittenPackage)
			// The stage's own setup is what ddtest reads, whatever the
			// provider's setup can do in a container.
			setup := firstJobHandWrittenSetup(t, root, "javascript", "jest", "")
			require.NotNil(t, setup)
			require.Equal(t, "hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)", setup.Reference)
			require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
			require.Equal(t, tc.configured, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
			result := handWrittenRuntime(t, root, "jest")
			require.Equal(t, tc.status, result.Status, "%+v", result)
			require.Equal(t, setup.Reference, result.Jobs[0].Action)
			if tc.reason != "" {
				require.Contains(t, result.Jobs[0].Reason, tc.reason)
			}
		})
	}
	// An inline prefix in the sh script is the command's environment there too.
	root := jenkinsRepository(t, strings.Replace(pipeline("    DD_API_KEY = credentials('datadog-api-key')\n", "sh 'npx jest'"), "sh 'npx jest'", "sh 'NODE_OPTIONS=\"-r dd-trace/ci/init\" DD_CIVISIBILITY_AGENTLESS_ENABLED=true npx jest'", 1))
	writeFile(t, root, "package.json", handWrittenPackage)
	require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
	check := handWrittenRuntime(t, root, "jest")
	require.Equal(t, StatusCompatible, check.Status)
	require.Equal(t, "hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)", check.Jobs[0].Action)
	// Without a hand-written setup ddtest reads none, and the provider's
	// setup alone decides: a stage it cannot instrument stays unsupported.
	source := pipeline("    DD_SITE = 'datadoghq.com'\n", "sh 'npx jest'")
	root = jenkinsRepository(t, source)
	writeFile(t, root, "package.json", handWrittenPackage)
	require.Nil(t, firstJobHandWrittenSetup(t, root, "javascript", "jest", ""))
	if jenkinsJobs(t, source)["Test"].SetupUnsupported != "" {
		var unsupported *CIProviderUnsupportedError
		require.ErrorAs(t, CheckCIProviderSupport(root, "javascript", "jest"), &unsupported)
	} else {
		require.NoError(t, CheckCIProviderSupport(root, "javascript", "jest"))
	}
}

// Setup instructions keep a job's hand-written setup and list only what it
// lacks, instead of prescribing the provider's setup for it.
func TestHandWrittenSetupInstructions(t *testing.T) {
	provider := handWrittenProviders[0]
	agentless := handWrittenAgentless("-r dd-trace/ci/init")
	root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(with(agentless, "DD_SITE", "datadoghq.eu"), []string{"npm ci"}, "npx jest"), nil)
	request := javascriptDiscovery{}.ciSetupRequest(root, "jest", "datadoghq.com", gitLabCI{}.setupTerms())
	notes := strings.Join(request.Notes, "\n")
	require.Contains(t, notes, "These CI jobs already set up Test Optimization by hand. Keep that setup and do not add the Datadog setup script to them")
	require.Contains(t, notes, ".gitlab-ci.yml / test (hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)): It is incomplete: The tracer reports to datadoghq.eu, but the selected Datadog site is datadoghq.com: set DD_SITE=datadoghq.com in the job's variables.")
	text := providerSetupInstructions(root, gitLabCI{}, "javascript", "jest", "datadoghq.com", false)
	require.Contains(t, text, "# Keep the hand-written Datadog Test Optimization setup for jest on GitLab CI")
	require.Contains(t, text, "- .gitlab-ci.yml / test (hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)): It is incomplete")
	require.Contains(t, text, CIProviderHandWrittenKeyAction())
	require.NotContains(t, text, "test-optimization-gitlab-script")
	// A job without its own setup still gets the provider's.
	writeFile(t, root, provider.file, provider.config(agentless, []string{"npm ci"}, "npx jest")+"other:\n  image: node:22\n  script:\n    - npx jest\n")
	text = providerSetupInstructions(root, gitLabCI{}, "javascript", "jest", "datadoghq.com", false)
	require.Contains(t, text, "# Enable Datadog Test Optimization for jest on GitLab CI")
	require.Contains(t, text, "test-optimization-gitlab-script")
	require.Contains(t, text, ".gitlab-ci.yml / test (hand-written dd-trace@5.40.0 setup (NODE_OPTIONS)): complete; ddtest validates it as it is.")

	rubyProvider := rubyHandWrittenProviders[0]
	root = t.TempDir()
	writeFile(t, root, "Gemfile", handWrittenGemfile)
	writeFile(t, root, "Gemfile.lock", handWrittenGemfileLock)
	writeFile(t, root, rubyProvider.file, rubyProvider.config(map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true"}, []string{"bundle install"}, "bundle exec rspec"))
	t.Chdir(root)
	useCIProvider(t, rubyProvider.name)
	state, err := rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.com"})
	require.Error(t, err)
	require.Equal(t, "ci_setup_required", state.Code)
	require.Contains(t, state.Reason, "Jobs that set it up by hand keep their setup")
	require.Contains(t, state.Reason, "set DD_CIVISIBILITY_ENABLED=true")
	require.Contains(t, state.Instruction, ".gitlab-ci.yml / rspec (hand-written datadog-ci 1.42.0 setup (RUBYOPT)): It is incomplete")
	require.Contains(t, state.Instruction, "# Keep the hand-written Datadog Test Optimization setup for rspec on GitLab CI")
	require.NotContains(t, state.Instruction, "test-optimization-gitlab-script", "no job gets the provider's setup")

	writeFile(t, root, rubyProvider.file, rubyProvider.config(map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument", "DD_CIVISIBILITY_ENABLED": "true", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true"}, []string{"bundle install"}, "bundle exec rspec"))
	state, err = rubyAgentStart(t, StartOptions{Format: "json", Site: "datadoghq.com"})
	require.NoError(t, err)
	require.Equal(t, "ready", state.Status)
	require.Contains(t, state.Reason, "with the Datadog setup script or a complete hand-written setup")
}

// dd-trace's documentation runs the tests with an inline prefix,
// NODE_OPTIONS="-r dd-trace/ci/init" DD_ENV=ci yarn test; the shell sets it
// for that command alone, so it is the command's environment.
func TestHandWrittenInlineSetup(t *testing.T) {
	command := `NODE_OPTIONS='-r dd-trace/ci/init' DD_CIVISIBILITY_AGENTLESS_ENABLED=true DD_ENV=ci npx jest`
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(map[string]string{"DD_SITE": "datadoghq.com"}, []string{"npm ci"}, command), nil)
			workflows, err := readProviderWorkflows(ciProviderNamed(provider.name), root)
			require.NoError(t, err)
			for _, job := range workflows[0].Jobs {
				test := job.Steps[len(job.Steps)-1]
				require.Equal(t, "npx jest", test.Run)
				require.Equal(t, "-r dd-trace/ci/init", test.Env["NODE_OPTIONS"])
				require.Equal(t, "ci", test.Env["DD_ENV"])
			}
			require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
			result := handWrittenRuntime(t, root, "jest")
			require.Equal(t, StatusCompatible, result.Status, "%+v", result)

			// Without agentless reporting it stays unresolved, never certified.
			writeFile(t, root, provider.file, provider.config(nil, []string{"npm ci"}, `NODE_OPTIONS='-r dd-trace/ci/init' DD_ENV=ci yarn test`))
			result = handWrittenRuntime(t, root, "jest")
			require.Equal(t, StatusInconclusive, result.Status)
			require.Contains(t, result.Jobs[0].Reason, "reports through a Datadog Agent (localhost:8126)")
			require.Equal(t, CodeUnverifiedInitialization, result.Jobs[0].Code, "an Agent ddtest cannot see is unresolved, not missing")
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
		})
	}
	// After the provider's setup, an inline prefix that reads $DD_TRACE_PACKAGE
	// would start Node with -r and no module.
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"devDependencies": {"jest": "30.0.0"}}`)
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - NODE_OPTIONS=\"$NODE_OPTIONS -r $DD_TRACE_PACKAGE\" npx jest\n")
	t.Chdir(root)
	useCIProvider(t, "gitlab")
	result := checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusInconclusive, result.Status)
	require.Contains(t, result.Jobs[len(result.Jobs)-1].Reason, "reads $DD_TRACE_PACKAGE")
	writeFile(t, root, ".gitlab-ci.yml", "test:\n  image: node:22\n  script:\n"+gitLabSetupEntry(gitLabJestSetup)+"    - NODE_OPTIONS=\"$NODE_OPTIONS --max-old-space-size=4096\" npx jest\n")
	result = checkCIRuntimes(t.Context(), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
		return tracerRequirement{Version: "5.133.0", Node: ">=18"}, nil
	}, "jest")
	require.Equal(t, StatusCompatible, result.Status, "%+v", result)
}

// A hand-written setup that reports through a Datadog Agent is not pushed to
// agentless reporting: no configuration shows the Agent, so ddtest asks the
// user to confirm it, and certifies it as stated by the user.
func TestHandWrittenAgentModeIsConfirmedByTheUser(t *testing.T) {
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			agent := map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "DD_AGENT_HOST": "datadog-agent"}
			root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(agent, []string{"npm ci"}, "npx jest"), nil)
			t.Cleanup(func() { require.NoError(t, UseCIStatements(nil, nil)) })
			required := RequiredCIConfirmations(root, "javascript", "jest")
			require.NotNil(t, required)
			require.Len(t, required.Facts, 1)
			require.Equal(t, "datadog-agent", required.Facts[0].Key)
			result := CIConfirmationRequiredResult(required)
			require.Equal(t, "ci_setup_confirmation_required", result.Code)
			require.Contains(t, result.Instruction, "Does a Datadog Agent run where those tests run")
			require.Contains(t, result.Instruction, "--ci-confirm datadog-agent")
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
			check := handWrittenRuntime(t, root, "jest")
			require.Equal(t, StatusInconclusive, check.Status)
			require.Contains(t, check.Jobs[0].Reason, "reports through a Datadog Agent (DD_AGENT_HOST=datadog-agent)")
			require.Contains(t, check.Jobs[0].Reason, "Keep reporting through the Agent unless the user decides otherwise")
			require.NotContains(t, check.Jobs[0].Reason, "set DD_CIVISIBILITY_AGENTLESS_ENABLED")

			require.False(t, check.Jobs[0].AgentReporting, "the user has not confirmed the Agent")

			require.NoError(t, UseCIStatements(nil, []CIFact{{Key: "datadog-agent"}}))
			require.Nil(t, RequiredCIConfirmations(root, "javascript", "jest"))
			require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", ""))
			check = handWrittenRuntime(t, root, "jest")
			require.Equal(t, StatusCompatible, check.Status, "%+v", check)
			require.True(t, check.Jobs[0].AgentReporting, "the confirmed Agent holds the API key")
			stated, _ := StatedFacts(&check, "javascript")
			require.Contains(t, stated, "A Datadog Agent runs where the hand-written CI setups' tests run, at the address they use, with an API key and the selected Datadog site (--ci-confirm)")
		})
	}
}

// A hand-written setup one test step writes for itself, inline or in its
// own variables, reaches no other step: a job is certified only when every
// test step loads the tracer, and each Ruby step is checked on its own.
func TestHandWrittenSetupReachesEveryTestStep(t *testing.T) {
	inline := `NODE_OPTIONS='-r dd-trace/ci/init' npx jest unit`
	env := with(handWrittenAgentless("-r dd-trace/ci/init"), "NODE_OPTIONS", "")
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			root := handWrittenRepository(t, provider, handWrittenPackage, provider.config(env, []string{"npm ci", inline}, "npx jest integration"), nil)
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
			require.Contains(t, strings.Join(existingSetupProblems(root, "javascript", "jest", "datadoghq.com"), "\n"), "(npx jest integration) gets none of the hand-written setup")
			_, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			var gap *SetupGap
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "gets none of the hand-written setup, so its tests run untraced")

			// A later step's setup is kept too when the first test step has
			// none: onboard --apply adds no second setup beside it.
			both := `NODE_OPTIONS='-r dd-trace/ci/init' npx jest integration`
			writeFile(t, root, provider.file, provider.config(env, []string{"npm ci", "npx jest unit"}, both))
			require.False(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
			require.Contains(t, strings.Join(existingSetupProblems(root, "javascript", "jest", "datadoghq.com"), "\n"), "(npx jest unit) gets none of the hand-written setup")
			_, err = PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			require.ErrorAs(t, err, &gap)
			require.Contains(t, gap.Reason, "keep it, without adding a second setup")
			require.Contains(t, gap.Reason, "(npx jest unit) gets none of the hand-written setup")

			// Both commands with the setup are certified.
			writeFile(t, root, provider.file, provider.config(env, []string{"npm ci", inline}, both))
			require.True(t, javascriptDiscovery{}.ciSetupConfigured(root, "jest", "datadoghq.com"))
			plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.True(t, plan.Configured())
		})
	}
	auto := map[string]string{"RUBYOPT": "-rbundler/setup -rdatadog/ci/auto_instrument", "DD_CIVISIBILITY_ENABLED": "true", "DD_CIVISIBILITY_AGENTLESS_ENABLED": "true", "DD_SITE": "datadoghq.com"}
	for _, provider := range rubyHandWrittenProviders {
		t.Run(provider.name+"/ruby", func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "Gemfile", handWrittenGemfile)
			writeFile(t, root, "Gemfile.lock", handWrittenGemfileLock)
			env, before := handWrittenPreloadEntries(provider.name, auto, []string{"bundle install"})
			writeFile(t, root, provider.file, provider.config(env, append(before, "bundle exec rspec spec/unit"), "bundle exec rspec spec/integration"))
			t.Chdir(root)
			useCIProvider(t, provider.name)
			check := RubyCIConfigurationForSite(root, "rspec", "datadoghq.com")
			require.Len(t, check.Jobs, 2, "%+v", check)
			for _, job := range check.Jobs {
				require.Equal(t, "not checked", job.Status, job.Reason)
				require.Contains(t, job.Action, "hand-written datadog-ci 1.42.0 setup")
			}
		})
	}
}

// An install in another directory than the tests' does not pin the
// release their -r dd-trace/ci/init loads.
func TestHandWrittenTracerInstallDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/package.json", `{"devDependencies": {"jest": "30.0.0", "dd-trace": "^4.0.0"}}`)
	job := runtimeJob{Steps: []runtimeStep{
		{Run: "npm install --no-save dd-trace@5.40.0", WorkingDirectory: "tools"},
		{Run: "npx jest", WorkingDirectory: "app"},
	}}
	setup := &handWrittenSetup{}
	version, _ := javascriptHandWrittenTracer(setup, root, "app", ciWorkflow{}, job, 1, nil)
	require.Empty(t, version)
	require.Contains(t, strings.Join(setup.Unresolved, " "), "outside app, where the tests run")

	job.Steps[0].WorkingDirectory = "app"
	setup = &handWrittenSetup{}
	version, _ = javascriptHandWrittenTracer(setup, root, "app", ciWorkflow{}, job, 1, nil)
	require.Equal(t, "5.40.0", version)
	require.Empty(t, setup.Problems)
	require.Empty(t, setup.Unresolved)
}

// A production-only install is read in its own step's environment: a
// NODE_ENV=production there omits devDependencies, one only the test step
// sets does not.
func TestHandWrittenProductionInstallEnvironment(t *testing.T) {
	job := runtimeJob{Steps: []runtimeStep{
		{Run: "npm ci", Env: map[string]string{"NODE_ENV": "production"}},
		{Run: "npx jest"},
	}}
	require.True(t, javascriptOmitsGroup(ciWorkflow{}, job, 1, ".", ".", javascriptDevelopment))
	job.Steps[0].Env, job.Steps[1].Env = nil, map[string]string{"NODE_ENV": "production"}
	require.False(t, javascriptOmitsGroup(ciWorkflow{}, job, 1, ".", ".", javascriptDevelopment))
	job.Env = map[string]string{"NODE_ENV": "production"}
	require.True(t, javascriptOmitsGroup(ciWorkflow{}, job, 1, ".", ".", javascriptDevelopment), "the job's variables reach the install too")
}

// The last install where the tests run decides whether they find the
// devDependencies: one in another directory restores none of them there.
func TestHandWrittenFinalInstallDecidesDevelopment(t *testing.T) {
	for _, tc := range []struct {
		later   runtimeStep
		omitted bool
	}{
		{runtimeStep{Run: "npm ci"}, false},
		{runtimeStep{Run: "npm prune"}, true},
		{runtimeStep{Run: "cd docs && npm ci"}, true},
		{runtimeStep{Run: "npm ci --prefix docs"}, true},
		{runtimeStep{Run: "npm ci", WorkingDirectory: "docs"}, true},
	} {
		job := runtimeJob{Steps: []runtimeStep{{Run: "npm ci --omit=dev"}, tc.later, {Run: "npx jest"}}}
		require.Equal(t, tc.omitted, javascriptOmitsGroup(ciWorkflow{}, job, 2, ".", ".", javascriptDevelopment), tc.later)
	}
}

// npm's --include, or npm_config_include, installs a group whatever omits
// it; npm_config_omit omits one as --omit does; and the optional
// dependencies are left out as the devDependencies are.
func TestHandWrittenInstallGroupOverrides(t *testing.T) {
	for _, tc := range []struct {
		command string
		env     map[string]string
		group   string
		omitted bool
	}{
		{"npm ci --include=dev", map[string]string{"NODE_ENV": "production"}, javascriptDevelopment, false},
		{"npm ci --include dev", map[string]string{"NODE_ENV": "production"}, javascriptDevelopment, false},
		{"npm ci --omit=dev --include=dev", nil, javascriptDevelopment, false},
		{"npm ci", map[string]string{"NODE_ENV": "production", "npm_config_include": "dev"}, javascriptDevelopment, false},
		{"npm ci --omit=dev", map[string]string{"NPM_CONFIG_INCLUDE": "dev"}, javascriptDevelopment, false},
		{"npm ci --include=optional", map[string]string{"NODE_ENV": "production"}, javascriptDevelopment, true},
		{"npm ci", map[string]string{"npm_config_omit": "dev"}, javascriptDevelopment, true},
		{"yarn install --production=false", map[string]string{"NODE_ENV": "production"}, javascriptDevelopment, false},
		{"pnpm install --include=dev", map[string]string{"NODE_ENV": "production"}, javascriptDevelopment, true},
		{"npm ci --omit=optional", nil, javascriptOptional, true},
		{"npm ci --omit optional", nil, javascriptOptional, true},
		{"npm ci --no-optional", nil, javascriptOptional, true},
		{"yarn install --ignore-optional", nil, javascriptOptional, true},
		{"pnpm install --no-optional", nil, javascriptOptional, true},
		{"npm ci", map[string]string{"npm_config_omit": "dev optional"}, javascriptOptional, true},
		{"npm ci --omit=optional --include=optional", nil, javascriptOptional, false},
		{"npm ci --omit=dev", map[string]string{"NODE_ENV": "production"}, javascriptOptional, false},
		{"npm ci --omit=optional", nil, "", false},
	} {
		words, err := staticCommands(tc.command)
		require.NoError(t, err)
		require.Equal(t, tc.omitted, javascriptOmitsGroupCommand(tc.env, words[0], tc.group), "%s %v (%s)", tc.command, tc.env, tc.group)
	}
}

// The shell expands a command's arguments before its own assignments take
// effect, so a command whose arguments read a variable it assigns keeps
// them: folded into the step's environment, the argument would read the
// assigned value instead of the earlier one.
func TestFoldCommandEnvironmentKeepsArgumentExpansion(t *testing.T) {
	known := map[string]string{"TEST_TARGET": "unit"}
	_, _, ok := foldCommandEnvironment(`DD_ENV=ci TEST_TARGET=integration npx jest "$TEST_TARGET"`, known, "")
	require.False(t, ok)
	_, _, ok = foldCommandEnvironment(`DD_ENV=ci TEST_TARGET=integration npx jest "${TEST_TARGET}/specs"`, known, "")
	require.False(t, ok)
	run, env, ok := foldCommandEnvironment(`DD_ENV=ci npx jest "$TEST_TARGET"`, known, "")
	require.True(t, ok)
	require.Equal(t, `npx jest "$TEST_TARGET"`, run)
	require.Equal(t, map[string]string{"DD_ENV": "ci"}, env)
}

// On Jenkins, the CI command onboard --apply prescribes for a direct
// Cucumber invocation reads as the other providers read it: its scoped
// prefix and impacted-tests workaround are the command's, so the next apply
// certifies it instead of prescribing the same edit again.
func TestHandWrittenJenkinsDirectCucumberRoundTrip(t *testing.T) {
	pipeline := func(command string) string {
		return "pipeline {\n  agent { docker { image 'node:22' } }\n  environment {\n    DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n    DD_SITE = 'datadoghq.com'\n    DD_API_KEY = credentials('datadog-api-key')\n  }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        withEnv(['NODE_OPTIONS=-r dd-trace/ci/init --import dd-trace/register.js']) {\n          sh '" + command + "'\n        }\n      }\n    }\n  }\n}\n"
	}
	root := jenkinsRepository(t, pipeline("npx cucumber-js"))
	writeFile(t, root, "package.json", `{"scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "dd-trace": "5.40.0"}}`)
	require.NoError(t, os.RemoveAll(filepath.Join(root, "jest.config.js")))
	_, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	var gap *EditThenApplyGap
	require.ErrorAs(t, err, &gap)
	_, after, ok := strings.Cut(gap.Reason, "Change this CI command to exactly: ")
	require.True(t, ok, gap.Reason)
	written, _, _ := strings.Cut(after, ". Then rerun onboard --apply")
	require.True(t, strings.HasPrefix(written, scopedPrefix("DD_CUCUMBER_NODE_OPTIONS")+" "+cucumberImpactedWorkaround+" "), written)

	writeFile(t, root, "Jenkinsfile", pipeline(written))
	steps := jenkinsJobs(t, pipeline(written))["Test"].Steps
	last := steps[len(steps)-1]
	require.Equal(t, "DD_CUCUMBER_NODE_OPTIONS", last.scopedVariable, "%+v", last)
	require.True(t, last.impactedWorkaround)
	require.Equal(t, "npx --no-install nyc -- cucumber-js", last.Run)
	plan, err := PlanJavaScriptSetup(root, "cucumber", "5.133.0", "datadoghq.com", ">=18")
	require.NoError(t, err)
	require.True(t, plan.Configured(), "the prescribed command completes the setup")
	require.Equal(t, StatusCompatible, handWrittenRuntime(t, root, "cucumber").Status)
}

// onboard --agent and onboard --apply agree on a hand-written setup that
// onboard --apply refuses as incomplete, such as a preload in the job's
// variables that reaches the dependency install too: the handoff asks for
// the edit that completes the setup, with the reason onboard --apply gives,
// and keeps it, rather than handing off an apply that refuses it. Once the
// setup is complete, it hands off onboard --apply, which accepts it.
func TestHandWrittenIncompleteSetupHandoffAgreesWithApply(t *testing.T) {
	agentless := handWrittenAgentless("-r dd-trace/ci/init")
	// jobPreload sets the preload in the job's variables, which reach every
	// command of the job, npm ci too.
	jobPreload := func(config string) string {
		return regexp.MustCompile(`( *)DD_SITE: 'datadoghq.com'\n`).ReplaceAllString(config, "${0}${1}NODE_OPTIONS: '-r dd-trace/ci/init'\n")
	}
	start := func(t *testing.T) (AgentResult, error) {
		t.Helper()
		var out bytes.Buffer
		err := StartWithOptions(&out, StartOptions{Format: "json"})
		var state AgentResult
		require.NoError(t, json.Unmarshal(out.Bytes(), &state), out.String())
		return state, err
	}
	// agree checks that onboard --agent asks for exactly the edit onboard
	// --apply refuses the setup without.
	agree := func(t *testing.T, root, framework, problem string) {
		t.Helper()
		_, err := PlanJavaScriptSetup(root, framework, "5.133.0", "datadoghq.com", ">=18")
		var gap *SetupGap
		require.ErrorAs(t, err, &gap)
		require.Contains(t, gap.Reason, problem)
		state, err := start(t)
		require.Error(t, err)
		require.Equal(t, "needs_input", state.Status)
		require.Equal(t, "ci_setup_required", state.Code)
		require.Equal(t, "apply_source_edit", state.NextAction)
		require.Contains(t, state.Reason, "Jobs that set it up by hand keep their setup")
		require.Contains(t, state.Reason, strings.TrimSuffix(gap.Reason, " Then rerun onboard --apply."), "the reason onboard --apply gives")
		require.Contains(t, state.Instruction, problem)
		require.Contains(t, state.Instruction, "Run resume_command once, after the edits above")
		require.Contains(t, strings.Join(state.ResumeCommand, " "), "onboard --framework "+framework+" --apply --yes")
	}
	for _, provider := range handWrittenProviders {
		t.Run(provider.name, func(t *testing.T) {
			config := jobPreload(provider.config(with(agentless, "NODE_OPTIONS", ""), []string{"npm ci"}, "npx jest"))
			require.Contains(t, config, "NODE_OPTIONS: '-r dd-trace/ci/init'")
			root := handWrittenRepository(t, provider, handWrittenPackage, config, nil)
			agree(t, root, "jest", "NODE_OPTIONS preloads dd-trace for npm ci at ")
			agree(t, root, "jest", "Set NODE_OPTIONS for the test commands only (inline on each, or for the commands after the install).")

			// Every other piece the setup lacks is asked for the same way.
			writeFile(t, root, provider.file, provider.config(with(agentless, "DD_SITE", "datadoghq.eu"), []string{"npm ci"}, "npx jest"))
			agree(t, root, "jest", "set DD_SITE=datadoghq.com")
			writeFile(t, root, provider.file, provider.config(with(agentless, "DD_API_KEY", "0123456789abcdef"), []string{"npm ci"}, "npx jest"))
			agree(t, root, "jest", "never commit the key")

			// The preload set after the install completes it.
			writeFile(t, root, provider.file, provider.config(agentless, []string{"npm ci"}, "npx jest"))
			plan, err := PlanJavaScriptSetup(root, "jest", "5.133.0", "datadoghq.com", ">=18")
			require.NoError(t, err)
			require.True(t, plan.Configured())
			state, err := start(t)
			require.NoError(t, err)
			require.Equal(t, "ready", state.Status)
			require.Contains(t, strings.Join(state.NextCommand, " "), "onboard --framework jest --apply --yes")
		})
		// Native ESM Cucumber needs the Istanbul ESM loader in the
		// hand-written NODE_OPTIONS, which onboard --apply cannot add.
		t.Run(provider.name+"/native ESM Cucumber", func(t *testing.T) {
			pkg := `{"type": "module", "scripts": {"test": "cucumber-js"}, "devDependencies": {"@cucumber/cucumber": "11.0.1", "nyc": "17.1.0", "@istanbuljs/esm-loader-hook": "0.3.0", "dd-trace": "5.40.0"}}`
			esm := handWrittenAgentless("-r dd-trace/ci/init --import dd-trace/register.js")
			root := handWrittenRepository(t, provider, pkg, provider.config(esm, []string{"npm ci"}, "npm test"), nil)
			agree(t, root, "cucumber", "without the "+cucumberESMLoaderHook)
		})
	}
	// So does Jenkins, whose plugin setup onboard --apply writes.
	const pipeline = "pipeline {\n  agent { docker { image 'node:22' } }\n  environment {\n    DD_CIVISIBILITY_AGENTLESS_ENABLED = 'true'\n    DD_API_KEY = credentials('datadog-api-key')\n    NODE_OPTIONS = '-r dd-trace/ci/init'\n  }\n  stages {\n    stage('Test') {\n      steps {\n        sh 'npm ci'\n        sh 'npx jest'\n      }\n    }\n  }\n}\n"
	root := jenkinsRepository(t, pipeline)
	writeFile(t, root, "package.json", handWrittenPackage)
	agree(t, root, "jest", "NODE_OPTIONS preloads dd-trace for npm ci at Jenkinsfile:")
}
