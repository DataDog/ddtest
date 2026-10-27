// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/Masterminds/semver/v3"
	"github.com/kballard/go-shellquote"
	"go.yaml.in/yaml/v3"
	"mvdan.cc/sh/v3/syntax"
)

// RuntimeCheck is separate from local test compatibility: a local run cannot
// validate the other runtimes selected by a CI workflow.
type RuntimeCheck struct {
	Status string           `json:"status"`
	Reason string           `json:"reason"`
	Jobs   []RuntimeFinding `json:"jobs,omitempty"`
	Review []RuntimeFinding `json:"review,omitempty"`
}

// RuntimeFinding records the metadata used to check one CI runtime.
// GuardedActionBranch explains a test step whose Datadog action is skipped by
// its own condition on some branch. The step still runs, uninstrumented.
const GuardedActionBranch = "The Datadog action's condition is false for this branch (for example, no API key on forks), so this test step runs uninstrumented by design. This branch is not validated as instrumented."

// WindowsRunnerBranch explains a Windows row whose Datadog action is skipped:
// ddtest doesn't set up or validate Windows runners locally.
const WindowsRunnerBranch = "Windows runner: the Datadog action is skipped here because ddtest doesn't set up or validate Windows runners locally. These tests run uninstrumented; add manual initialization to instrument them."

type RuntimeFinding struct {
	// Location names the CI entry, including its matrix values.
	Location string `json:"location,omitempty"`
	// Runtime is the version a non-Node runtime selects, such as Ruby's.
	Runtime         string          `json:"runtime,omitempty"`
	Workflow        string          `json:"workflow"`
	Job             string          `json:"job"`
	Step            int             `json:"step,omitempty"`
	Source          string          `json:"source,omitempty"`
	Command         string          `json:"command,omitempty"`
	Resolution      string          `json:"resolution,omitempty"`
	Node            string          `json:"node,omitempty"`
	NodeSource      string          `json:"node_source,omitempty"`
	NodeResolution  *NodeResolution `json:"node_resolution,omitempty"`
	Action          string          `json:"action,omitempty"`
	Tracer          string          `json:"tracer,omitempty"`
	TracerRequested string          `json:"tracer_requested,omitempty"`
	TracerFloating  bool            `json:"tracer_floating"`
	Requirement     string          `json:"requirement,omitempty"`
	Status          string          `json:"status"`
	Reason          string          `json:"reason"`
	Code            string          `json:"code,omitempty"`
	// setupInputs are the inputs of the Datadog setup action that precedes
	// this entry, for a language's own setup checks. Not serialized.
	setupInputs map[string]string
}

// A composite call's condition sees the caller's environment and string inputs,
// before any descendant overrides. These snapshots are discovery data only.
type runtimeConditionScope struct {
	Expression string
	Env        map[string]string
	Inputs     map[string]string
}

type runtimeStep struct {
	Native           *nativeCommand          `yaml:"-"`
	NewShell         bool                    `yaml:"-"`
	ActionNode       string                  `yaml:"-"`
	Inputs           map[string]string       `yaml:"-"`
	ID               string                  `yaml:"id"`
	Number           int                     `yaml:"-"`
	Source           string                  `yaml:"-"`
	Parents          []runtimeConditionScope `yaml:"-"`
	ResolutionError  string                  `yaml:"-"`
	RemoteGrid       string                  `yaml:"-"`
	WorkingDirectory string                  `yaml:"working-directory"`
	Shell            string                  `yaml:"shell"`
	Env              map[string]string       `yaml:"env"`
	LiteralEnv       map[string]string       `yaml:"-"` // Values already resolved by the source shell.
	Uses             string                  `yaml:"uses"`
	Run              string                  `yaml:"run"`
	If               string                  `yaml:"if"`
	With             map[string]string       `yaml:"with"`
}

