package testdrive

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

const cypressComponentNotExercised = "Cypress component testing is configured but not supported by ddtest; only the selected e2e command was exercised."

//go:embed scripts/cypress_observer.cjs
var cypressObserver string

//go:embed scripts/cypress_api.cjs
var cypressAPI string

const cypressPackageMetadata = `
const path = require('node:path')
const local = require('node:module').createRequire(path.join(process.argv[1], 'package.json'))
let preload = ''
try { preload = local.resolve('dd-trace/ci/init') } catch (error) {
  if (error.code !== 'MODULE_NOT_FOUND') throw error
}
const cypress = local('cypress/package.json')
// Read the manifest directly: package exports can hide package.json.
let codeCoverage = ''
for (const directory of local.resolve.paths('@cypress/code-coverage') || []) {
  try {
    codeCoverage = JSON.parse(require('node:fs').readFileSync(path.join(directory, '@cypress', 'code-coverage', 'package.json'), 'utf8')).version
    break
  } catch (error) {
    if (error.code !== 'ENOENT') throw error
  }
}
console.log(JSON.stringify({
  preload,
  codeCoverage,
  cli: path.join(path.dirname(local.resolve('cypress/package.json')), 'bin', 'cypress'),
  version: cypress.version,
  requirements: [cypress.engines?.node, local('./package.json').engines?.node].filter(Boolean)
}))
`

func (t *Testdrive) checkCypressPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), ExecutionPath: "cypress-cli", Verdict: verdict{Status: "inconclusive", Reason: "Cypress metadata was not resolved."}}
	if t.cypress != nil {
		check.Command = t.cypressCommand
		if t.cypress.Action != "" {
			check.ExecutionPath = "cypress-api"
		}
	}
	result.Preflight = check
	if platform := t.cypressSelected.Platform; platform != "" && platform != runtime.GOOS {
		return fmt.Errorf("cypress %s configuration cannot be validated on %s", platform, runtime.GOOS)
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("cypress process supervision is not supported on Windows; this configuration remains unvalidated")
	}
	for _, argument := range t.args {
		if argument == "--component" {
			return fmt.Errorf("cypress component testing is not supported")
		}
	}
	lookup := t.repositoryRoot
	if t.cypressPackage != "" {
		lookup = filepath.Join(t.checkoutRoot(), t.cypressPackage)
	}
	relative, err := filepath.Rel(t.checkoutRoot(), lookup)
	if err != nil {
		return err
	}
	if _, err := localSetupDirectory(t.checkoutRoot(), relative); err != nil {
		return err
	}

	data, err := t.executor.CombinedOutput(ctx, "node", []string{"-e", cypressPackageMetadata, lookup}, map[string]string{"NODE_OPTIONS": stripDatadogNodeOptions(t.nodeOptions())})
	if err != nil {
		return fmt.Errorf("inspect Cypress: %w: %s", err, commandDiagnostic(data))
	}
	var metadata struct {
		Preload      string
		CLI          string
		Version      string
		CodeCoverage string
		Requirements []string
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("inspect Cypress package metadata: %w", err)
	}
	check.Version = metadata.Version
	t.cypressCodeCoverage = metadata.CodeCoverage
	// Resolve the tracer before the browser and runtime checks, so a blocked
	// preflight still reports the project tracer it would have used.
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("cypress requires JavaScript tracer selection")
	}
	options := platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args, Environment: t.configurationEnvironment}
	if metadata.Preload != "" && options.Environment["DD_TRACE_PACKAGE"] == "" && os.Getenv("DD_TRACE_PACKAGE") == "" {
		options.Environment = mergeConfigurationEnvironment(t.configurationEnvironment, map[string]string{"DD_TRACE_PACKAGE": metadata.Preload})
		preload, err := installer.DetectTracer(ctx, options)
		if err != nil {
			return fmt.Errorf("inspect consumer package tracer: %w", err)
		}
		t.projectTracer = preload
		// Keep later verification on the same package-local preload as preflight.
		t.configurationEnvironment = options.Environment
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, options)
	result.Selection = &selection
	if err != nil {
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	if t.cypress != nil {
		if browser, ok := t.cypress.Options["browser"].(string); ok && browser != "" && browser != "electron" && !strings.ContainsAny(browser, "/\\") {
			data, err := t.executor.CombinedOutput(ctx, "node", []string{metadata.CLI, "info"}, map[string]string{"NODE_OPTIONS": stripDatadogNodeOptions(t.nodeOptions()), "NO_COLOR": "1"})
			available, known := cypressBrowserAvailable(string(data), browser)
			if err != nil || !known || !available {
				check.Verdict = verdict{Status: "inconclusive", Reason: "Selected Cypress browser " + browser + " is unavailable or its installed inventory could not be verified. This configuration was not executed; install the declared browser or use a matching runner, preserving the CI browser."}
				return fmt.Errorf("cypress browser preflight: %s", check.Verdict.Reason)
			}
		}
	}
	if t.cypress != nil && t.cypress.Action != "" {
		node, err := onboard.CypressActionNode(ctx, t.cypress.Action)
		if err != nil {
			return err
		}
		major, _, known := releaseParts(check.Node)
		required, err := strconv.Atoi(node)
		if err != nil || !known || major != required {
			mismatch := &localRuntimeError{Source: "The Cypress action API (" + t.cypress.Action + ")", Required: node, Local: check.Node}
			check.Verdict = verdict{Status: "inconclusive", Reason: mismatch.Error()}
			return fmt.Errorf("cypress API preflight: %w", mismatch)
		}
	}
	for _, requirement := range metadata.Requirements {
		if status, reason := checkCypressNodeEngine(check.Node, requirement); status != "compatible" {
			check.Verdict = verdict{Status: status, Reason: "Cypress/project Node requirement " + requirement + ": " + reason}
			return fmt.Errorf("cypress preflight: %s", check.Verdict.Reason)
		}
	}
	check.Verdict = checkCypressSupport(*check, selection)
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	_, _ = fmt.Fprintf(output, "Cypress %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("cypress preflight: %s", check.Verdict.Reason)
	}
	return nil
}

