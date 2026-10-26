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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/utils"
	"github.com/kballard/go-shellquote"
)

// First tracer releases that instrument WDIO workers; capability gates, not installation pins.
const webdriverIOMinTracer5Minor = 121
const webdriverIOMinTracer6Minor = 10

const webdriverIOSkippingReason = "dd-trace-js does not support Test Impact Analysis skipping or code coverage for WebdriverIO. These are not exercised or counted as passed checks."

func (t *Testdrive) inspectWebdriverIO(ctx context.Context, directory string) (framework.WebdriverIOConfig, error) {
	var config framework.WebdriverIOConfig
	observer, err := framework.PrepareWebdriverIOObserver(directory)
	if err != nil {
		return config, err
	}
	env := testEnvironment("http://127.0.0.1:1", "webdriverio-preflight")
	env["NODE_OPTIONS"] = stripDatadogNodeOptions(t.nodeOptions()) + " --import " + strconv.Quote(utils.FileURL(observer))
	env["DD_CIVISIBILITY_ENABLED"], env["DD_TRACE_ENABLED"] = "false", "false"
	env["DDTEST_WDIO_INSPECT"] = filepath.Join(directory, "config.json")
	data, err := t.executor.CombinedOutput(ctx, t.configurationExecutable(t.command), t.args, env)
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
	check.Node = config.Node
	check.Version = config.Version
	installer, ok := t.platform.(*platform.JavaScript)
	if !ok {
		return fmt.Errorf("webdriverio requires JavaScript tracer selection")
	}
	selection, err := installer.ResolveTestdriveTracer(ctx, platform.TracerOptions{Version: t.tracerVersion, Command: t.command, Args: t.args, Environment: t.configurationEnvironment})
	result.Selection = &selection
	if err != nil {
		return err
	}
	result.Tracer, result.TracerSource = "dd-trace@"+selection.Version, selection.Source
	check.Verdict = checkWebdriverIOSupport(*check, selection)
	// A git: build has no version until installation; Run checks it again then.
	deferred := check.Verdict.Code == webdriverIOTracerUnresolved && !t.checkOnly
	if check.Verdict.Status == "compatible" || deferred {
		if err := checkWebdriverIOServerPorts(config.ServerPorts); err != nil {
			check.Verdict = verdict{Status: "inconclusive", Code: "static_server_port_unavailable", Reason: err.Error()}
			_, _ = fmt.Fprintln(output, "Preserve the existing process. To use another port, add an optional override for both the static-server port and baseUrl, preserving CI defaults. Use the same override for both comparison modes and feature controls.")
		}
	}
	if result.CIRuntime != nil {
		result.CISelection = compareCISelection(*result.CIRuntime, selection.Version)
	}
	_, _ = fmt.Fprintf(output, "WebdriverIO %s (%s, %s); Node %s; tracer %s. Preflight: %s — %s\n", config.Version, config.Framework, config.Runner, check.Node, result.Tracer, check.Verdict.Status, check.Verdict.Reason)
	t.recommendTracer(ctx, output, result)
	if check.Verdict.Status != "compatible" && (check.Verdict.Code != webdriverIOTracerUnresolved || t.checkOnly) {
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
			return fmt.Errorf("WDIO static-server port %d is unavailable: %w", port, err)
		}
		_ = listener.Close()
		// On macOS the wildcard bind above succeeds even while another process
		// listens on 127.0.0.1 or 0.0.0.0, so also look for a live listener.
		for _, host := range []string{"127.0.0.1", "::1"} {
			address := net.JoinHostPort(host, strconv.Itoa(port))
			if connection, err := net.DialTimeout("tcp", address, webdriverIOPortProbeTimeout); err == nil {
				_ = connection.Close()
				return fmt.Errorf("WDIO static-server port %d is unavailable: another process is listening on %s", port, address)
			}
		}
	}
	return nil
}

// webdriverIOTracerUnresolved marks a git: tracer whose version is only known
// once it is installed; full runs defer the tracer check until then.
const webdriverIOTracerUnresolved = "tracer_unresolved"

