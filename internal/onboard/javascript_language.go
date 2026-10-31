// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
)

// javascriptDiscovery onboards Node.js test frameworks with dd-trace.
type javascriptDiscovery struct{}

func (javascriptDiscovery) name() string { return "javascript" }

func (javascriptDiscovery) actionLanguage() string { return "js" }

func (javascriptDiscovery) bootstrap(framework string) string {
	if framework == "cucumber" {
		return javascriptBootstrap + "\n\nFor Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false on the test step. This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected."
	}
	return javascriptBootstrap
}

func (javascriptDiscovery) resolvesTestSteps(framework string) bool {
	return slices.Contains([]string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio"}, framework)
}

func (javascriptDiscovery) coordinated() bool { return true }

func (javascriptDiscovery) automaticCISetup() bool { return true }

// JavaScript setup edits and checks GitHub Actions workflows, GitLab CI,
// CircleCI and Buildkite, and checks Jenkinsfiles.
func (javascriptDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider || provider == jenkinsProvider
}

// Outside GitHub Actions the installer appends its require preload to the
// current NODE_OPTIONS and exports the ESM loader path for an explicit
// --import.
func (javascriptDiscovery) setupShellExports(current map[string]string) (map[string]string, map[string]string) {
	options := strings.TrimSpace(current["NODE_OPTIONS"] + " -r ${{ env.DD_TRACE_PACKAGE }}")
	return map[string]string{"NODE_OPTIONS": options}, map[string]string{"DD_TRACE_ESM_IMPORT": "${{ env.DD_TRACE_ESM_IMPORT }}", "DD_TRACE_PACKAGE": "${{ env.DD_TRACE_PACKAGE }}"}
}

// On GitHub Actions onboard --apply owns the JavaScript setup check. Other
// providers share the generic one, for setup agents apply by hand.
func (javascriptDiscovery) ciSetupConfigured(root, framework, site string) bool {
	provider, err := currentCIProvider(root)
	if err != nil || provider.sharedSyntax() {
		return false
	}
	return providerSetupConfigured(root, provider, "javascript", framework, site, "js-tracer-version", exactRelease)
}

func (javascriptDiscovery) agentInstructions(string, string) string { return "" }

func (javascriptDiscovery) frameworks() []string {
	return []string{"jest", "vitest", "mocha", "playwright", "cypress", "cucumber", "webdriverio"}
}

func (javascriptDiscovery) resolveStep(string, ciWorkflow, runtimeJob, runtimeStep, string) (commandResolution, bool) {
	return commandResolution{}, false
}

func (javascriptDiscovery) localCommands(string, string, string) ([]TestCommand, bool) {
	return nil, false
}

func (javascriptDiscovery) variantValue(ciWorkflow, runtimeJob, runtimeStep, map[string]any, string, string) (string, bool, error) {
	return "", false, nil
}

func (javascriptDiscovery) skipsUnsupportedNode() bool { return true }

func (javascriptDiscovery) instructions(string) string { return "" }

func (javascriptDiscovery) onboardingScope(framework string) (bool, bool) {
	discover := slices.Contains([]string{"mocha", "playwright", "cypress", "cucumber", "webdriverio"}, framework)
	return discover, slices.Contains([]string{"webdriverio", "cypress", "cucumber"}, framework)
}

func (javascriptDiscovery) keepsCISetting(string) bool { return false }

func (javascriptDiscovery) jobConsumer(string, ciWorkflow, runtimeJob, string) (bool, bool) {
	return false, false
}

const javascriptBootstrap = "Use Node.js 22 or newer. GitHub Actions cannot set NODE_OPTIONS for later steps, so merge this into the existing test step, preserving any current Node options:\n\n```yaml\nenv:\n  NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}\n```\n\nThe --import loader is required for Vitest and other ESM tests."

// esmImportCommand registers the tracer's ESM loader after a setup that runs
// in the job's shell, as the GitLab setup script's README prescribes.
// It runs only when the setup exported the loader path: without the API key
// or after a failed install, a bare --import would stop Node.
const esmImportCommand = `if [ -n "${DD_TRACE_ESM_IMPORT:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_ESM_IMPORT"; fi`

// pluginESMImportCommand registers the ESM loader of the dd-trace the
// Datadog Jenkins plugin installs, which exports its package path as
// DD_TRACE_PATH; without the plugin's setup it adds nothing.
const pluginESMImportCommand = `if [ -n "${DD_TRACE_PATH:-}" ]; then export NODE_OPTIONS="${NODE_OPTIONS:-} --import $DD_TRACE_PATH/register.js"; fi`

func (javascriptDiscovery) ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	if terms.Plugin {
		return javascriptPluginSetupRequest(root, framework, site, terms)
	}
	request := ciSetupRequest{Language: "js", Site: site, TracerInput: "js-tracer-version", TracerVersion: "<exact dd-trace release>"}
	if setupImportsESM(framework) || framework == "mocha" {
		request.After = []string{esmImportCommand}
	}
	request.Notes = append(request.Notes, "The "+terms.Installer+" appends its dd-trace preload to NODE_OPTIONS for "+terms.Later+". Run the tests on Node.js 18 or newer (the job's image). After "+terms.Entry+", extend NODE_OPTIONS ("+terms.Persist(`export NODE_OPTIONS="$NODE_OPTIONS ..."`)+") instead of replacing it, including in an inline NODE_OPTIONS=... prefix of the test command.")
	if framework == "mocha" {
		request.Notes = append(request.Notes, "Keep the export after "+terms.Entry+" only for Mocha ESM test files; CommonJS suites need only the preload.")
	}
	if note := setupImageNote(root, "javascript", framework); note != "" {
		request.Notes = append(request.Notes, note)
	}
	if framework == "cucumber" {
		request.Notes = append(request.Notes, "For Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false in "+terms.Variables+". This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected.")
	}
	return request
}