// Parse the public cypress info inventory, including a requested release
// channel. Do not infer availability from browser names elsewhere in the text.
func cypressBrowserAvailable(info, requested string) (bool, bool) {
	known := strings.Contains(info, "Detected ") && strings.Contains(info, "browser")
	name, channel, _ := strings.Cut(requested, ":")
	current := ""
	for _, line := range strings.Split(info, "\n") {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if value, ok := strings.CutPrefix(text, "Name:"); ok {
			current = strings.TrimSpace(value)
			if current == name && channel == "" {
				return true, true
			}
		}
		if value, ok := strings.CutPrefix(text, "Channel:"); ok && current == name && strings.TrimSpace(value) == channel {
			return true, true
		}
	}
	return false, known
}

// Bound engine ranges read from installed packages before comparing them.
const (
	cypressEngineRangeLimit    = 4096
	cypressEngineBranchesLimit = 16
)

// Cypress and application engines commonly list supported Node release lines
// with caret unions. Keep other npm range syntax explicitly unvalidated.
func checkCypressNodeEngine(node, requirement string) (string, string) {
	branches := strings.Split(requirement, "||")
	if len(requirement) > cypressEngineRangeLimit || len(branches) > cypressEngineBranchesLimit {
		return "inconclusive", "Node engine range exceeds the validation limit."
	}
	status := "incompatible"
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if strings.HasPrefix(branch, "^") {
			minimum := strings.TrimPrefix(branch, "^")
			major, _, known := releaseParts(minimum)
			if !known || major == 0 {
				return "inconclusive", "Unsupported Node caret engine range: " + branch
			}
			branch = ">=" + minimum + " <" + strconv.Itoa(major+1)
		}
		current, reason := onboard.CompareNodeRequirement(node, branch)
		if current == "inconclusive" {
			return current, reason
		}
		if current == "compatible" {
			status = current
		}
	}
	return status, fmt.Sprintf("Node %s: %s with %s.", node, status, requirement)
}

func checkCypressSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, minor, known := releaseParts(check.Version)
	tracerMajor, _, tracerKnown := releaseParts(selection.Version)
	if !known || !tracerKnown {
		return verdict{Status: "inconclusive", Reason: "Could not resolve Cypress and tracer versions."}
	}
	if major < 6 || major == 6 && minor < 7 || tracerMajor >= 6 && major < 12 {
		return verdict{Status: "incompatible", Reason: "dd-trace 6 requires Cypress >=12; dd-trace 5 requires Cypress >=6.7. Preserve the existing test matrix and select a compatible tracer."}
	}
	if status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node); status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "No known tracer/runtime incompatibility blocks Cypress. Browser instrumentation, paired execution and controlled features still require execution."}
}

func (t *Testdrive) runCypress(ctx context.Context, output io.Writer, session *Session, preload string, spec runSpec) (run validationRun, runErr error) {
	defer func() {
		run.Command = onboard.RedactCypressCommand(run.Command)
		run.NativeCommand = onboard.RedactCypressCommand(run.NativeCommand)
		run.commandOutput = onboard.RedactCypressCommand(run.commandOutput)
	}()
	if t.workflowSourceCheck != nil {
		if err := t.workflowSourceCheck(ctx); err != nil {
			return run, err
		}
	}
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Role = spec.Role
	run.Name, run.Instrumented, run.ProbeMode = name, instrumented, probeMode
	run.ExecutionContext = "Both comparison modes use ddtest native-result observers and process supervision. Baseline disables dd-trace; it is not an unmodified shell invocation."
	run.framework, run.root, run.ExecutionPath = "cypress", t.checkoutRoot(), "cypress-cli"
	if t.cypress != nil && t.cypress.Action != "" {
		run.ExecutionPath = "cypress-api"
	}
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	observer, children := filepath.Join(directory, "observer.cjs"), filepath.Join(directory, "children.cjs")
	if err := os.WriteFile(observer, []byte(cypressObserver), 0600); err != nil {
		return run, err
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
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(observer) + " --require " + strconv.Quote(children)
	env["DDTEST_CYPRESS_DIRECTORY"], env["DDTEST_CYPRESS_PROBE"], env["DDTEST_PROBE_MODE"] = directory, probePath, probeMode
	// The observer moves paths into the original checkout into this copy and
	// refuses configuration outside it, so its wrappers stay disposable.
	env["DDTEST_CYPRESS_CHECKOUT"] = t.checkoutRoot()
	if t.isolated {
		env["DDTEST_CYPRESS_SOURCE_ROOT"] = t.configurationSourceRoot
	}
	defer func() { runErr = errors.Join(runErr, removeCypressWrappers(directory)) }()
	owner, err := t.ownProcesses(directory, env)
	if err != nil {
		return run, err
	}
	env["CYPRESS_RECORD_KEY"], env["CYPRESS_CLOUD_TOKEN"], env["CYPRESS_RECORD"], env["CYPRESS_PARALLEL"] = "", "", "false", "false"
	env["PATH"] = filepath.Join(t.checkoutRoot(), "node_modules", ".bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	defer func() {
		runErr = errors.Join(runErr, owner.Close())
	}()
	run.Command = shellquote.Join(append([]string{t.command}, t.args...)...)
	run.Diagnostic = "Original CLI command with an after:run observer. Cloud recording/parallel grouping are disabled locally; application startup, browser, reporter and user hooks are retained."
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	command, args := t.command, t.args
	if t.cypress != nil {
		api, plan := filepath.Join(directory, "api.cjs"), filepath.Join(directory, "plan.json")
		data, err := json.Marshal(t.cypress)
		if err != nil {
			return run, err
		}
		if err := os.WriteFile(api, []byte(cypressAPI), 0600); err != nil {
			return run, err
		}
		if err := os.WriteFile(plan, data, 0600); err != nil {
			return run, err
		}
		command, args = "node", []string{api, plan}
		run.Command = t.cypressCommand
		run.Diagnostic = "Preserved Cypress action API options or managed CLI startup; Cloud recording/parallel grouping disabled locally. User config, hooks, reporter and browser retained."
	}
	data, commandErr := t.executor.CombinedOutput(ctx, command, args, env)
	run.commandOutput, run.commandError, run.ExitCode = string(data), commandErr, commandExitCode(commandErr)
	err = server.Close()
	closed = true
	if err != nil {
		return run, err
	}
	if ctx.Err() != nil {
		return run, ctx.Err()
	}
	run.Facts, err = server.Facts()
	if err != nil {
		return run, err
	}
	readCypressResults(filepath.Join(directory, "results.json"), &run)
	if instrumented && probePath == "" && run.ResultError == "" {
		code := run.cypressCode
		if code.Project == "" {
			code.Project = filepath.ToSlash(t.cypressPackage)
		}
		if t.isolated {
			code.Source = t.configurationSourceRoot
		}
		run.Coverage = cypressSourceCoverage(t.checkoutRoot(), code, run.Facts, run.Tests)
	}

	var invocation struct {
		Command string
		Args    []string
	}
	if data, err := os.ReadFile(filepath.Join(directory, "invocation.json")); err == nil && json.Unmarshal(data, &invocation) == nil {
		run.NativeCommand = shellquote.Join(append([]string{invocation.Command}, invocation.Args...)...)
	}
	if run.ExitCode != 0 || run.ResultError != "" {
		run.Diagnostic += " " + commandDiagnostic(data)
		if len(data) == 0 && commandErr != nil {
			run.Diagnostic += " " + commandErr.Error()
		}
	}
	if scenario.Feature == "skipping" {
		run.Skipping = diagnoseSkipping(run, scenario)
	}
	if failure, err := os.ReadFile(filepath.Join(directory, "execution-error.txt")); err == nil {
		run.ResultError = "Cypress execution setup failed: " + commandDiagnostic(failure)
		return run, errors.New(run.ResultError)
	} else if !errors.Is(err, os.ErrNotExist) {
		return run, fmt.Errorf("read Cypress execution setup status: %w", err)
	}
	return run, nil
}

func readCypressResults(path string, run *validationRun) {
	var result struct {
		Status              string
		Complete            bool
		ConfigNode          string
		ComponentConfigured bool
		ProjectRoot         string
		SupportFile         string
		ProbeExecutions     *int
		Message             string
		Runs                []struct {
			Spec  struct{ Absolute string }
			Error string
			Tests []struct {
				Title        []string
				State        string
				DisplayError string
				Attempts     []struct {
					State string
					Error *struct{ Message string }
				}
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || (result.Status != "" && result.Status != "finished") || result.Runs == nil {
		run.ResultError = "Cypress native results are missing, invalid or incomplete"
		return
	}
	run.FrameworkNode = result.ConfigNode
	run.cypressCode = cypressTestCode{Project: result.ProjectRoot, SupportFile: result.SupportFile}
	if result.ComponentConfigured {
		run.NotExercised = append(run.NotExercised, cypressComponentNotExercised)
	}
	run.ProbeExecutions = result.ProbeExecutions
	for _, spec := range result.Runs {
		if spec.Error != "" {
			run.SuiteErrors = append(run.SuiteErrors, spec.Error)
		}
		for _, test := range spec.Tests {
			item := jestTest{File: spec.Spec.Absolute, Name: strings.Join(test.Title, " "), Status: test.State, Failure: failureSignature(test.DisplayError)}
			if item.Status == "pending" {
				item.Status = "skipped"
			}
			for _, attempt := range test.Attempts {
				item.CypressAttempts = append(item.CypressAttempts, attempt.State)
			}
			if len(test.Attempts) > 0 {
				last := test.Attempts[len(test.Attempts)-1]
				if last.Error != nil {
					item.Failure = failureSignature(last.Error.Message)
				}
			}
			run.Tests = append(run.Tests, item)
		}
	}
}

// A native retry history is part of the outcome: a test that needed a retry
// without instrumentation but passed at once with it behaved differently.
func cypressOutcome(test jestTest) string {
	if len(test.CypressAttempts) == 0 {
		return ""
	}
	return "\nattempts=" + strings.Join(test.CypressAttempts, ",")
}

func (t *Testdrive) runCypressFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probe, err := createProbe(t.checkoutRoot(), baseline, cypressProbeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.checkoutRoot(), probe)) }()
	outputs := map[string]string{}
	err = runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		role := roleProbeFeature
		if scenario.Feature == "" {
			role = roleProbeControl
		}
		run, err := t.runCypress(ctx, output, session, preload, runSpec{Name: name, Role: role, Instrumented: true, Scenario: scenario, ProbePath: probe, ProbeMode: mode})
		outputs[scenario.Feature] = run.commandOutput
		return run, err
	}, result)
	explainCypressCodeCoverageSkips(result.Features, t.cypressCodeCoverage, outputs)
	return err
}

