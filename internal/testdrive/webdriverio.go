package testdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
)

const webdriverIOSkippingReason = "dd-trace-js does not support Test Impact Analysis skipping or code coverage for WebdriverIO. These are not exercised or counted as passed checks."

func browserSkippingReason(name string) string {
	if name == "webdriverio" {
		return webdriverIOSkippingReason
	}
	return playwrightSkippingReason
}

func (t *Testdrive) inspectWebdriverIO(ctx context.Context, directory string) (framework.WebdriverIOConfig, error) {
	var config framework.WebdriverIOConfig
	observer, err := framework.PrepareWebdriverIOObserver(directory)
	if err != nil {
		return config, err
	}
	env := testEnvironment("http://127.0.0.1:1", "webdriverio-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS")) + " --import " + strconv.Quote(observer)
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	env["DDTEST_WDIO_INSPECT"] = filepath.Join(directory, "config.json")
	data, err := t.executor.CombinedOutput(ctx, t.command, t.args, env)
	if err != nil {
		return config, fmt.Errorf("inspect WebdriverIO config: %w: %s", err, commandDiagnostic(data))
	}
	data, err = os.ReadFile(env["DDTEST_WDIO_INSPECT"])
	if err == nil {
		err = json.Unmarshal(data, &config)
	}
	return config, err
}
func (t *Testdrive) checkWebdriverIOPreflight(ctx context.Context, output io.Writer, result *validationResult) error {
	check := &jestPreflight{Node: t.nodeVersion(), Command: shellquote.Join(append([]string{t.command}, t.args...)...), Verdict: verdict{Status: "inconclusive", Reason: "WebdriverIO effective config was not resolved."}}
	result.Preflight = check
	directory, err := os.MkdirTemp("", "ddtest-wdio-config-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	config, err := t.inspectWebdriverIO(ctx, directory)
	if err != nil {
		return err
	}
	config.FileCount = len(config.Files)
	config.Files = nil // Keep counts, not every spec path, in the retained JSON report.
	check.WebdriverIO = &config
	check.Version = config.Version
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("webdriverio requires JavaScript tracer selection")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args})
	result.Selection = &selection
	if err != nil {
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	check.Verdict = checkWebdriverIOSupport(*check, selection)
	if check.Verdict.Status == "compatible" {
		if err := checkWebdriverIOServerPorts(config.ServerPorts); err != nil {
			check.Verdict = verdict{Status: "inconclusive", Reason: err.Error()}
		}
	}
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	_, _ = fmt.Fprintf(output, "WebdriverIO %s (%s, %s); Node %s; tracer %s. Preflight: %s — %s\n", config.Version, config.Framework, config.Runner, check.Node, result.Tracer, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" {
		return fmt.Errorf("webdriverio preflight: %s", check.Verdict.Reason)
	}
	return nil
}

