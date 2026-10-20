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
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

const cypressTIADeferredReason = "Cypress Test Impact Analysis (skipping and application coverage) is deferred and was not validated. Application Istanbul instrumentation is not required by this testdrive; preserve any existing coverage setup."

//go:embed scripts/cypress_observer.cjs
var cypressObserver string

//go:embed scripts/cypress_api.cjs
var cypressAPI string

func (t *Testdrive) checkCypressPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), ExecutionPath: "cypress-cli", Verdict: verdict{Status: "inconclusive", Reason: "Cypress metadata was not resolved."}}
	if t.cypress != nil {
		check.Command = t.cypressCommand
		if t.cypress.Action != "" {
			check.ExecutionPath = "cypress-api"
		}
	}
	result.Preflight = check
	data, err := t.executor.CombinedOutput(ctx, "node", []string{"-p", "JSON.stringify({version:require('cypress/package.json').version,requirements:[require('cypress/package.json').engines?.node,require('./package.json').engines?.node].filter(Boolean)})"}, map[string]string{"NODE_OPTIONS": stripDatadogNodeOptions(t.nodeOptions())})
	if err != nil {
		return fmt.Errorf("inspect Cypress: %w: %s", err, commandDiagnostic(data))
	}
	var metadata struct {
		Version      string
		Requirements []string
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("inspect Cypress package metadata: %w", err)
	}
	check.Version = metadata.Version
	if t.cypress != nil {
		if browser, ok := t.cypress.Options["browser"].(string); ok && browser != "" && browser != "electron" && !strings.ContainsAny(browser, "/\\") {
			data, err := t.executor.CombinedOutput(ctx, "node", []string{filepath.Join(t.checkoutRoot(), "node_modules", "cypress", "bin", "cypress"), "info"}, map[string]string{"NODE_OPTIONS": stripDatadogNodeOptions(t.nodeOptions()), "NO_COLOR": "1"})
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
			check.Verdict = verdict{Status: "inconclusive", Reason: "The Cypress action API uses Node " + node + ". Run testdrive with a compatible Node " + node + " runtime to validate this path; local " + check.Node + " is a different runtime."}
			return fmt.Errorf("cypress API preflight: %s", check.Verdict.Reason)
		}
	}
	for _, requirement := range metadata.Requirements {
		if status, reason := checkCypressNodeEngine(check.Node, requirement); status != "compatible" {
			check.Verdict = verdict{Status: status, Reason: "Cypress/project Node requirement " + requirement + ": " + reason}
			return fmt.Errorf("cypress preflight: %s", check.Verdict.Reason)
		}
	}
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("cypress requires JavaScript tracer selection")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
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

// Cypress and application engines commonly list supported Node release lines
// with caret unions. Keep other npm range syntax explicitly unvalidated.
func checkCypressNodeEngine(node, requirement string) (string, string) {
	branches := strings.Split(requirement, "||")
	if len(requirement) > 4096 || len(branches) > 16 {
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
	owner, err := startProcessOwner(directory, env)
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
	if instrumented && probePath == "" && run.Facts.TestCount > 0 {
		run.Coverage = cypressSourceCoverage(t.checkoutRoot(), run.Facts, run.Tests)
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
		Status          string
		Complete        bool
		ConfigNode      string
		ProbeExecutions *int
		Message         string
		Runs            []struct {
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

func (t *Testdrive) runCypressFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probe, err := createProbe(t.checkoutRoot(), baseline, cypressProbeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.checkoutRoot(), probe)) }()
	return runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
		return t.runCypress(ctx, output, session, preload, runSpec{Name: name, Role: roleProbeFeature, Instrumented: true, Scenario: scenario, ProbePath: probe, ProbeMode: mode})
	}, result)
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