const cypressCodeCoverageSkipReason = "@cypress/code-coverage %s fails its afterEach hook when the tracer skips the first test of a spec: its support file reads coverage state that only its own beforeEach initializes, and the tracer's earlier beforeEach skipped the test. Test Impact Analysis skipping and disabled tests can't run with it. Upgrade to @cypress/code-coverage 4 or later, which requires Cypress 15.10 or later."

// explainCypressCodeCoverageSkips names the known @cypress/code-coverage 3
// crash when a runtime skip failed in an afterEach hook.
func explainCypressCodeCoverageSkips(features []featureResult, codeCoverage string, outputs map[string]string) {
	major, _, known := releaseParts(codeCoverage)
	if !known || major >= 4 {
		return
	}
	for i, feature := range features {
		output := outputs[feature.Name]
		if (feature.Name != "skipping" && feature.Name != "disabled") || feature.Status != "failed" || !strings.Contains(output, "after each") || !strings.Contains(output, "code-coverage") {
			continue
		}
		features[i].Reason = fmt.Sprintf(cypressCodeCoverageSkipReason, codeCoverage)
	}
}

const cypressProbeSource = `let attempts = 0;
const mode = Cypress.env('__ddtest_probe_mode');
describe('ddtest', () => { (mode ? it : it.skip)('validation probe', () => {
 attempts++;
 return cy.task('ddtest:validation:attempt').then(() => {
  if (mode === 'fail' || (mode === 'fail-once' && attempts === 1)) throw new Error('ddtest intentional failure');
 });
}); });
`