func checkWebdriverIOServerPorts(ports []int) error {
	for _, port := range ports {
		if port <= 0 || port > 65535 {
			return fmt.Errorf("WDIO static-server has an invalid port: %d", port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("WDIO static-server port %d is unavailable. Preserve the existing process. Add a documented optional port override to the original config, updating both the static-server port and baseUrl while keeping their CI defaults. Use the same override for baseline, reporting-only and features, then rerun testdrive: %w", port, err)
		}
		_ = listener.Close()
	}
	return nil
}
func checkWebdriverIOSupport(check jestPreflight, selection platform.JSSelection) verdict {
	major, _, known := releaseParts(check.Version)
	if !known {
		return verdict{Status: "inconclusive", Reason: "WebdriverIO version was not resolved."}
	}
	if major < 9 {
		return verdict{Status: "incompatible", Reason: "dd-trace WebdriverIO instrumentation requires WebdriverIO >=9."}
	}
	if check.WebdriverIO == nil || check.WebdriverIO.Runner != "local" || (check.WebdriverIO.Framework != "mocha" && check.WebdriverIO.Framework != "jasmine") {
		return verdict{Status: "inconclusive", Reason: "WebdriverIO Test Optimization supports the local runner with Mocha or Jasmine. Browser runner/custom adapters remain unvalidated; preserve the original config."}
	}
	tracerMajor, tracerMinor, known := releaseParts(selection.Version)
	if !known || tracerMajor < 5 || tracerMajor == 5 && tracerMinor < 121 || tracerMajor == 6 && tracerMinor < 10 {
		return verdict{Status: "incompatible", Reason: "WebdriverIO requires dd-trace >=5.121.0 on v5 or >=6.10.0 on v6. Resolve a compatible release; these are capability minimums, not installation pins."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	if len(check.WebdriverIO.Capabilities) == 0 || (len(check.WebdriverIO.Files) == 0 && check.WebdriverIO.FileCount == 0) {
		return verdict{Status: "inconclusive", Reason: "Effective WebdriverIO capabilities and selected specs must be resolved; multiremote/empty selections remain unvalidated."}
	}
	return verdict{Status: "compatible", Reason: "Runtime and tracer requirements match. Paired execution and supported feature controls are still required; TIA skipping/coverage are not supported."}
}

func readWebdriverIOResults(directory string, run *validationRun) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		run.ResultError = "WebdriverIO did not produce native results"
		return
	}
	var metadata struct {
		Selections []struct {
			Browser string
			Files   []string
		}
	}
	data, metaErr := os.ReadFile(filepath.Join(filepath.Dir(directory), "metadata.json"))
	if metaErr != nil || json.Unmarshal(data, &metadata) != nil || len(metadata.Selections) == 0 {
		run.ResultError = "WebdriverIO effective worker selection is unavailable"
		return
	}
	pending := map[string]int{}
	for _, selection := range metadata.Selections {
		for _, file := range selection.Files {
			pending[selection.Browser+"\x00"+filepath.Clean(file)]++
		}
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var result struct {
			Complete bool
			Specs    []string
			Browser  string
			Tests    []struct{ File, Name, Status, Failure string }
			Errors   []string
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil || json.Unmarshal(data, &result) != nil || !result.Complete || result.Tests == nil || len(result.Specs) == 0 {
			run.ResultError = "WebdriverIO native worker results are invalid or incomplete"
			return
		}
		count++
		for _, file := range result.Specs {
			key := result.Browser + "\x00" + filepath.Clean(file)
			pending[key]--
		}
		run.SuiteErrors = append(run.SuiteErrors, result.Errors...)
		for _, test := range result.Tests {
			if test.File == "" || (test.Status != "passed" && test.Status != "failed" && test.Status != "skipped") {
				run.ResultError = "WebdriverIO native test identity or status is invalid"
				return
			}
			file := test.File
			if !filepath.IsAbs(file) {
				file = filepath.Join(run.root, file)
			}
			run.Tests = append(run.Tests, jestTest{File: filepath.Clean(file), Name: test.Name, Status: test.Status, WebdriverIOBrowser: result.Browser, Failure: failureSignature(test.Failure)})
		}
	}
	for _, missing := range pending {
		if missing != 0 {
			run.ResultError = "WebdriverIO native reports do not cover the selected specs and capabilities"
			return
		}
	}
	if count == 0 {
		run.ResultError = "WebdriverIO did not finish any native worker report"
	}
}
func (t *Testdrive) runWebdriverIO(ctx context.Context, output io.Writer, session *Session, preload, name string, instrumented bool, scenario intake.Scenario, probePath, probeMode string) (run validationRun, runErr error) {
	run.Name, run.Instrumented, run.ProbeMode, run.root, run.framework = name, instrumented, probeMode, t.repositoryRoot, "webdriverio"
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	observer, err := framework.PrepareWebdriverIOObserver(directory)
	if err != nil {
		return run, err
	}
	children := filepath.Join(directory, "children.cjs")
	if err := os.WriteFile(children, []byte(playwrightChildren), 0600); err != nil {
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
	if err := run.ownTemporaryDirectory(directory, env); err != nil {
		return run, err
	}
	configureScenarioEnvironment(env, scenario.Feature)
	if !instrumented {
		env["NODE_OPTIONS"] = stripDatadogNodeOptions(os.Getenv("NODE_OPTIONS"))
		env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	}
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(children) + " --import " + strconv.Quote(observer)
	env["DDTEST_PLAYWRIGHT_CHILDREN"] = filepath.Join(directory, "children.txt")
	defer func() {
		if err := cleanupPlaywrightChildren(env["DDTEST_PLAYWRIGHT_CHILDREN"]); err != nil {
			runErr = errors.Join(runErr, &playwrightCleanupError{err})
		}
	}()
	env["DDTEST_WDIO_INSPECT"], env["DDTEST_WDIO_PROBE"], env["DDTEST_WDIO_FILES"], env["DDTEST_WDIO_CAPABILITY"] = "", "", "", ""
	if t.webdriverIOCapability != nil {
		env["DDTEST_WDIO_CAPABILITY"] = strconv.Itoa(*t.webdriverIOCapability)
	}
	env["DDTEST_WDIO_RESULTS"] = filepath.Join(directory, "native")
	env["DDTEST_WDIO_METADATA"] = filepath.Join(directory, "metadata.json")
	env["PUPPETEER_CACHE_DIR"] = run.temporaryRoot
	if probePath != "" {
		env["DDTEST_WDIO_PROBE"], env["DDTEST_PROBE_MODE"], env["DDTEST_PROBE_COUNTER"] = probePath, probeMode, filepath.Join(directory, "body.txt")
	}
	run.Command = shellquote.Join(append([]string{t.command}, t.args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, t.command, t.args, env)
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
	run.Facts.MissingCoverage = false
	readWebdriverIOResults(env["DDTEST_WDIO_RESULTS"], &run)
	if probePath != "" {
		body, err := os.ReadFile(env["DDTEST_PROBE_COUNTER"])
		if err == nil || os.IsNotExist(err) {
			count := strings.Count(string(body), "attempt\n")
			run.ProbeExecutions = &count
		} else {
			return run, err
		}
	}
	if run.ExitCode != 0 || run.ResultError != "" || len(run.SuiteErrors) > 0 {
		run.Diagnostic = commandDiagnostic(data)
	}
	return run, nil
}

const webdriverIOProbeSource = `it('ddtest validation probe', async function () {
 const fs = await import('node:fs');
 const counter = process.env.DDTEST_PROBE_COUNTER;
 fs.appendFileSync(counter, 'attempt\n');
 const attempts = fs.readFileSync(counter, 'utf8').trim().split('\n').length;
 if (process.env.DDTEST_PROBE_MODE === 'fail' || (process.env.DDTEST_PROBE_MODE === 'fail-once' && attempts === 1)) throw new Error('ddtest intentional failure');
});
`

func (t *Testdrive) runWebdriverIOFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	defer func() {
		for i := range result.Features {
			if result.Features[i].Name == "skipping" {
				result.Features[i].Status, result.Features[i].Reason = "not supported", webdriverIOSkippingReason
			}
		}
	}()
	probe, err := createProbe(t.repositoryRoot, baseline, webdriverIOProbeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, os.Remove(probe)) }()
	if result.Preflight == nil || result.Preflight.WebdriverIO == nil {
		return fmt.Errorf("webdriverio capability selection is unavailable")
	}
	for i, capability := range result.Preflight.WebdriverIO.Capabilities {
		firstFeature, firstRun := len(result.Features), len(result.Runs)
		child := *t
		child.webdriverIOCapability = &i
		err := runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
			value, err := child.runWebdriverIO(ctx, output, session, preload, fmt.Sprintf("capability-%d/%s", i+1, name), true, scenario, probe, mode)
			value.Name = name
			return value, err
		}, result)
		label := fmt.Sprintf("capability %d (%s)", i+1, capability["browserName"])
		for j := firstFeature; j < len(result.Features); j++ {
			result.Features[j].Project = label
		}
		for j := firstRun; j < len(result.Runs); j++ {
			result.Runs[j].Project = label
		}
		if err != nil {
			return err
		}
	}
	return nil
}