// Loopback connections are refused immediately when nothing listens.
const webdriverIOPortProbeTimeout = 500 * time.Millisecond

func checkWebdriverIOSupport(check jestPreflight, selection platform.JSSelection) verdict {
	if check.WebdriverIO != nil && len(check.WebdriverIO.CloudGrid) > 0 {
		return verdict{Status: "inconclusive", Code: "cloud_grid_unsupported", Reason: "Cloud grids are not supported locally: " + strings.Join(check.WebdriverIO.CloudGrid, ", ")}
	}
	if check.WebdriverIO != nil && check.WebdriverIO.Multiremote {
		return verdict{Status: "inconclusive", Code: "multiremote_unsupported", Reason: "WebdriverIO multiremote (object-form capabilities) is not supported by testdrive; it remains unvalidated. Preserve the original config."}
	}
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
	if len(check.WebdriverIO.Capabilities) == 0 || (len(check.WebdriverIO.Files) == 0 && check.WebdriverIO.FileCount == 0) {
		return verdict{Status: "inconclusive", Reason: "Effective WebdriverIO capabilities and selected specs must be resolved; multiremote/empty selections remain unvalidated."}
	}
	if selection.Version == "" && strings.HasPrefix(selection.Requested, "git:") {
		return verdict{Status: "inconclusive", Code: webdriverIOTracerUnresolved, Reason: "The git: dd-trace build has no version until it is installed; its WebdriverIO and Node requirements are checked after installation."}
	}
	tracerMajor, tracerMinor, known := releaseParts(selection.Version)
	if !known || tracerMajor < 5 || tracerMajor == 5 && tracerMinor < webdriverIOMinTracer5Minor || tracerMajor == 6 && tracerMinor < webdriverIOMinTracer6Minor {
		return verdict{Status: "incompatible", Reason: "WebdriverIO requires dd-trace >=5.121.0 on v5 or >=6.10.0 on v6. Resolve a compatible release; these are capability minimums, not installation pins."}
	}
	status, reason := onboard.CompareNodeRequirement(check.Node, selection.Node)
	if status != "compatible" {
		return verdict{Status: status, Reason: reason}
	}
	reason = "Runtime and tracer requirements match. Paired execution and supported feature controls are still required; TIA skipping/coverage are not supported."
	if limitation := webdriverIOJasmineLimitation(check.WebdriverIO, "auto-retries", selection.Version); limitation != "" {
		reason += " " + limitation
	}
	return verdict{Status: "compatible", Reason: reason}
}

// dd-trace's WDIO Jasmine hook reads the running spec when jasmine-core's
// Spec execution starts; jasmine-core 5 only sets it later, so these
// controls never engage there.
var webdriverIOJasmineLimitedFeatures = []string{"auto-retries", "early-flake-detection", "disabled", "attempt-to-fix"}

// webdriverIOJasmineFixedMinor maps a dd-trace major to its first release
// that sees jasmine-core 5 specs at execution start (DataDog/dd-trace-js#10929).
// Add each release line once it ships; until then every tracer is limited.
var webdriverIOJasmineFixedMinor = map[int]int{}

// webdriverIOJasmineLimitation explains a feature the tracer is known not to
// apply for this WDIO Jasmine installation, or returns "". An empty tracer
// (not selected yet) is treated as limited.
func webdriverIOJasmineLimitation(config *framework.WebdriverIOConfig, feature, tracer string) string {
	if config == nil || config.Framework != "jasmine" || !slices.Contains(webdriverIOJasmineLimitedFeatures, feature) {
		return ""
	}
	if tracerMajor, tracerMinor, known := releaseParts(strings.TrimPrefix(tracer, "dd-trace@")); known {
		if fixed, ok := webdriverIOJasmineFixedMinor[tracerMajor]; ok && tracerMinor >= fixed {
			return ""
		}
	}
	major, _, known := releaseParts(config.JasmineCore)
	if !known || major >= 6 {
		return ""
	}
	return "Known dd-trace limitation: with jasmine-core " + config.JasmineCore + " (<6), the tracer's WebdriverIO Jasmine hook does not see the running spec, so auto-retries, early flake detection, disabled and attempt-to-fix are not applied. Quarantine is unaffected. Upgrading to WebdriverIO 10 (jasmine-core 6) enables them; no other customer change is expected to."
}