// Cypress reports ordered native attempts, but its tracer events can arrive in
// the opposite order. Compare the complete attempt multiset rather than guessing
// the final attempt from arrival order. The original run evidence is unchanged.
func verifyCypressTelemetry(run validationRun) verdict {
	view := run
	view.framework = ""
	view.Tests = nil
	for _, test := range run.Tests {
		if len(test.CypressAttempts) == 0 {
			view.Tests = append(view.Tests, test)
			continue
		}
		for _, status := range test.CypressAttempts {
			attempt := test
			attempt.Status = status
			view.Tests = append(view.Tests, attempt)
		}
	}
	view.Facts.Tests = slices.Clone(run.Facts.Tests)
	for i, test := range run.Facts.Tests {
		view.Facts.Tests[i].Attempts = slices.Clone(test.Attempts)
		for j, attempt := range test.Attempts {
			// Native retries count as individual attempts in this comparison view.
			// Keep tracer-controlled retries marked, so the shared verifier rejects them.
			if attempt.RetryReason == "external" || attempt.RetryReason == "" {
				view.Facts.Tests[i].Attempts[j].Retry = false
			}
		}
	}
	return verifyJestTelemetry(view)
}

// removeCypressWrappers deletes the configuration wrappers the observer
// generated for one run, wherever they were written.
func removeCypressWrappers(directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, "wrappers"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failed error
	for _, wrapper := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if wrapper == "" || !strings.HasPrefix(filepath.Base(wrapper), "ddtest-observed-") {
			continue
		}
		if err := os.Remove(wrapper); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = errors.Join(failed, err)
		}
	}
	return failed
}
