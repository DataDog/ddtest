// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/cucumber_support.cjs
var cucumberSupportObserver string

type cucumberSupport struct {
	RequirePaths, ImportPaths, ConfigFiles, RequireModules, Loaders []string
	// Profile options that World and hooks need, such as forceExit and
	// worldParameters. Probes keep them; selection and retries stay out.
	RunOptions      map[string]any `json:"runOptions"`
	RunOptionsError string         `json:"runOptionsError"`
}

const cucumberMetadataScript = `const path = require('node:path');
const manifest = require.resolve('@cucumber/cucumber/package.json');
const pkg = require(manifest);
const coverage = {};
for (const name of ['nyc','c8']) { try { const manifest = require.resolve(name+'/package.json');const pkg=require(manifest);const bin=typeof pkg.bin==='string'?pkg.bin:pkg.bin[name];coverage[name]=path.resolve(path.dirname(manifest),bin); } catch {} }
console.log('DDTEST_CUCUMBER_METADATA=' + JSON.stringify({
 version: pkg.version, node: process.versions.node, nodePath: process.execPath,
 executable: path.resolve(path.dirname(manifest), pkg.bin['cucumber-js']), coverage
}));`

func (t *Testdrive) checkCucumberPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "Cucumber package was not resolved."}}
	result.Preflight = check
	env := testEnvironment("http://127.0.0.1:1", "cucumber-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	data, err := t.executor.CombinedOutput(ctx, "node", []string{"-e", cucumberMetadataScript}, env)
	if err != nil {
		return fmt.Errorf("inspect Cucumber: %w: %s", err, commandDiagnostic(data))
	}
	for _, line := range strings.Split(string(data), "\n") {
		if metadata, ok := strings.CutPrefix(line, "DDTEST_CUCUMBER_METADATA="); ok {
			var installed struct {
				Version, Node, NodePath, Executable string
				Coverage                            map[string]string
			}
			if err := json.Unmarshal([]byte(metadata), &installed); err != nil {
				return err
			}
			check.Version, check.Node = installed.Version, installed.Node
			t.cucumberCoverage = installed.Coverage
			t.cucumberNode, t.cucumberExecutable = installed.NodePath, installed.Executable
		}
	}
	// Setup planning runs this preflight too, and setup is what scopes the loader.
	if reason := onboard.CucumberLoaderOverride(t.repositoryRoot, check.Command); reason != "" {
		_, _ = fmt.Fprintf(output, "Cucumber setup required before tests: %s\n", reason)
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("cucumber requires the JavaScript tracer selector")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	result.Selection = &selection
	if err != nil {
		check.Verdict.Reason = "Tracer metadata unavailable: " + reportText(err.Error())
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	check.Verdict = checkCucumberSupport(*check, selection)
	_, _ = fmt.Fprintf(output, "Cucumber %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	// No tracer choice fixes an unsupported Cucumber release; don't suggest one.
	if major, _, known := releaseParts(check.Version); !known || major >= cucumberMinimumMajor {
		t.recommendTracer(ctx, output, result)
	}
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("cucumber preflight: %s", check.Verdict.Reason)
	}
	return nil
}

// The oldest Cucumber release exposing isolated probe configuration and
// support metadata.
const cucumberMinimumMajor = 8

func checkCucumberSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, _, known := releaseParts(check.Version)
	if !known {
		return verdict{Status: "inconclusive", Reason: "Could not resolve the installed Cucumber version."}
	}
	if major < cucumberMinimumMajor {
		return verdict{Status: "incompatible", Reason: "Cucumber testdrive requires @cucumber/cucumber >=8 for isolated probe configuration and support metadata. Preserve the customer's framework and select a supported configuration."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No known Cucumber/tracer runtime incompatibility. Paired execution and controlled feature checks are still required."}
}

// Read Cucumber's public message formatter, including native hooks and retries.
// A final envelope is mandatory: partial files and crashes cannot look passed.
func readCucumberResults(filename, directory string, run *validationRun) {
	file, err := os.Open(filename)
	if err != nil {
		run.ResultError = "Cucumber did not produce native runner results"
		return
	}
	defer func() { _ = file.Close() }()
	type pickle struct {
		ID, URI, Name string
		AstNodeIds    []string
	}
	locations := map[string]int{}
	type attempt struct {
		ID, TestCaseID  string
		Status, Failure string
		WillBeRetried   bool
	}
	pickles := map[string]pickle{}
	cases := map[string]string{}
	attempts := map[string]*attempt{}
	var finished []string
	complete := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	for scanner.Scan() {
		var envelope struct {
			GherkinDocument  any
			Pickle           *pickle
			TestCase         *struct{ ID, PickleID string }
			TestCaseStarted  *struct{ ID, TestCaseID, WorkerID string }
			TestStepFinished *struct {
				TestCaseStartedID string
				TestStepResult    struct{ Status, Message string }
			}
			TestCaseFinished *struct {
				TestCaseStartedID string
				WillBeRetried     bool
			}
			TestRunFinished *struct {
				Success   bool
				Message   string
				Exception *struct{ Message string }
			}
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			run.ResultError = "Cucumber native messages are invalid"
			return
		}
		if envelope.GherkinDocument != nil {
			cucumberASTLocations(envelope.GherkinDocument, locations)
		}
		if p := envelope.Pickle; p != nil {
			pickles[p.ID] = *p
		}
		if c := envelope.TestCase; c != nil {
			cases[c.ID] = c.PickleID
		}
		if a := envelope.TestCaseStarted; a != nil {
			if a.WorkerID != "" {
				run.cucumberParallel = true
			}
			attempts[a.ID] = &attempt{ID: a.ID, TestCaseID: a.TestCaseID, Status: "passed"}
		}
		if step := envelope.TestStepFinished; step != nil {
			if a := attempts[step.TestCaseStartedID]; a != nil {
				switch step.TestStepResult.Status {
				case "FAILED", "AMBIGUOUS":
					a.Status = "failed"
					a.Failure += step.TestStepResult.Status + ": " + step.TestStepResult.Message + "\n"
				case "UNDEFINED", "PENDING":
					if a.Status != "failed" {
						a.Status = strings.ToLower(step.TestStepResult.Status)
						a.Failure += step.TestStepResult.Status + ": " + step.TestStepResult.Message + "\n"
					}
				case "SKIPPED":
					if a.Status == "passed" {
						a.Status = "skipped"
					}
				}
			}
		}
		if a := envelope.TestCaseFinished; a != nil {
			if value := attempts[a.TestCaseStartedID]; value != nil {
				value.WillBeRetried = a.WillBeRetried
				finished = append(finished, a.TestCaseStartedID)
			}
		}
		if end := envelope.TestRunFinished; end != nil {
			complete = true
			if end.Message != "" {
				run.SuiteErrors = append(run.SuiteErrors, end.Message)
			}
			if end.Exception != nil {
				run.SuiteErrors = append(run.SuiteErrors, end.Exception.Message)
			}
		}
	}
	if scanner.Err() != nil || !complete || len(finished) != len(attempts) {
		run.ResultError = "Cucumber native results are incomplete"
		return
	}
	nativeAttempts := map[string][]string{}
	for _, id := range finished {
		a := attempts[id]
		nativeAttempts[cases[a.TestCaseID]] = append(nativeAttempts[cases[a.TestCaseID]], a.Status)
		if a.WillBeRetried {
			continue
		}
		p, ok := pickles[cases[a.TestCaseID]]
		if !ok {
			run.ResultError = "Cucumber native results reference an unknown scenario"
			return
		}
		name := p.Name
		line := 0
		for _, id := range p.AstNodeIds {
			if locations[id] > 0 {
				line = locations[id]
			}
		}
		run.Tests = append(run.Tests, jestTest{CucumberAttempts: nativeAttempts[p.ID], CucumberLine: line, File: filepath.Clean(filepath.Join(directory, p.URI)), Name: name, Status: a.Status, Failure: failureSignature(a.Failure)})
	}
	if run.Instrumented {
		if reason := cucumberSharedExampleNames(directory, run.Tests); reason != "" {
			run.NotExercised = append(run.NotExercised, reason)
		}
	}
}

// cucumberSharedExampleNames explains the limit of telemetry verification for
// outline examples that share a name: dd-trace reports no example identity, so
// their events are matched by name and count, not one by one.
func cucumberSharedExampleNames(root string, tests []jestTest) string {
	lines := map[[2]string]map[int]bool{}
	for _, test := range tests {
		if test.CucumberLine == 0 {
			continue
		}
		key := [2]string{test.File, test.Name}
		if lines[key] == nil {
			lines[key] = map[int]bool{}
		}
		lines[key][test.CucumberLine] = true
	}
	var shared []string
	for key, examples := range lines {
		if len(examples) < 2 {
			continue
		}
		file := key[0]
		if relative, err := filepath.Rel(root, file); err == nil && filepath.IsLocal(relative) {
			file = filepath.ToSlash(relative)
		}
		shared = append(shared, fmt.Sprintf("%s: %q, %d examples", file, key[1], len(examples)))
	}
	if len(shared) == 0 {
		return ""
	}
	slices.Sort(shared)
	if len(shared) > 3 {
		shared = append(shared[:3], fmt.Sprintf("%d more", len(shared)-3))
	}
	return "Cucumber outline examples that share a name (" + strings.Join(shared, "; ") + ") are matched to telemetry by name and count: dd-trace reports no per-example identity, so a swap between those examples can't be detected. Give each example a distinct name with a <placeholder> in the outline title to verify them one by one."
}

func (t *Testdrive) runCucumber(ctx context.Context, output io.Writer, session *Session, preload string, spec runSpec) (run validationRun, runErr error) {
	// A replaced NODE_OPTIONS drops the tracer preload, so no run would report.
	if reason := onboard.CucumberLoaderOverride(t.repositoryRoot, shellquote.Join(append([]string{t.command}, t.args...)...)); reason != "" {
		return run, errors.New(reason)
	}
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Role = spec.Role
	run.framework, run.root, run.Name, run.Instrumented, run.ProbeMode = "cucumber", t.checkoutRoot(), name, instrumented, probeMode
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	// Cucumber consumes cwd-relative suite identities, unlike Jest's
	// repository-relative source-file selection. Keep the real source tag.
	if scenario.Feature == "skipping" {
		scenario.SuiteSelector = scenario.Suite
	}
	server, err := t.startIntake(directory, scenario)
	if err != nil {
		return run, err
	}
	closed := false
	defer func() {
		if !closed {
			runErr = errors.Join(runErr, server.Close())
		}
	}()
	env := t.environment(preload, server.URL(), session.ID()+" "+name)
	if err := run.ownTemporaryDirectory(session.Directory(), env); err != nil {
		return run, err
	}

	configureScenarioEnvironment(env, scenario.Feature)
	env["CUCUMBER_PUBLISH_ENABLED"] = "false"
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	command, args := t.command, append([]string{}, t.args...)
	if probePath != "" {
		command, args, err = t.cucumberProbeCommand(probePath)
		if err != nil {
			return run, err
		}
		env["DDTEST_PROBE_MODE"], env["DDTEST_PROBE_COUNTER"] = probeMode, filepath.Join(directory, "probe-bodies")
		env["NODE_OPTIONS"] += " " + t.configurationEnvironment["DD_CUCUMBER_NODE_OPTIONS"]
		// pnpm's bin shims export NODE_PATH into its virtual store; probes run
		// the resolved scripts directly, so keep it for the wrapper's plugins.
		if env["NODE_PATH"] == "" {
			if paths := shimNodePath(filepath.Join(t.repositoryRoot, "node_modules/.bin"), shimNames(command, args)...); len(paths) > 0 {
				env["NODE_PATH"] = strings.Join(paths, string(os.PathListSeparator))
			}
		}
		// The scoped script appends its source loaders after the preload; the
		// probe calls the runner directly, so keep them in the same order.
		if loaders := onboard.CucumberProbeLoaders(t.repositoryRoot, shellquote.Join(append([]string{t.command}, t.args...)...)); loaders != "" {
			env["NODE_OPTIONS"] += " " + loaders
		}
		run.ProbeCoverageThresholdsDisabled = strings.Contains(shellquote.Join(append([]string{command}, args...)...), "--check-coverage=false")
	}
	observer := filepath.Join(directory, "cucumber_support.cjs")
	if err := os.WriteFile(observer, []byte(cucumberSupportObserver), 0600); err != nil {
		return run, err
	}
	env["DDTEST_CUCUMBER_SUPPORT"] = filepath.Join(directory, "support.json")
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(observer)
	run.ExecutionContext = "Both modes preload native Cucumber message and support-metadata formatters into the actual CLI, preserving package aliases and lifecycle commands; baseline tracing is disabled."
	results := filepath.Join(directory, "native.ndjson")
	formatters, err := json.Marshal([]string{"--format", observer, "--format", "message:" + results})
	if err != nil {
		return run, err
	}
	env["DDTEST_CUCUMBER_FORMATTERS"] = string(formatters)
	selected := *t
	selected.command, selected.args = command, args
	command, args = selected.executionCommand()
	env["PATH"] = filepath.Join(t.repositoryRoot, "node_modules/.bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	owner, err := t.ownProcesses(directory, env)
	if err != nil {
		return run, err
	}
	defer func() { runErr = errors.Join(runErr, owner.Close()) }()
	// Every Node process of the run, including nyc and cucumber-js.
	superviseNodeProcesses(env, directory)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	run.missingSourceLoader = missingSourceLoader(run.commandOutput, env["NODE_OPTIONS"])
	if err := server.Close(); err != nil {
		return run, err
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return run, err
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readCucumberResults(results, t.repositoryRoot, &run)
	var support cucumberSupport
	metadata, err := os.ReadFile(env["DDTEST_CUCUMBER_SUPPORT"])
	if err != nil || json.Unmarshal(metadata, &support) != nil {
		run.ResultError = "Cucumber effective support metadata is unavailable: " + commandDiagnostic(data)
	} else {
		run.cucumberSupport = &support
	}
	if instrumented && probePath == "" {
		run.Coverage = cucumberSourceCoverage(t.checkoutRoot(), run.Facts, run.Tests, run.cucumberSupport)
		if run.cucumberParallel && run.Coverage.Status == "not observed" {
			run.Coverage.Reason = "No application source-coverage events arrived from Cucumber parallel workers: the selected dd-trace sends no suite coverage from parallel workers, so this setup cannot validate parallel TIA. NYC terminal output cannot substitute for missing test-coverage telemetry. Preserve the original parallel command and keep TIA unvalidated."
		}
	}
	if probePath != "" {
		data, _ := os.ReadFile(env["DDTEST_PROBE_COUNTER"])
		count := strings.Count(string(data), "\n")
		run.ProbeExecutions = &count
	}
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if run.ResultError != "" {
		writeCommandFailure(output, "Cucumber "+name, data)
	}
	if run.ExitCode != 0 || run.ResultError != "" {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

// cucumberCoverageSetup describes the coverage tool already wrapping the
// selected Cucumber leaf, and whether the run used parallel workers.
func (t *Testdrive) cucumberCoverageSetup(run validationRun) intake.CoverageSetup {
	return intake.CoverageSetup{
		Wrapper:  onboard.CucumberCoverageWrapper(t.repositoryRoot, shellquote.Join(append([]string{t.command}, t.args...)...)),
		Parallel: run.cucumberParallel,
	}
}

func cucumberASTLocations(value any, locations map[string]int) {
	switch node := value.(type) {
	case map[string]any:
		if id, ok := node["id"].(string); ok {
			if location, ok := node["location"].(map[string]any); ok {
				if line, ok := location["line"].(float64); ok {
					locations[id] = int(line)
				}
			}
		}
		for _, child := range node {
			cucumberASTLocations(child, locations)
		}
	case []any:
		for _, child := range node {
			cucumberASTLocations(child, locations)
		}
	}
}
func cucumberLocation(test jestTest) string {
	if test.CucumberLine > 0 {
		return fmt.Sprintf(" (line %d)", test.CucumberLine)
	}
	return ""
}

// A native retry history is part of the outcome: a scenario that needed a
// retry without instrumentation but passed at once with it behaved differently.
func cucumberOutcome(test jestTest) string {
	if len(test.CucumberAttempts) == 0 {
		return ""
	}
	return "\nattempts=" + strings.Join(test.CucumberAttempts, ",")
}

func verifyCucumberTelemetry(run validationRun) verdict {
	run.framework = "cucumber-attempts"
	original := run.Tests
	run.Tests = nil
	for _, test := range original {
		attempts := test.CucumberAttempts
		if len(attempts) == 0 {
			attempts = []string{test.Status}
		}
		for _, status := range attempts {
			attempt := test
			attempt.Status = status
			run.Tests = append(run.Tests, attempt)
		}
	}
	// Native retries are verified attempt by attempt, retaining tracer retry reasons.
	run.Facts.Tests = slices.Clone(run.Facts.Tests)
	for i, test := range run.Facts.Tests {
		attempts := slices.Clone(test.Attempts)
		for j, attempt := range attempts {
			if attempt.Retry && (attempt.RetryReason == "" || attempt.RetryReason == "external") {
				attempts[j].Retry = false
			}
		}
		run.Facts.Tests[i].Attempts = attempts
	}
	return verifyJestTelemetry(run)
}

var moduleNotFound = regexp.MustCompile(`ERR_MODULE_NOT_FOUND\]?:? Cannot find module '([^']+)\.js'`)

// missingSourceLoader explains a failed `.js` import whose TypeScript sibling
// exists when no source loader such as tsx was active, or returns "".
func missingSourceLoader(output, nodeOptions string) string {
	if onboard.CucumberSourceLoaderActive(nodeOptions) {
		return ""
	}
	for _, match := range moduleNotFound.FindAllStringSubmatch(output, -1) {
		for _, extension := range []string{".ts", ".tsx", ".mts", ".cts"} {
			if info, err := os.Stat(match[1] + extension); err == nil && info.Mode().IsRegular() {
				return fmt.Sprintf("%s.js is only available as %s%s, and no source loader such as tsx was active. The command needs its source loader (for example NODE_OPTIONS=\"--import tsx\") in this run.", filepath.Base(match[1]), filepath.Base(match[1]), extension)
			}
		}
	}
	return ""
}

var shimNodePathExport = regexp.MustCompile(`(?m)^\s*export NODE_PATH="([^"]*)"\s*$`)

// shimNodePath returns the NODE_PATH entries pnpm's shell shims for names set
// when NODE_PATH is unset, with $basedir expanded, in order and without repeats.
func shimNodePath(binDirectory string, names ...string) []string {
	var paths []string
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(binDirectory, name))
		if err != nil {
			continue
		}
		match := shimNodePathExport.FindSubmatch(data)
		if match == nil {
			continue
		}
		for _, path := range filepath.SplitList(strings.ReplaceAll(string(match[1]), "$basedir", binDirectory)) {
			if path != "" && !strings.Contains(path, "$") && !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// shimNames are the package bin names behind a probe's node invocations, such
// as nyc and cucumber-js, taken from the resolved scripts' package folders.
func shimNames(command string, args []string) []string {
	var names []string
	for _, word := range append([]string{command}, args...) {
		switch filepath.Base(word) {
		case "nyc.js":
			names = append(names, "nyc")
		case "c8.js":
			names = append(names, "c8")
		case "cucumber.js", "cucumber-js":
			names = append(names, "cucumber-js")
		}
	}
	return names
}