type webdriverIOWorkerReport struct {
	Complete bool
	Retry    int
	// The worker's specFileRetries, when the reporter could read it.
	SpecFileRetries          *int
	Specs                    []string
	Browser, Capability, CID string
	Tests                    []struct {
		ID, File, Name, Status, Failure string
		Attempts                        []string
	}
	Errors []string
}

func readWebdriverIOResults(directory string, run *validationRun) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		run.ResultError = "WebdriverIO native worker evidence is missing: no native results were written"
		return
	}
	var metadata struct {
		Node       string
		Selections []struct {
			Browser, Capability string
			Files               []string
		}
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(directory), "metadata.json"))
	if err != nil || json.Unmarshal(data, &metadata) != nil || len(metadata.Selections) == 0 {
		run.ResultError = "WebdriverIO effective worker selection is unavailable"
		return
	}
	run.FrameworkNode = metadata.Node
	pending := map[string]bool{}
	for _, selection := range metadata.Selections {
		for _, file := range selection.Files {
			pending[selection.Capability+"\x00"+filepath.Clean(file)] = true
		}
	}
	groups := map[string][]webdriverIOWorkerReport{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var report webdriverIOWorkerReport
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil || json.Unmarshal(data, &report) != nil || !report.Complete || report.Tests == nil || len(report.Specs) == 0 || report.Retry < 0 {
			run.ResultError = "WebdriverIO native worker results are invalid or incomplete"
			return
		}
		for i, file := range report.Specs {
			report.Specs[i] = filepath.Clean(file)
			key := report.Capability + "\x00" + report.Specs[i]
			if _, ok := pending[key]; !ok {
				run.ResultError = "WebdriverIO native report contains an unselected spec or capability"
				return
			}
			pending[key] = false
		}
		slices.Sort(report.Specs)
		// WebdriverIO 9.20 numbers spec-file attempts 0..specFileRetries.
		// Later releases start a spec with -1 remaining retries, so its first
		// attempt reports specFileRetries+1 and the retries that follow 1, 2, ...
		if report.SpecFileRetries != nil && report.Retry > *report.SpecFileRetries {
			report.Retry = 0
		}
		key := report.Capability + "\x00" + report.CID + "\x00" + strings.Join(report.Specs, "\x00")
		groups[key] = append(groups[key], report)
	}
	for _, missing := range pending {
		if missing {
			run.ResultError = "WebdriverIO native reports do not cover the selected specs and capabilities"
			return
		}
	}
	if len(groups) == 0 {
		run.ResultError = "WebdriverIO native worker evidence is missing: no worker finished its native report"
		return
	}
	for _, reports := range groups {
		slices.SortFunc(reports, func(a, b webdriverIOWorkerReport) int { return a.Retry - b.Retry })
		// Native attempts per test across test and spec-file retries, so paired
		// runs compare retry behavior and not only final outcomes.
		history := map[string][]string{}
		for i, report := range reports {
			if report.Retry != i {
				attempts := make([]string, len(reports))
				for j, report := range reports {
					attempts[j] = strconv.Itoa(report.Retry)
				}
				run.ResultError = fmt.Sprintf("WebdriverIO native spec retry sequence is incomplete or duplicated (attempts %s)", strings.Join(attempts, ", "))
				return
			}
			final := i == len(reports)-1
			if final {
				run.SuiteErrors = append(run.SuiteErrors, report.Errors...)
			}
			for _, test := range report.Tests {
				if test.File == "" || !slices.Contains([]string{"passed", "failed", "skipped"}, test.Status) {
					run.ResultError = "WebdriverIO native test identity or status is invalid"
					return
				}
				file := test.File
				if !filepath.IsAbs(file) {
					file = filepath.Join(run.root, file)
				}
				value := jestTest{File: filepath.Clean(file), Name: test.Name, Status: test.Status, WebdriverIOBrowser: report.Browser, WebdriverIOCapability: report.Capability, WebdriverIOTestID: test.ID, Failure: failureSignature(test.Failure)}
				attempts := test.Attempts
				if len(attempts) == 0 {
					attempts = []string{test.Status}
				}
				identity := value.File + "\x00" + value.Name + "\x00" + value.WebdriverIOTestID
				for _, status := range attempts {
					if !slices.Contains([]string{"passed", "failed", "skipped"}, status) {
						run.ResultError = "WebdriverIO native attempt status is invalid"
						return
					}
					attempt := value
					attempt.Status = status
					run.webdriverIOAttempts = append(run.webdriverIOAttempts, attempt)
				}
				history[identity] = append(history[identity], attempts...)
				if final {
					value.WebdriverIOAttempts = slices.Clone(history[identity])
					run.Tests = append(run.Tests, value)
				}
			}
		}
	}
}

