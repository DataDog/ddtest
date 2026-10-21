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
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

//go:embed scripts/playwright_reporter.cjs
var playwrightReporter string

//go:embed scripts/playwright_arguments.cjs
var playwrightArguments string

func preparePlaywrightProbe(directory string, env map[string]string, probe, project string) error {
	helper := filepath.Join(directory, "playwright-arguments.cjs")
	if err := os.WriteFile(helper, []byte(playwrightArguments), 0600); err != nil {
		return err
	}
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(helper)
	env["DDTEST_PLAYWRIGHT_PROBE"], env["DDTEST_PLAYWRIGHT_PROJECT"] = probe, project
	env["DDTEST_PLAYWRIGHT_ARGUMENTS_REPORT"] = filepath.Join(directory, "playwright-arguments.json")
	return nil
}

func resolvedPlaywrightProbe(env map[string]string) (string, error) {
	data, err := os.ReadFile(env["DDTEST_PLAYWRIGHT_ARGUMENTS_REPORT"])
	if err != nil {
		return "", fmt.Errorf("playwright probe CLI arguments were not resolved: %w", err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		return "", err
	}
	return shellquote.Join(args...), nil
}

const playwrightSkippingReason = "dd-trace-js does not support Test Impact Analysis skipping or per-test coverage for Playwright. This feature is not exercised and is not counted as a passed check."

type playwrightProject struct {
	Name         string   `json:"name"`
	Root         string   `json:"root"`
	Dependencies []string `json:"dependencies,omitempty"`
	Teardown     string   `json:"teardown,omitempty"`
}
type playwrightAttempt struct {
	Status  string `json:"status"`
	Retry   int    `json:"retry"`
	Failure string `json:"failure"`
}
type playwrightIdentity struct {
	Project        string              `json:"project"`
	Browser        string              `json:"browser"`
	Repeat         int                 `json:"repeat"`
	ExpectedStatus string              `json:"expectedStatus"`
	Attempts       []playwrightAttempt `json:"attempts"`
}
type playwrightResults struct {
	Node     string              `json:"node"`
	Version  string              `json:"version"`
	Projects []playwrightProject `json:"projects"`
	Tests    []struct {
		File   string `json:"file"`
		Name   string `json:"name"`
		Status string `json:"status"`
		playwrightIdentity
	} `json:"tests"`
	Errors []string `json:"errors"`
}

var playwrightLaunchNoise = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`\bpid=\d+\b`), "pid=<process>"},
	{regexp.MustCompile(`playwright_[a-z]+_profile-[a-zA-Z0-9]+`), "playwright_browser_profile-<temporary>"},
	{regexp.MustCompile(`\[0x[0-9a-fA-F]+\]`), "[<address>]"},
}

func playwrightFailureSignature(message string) string {
	message = failureSignature(message)
	if strings.Contains(message, "browserType.launch:") {
		// Browser launch errors contain per-process IDs, temporary profiles and
		// unsymbolized ASLR addresses. Keep the actual signal and error text.
		for _, noise := range playwrightLaunchNoise {
			message = noise.pattern.ReplaceAllString(message, noise.replacement)
		}
	}
	return message
}

func playwrightOutcome(test jestTest) string {
	if test.Playwright == nil {
		return ""
	}
	data, _ := json.Marshal(test.Playwright)
	return "\n" + string(data)
}

func playwrightLabel(test jestTest) string {
	if test.Playwright == nil {
		return ""
	}
	return fmt.Sprintf(" [project=%q browser=%q repeat=%d]", test.Playwright.Project, test.Playwright.Browser, test.Playwright.Repeat)
}

func readPlaywrightResults(path string, run *validationRun) (playwrightResults, error) {
	var result playwrightResults
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	if err != nil || result.Tests == nil {
		run.ResultError = "Playwright did not produce usable native results"
		return result, fmt.Errorf("%s: %v", run.ResultError, err)
	}
	for _, item := range result.Tests {
		identity := item.playwrightIdentity
		for i := range identity.Attempts {
			identity.Attempts[i].Failure = playwrightFailureSignature(identity.Attempts[i].Failure)
		}
		status := item.Status
		if status == "timedOut" || status == "interrupted" {
			status = "failed"
		}
		test := jestTest{File: filepath.Clean(item.File), Name: item.Name, Status: status, Playwright: &identity}
		if len(identity.Attempts) > 0 {
			test.Failure = identity.Attempts[len(identity.Attempts)-1].Failure
		}
		run.Tests = append(run.Tests, test)
	}
	for _, message := range result.Errors {
		run.SuiteErrors = append(run.SuiteErrors, failureSignature(message))
	}
	slices.Sort(run.SuiteErrors)
	return result, nil
}