// javascriptPluginSetupRequest is the JavaScript part of a CI server plugin's
// setup: the plugin installs the latest dd-trace and sets NODE_OPTIONS itself.
func javascriptPluginSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
	request := ciSetupRequest{Language: "js", Site: site, Service: packageName(root)}
	if setupImportsESM(framework) || framework == "mocha" {
		request.After = []string{pluginESMImportCommand}
	}
	request.Notes = append(request.Notes, "The "+terms.Installer+" installs the latest dd-trace release with the build agent's default npm when each build runs (it cannot pin a release) and prepends its dd-trace preload to NODE_OPTIONS for "+terms.Later+". ddtest validates locally with the latest release and reports that it floats. The agents need npm on their default PATH, and the tests a Node version the latest dd-trace release supports (its package.json engines): ddtest checks the stated CI Node against the latest release, and a Node it excludes cannot use the "+terms.Installer+".",
		"Set Node options inside the sh script that runs the tests (export NODE_OPTIONS=\"$NODE_OPTIONS ...\" before the test command): the "+terms.Installer+" sets NODE_OPTIONS once per build, so a NODE_OPTIONS in a stage's environment directive or withEnv is overwritten.")
	if framework == "mocha" {
		request.Notes = append(request.Notes, "Keep the ESM line only for Mocha ESM test files; CommonJS suites need only the preload.")
	}
	if note := setupUnsupportedNote(root, "javascript", framework); note != "" {
		request.Notes = append(request.Notes, note)
	}
	if framework == "cucumber" {
		request.Notes = append(request.Notes, "For Cucumber, also set DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED=false in "+terms.Variables+". This avoids a tracer crash on Cucumber Background/Rule nodes; basic reporting is unaffected.")
	}
	return request
}

// Scoped initialization (Cucumber, WebdriverIO) edits package scripts for
// a per-step variable; outside GitHub Actions it is set up manually.
func (javascriptDiscovery) automaticProviderSetup(framework string) bool {
	return framework != "cucumber" && framework != "webdriverio"
}

// Node versions no tracer supports are skipped through skipsUnsupportedNode.
func (javascriptDiscovery) unsupportedImage(string) string { return "" }

func (javascriptDiscovery) runtimeVariable(name string) bool {
	return slices.Contains([]string{"NODE_OPTIONS", "NODE_PATH", "NODE_ENV"}, name) || strings.HasPrefix(strings.ToLower(name), "npm_config_")
}

func (javascriptDiscovery) cacheVariable(name string) bool {
	return slices.Contains([]string{"npm_config_store_dir", "PNPM_STORE_DIR", "PNPM_HOME"}, name)
}

// setupNodeSource is the source of a Node actions/setup-node selects.
const setupNodeSource = "actions/setup-node"

// javascriptStatedVersion is a stated Node version.
var javascriptStatedVersion = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