type runtimeJob struct {
	Node, NodeSource string            `yaml:"-"`
	Gaps             []string          `yaml:"-"`
	Container        any               `yaml:"container"`
	Context          map[string]string `yaml:"-"`
	RunsOn           any               `yaml:"runs-on"`
	Defaults         ciDefaults        `yaml:"defaults"`
	Env              map[string]string `yaml:"env"`
	If               string            `yaml:"if"`
	// A job that calls a reusable workflow, and the secrets it passes.
	Uses     string `yaml:"uses"`
	Secrets  any    `yaml:"secrets"`
	Strategy struct {
		Matrix any `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []runtimeStep `yaml:"steps"`
}

type tracerRequirement struct{ Version, Node, Requested string }

type requirementResolver func(context.Context, string, string) (tracerRequirement, error)

// CheckCIRuntimes checks Jest jobs in GitHub Actions without executing workflow
// code or contacting Datadog. Unknown expressions are never treated as a pass.
func CheckCIRuntimes(ctx context.Context, root string, frameworks ...string) RuntimeCheck {
	client := &http.Client{Timeout: 10 * time.Second}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkCIRuntimes(ctx, root, setupNodeResolver(client), func(ctx context.Context, action, version string) (tracerRequirement, error) {
		value, err := cachedCIRequirement(action+"\n"+version, func() (tracerRequirement, error) {
			return resolveRequirement(ctx, client, action, version, platform.NewJavaScript().ResolveRegistryTracer)
		})
		if err != nil {
			if local, ok := installedTracerRequirement(root, version); ok {
				return local, nil
			}
		}
		return value, err
	}, frameworks...)
}

type ciRequirementResult struct {
	value tracerRequirement
	err   error
}

// --all checks every configuration in its own copy. Remember lookups for the
// whole invocation, failures included, so a registry outage costs one bounded
// wait per CI selector rather than one per configuration.
var ciRequirements sync.Map

func cachedCIRequirement(key string, resolve func() (tracerRequirement, error)) (tracerRequirement, error) {
	if cached, ok := ciRequirements.Load(key); ok {
		result := cached.(ciRequirementResult)
		return result.value, result.err
	}
	value, err := resolve()
	ciRequirements.Store(key, ciRequirementResult{value, err})
	return value, err
}

// An exact CI pin that matches the installed tracer needs no registry lookup:
// the installed package declares the same Node requirement.
func installedTracerRequirement(root, version string) (tracerRequirement, bool) {
	if _, err := semver.StrictNewVersion(version); err != nil {
		return tracerRequirement{}, false
	}
	data, err := os.ReadFile(filepath.Join(root, "node_modules", "dd-trace", "package.json"))
	if err != nil {
		return tracerRequirement{}, false
	}
	var manifest struct {
		Version string `json:"version"`
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Version != version || manifest.Engines.Node == "" {
		return tracerRequirement{}, false
	}
	return tracerRequirement{Version: version, Node: manifest.Engines.Node, Requested: version}, true
}

func checkCIRuntimes(ctx context.Context, root string, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) RuntimeCheck {
	ctx = WithCypressMetadataCache(ctx)
	framework := "jest"
	if len(frameworks) > 0 {
		framework = frameworks[0]
	}
	result := RuntimeCheck{Status: StatusNotApplicable, Reason: "No candidate CI " + framework + " test commands found; CI runtime compatibility was not checked."}
	workflows, err := readWorkflows(root)
	if err != nil {
		return RuntimeCheck{Status: StatusInconclusive, Reason: err.Error()}
	}
	type cachedRequirement struct {
		value tracerRequirement
		err   error
	}
	cache := map[string]cachedRequirement{}
	cachedResolve := func(ctx context.Context, action, version string) (tracerRequirement, error) {
		key := action + "\n" + version
		if value, ok := cache[key]; ok {
			return value.value, value.err
		}
		value, err := resolve(ctx, action, version)
		cache[key] = cachedRequirement{value, err}
		return value, err
	}
	for _, workflow := range workflows {
		if workflow.Provider != "github" && workflow.Provider != "" {
			result.Jobs = append(result.Jobs, checkNativeRuntimes(ctx, root, workflow, framework, cachedResolve)...)
			continue
		}
		path := workflow.Path
		for _, name := range slices.Sorted(maps.Keys(workflow.Jobs)) {
			job := workflow.Jobs[name]

			resolutions := make([]commandResolution, len(job.Steps))
			consumer := false
			for i, step := range job.Steps {
				resolutions[i] = resolveTestStep(root, workflow, job, step, "javascript", framework)
				if resolutions[i].Review {
					result.Review = append(result.Review, RuntimeFinding{Workflow: path, Job: name, Step: stepNumber(step, i), Command: step.Run, Status: StatusNotExercised, Code: CodeUnresolvedCommand, Reason: resolutions[i].Reason})
					resolutions[i] = commandResolution{}
				}
				consumer = consumer || resolutions[i].Matched || resolutions[i].Reason != "" && stepMayStartTests(root, workflow, job, step, framework)
			}
			if !consumer {
				// No step can start a selected test, so no instrumented runtime runs here.
				for i, step := range job.Steps {
					if resolutions[i].Reason != "" {
						result.Review = append(result.Review, RuntimeFinding{Workflow: path, Job: name, Step: stepNumber(step, i), Command: step.Run, Status: StatusNotExercised, Code: CodeUnresolvedCommand, Reason: "Not a " + framework + " test consumer: " + resolutions[i].Reason})
					}
				}
				continue
			}
			rows, _, err := jobMatrix(job)
			if err != nil {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: StatusInconclusive, Code: CodeInvalidMatrix, Reason: err.Error()})
				continue
			}
			if len(rows) == 0 {
				result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: path, Job: name, Status: StatusInconclusive, Code: CodeInvalidMatrix, Reason: "Static matrix has no entries; no instrumented CI runtime could be checked."})
				continue
			}
			for _, row := range rows {
				rowResolutions := slices.Clone(resolutions)
				rowJob := job
				if framework == "cypress" {
					if label, ok := job.RunsOn.(string); ok {
						resolved, err := runtimeValue(label, row)
						if err == nil {
							rowJob.RunsOn = resolved
						}
					}
				}
				rowJob.Steps = slices.Clone(job.Steps)
				rowCandidate := false
				for i, step := range job.Steps {
					resolved, _, resolveErr := resolveVariantStep(workflow, job, step, row, framework)
					if uses, _, _ := strings.Cut(strings.ToLower(resolved.Uses), "@"); uses == "actions/setup-node" {
						version, versionErr := setupNodeVersion(root, resolved, row)
						resolved.With = maps.Clone(resolved.With)
						if resolved.With == nil {
							resolved.With = map[string]string{}
						}
						resolved.With["node-version"] = version
						if versionErr != nil {
							resolved.ResolutionError = versionErr.Error()
						}
					}
					rowJob.Steps[i] = resolved
					rowResolutions[i] = resolveTestStep(root, workflow, rowJob, resolved, "javascript", framework)
					rowResolutions[i] = resolveFactBackedRuntime(ctx, root, workflow, rowJob, resolved, framework, rowResolutions[i])
					if rowResolutions[i].Review {
						rowResolutions[i] = commandResolution{}
					} else if resolveErr != nil && (rowResolutions[i].Matched || rowResolutions[i].Reason != "") {
						// Setup steps such as npm ci are not test commands, even
						// when their environment holds a secret.
						rowResolutions[i].Reason = resolveErr.Error()
					}
					rowCandidate = rowCandidate || rowResolutions[i].Matched || rowResolutions[i].Reason != ""
				}
				if !rowCandidate {
					continue
				}
				jobs := []runtimeJob{rowJob}
				if framework == "cypress" {
					for i, step := range rowJob.Steps {
						uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")

						if uses == "cypress-io/github-action" && step.With["command"] == "" && cypressInputBool(step.With["runTests"], true) {
							node, metadataErr := CypressActionNode(ctx, step.Uses)
							rowJob.Steps[i].ActionNode = node
							if metadataErr != nil {
								rowResolutions[i].Reason = "Cannot resolve Cypress action Node runtime: " + metadataErr.Error()
							}
						}
					}
					var contextErr error
					jobs, contextErr = cypressContextJobs(root, workflow, rowJob)
					if contextErr != nil {
						result.Jobs = append(result.Jobs, RuntimeFinding{Workflow: workflow.Path, Job: name, Status: "inconclusive", Reason: contextErr.Error()})
						continue
					}
				}

				// Context expansions of one row (such as a guarded action's
				// branches) can repeat a step's finding; report each once.
				seen := map[string]bool{}
				for _, contextJob := range jobs {
					for _, finding := range checkRuntimeJob(ctx, root, workflow, name, contextJob, rowResolutions, row, resolveNode, cachedResolve, framework) {
						key, _ := json.Marshal(finding)
						if !seen[string(key)] {
							seen[string(key)] = true
							result.Jobs = append(result.Jobs, finding)
						}
					}
				}

			}
		}
	}
	if len(result.Jobs) == 0 {
		return result
	}
	result.Status = StatusCompatible
	result.Reason = "Every identified instrumented " + framework + " step satisfies its selected tracer's Node requirement. Other entry points listed for review are outside this check. This does not execute CI or verify the Datadog backend."
	checked := false
	for _, job := range result.Jobs {
		checked = checked || job.Status == StatusCompatible
		if job.Status == StatusIncompatible {
			result.Status = StatusIncompatible
			break
		}
		if job.Status == StatusInconclusive {
			result.Status = StatusInconclusive
		}
	}
	if !checked && result.Status == StatusCompatible {
		result.Status = StatusInconclusive
	}
	if result.Status != StatusCompatible {
		result.Reason = "CI runtime validation is incomplete or incompatible. Fix known incompatibilities. Unknown syntax or metadata requires review; preserve the workflow and report that tool limitation without claiming CI was validated."
	}
	return result
}

type runtimeState struct {
	node, source, platform, action, tracer string
	skippedAction                          bool
}

func (state *runtimeState) trackNode(job runtimeJob, step runtimeStep, row map[string]any) {
	uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
	if uses == "actions/setup-node" {
		state.node = step.With["node-version"]
		state.source = step.Source
		state.platform = nodeDistributionPlatform(job.RunsOn, step.With["architecture"], row)
	} else if node := persistedNVMRuntime(step.Run); node != "" {
		state.node, state.source = node, step.Source
		state.platform = nodeDistributionPlatform(job.RunsOn, "", row)
	} else if changesNodeRuntime(step.Run) {
		state.node = "" // A run step cannot establish later shells' PATH without verified persistence.
		state.source = step.Source
		state.platform = nodeDistributionPlatform(job.RunsOn, "", row)
	}
}

func (state *runtimeState) trackAction(step runtimeStep, row map[string]any) error {
	selected, err := actionSelectsJavaScript(step, row)
	if err != nil || !selected {
		return err
	}

	state.action = step.Uses
	state.tracer, err = runtimeValue(step.With["js-tracer-version"], row)
	if _, explicit := step.With["js-tracer-version"]; explicit && state.tracer == "" {
		state.tracer = "latest"
	}
	return err
}

func checkRuntimeJob(ctx context.Context, root string, workflow ciWorkflow, name string, job runtimeJob, resolutions []commandResolution, row map[string]any, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) []RuntimeFinding {
	finding := RuntimeFinding{Workflow: workflow.Path, Job: name, Status: StatusInconclusive}
	var findings []RuntimeFinding
	fail := func(code, reason string) []RuntimeFinding {
		finding.Status, finding.Code, finding.Reason = StatusInconclusive, code, reason
		return append(findings, finding)
	}
	active, err := possibleJobCondition(job.If, row)
	if job.Context != nil {
		active, err = runtimeCondition(job.If, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
	}
	if err != nil {
		return fail(CodeUnresolvedCondition, err.Error())
	}
	if !active {
		finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeExcludedRuntime, "Job is explicitly excluded."
		return []RuntimeFinding{finding}
	}
	state := runtimeState{}
	if len(frameworks) > 0 && frameworks[0] == "cypress" {
		state.node = cypressContainerNode(job.Container)
		if state.node != "" {
			state.source = "container image"
		}
	}
	skippedTest := false
	for i, step := range job.Steps {
		uses, _, _ := strings.Cut(strings.ToLower(step.Uses), "@")
		resolution := resolutions[i]
		// An unresolved step that can't start a test is setup, not a test entry.
		test := resolution.Matched || resolution.Reason != "" && stepMayStartTests(root, workflow, job, step, frameworkName(frameworks))
		if uses != "actions/setup-node" && uses != githubAction && !changesNodeRuntime(step.Run) && !test {
			continue
		}
		finding = RuntimeFinding{Workflow: workflow.Path, Job: name, Step: stepNumber(step, i), Source: step.Source, Command: step.Run, Resolution: resolution.Evidence, Node: state.node, NodeSource: state.source, Status: StatusInconclusive}
		if uses == githubAction {
			finding.Action = step.Uses
		}
		active, err := stepCondition(step, row, conditionEnvironment(workflow, job, runtimeStep{}, row))
		if job.Context != nil && len(step.Parents) == 0 {
			active, err = runtimeCondition(step.If, row, conditionEnvironment(workflow, job, step, row))
		}
		if err != nil {
			if uses == "actions/setup-node" || changesNodeRuntime(step.Run) {
				finding.Node = ""
				finding.NodeResolution = nil
			}
			return fail(CodeUnresolvedCondition, err.Error())
		}
		if !active {
			skippedTest = skippedTest || test
			if job.Context != nil && step.ID != "" {
				job.Context["steps."+step.ID+".outcome"] = "skipped"
			}
			if uses == githubAction {
				selected, err := actionSelectsJavaScript(step, row)
				if err != nil && state.action == "" {
					return fail(CodeUnverifiedExclusion, err.Error())
				}
				state.skippedAction = state.skippedAction || selected
			}
			continue
		}
		if job.Context != nil && step.ID != "" {
			job.Context["steps."+step.ID+".outcome"] = "success"
		}
		if uses == githubAction && strings.HasPrefix(job.Context["scenario"], "fork_pull_request") && (strings.Contains(step.With["api_key"], "secrets.") || strings.Contains(step.With["api-key"], "secrets.")) {
			finding.Status, finding.Code, finding.Reason = StatusIncompatible, "missing_fork_guard", "Datadog installation still runs on a fork pull request without credentials. Guard installation and its preload together."
			return append(findings, finding)
		}
		if step.ResolutionError != "" {
			if uses == "actions/setup-node" || changesNodeRuntime(step.Run) {
				finding.Node = ""
				finding.NodeResolution = nil
			}
			return fail(CodeNodeResolution, step.ResolutionError)
		}
		state.trackNode(job, step, row)
		if uses == githubAction {
			if err := state.trackAction(step, row); err != nil {
				return fail(CodeTracerMetadata, err.Error())
			}
		}
		if !test {
			continue
		}
		finding.Node, finding.NodeSource = state.node, state.source
		if resolution.Reason != "" {
			// A row on a Node no dd-trace release supports is never tried, so
			// its unresolved steps can't block.
			if !UnsupportedNode(finding.Node) || state.action != "" {
				finding.Code, finding.Reason = CodeUnresolvedCommand, "Could not resolve CI test command: "+resolution.Reason
				findings = append(findings, finding)
			}
			if !resolution.Matched {
				continue
			}
		}
		findings = append(findings, evaluateRuntimeTest(ctx, root, workflow, job, step, row, resolution, state, finding, resolveNode, resolve, frameworks...))
	}
	if len(findings) == 0 && skippedTest {
		// The row runs the job without its tests, e.g. `if: matrix.node != '20'`.
		finding.Step, finding.Command, finding.Resolution = 0, "", ""
		finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeExcludedRuntime, "Test steps are skipped by their conditions on this matrix row."
		return []RuntimeFinding{finding}
	}
	if len(findings) == 0 {
		return fail(CodeUnresolvedCommand, "No active test step could be checked.")
	}
	return findings
}

func evaluateRuntimeTest(ctx context.Context, root string, workflow ciWorkflow, job runtimeJob, step runtimeStep, row map[string]any, resolution commandResolution, state runtimeState, finding RuntimeFinding, resolveNode nodeResolver, resolve requirementResolver, frameworks ...string) RuntimeFinding {
	finding.Code = ""
	if len(resolution.Tests) > 0 && resolution.Tests[0].Cypress != nil && resolution.Tests[0].Cypress.Action != "" {
		finding.Node, finding.NodeSource = step.ActionNode, step.Uses+" / runs.using"
	}
	if len(frameworks) > 0 && frameworks[0] == "mocha" && mochaInstrumentationDisabled(workflow, job, step, row) {
		finding.Status, finding.Code = StatusExcluded, CodeExcludedRuntime
		finding.Reason = "Mocha tests still run, but DD_CIVISIBILITY_ENABLED=false excludes this entry from instrumentation. This runtime was not validated with the tracer."
		if reason := checkExcludedJestBootstrap(workflow, job, step, row); reason != "" {
			finding.Status, finding.Code, finding.Reason = StatusInconclusive, CodeUnverifiedExclusion, reason
		}
		return finding
	}
	if state.action == "" {
		finding.Status, finding.Code = StatusIncompatible, CodeMissingInstrumentation
		if state.skippedAction {
			if len(frameworks) > 0 && frameworks[0] == "cypress" {
				if reason := cypressExcludedBootstrap(workflow, job, step, row); reason != "" {
					finding.Reason = reason
					return finding
				}
			}
			finding.Status, finding.Code = StatusExcluded, CodeExcludedRuntime
			if reason := checkExcludedJestBootstrap(workflow, job, step, row); reason != "" {
				finding.Status, finding.Code, finding.Reason = StatusInconclusive, CodeUnverifiedExclusion, reason
				return finding
			}
			finding.Reason = GuardedActionBranch
			if windowsRow(job, row) {
				finding.Reason = WindowsRunnerBranch
			}
			return finding
		}
		// No dd-trace release runs on this Node, so the missing action is not a
		// gap: ddtest leaves the row uninstrumented and doesn't try it.
		if UnsupportedNode(finding.Node) {
			finding.Status, finding.Code, finding.Reason = StatusExcluded, CodeUnsupportedNode, UnsupportedNodeReason(finding.Node)
			return finding
		}
		finding.Reason = "No Datadog JavaScript action runs before this test step for this matrix entry. It is not validated as instrumented."
		return finding
	}
	requirement, err := resolve(ctx, state.action, state.tracer)
	if err != nil {
		finding.Code, finding.Reason = CodeTracerMetadata, err.Error()
		return finding
	}
	finding.Action, finding.Tracer = state.action, "dd-trace@"+requirement.Version
	finding.TracerRequested = requirement.Requested
	if finding.TracerRequested == "" {
		finding.TracerRequested = state.tracer
	}
	finding.TracerFloating = finding.TracerRequested != "" && strings.TrimPrefix(finding.TracerRequested, "v") != requirement.Version
	finding.Requirement = requirement.Node
	for _, entry := range resolution.Tests {
		words, _ := shellquote.Split(entry.Command)
		if len(words) > 0 && !slices.Contains([]string{"node", "node.exe"}, words[0]) && (filepath.Base(words[0]) == "node" || filepath.Base(words[0]) == "node.exe") {
			finding.Node, finding.NodeResolution = "", nil
			finding.Code, finding.Reason = CodeRuntimeUnknown, "The test command selects an explicit Node executable; its CI version cannot be inferred from setup-node."
			return finding
		}
	}
	node := state.node
	actionAPI := len(resolution.Tests) > 0 && resolution.Tests[0].Cypress != nil && resolution.Tests[0].Cypress.Action != ""
	if actionAPI {
		node = step.ActionNode
		finding.Node, finding.NodeSource = node, step.Uses+" / runs.using"
	}
	if node == "" {
		finding.Code, finding.Reason = CodeRuntimeUnknown, "The CI Node runtime is unknown after setup; preserve the workflow and resolve it before claiming compatibility."
		return finding
	}
	if resolveNode != nil && !actionAPI {
		resolved, err := resolveNode(ctx, node, state.platform)
		if err != nil {
			finding.Node = ""
			finding.Code, finding.Reason = CodeNodeResolution, err.Error()
			return finding
		}
		finding.NodeResolution = &resolved
		node = resolved.Version
	}
	finding.Status, finding.Reason = CompareNodeRequirement(node, requirement.Node)
	if finding.Status == StatusInconclusive {
		finding.Code = CodeRuntimeUnknown
	}
	if finding.Status == StatusCompatible {
		reason := ""
		if len(frameworks) > 0 && frameworks[0] == "cucumber" {
			reason = checkCucumberBootstrap(root, workflow, job, step, row, resolution.Tests)
		} else if len(frameworks) > 0 && frameworks[0] == "webdriverio" {
			reason = checkWebdriverIOBootstrap(root, workflow, job, step, row)
		} else {
			reason = checkTestBootstrap(workflow, job, step, row, resolution.Tests, frameworks...)
		}
		if reason != "" {
			finding.Status, finding.Code, finding.Reason = StatusInconclusive, CodeUnverifiedInitialization, reason
			if reason == unverifiedPreloadReason && stepSetsNoNodeOptions(workflow, job, step) {
				// Nothing custom to preserve: the step only lacks the action's preload.
				finding.Code = CodeMissingInitialization
				finding.Reason = "The Datadog action runs, but this test step sets no NODE_OPTIONS to load its preload. Add NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }} (with --import ${{ env.DD_TRACE_ESM_IMPORT }} for ESM) to the step; ddtest onboard --apply can add it."
			}
		}
	}
	return finding
}

func changesNodeRuntime(command string) bool {
	file, ok := parseCIShell(command)
	if !ok {
		return false
	}
	changed := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			name := filepath.Base(call.Args[0].Lit())
			if name == "n" {
				changed = true
			}
			if (name == "nvm" || name == "fnm") && len(call.Args) > 1 && slices.Contains([]string{"use", "install", "exec", "env"}, call.Args[1].Lit()) {
				changed = true
			}
		}
		return true
	})
	return changed
}

func resolveRequirement(ctx context.Context, client *http.Client, action, version string, resolveRegistry func(context.Context, string) (platform.JSSelection, error)) (tracerRequirement, error) {
	if version == "" {
		_, ref, ok := strings.Cut(action, "@")
		if !ok || ref == "" {
			return tracerRequirement{}, fmt.Errorf("datadog action is missing its ref")
		}
		data, err := fetchRuntimeMetadata(ctx, client, "https://raw.githubusercontent.com/DataDog/test-visibility-github-action/"+url.PathEscape(ref)+"/action.yml")
		if err != nil {
			return tracerRequirement{}, err
		}
		var definition struct {
			Inputs map[string]struct {
				Default string `yaml:"default"`
			} `yaml:"inputs"`
		}
		if err := yaml.Unmarshal(data, &definition); err != nil {
			return tracerRequirement{}, fmt.Errorf("decode action metadata: %w", err)
		}
		version = definition.Inputs["js-tracer-version"].Default
		if version == "" {
			version = "latest"
		}
	}
	if version == "" || strings.Contains(version, "${{") {
		return tracerRequirement{}, fmt.Errorf("could not resolve the action's JavaScript tracer version")
	}
	selected, err := resolveRegistry(ctx, version)
	if err != nil {
		return tracerRequirement{}, err
	}
	return tracerRequirement{Version: selected.Version, Node: selected.Node, Requested: version}, nil
}

func fetchRuntimeMetadata(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read CI runtime metadata: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read CI runtime metadata from %s: HTTP %d", address, response.StatusCode)
	}
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("CI runtime metadata exceeds 1 MiB")
	}
	return data, nil
}

func actionSelectsJavaScript(step runtimeStep, row map[string]any) (bool, error) {
	return actionSelectsLanguage(step, row, "js")
}

// actionSelectsLanguage reports whether the setup action installs the tracer of
// the language named by its languages input.
func actionSelectsLanguage(step runtimeStep, row map[string]any, language string) (bool, error) {
	languages, err := runtimeValue(step.With["languages"], row)
	if err != nil {
		return false, err
	}
	return languages == "all" || slices.Contains(strings.Fields(languages), language), nil
}

func mochaInstrumentationDisabled(workflow ciWorkflow, job runtimeJob, step runtimeStep, rows ...map[string]any) bool {
	value := ""
	for _, env := range []map[string]string{workflow.Env, job.Env, step.Env} {
		if current, ok := env["DD_CIVISIBILITY_ENABLED"]; ok {
			value = current
		}
	}
	if len(rows) > 0 {
		resolved, err := bootstrapOptions(value, rows[0], conditionEnvironment(workflow, job, step, rows[0]))
		return err == nil && resolved == "false"
	}
	return value == "false"
}

func frameworkName(frameworks []string) string {
	if len(frameworks) == 0 {
		return "jest"
	}
	return frameworks[0]
}