func playwrightArgs(command string, args []string, additions ...string) []string {
	result := slices.Clone(args)
	if filepath.Base(command) == "npm" && !slices.Contains(result, "--") {
		result = append(result, "--")
	}
	return append(result, additions...)
}

func (t *Testdrive) playwrightInvocation(args []string) (string, []string) {
	if t.playwrightGuard {
		// Pass arguments positionally; never interpret customer arguments as shell code.
		return "sh", append([]string{"-c", `"$@" || exit 1`, "ddtest-playwright", t.command}, args...)
	}
	return t.command, args
}

func (t *Testdrive) inspectPlaywright(ctx context.Context, directory, probe, project string) (playwrightResults, error) {
	directory, err := os.MkdirTemp(directory, "playwright-discovery-")
	if err != nil {
		return playwrightResults{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	reporter := filepath.Join(directory, "playwright-reporter.cjs")
	if err := os.WriteFile(reporter, []byte(playwrightReporter), 0600); err != nil {
		return playwrightResults{}, err
	}
	resultPath := filepath.Join(directory, "playwright-discovery.json")
	env := testEnvironment("http://127.0.0.1:1", "playwright-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions())
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	env["DDTEST_PLAYWRIGHT_REPORT"] = resultPath
	args := playwrightArgs(t.command, t.args, "--list", "--reporter="+reporter)
	if probe != "" {
		if err := preparePlaywrightProbe(directory, env, probe, project); err != nil {
			return playwrightResults{}, err
		}
	}
	command, args := t.playwrightInvocation(args)
	data, err := t.executor.CombinedOutput(ctx, command, args, env)
	if err != nil {
		return playwrightResults{}, fmt.Errorf("inspect Playwright: %w: %s", err, commandDiagnostic(data))
	}
	if probe != "" {
		if _, err := resolvedPlaywrightProbe(env); err != nil {
			return playwrightResults{}, err
		}
	}
	var run validationRun
	result, err := readPlaywrightResults(resultPath, &run)
	if len(result.Errors) > 0 {
		err = fmt.Errorf("playwright discovery: %s", strings.Join(result.Errors, "; "))
	}
	return result, err
}

func (t *Testdrive) checkPlaywrightPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), ExecutionPath: "playwright-cli", Verdict: verdict{Status: "inconclusive", Reason: "Playwright discovery did not complete."}}
	result.Preflight = check
	directory, err := os.MkdirTemp("", "ddtest-playwright-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	discovered, err := t.inspectPlaywright(ctx, directory, "", "")
	if err != nil {
		return err
	}
	check.Version, check.PlaywrightProjects, check.Node = discovered.Version, discovered.Projects, discovered.Node
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("playwright requires JavaScript tracer selection")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	check.Verdict = checkPlaywrightSupport(*check, selection)
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	_, _ = fmt.Fprintf(output, "Playwright %s; Node %s; tracer %s (%s). Preflight: %s — %s\n", check.Version, check.Node, result.Tracer, selection.Source, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("playwright preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkPlaywrightSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, minor, known := releaseParts(check.Version)
	if !known || len(check.PlaywrightProjects) == 0 {
		return verdict{Status: "inconclusive", Reason: "The Playwright version and effective projects must be discovered."}
	}
	if major < 1 || major == 1 && minor < 38 {
		return verdict{Status: "incompatible", Reason: "dd-trace Playwright optimization features require Playwright >=1.38.0."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	return verdict{Status: "compatible", Reason: "Runtime requirements match; paired execution and supported feature controls are still required."}
}

func (t *Testdrive) runPlaywright(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.Name, run.Instrumented, run.ProbeMode, run.root, run.framework = name, instrumented, probeMode, t.checkoutRoot(), "playwright"
	run.ExecutionContext = "Both comparison modes use ddtest native-result observers and process supervision, with private process groups for non-IPC child launches. Baseline disables dd-trace; it is not an unmodified shell invocation."
	run.ExecutionPath = "playwright-cli"
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	reporter, children := filepath.Join(directory, "reporter.cjs"), filepath.Join(directory, "children.cjs")
	if err := os.WriteFile(reporter, []byte(playwrightReporter), 0600); err != nil {
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
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(children)
	owner, err := startProcessOwner(directory, env)
	if err != nil {
		return run, err
	}
	env["DDTEST_PLAYWRIGHT_REPORT"] = filepath.Join(directory, "results.json")
	defer func() {
		runErr = errors.Join(runErr, owner.Close())
	}()
	args := playwrightArgs(t.command, t.args, "--reporter=line,"+reporter, "--output="+filepath.Join(directory, "artifacts"))
	if probePath != "" {
		if err := preparePlaywrightProbe(directory, env, probePath, t.playwrightProject); err != nil {
			return run, err
		}
		env["DDTEST_PROBE_MODE"] = probeMode
	}
	command, args := t.playwrightInvocation(args)
	run.Command = shellquote.Join(append([]string{command}, args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
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
	// Playwright does not implement TIA coverage. Do not diagnose its absence as
	// broken customer setup; the HTML includes an explicit capability notice.
	run.Facts.MissingCoverage = false
	if probePath != "" {
		run.NativeCommand, err = resolvedPlaywrightProbe(env)
		if err != nil {
			run.ResultError = err.Error()
			return run, err
		}
	}
	_, _ = readPlaywrightResults(env["DDTEST_PLAYWRIGHT_REPORT"], &run)
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = playwrightDiagnostic(run, data)
	}
	if commandErr != nil && probePath == "" {
		writeCommandFailure(output, "Playwright "+name, data)
	}
	return run, nil
}

func playwrightDiagnostic(run validationRun, output []byte) string {
	// Playwright's final output may list hundreds of failed titles without their
	// causes. Keep one native error before the bounded command-output tail.
	first := ""
	if len(run.SuiteErrors) > 0 {
		first = "Playwright error: " + run.SuiteErrors[0]
	} else {
		for _, test := range run.Tests {
			if test.Failure != "" {
				cause := test.Failure
				if strings.Contains(cause, "browserType.launch:") {
					header, _, _ := strings.Cut(cause, "\n")
					for _, line := range strings.Split(cause, "\n") {
						if strings.Contains(line, "Received signal") {
							header += "\n" + line
							break
						}
					}
					cause = header + "\n" + cause
				}
				first = "First failed test: " + test.Name + "\n" + cause
				break
			}
		}
	}
	return reportText(strings.TrimSpace(first + "\n" + commandDiagnostic(output)))
}

func verifyPlaywrightTelemetry(run validationRun) verdict {
	if run.Facts.TestEventCount == 0 || len(run.Facts.ConfigurationErrors) > 0 {
		return verdict{Status: "inconclusive", Reason: "Playwright reporting telemetry is missing or has configuration errors."}
	}
	// dd-trace names this parameter "browser", but populates it with the
	// Playwright project name. Keep the actual browser separately in native
	// comparisons so a device/project alias is not mistaken for a mismatch.
	type identity struct{ file, name, project, status string }
	counts := map[identity]int{}
	for _, test := range run.Tests {
		if test.Playwright == nil {
			return verdict{Status: "inconclusive", Reason: "Missing native Playwright identity."}
		}
		for _, attempt := range test.Playwright.Attempts {
			status := map[string]string{"passed": "pass", "failed": "fail", "timedOut": "fail", "interrupted": "fail", "skipped": "skip"}[attempt.Status]
			if status != "" {
				counts[identity{test.File, test.Name, test.Playwright.Project, status}]++
			}
		}
	}
	for _, test := range run.Facts.Tests {
		var parameters struct {
			Arguments struct {
				Browser string `json:"browser"`
			} `json:"arguments"`
		}
		if test.Parameters != "" && json.Unmarshal([]byte(test.Parameters), &parameters) != nil {
			return verdict{Status: "inconclusive", Reason: "Playwright telemetry contains malformed project parameters."}
		}
		file := test.SourceFile
		if !filepath.IsAbs(file) {
			file = filepath.Join(run.root, file)
		}
		for _, attempt := range test.Attempts {
			counts[identity{filepath.Clean(file), test.Name, parameters.Arguments.Browser, attempt.Status}]--
		}
	}
	var differences []string
	for key, count := range counts {
		if count != 0 {
			differences = append(differences, fmt.Sprintf("Native/telemetry count difference %d: %s › %s [%s] %s", count, key.file, key.name, key.project, key.status))
		}
	}
	if len(differences) > 0 {
		slices.Sort(differences)
		return verdict{Status: "inconclusive", Reason: "Playwright telemetry does not match native test attempts and project identity.", Differences: differences}
	}
	return verdict{}
}

// Disabled Playwright tests are synthesized by the tracer's reporter without
// test.module. Require the original source and project before accepting that
// specific omission; suite/name and native skipped results are checked too.
func playwrightDisabledProbe(tags map[string]string, control intake.Test, run validationRun) bool {
	if tags["test.module"] != "" || tags["test.status"] != "skip" || tags["test.test_management.is_test_disabled"] != "true" || control.SourceFile == "" || tags["test.source.file"] != control.SourceFile {
		return false
	}
	project := func(value string) (string, bool) {
		if value == "" {
			return "", true
		}
		var parameters struct {
			Arguments struct {
				Browser string `json:"browser"`
			} `json:"arguments"`
		}
		if json.Unmarshal([]byte(value), &parameters) != nil {
			return "", false
		}
		return parameters.Arguments.Browser, true
	}
	want, valid := project(control.Parameters)
	if !valid {
		return false
	}
	if tags["test.parameters"] != "" {
		got, valid := project(tags["test.parameters"])
		return valid && got == want
	}
	// Reporter-created disabled spans can also omit project parameters. The
	// random probe exists only in this selected project; require its one native
	// skipped result despite those missing tags.
	return len(run.Tests) == 1 && run.Tests[0].Playwright != nil && run.Tests[0].Playwright.Project == want && run.Tests[0].Status == "skipped"
}

const playwrightProbeSource = `import { test } from '@playwright/test';
test('ddtest validation probe', async ({}, testInfo) => {
  if (process.env.DDTEST_PROBE_MODE === 'fail' ||
      (process.env.DDTEST_PROBE_MODE === 'fail-once' && testInfo.retry === 0)) {
    throw new Error('ddtest intentional failure');
  }
});
`

func (t *Testdrive) runPlaywrightFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {

	projects := map[string]validationRun{}
	for _, test := range baseline.Tests {
		if test.Playwright != nil {
			v := projects[test.Playwright.Project]
			v.Tests = append(v.Tests, test)
			projects[test.Playwright.Project] = v
		}
	}
	var names []string
	for name := range projects {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		unavailableFeatures(result, "No executed Playwright project is available for probes.")
		return nil
	}
	for i, project := range names {
		firstFeature, firstRun := len(result.Features), len(result.Runs)
		probe, err := createProbe(t.checkoutRoot(), projects[project], playwrightProbeSource)
		if err != nil {
			unavailableFeatures(result, err.Error())
			continue
		}
		err = func() (probeErr error) {
			defer func() { probeErr = errors.Join(probeErr, removeProbe(t.checkoutRoot(), probe)) }()
			discovered, err := t.inspectPlaywright(ctx, session.Directory(), probe, project)
			if err != nil || len(discovered.Tests) != 1 || discovered.Tests[0].File != probe {
				unavailableFeatures(result, fmt.Sprintf("Probe was not uniquely discovered under the original Playwright configuration: %v", err))
				return nil
			}
			child := *t
			child.playwrightProject = project
			run := func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
				value, err := child.runPlaywright(ctx, output, session, preload, fmt.Sprintf("project-%d/%s", i+1, name), true, scenario, probe, mode)
				value.Name = name
				return value, err
			}
			return runProbeFeatures(run, result)
		}()
		for j := firstFeature; j < len(result.Features); j++ {
			result.Features[j].Project = project
		}
		for j := firstRun; j < len(result.Runs); j++ {
			result.Runs[j].Project = project
		}
		if err != nil {
			return err
		}
	}
	return nil
}