func verifyWebdriverIOTelemetry(run validationRun) verdict {
	run.framework = "webdriverio-attempts"
	if len(run.webdriverIOAttempts) > 0 {
		run.Tests = run.webdriverIOAttempts
	}
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

func (t *Testdrive) runWebdriverIO(ctx context.Context, output io.Writer, session *Session, preload string, spec runSpec) (run validationRun, runErr error) {
	name, instrumented, scenario, probePath, probeMode := spec.Name, spec.Instrumented, spec.Scenario, spec.ProbePath, spec.ProbeMode
	run.Name, run.Role, run.Instrumented, run.ProbeMode, run.root, run.framework = name, spec.Role, instrumented, probeMode, t.checkoutRoot(), "webdriverio"
	run.ExecutionContext = "Both comparison modes use ddtest native-result observers and process supervision. Baseline disables dd-trace; it is not an unmodified shell invocation."
	directory := filepath.Join(session.Directory(), name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return run, err
	}
	observer, err := framework.PrepareWebdriverIOObserver(directory)
	if err != nil {
		return run, err
	}
	children := filepath.Join(directory, "children.cjs")
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
	env["NODE_OPTIONS"] += " --require " + strconv.Quote(children) + " --import " + strconv.Quote(utils.FileURL(observer))
	owner, err := t.ownProcesses(directory, env)
	if err != nil {
		return run, err
	}
	defer func() {
		runErr = errors.Join(runErr, owner.Close())
	}()
	env["DDTEST_WDIO_INSPECT"], env["DDTEST_WDIO_PROBE"], env["DDTEST_WDIO_FILES"], env["DDTEST_WDIO_CAPABILITY"] = "", "", "", ""
	if t.webdriverIOCapability != nil {
		env["DDTEST_WDIO_CAPABILITY"] = strconv.Itoa(*t.webdriverIOCapability)
	}
	env["DDTEST_WDIO_RESULTS"] = filepath.Join(directory, "native")
	env["DDTEST_WDIO_METADATA"] = filepath.Join(directory, "metadata.json")
	webdriverIOCacheEnvironment(env)
	if probePath != "" {
		env["DDTEST_WDIO_PROBE"], env["DDTEST_PROBE_MODE"], env["DDTEST_PROBE_COUNTER"] = probePath, probeMode, filepath.Join(directory, "body.txt")
	}
	run.Command = shellquote.Join(append([]string{t.command}, t.args...)...)
	_, _ = fmt.Fprintf(output, "Running %s...\n", name)
	data, commandErr := t.executor.CombinedOutput(ctx, t.configurationExecutable(t.command), t.args, env)
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

const webdriverIOProbeSource = `(typeof it === 'function' ? it : test)('ddtest validation probe', async function () {
 if (typeof jasmine !== 'undefined') expect(true).toBe(true);
 const fs = await import('node:fs');
 const counter = process.env.DDTEST_PROBE_COUNTER;
 fs.appendFileSync(counter, 'attempt\n');
 const attempts = fs.readFileSync(counter, 'utf8').trim().split('\n').length;
 if (process.env.DDTEST_PROBE_MODE === 'fail' || (process.env.DDTEST_PROBE_MODE === 'fail-once' && attempts === 1)) throw new Error('ddtest intentional failure');
});
`

func (t *Testdrive) runWebdriverIOFeatures(ctx context.Context, output io.Writer, session *Session, preload string, baseline validationRun, result *validationResult) (runErr error) {
	probe, err := createProbe(t.repositoryRoot, baseline, webdriverIOProbeSource)
	if err != nil {
		unavailableFeatures(result, err.Error())
		return nil
	}
	defer func() { runErr = errors.Join(runErr, removeProbe(t.repositoryRoot, probe)) }()
	if result.Preflight == nil || result.Preflight.WebdriverIO == nil {
		return fmt.Errorf("webdriverio capability selection is unavailable")
	}
	for i, capability := range result.Preflight.WebdriverIO.Capabilities {
		firstFeature, firstRun := len(result.Features), len(result.Runs)
		child := *t
		child.webdriverIOCapability = &i
		err := runProbeFeatures(func(name string, scenario intake.Scenario, mode string) (validationRun, error) {
			role := roleProbeFeature
			if scenario.Feature == "" {
				role = roleProbeControl
			}
			value, err := child.runWebdriverIO(ctx, output, session, preload, runSpec{Name: fmt.Sprintf("capability-%d/%s", i+1, name), Role: role, Instrumented: true, Scenario: scenario, ProbePath: probe, ProbeMode: mode})
			value.Name = name
			return value, err
		}, result)
		label := fmt.Sprintf("capability %d", i+1)
		if capability["browserName"] != "" {
			label += " (" + capability["browserName"] + ")"
		}
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

// webdriverIOCacheEnvironment keeps WebdriverIO's own default download cache,
// os.tmpdir() of the real temporary folder, for a config without cacheDir.
// Runs redirect TMPDIR into the session, which is removed afterwards, so the
// default would otherwise download browsers and drivers again on every run.
func webdriverIOCacheEnvironment(env map[string]string) {
	env["DDTEST_WDIO_CACHE"] = os.TempDir()
}

func webdriverIOOutcome(test jestTest) string {
	if test.WebdriverIOCapability == "" && test.WebdriverIOTestID == "" {
		return ""
	}
	return " [capability " + webdriverIOCapabilityNumber(test.WebdriverIOCapability) + ", test " + test.WebdriverIOTestID + "]"
}

// webdriverIOAttemptOutcome is the native retry history of a test that was
// retried; a single attempt adds nothing beyond its final outcome.
func webdriverIOAttemptOutcome(test jestTest) string {
	if len(test.WebdriverIOAttempts) < 2 {
		return ""
	}
	return "\nattempts=" + strings.Join(test.WebdriverIOAttempts, ",")
}

// Native capability indices start at 0; reports number capabilities from 1.
func webdriverIOCapabilityNumber(index string) string {
	if value, err := strconv.Atoi(index); err == nil && value >= 0 {
		return strconv.Itoa(value + 1)
	}
	return index
}

// installedFeatureLimitations reads the installed packages, before any edit or
// run, for features the tracer is known not to apply, so the plan names them
// up front. Only WebdriverIO with jasmine-core <6 is known today.
func installedFeatureLimitations(root, frameworkName string) map[string]string {
	if frameworkName != "webdriverio" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "@wdio", "jasmine-framework", "package.json")); err != nil {
		return nil
	}
	version := ""
	for _, candidate := range []string{
		filepath.Join(root, "node_modules", "@wdio", "jasmine-framework", "node_modules", "jasmine-core", "package.json"),
		filepath.Join(root, "node_modules", "jasmine-core", "package.json"),
	} {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &manifest) == nil && manifest.Version != "" {
			version = manifest.Version
			break
		}
	}
	config := &framework.WebdriverIOConfig{Framework: "jasmine", JasmineCore: version}
	limitations := map[string]string{}
	for _, feature := range webdriverIOJasmineLimitedFeatures {
		if reason := webdriverIOJasmineLimitation(config, feature, ""); reason != "" {
			limitations[feature] = reason
		}
	}
	if len(limitations) == 0 {
		return nil
	}
	return limitations
}