// A stated Node is compared like a node:<version> image tag's.
var javascriptStatedRuntime = &statedRuntime{
	Names:   []string{"node", "js"},
	Display: "Node",
	Version: javascriptStatedVersion,
	Example: "22, 22.11 or 22.11.0",
	base:    javascriptImageRuntime,
	// setup-node (also through a node-version-file) takes node-semver
	// selectors; an image tag, a node step or nvm is compared as written.
	compare: func(configured, source, stated string) (bool, bool) {
		if source == setupNodeSource {
			return nodeSelectorAgrees(configured, stated)
		}
		return sameVersionLine(configured, stated), true
	},
	configured: func(root string, workflow ciWorkflow, job runtimeJob) (string, string) {
		rows, _, err := jobMatrix(job)
		return installedRuntime(rows, err, func(row map[string]any) (string, string, bool) {
			if active, err := runtimeJobActive(workflow, job, row); err == nil && !active {
				return "", "", false
			}
			// The tests run on the last selection before them.
			version, source := "", ""
			for _, step := range stepsThroughFirstTest(job.Steps, func(step runtimeStep) bool { return javascriptTestStep(root, workflow, job, step, row) }) {
				// A step that may not run selects nothing for certain.
				if active, err := runtimeStepActive(workflow, job, step, row); err != nil || !active {
					continue
				}
				if selected := step.Runtime["js"]; selected != "" {
					version, source = selected, step.Source
				} else if uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@"); uses == "actions/setup-node" {
					if selected, err := setupNodeVersion(root, step, row); err == nil && selected != "" {
						version, source = selected, setupNodeSource
					}
				} else if selected := nvmRuntime(step.Run); selected != "" {
					version, source = selected, "nvm"
				}
			}
			return version, source, true
		})
	},
	check: func(ctx context.Context, root, framework string) RuntimeCheck {
		// Offline: no Node, tracer or action metadata is needed to find
		// unknown host runtimes.
		return checkCIRuntimes(withOfflineRuntimeMetadata(ctx), root, nil, func(context.Context, string, string) (tracerRequirement, error) {
			return tracerRequirement{}, errors.New("tracer metadata is not read offline")
		}, framework)
	},
	runtime:        func(finding RuntimeFinding) string { return finding.Node },
	testStep:       javascriptTestStep,
	shellSelection: javascriptShellSelection,
	fileVersion: func(line string) string {
		if version := strings.TrimPrefix(line, "v"); javascriptStatedVersion.MatchString(version) {
			return version
		}
		return ""
	},
}

// javascriptShellSelection reads a Node version manager's selection: nvm,
// fnm or nodenv's use, install or shell, n <version>, and the scripts and
// init commands that load a manager, which select its default.
func javascriptShellSelection(words []string) (version, versionFile string, ok bool) {
	if len(words) == 0 {
		return "", "", false
	}
	var flags, positional []string
	for _, word := range words[min(2, len(words)):] {
		if strings.HasPrefix(word, "-") {
			flags = append(flags, word)
		} else {
			positional = append(positional, word)
		}
	}
	named := func(file string) (string, string, bool) {
		if slices.ContainsFunc(flags, func(flag string) bool { return strings.HasPrefix(flag, "--lts") }) {
			return "", "", true
		}
		switch {
		case len(positional) == 0:
			return "", file, true
		case len(positional) == 1 && javascriptStatedVersion.MatchString(strings.TrimPrefix(positional[0], "v")):
			return strings.TrimPrefix(positional[0], "v"), "", true
		}
		// An alias, such as default or lts/*: a version ddtest cannot tell.
		return "", "", true
	}
	sub := ""
	if len(words) > 1 {
		sub = words[1]
	}
	switch path.Base(words[0]) {
	case "nvm":
		if sub == "use" || sub == "install" {
			return named(".nvmrc")
		}
	case "fnm":
		if sub == "use" || sub == "install" {
			return named("")
		}
		if sub == "env" {
			return "", "", true
		}
	case "nodenv":
		if sub == "shell" || sub == "local" || sub == "global" {
			return named("")
		}
		if sub == "init" {
			return "", ".node-version", true
		}
	case "n":
		if len(words) == 2 && !strings.HasPrefix(sub, "-") && !slices.Contains([]string{"run", "exec", "which", "rm", "ls", "lsr", "prune", "uninstall", "doctor", "help"}, sub) {
			positional, flags = []string{sub}, nil
			return named("")
		}
	case "source", ".":
		if sub != "" && path.Base(sub) == "nvm.sh" && len(words) == 2 {
			return "", "", true
		}
	}
	return "", "", false
}

func (javascriptDiscovery) statedRuntime() *statedRuntime { return javascriptStatedRuntime }

func (javascriptDiscovery) ciPluginLanguage() string { return "JAVASCRIPT" }

