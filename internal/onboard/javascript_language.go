// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"errors"
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
// CircleCI and Buildkite.
func (javascriptDiscovery) supportsCIProvider(provider string) bool {
	return provider == gitHubActionsProvider || provider == gitLabCIProvider || provider == circleCIProvider || provider == buildkiteProvider
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

func (javascriptDiscovery) ciSetupRequest(root, framework, site string, terms ciSetupTerms) ciSetupRequest {
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

// A stated Node is compared like a node:<version> image tag's.
var javascriptStatedRuntime = &statedRuntime{
	Names:   []string{"node", "js"},
	Display: "Node",
	Version: regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`),
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
	runtime: func(finding RuntimeFinding) string { return finding.Node },
}

func (javascriptDiscovery) statedRuntime() *statedRuntime { return javascriptStatedRuntime }

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