// The Datadog Jenkins plugin prepends its dd-trace preload to NODE_OPTIONS
// and exports DD_TRACE_PATH, the installed dd-trace package, whose
// register.js is the ESM loader.
// The Datadog Jenkins plugin's JavascriptConfigurator prepends its preload
// to the NODE_OPTIONS of the first step it configures on an agent, and
// DatadogTracerConfigurator reuses that value for every later step there.
func (javascriptDiscovery) ciPluginComposes(key string) string {
	if key != "NODE_OPTIONS" {
		return ""
	}
	return "on each agent it sets NODE_OPTIONS for every step to its preload followed by the NODE_OPTIONS of the first step it configures there, so this value is lost, or, when its step is that first one (on a fresh workspace, and so on every build of an ephemeral agent, the first step after the workspace exists), it reaches every later step on the agent. ddtest validates with the NODE_OPTIONS where the datadog option is (the pipeline's environment, or the stage's)"
}

// Node version managers: n 20, nvm use, fnm use, volta, nodenv, and
// sourcing nvm.sh, which selects nvm's default.
func (javascriptDiscovery) selectsShellRuntime(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch name := path.Base(words[0]); name {
	case "n":
		return len(words) > 1 // n alone opens an interactive menu.
	case "nvm", "fnm":
		return len(words) > 1 && slices.Contains([]string{"use", "install", "exec", "run", "env", "alias"}, words[1])
	case "volta", "nodenv":
		return true
	case "source", ".":
		return len(words) > 1 && path.Base(words[1]) == "nvm.sh"
	}
	return false
}

// ciPluginContainerEffect: a real Jenkins run showed the plugin's
// NODE_OPTIONS reaching docker.image(…).inside and agent { docker } steps.
func (javascriptDiscovery) ciPluginContainerEffect() string {
	return "for JavaScript, the plugin's NODE_OPTIONS preloads the dd-trace it installed into the workspace with the agent's npm, so Node processes in the container load it only by accident, because Jenkins mounts the workspace there at the same path (with another path, every Node process fails to start), and on an agent without npm the plugin sets nothing, so the tests run untraced without any error"
}

func (javascriptDiscovery) ciPluginExports(current map[string]string) (map[string]string, map[string]string) {
	options := strings.TrimSpace("-r ${{ env.DD_TRACE_PACKAGE }} " + current["NODE_OPTIONS"])
	return map[string]string{"NODE_OPTIONS": options}, map[string]string{"DD_TRACE_PATH": "${{ env.DD_TRACE_PATH }}"}
}

// javascriptTestStep reports whether step may start a test of any JavaScript
// framework on a matrix row, as checkCIRuntimes identifies test steps: a
// statement is checked before the framework is known. A step a condition
// skips on the row is not one.
func javascriptTestStep(root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any) bool {
	if active, err := runtimeStepActive(workflow, job, step, row); err == nil && !active {
		return false
	}
	for _, framework := range (javascriptDiscovery{}).frameworks() {
		resolved, _, err := resolveVariantStep(workflow, job, step, row, framework)
		if err != nil {
			resolved = step
		}
		resolution := resolveTestStep(root, workflow, job, resolved, "javascript", framework)
		if !resolution.Review && startsTests(root, workflow, job, resolved, resolution, framework) {
			return true
		}
	}
	return false
}

// jobBaseNode is the Node a job starts with on a matrix row (nil: the job
// without a row): its image's, else the one the user stated for the row's
// host, else "". A GitHub-hosted row of a matrix that also runs on
// self-hosted runners keeps GitHub's runtime.
func jobBaseNode(job runtimeJob, row map[string]any) (node, source string) {
	if node := imageNode(job.Image); node != "" {
		return node, "image " + job.Image
	}
	if node := job.Stated["js"]; node != "" && runtimeHost(job, row, "Node") != "" {
		return node, statedRuntimeSource
	}
	return "", ""
}

// nodeImageTag reads the tag of a Node image, numeric or not.
var nodeImageTag = regexp.MustCompile(`^(?:(?:docker\.io/)?(?:library/)?node|cimg/node|circleci/node):([^@]+)`)

// javascriptImageRuntime is the Node version a job's image selects, or a
// Node image's floating tag (lts, current, or none): that image's runtime is
// not the user's to state.
func javascriptImageRuntime(job runtimeJob) string {
	if node := imageNode(job.Image); node != "" {
		return node
	}
	if match := nodeImageTag.FindStringSubmatch(withImplicitTag(job.Image)); match != nil {
		return match[1]
	}
	return ""
}
