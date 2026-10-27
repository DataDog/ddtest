package testdrive

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPlaywrightNativeCountsAndAttemptTelemetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	requireWriteFile(t, path, `{"version":"1.51.0","tests":[{"file":"/repo/test.ts","name":"suite test","status":"passed","project":"desktop","browser":"chromium","repeat":0,"expectedStatus":"passed","attempts":[{"status":"failed","retry":0,"failure":"known failure"},{"status":"passed","retry":1,"failure":""}]}],"errors":[]}`)
	run := validationRun{framework: "playwright", root: "/repo"}
	_, err := readPlaywrightResults(path, &run)
	require.NoError(t, err)
	require.Equal(t, 1, run.summary().TestCounts.Passed)
	run.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{{Name: "suite test", SourceFile: "test.ts", Parameters: `{"arguments":{"browser":"desktop"},"metadata":{}}`, Attempts: []intake.TestRun{{Status: "fail"}, {Status: "pass", Retry: true}}}}}
	require.Empty(t, verifyPlaywrightTelemetry(run).Status, "native retries must match, not be mistaken for tracer retries")
	run.Facts.Tests[0].Parameters = `{"arguments":{"browser":"firefox"}}`
	require.Equal(t, "inconclusive", verifyPlaywrightTelemetry(run).Status)
	run.Facts.Tests[0].Parameters = `{"arguments":{"browser":"desktop"}}`
	run.Facts.Tests[0].Attempts = run.Facts.Tests[0].Attempts[1:]
	require.Equal(t, "inconclusive", verifyPlaywrightTelemetry(run).Status, "missing attempt must fail validation")
}

func TestPlaywrightComparisonPreservesProjectAndExpectedStatus(t *testing.T) {
	baseline := validationRun{Tests: []jestTest{{File: "/repo/a.ts", Name: "same", Status: "failed", Failure: "known failure", Playwright: &playwrightIdentity{Project: "desktop", Browser: "chromium", ExpectedStatus: "failed", Attempts: []playwrightAttempt{{Status: "failed"}}}}}}
	data, err := json.Marshal(baseline.Tests)
	require.NoError(t, err)
	var tests []jestTest
	require.NoError(t, json.Unmarshal(data, &tests))
	instrumented := validationRun{Tests: tests}
	require.Equal(t, outcomeKeys(baseline), outcomeKeys(instrumented))
	instrumented.Tests[0].Playwright.Project = "mobile"
	require.NotEqual(t, outcomeKeys(baseline), outcomeKeys(instrumented))
	instrumented.Tests[0].Playwright.Project = "desktop"
	instrumented.Tests[0].Playwright.ExpectedStatus = "passed"
	require.NotEqual(t, outcomeKeys(baseline), outcomeKeys(instrumented))
}

func TestPlaywrightUnsupportedSkippingIsNotAPassOrABlocker(t *testing.T) {
	result := pairedReport("playwright", "passed")
	result.Framework = "playwright"
	unsupported := featureResult{Project: "Web", Name: "skipping", Status: "not supported", Reason: playwrightSkippingReason}
	for i := range result.Features {
		if result.Features[i].Name == "skipping" {
			result.Features[i] = unsupported
		}
	}
	prepareValidationResult(t.TempDir(), &result)
	require.True(t, result.LocalSuccess)
	require.True(t, featureSatisfied("playwright", unsupported))
	require.False(t, featureSatisfied("jest", unsupported))
	require.False(t, featureSatisfied("playwright", featureResult{Name: "auto-retries", Status: "not supported"}))
	model := buildReport("/repo", intake.Facts{TestEventCount: 1, TestCount: 1, MissingCoverage: true}, false, reportRuntime{Framework: "Playwright"})
	data, err := json.Marshal(model)
	require.NoError(t, err)
	require.Contains(t, string(data), "Playwright coverage and skipping are not supported")
	require.NotContains(t, string(data), "Test Impact Analysis is not set up correctly")
}

func TestPlaywrightSupportUsesRuntimeAndKnownMinimum(t *testing.T) {
	check := jestPreflight{Node: "20.20.2", Version: "1.51.0", PlaywrightProjects: []playwrightProject{{Name: "chromium"}}}
	require.Equal(t, "compatible", checkPlaywrightSupport(check, platform.JSSelection{Version: "5.131.1", Node: ">=18"}).Status)
	require.Equal(t, "incompatible", checkPlaywrightSupport(check, platform.JSSelection{Version: "6.20.1", Node: ">=22"}).Status)
	check.Version = "1.37.0"
	require.Equal(t, "incompatible", checkPlaywrightSupport(check, platform.JSSelection{Node: ">=18"}).Status)
	check.Version = "2.0.0"
	require.Equal(t, "compatible", checkPlaywrightSupport(check, platform.JSSelection{Node: ">=18"}).Status, "do not add a separate major allowlist")
}

func TestPlaywrightMissingResults(t *testing.T) {
	var run validationRun
	_, err := readPlaywrightResults(filepath.Join(t.TempDir(), "missing"), &run)
	require.Error(t, err)
	require.NotEmpty(t, run.ResultError)
}

func TestPlaywrightDisabledProbeWithoutModule(t *testing.T) {
	control := intake.Test{Module: "playwright", Suite: "probe.spec.ts", Name: probeName, SourceFile: "test/probe.spec.ts", Parameters: `{"arguments":{"browser":"chromium"}}`}
	tags := map[string]string{"test.suite": control.Suite, "test.name": control.Name, "test.source.file": control.SourceFile, "test.parameters": control.Parameters, "test.status": "skip", "test.test_management.is_test_disabled": "true"}
	run := validationRun{framework: "playwright", Tests: []jestTest{{Name: probeName, Status: "skipped", Playwright: &playwrightIdentity{Project: "chromium", Browser: "chromium"}}}, Facts: intake.Facts{TestEventCount: 1, Events: []intake.Event{{Type: "test", Tags: tags}}}}
	require.Equal(t, "passed", evaluateFeature("disabled", run, control).Status)
	require.Contains(t, evaluateFeature("disabled", run, control).Reason, "tracer omitted module/browser identity")
	for key, value := range map[string]string{"test.module": "another", "test.suite": "another", "test.name": "another", "test.source.file": "another", "test.parameters": `{"arguments":{"browser":"firefox"}}`, "test.status": "pass", "test.test_management.is_test_disabled": "false"} {
		t.Run(key, func(t *testing.T) {
			modified := maps.Clone(tags)
			modified[key] = value
			run.Facts.Events = []intake.Event{{Type: "test", Tags: modified}}
			require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
		})
	}
	run.Facts.Events = []intake.Event{{Type: "test", Tags: tags}}
	delete(tags, "test.parameters")
	require.Equal(t, "passed", evaluateFeature("disabled", run, control).Status)
	require.Contains(t, evaluateFeature("disabled", run, control).Reason, "tracer omitted module/browser identity")
	run.Tests[0].Playwright.Project = "firefox"
	require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
	run.Tests[0].Playwright.Project = "chromium"
	run.framework = "jest"
	require.NotEqual(t, "passed", evaluateFeature("disabled", run, control).Status)
}

func TestPlaywrightFailureGuardPreservesArgumentsAndExitBehavior(t *testing.T) {
	for _, status := range []int{0, 1, 7} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			drive := Testdrive{command: "sh", playwrightGuard: true}
			command, args := drive.playwrightInvocation([]string{"-c", `printf '%s' "$1"; exit "$2"`, "test", `literal; $(echo unsafe)`, strconv.Itoa(status)})
			output, err := exec.CommandContext(t.Context(), command, args...).CombinedOutput()
			require.Equal(t, `literal; $(echo unsafe)`, string(output))
			expected := 0
			if status != 0 {
				expected = 1
			}
			require.Equal(t, expected, commandExitCode(err))
		})
	}
}

func TestPlaywrightDiagnosticPreservesFailureCauseBeforeLongTitleList(t *testing.T) {
	run := validationRun{Tests: []jestTest{{Name: "opens drawer", Failure: "Cannot find module vaul/dist/index.js"}}}
	diagnostic := playwrightDiagnostic(run, []byte(strings.Repeat("failed test title\n", 200)))
	require.Contains(t, diagnostic, "Cannot find module vaul/dist/index.js")
	require.LessOrEqual(t, len([]rune(diagnostic)), 1100)
	run.SuiteErrors = []string{"Browser launch failed"}
	require.Contains(t, playwrightDiagnostic(run, nil), "Browser launch failed")
}

// writePlaywrightReporterFixture installs the validation reporter beside a fake
// playwright package whose reporters use either layout.
func writePlaywrightReporterFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	reporter := filepath.Join(root, "reporter.cjs")
	requireWriteFile(t, reporter, playwrightReporter)
	files["package.json"] = `{"name":"playwright","version":"0.0.0"}`
	for name, content := range files {
		path := filepath.Join(root, "node_modules", "playwright", name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		requireWriteFile(t, path, content)
	}
	return reporter
}

func runPlaywrightReporterFixture(t *testing.T, reporter string) (validationRun, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for reporter regression")
	}
	result := filepath.Join(filepath.Dir(reporter), "results.json")
	script := `const Reporter=require(process.argv[1]);
 const reporter=new Reporter();
 const project={name:'iPhone',testDir:'/repo/test',use:{defaultBrowserType:'webkit'}};
 const test={parent:{project:()=>project},titlePath:()=>['','iPhone','a.spec.ts','opens'],title:'opens',location:{file:'/repo/test/a.spec.ts'},results:[],repeatEachIndex:0,expectedStatus:'passed'};
 reporter.onBegin({version:'1.41.2',projects:[project]},{allTests:()=>[test]});Promise.resolve(reporter.onEnd({status:'passed'})).catch(e=>{console.error(e);process.exit(1)});`
	command := exec.CommandContext(t.Context(), node, "-e", script, reporter)
	command.Env = append(os.Environ(), "DDTEST_PLAYWRIGHT_REPORT="+result)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var run validationRun
	_, err = readPlaywrightResults(result, &run)
	require.NoError(t, err)
	return run, string(output)
}

func TestPlaywrightReporterUsesDeviceBrowserDefault(t *testing.T) {
	reporter := writePlaywrightReporterFixture(t, map[string]string{
		"lib/reporters/line.js": `exports.default=class{onBegin(){}onEnd(){}onError(){}}`,
	})
	run, _ := runPlaywrightReporterFixture(t, reporter)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "webkit", run.Tests[0].Playwright.Browser)
}

func TestPlaywrightReporterUsesTheBundledTerminalReporter(t *testing.T) {
	// Playwright 1.60+ has no lib/reporters/line.js; its terminal reporters are
	// bundled into lib/runner, which exports only their v2 TerminalReporter base.
	reporter := writePlaywrightReporterFixture(t, map[string]string{
		"lib/runner/index.js": `exports.base={TerminalReporter:class{
  version(){return 'v2'}
  onConfigure(config){this.configured=config}
  onBegin(suite){if(!this.configured)throw new Error('onConfigure must precede onBegin');this.total=suite.allTests().length}
  onError(){}
  async onEnd(){}
  epilogue(full){console.log('summary printed: '+this.total+' full='+full)}
}}`,
	})
	run, output := runPlaywrightReporterFixture(t, reporter)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "webkit", run.Tests[0].Playwright.Browser)
	require.Contains(t, output, "summary printed: 1 full=false", "the tracer reports disabled tests in the terminal summary")
}

func TestPlaywrightLaunchComparisonIgnoresOnlyPerProcessNoise(t *testing.T) {
	failure := "Error: browserType.launch: Target page, context or browser has been closed\n<launching> chromium --user-data-dir=/tmp/playwright_chromiumdev_profile-Abc123\n<launched> pid=123\n[pid=123][err] Received signal 11 SEGV_ACCERR 000100000008\n[pid=123][err] [0x000123abcd]"
	another := strings.NewReplacer("Abc123", "Xyz999", "pid=123", "pid=999", "0x000123abcd", "0x000999abcd").Replace(failure)
	require.Equal(t, playwrightFailureSignature(failure), playwrightFailureSignature(another))
	require.NotEqual(t, playwrightFailureSignature(failure), playwrightFailureSignature(strings.ReplaceAll(another, "signal 11", "signal 6")))
	require.NotEqual(t, playwrightFailureSignature(failure), playwrightFailureSignature(another+"\nNew browser startup error"))
	require.Equal(t, "Error: expected pid=123", playwrightFailureSignature("Error: expected pid=123"))
	run := validationRun{Tests: []jestTest{{Name: "opens browser", Failure: strings.ReplaceAll(failure, "<launching>", "<launching> "+strings.Repeat("--flag ", 300))}}}
	require.Contains(t, playwrightDiagnostic(run, nil), "Received signal 11")
}

func TestPlaywrightRepeatComparisonSeparatesBrowserProjects(t *testing.T) {
	run := func(phoneStatus, pixelFailure string) validationRun {
		return validationRun{root: "/repo", Tests: []jestTest{
			{File: "/repo/a.spec.ts", Name: "opens", Status: phoneStatus, Playwright: &playwrightIdentity{Project: "iPhone", Browser: "webkit"}},
			{File: "/repo/a.spec.ts", Name: "opens", Status: "failed", Failure: pixelFailure, Playwright: &playwrightIdentity{Project: "Pixel", Browser: "chromium"}},
		}}
	}
	got := compareRepeatedJavaScript(run("passed", "crash one"), run("failed", "crash two"), run("passed", "crash three"), run("failed", "crash four"))
	require.Equal(t, "suspected regression", got.Status)
	require.Contains(t, got.Reason, "1 difference recurs in both pairs")
	require.Contains(t, got.Reason, "1 varies between repeats")
	require.Contains(t, got.Differences[0], `project="iPhone"`)
	require.Contains(t, got.Differences[1], `project="Pixel"`)
}

func TestPlaywrightProbePreloadLeavesStdinHooksAlone(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required")
	}
	directory := t.TempDir()
	env := map[string]string{}
	require.NoError(t, preparePlaywrightProbe(directory, env, "probe.spec.cjs", "desktop"))
	command := exec.CommandContext(t.Context(), node, "-")
	command.Stdin = strings.NewReader("console.log('setup completed')")
	command.Env = append(os.Environ(), "NODE_OPTIONS="+env["NODE_OPTIONS"], "DDTEST_PLAYWRIGHT_PROBE=probe.spec.cjs")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "setup completed\n", string(output))
}

func TestPlaywrightRecordsTheConfiguredReporterItReplaces(t *testing.T) {
	t.Setenv("PW_TEST_REPORTER", "")
	require.Nil(t, replacedPlaywrightReporter(nil))
	require.Equal(t, []string{"PW_TEST_REPORTER=./ci-reporter.js"}, replacedPlaywrightReporter(map[string]string{"PW_TEST_REPORTER": "./ci-reporter.js"}))
	t.Setenv("PW_TEST_REPORTER", "./local.js")
	require.Equal(t, []string{"PW_TEST_REPORTER=./local.js"}, replacedPlaywrightReporter(nil))
}

func TestPlaywrightPreflightDefersGitTracersToInstallation(t *testing.T) {
	require.False(t, playwrightPreflightBlocks("compatible", "latest", false))
	require.True(t, playwrightPreflightBlocks("inconclusive", "latest", false))
	require.False(t, playwrightPreflightBlocks("inconclusive", "git:main", false), "the installed source build is checked later")
	require.True(t, playwrightPreflightBlocks("inconclusive", "git:main", true), "check-only never installs")
	require.True(t, playwrightPreflightBlocks("incompatible", "git:main", false))
}

func TestFocusedCommandEnvironmentIsReportedWithSecretsMasked(t *testing.T) {
	require.Nil(t, reportedEnvironment(nil))
	reported := reportedEnvironment(map[string]string{"PLAYWRIGHT_JUNIT_OUTPUT_NAME": "results.xml", "CYPRESS_RECORD_KEY": "abc", "NPM_TOKEN": "def"})
	require.Equal(t, map[string]string{"PLAYWRIGHT_JUNIT_OUTPUT_NAME": "results.xml", "CYPRESS_RECORD_KEY": "[REDACTED]", "NPM_TOKEN": "[REDACTED]"}, reported)
	data, err := marshalValidation(validationResult{CommandEnvironment: reported})
	require.NoError(t, err)
	require.Contains(t, string(data), `"PLAYWRIGHT_JUNIT_OUTPUT_NAME": "results.xml"`)
	require.NotContains(t, string(data), "abc")
}

func TestPlaywrightGeneratedOptionsPrecedeTheFileTerminator(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		want    []string
	}{
		{"playwright", []string{"test", "--", "tests/foo.spec.js"}, []string{"test", "--list", "--", "tests/foo.spec.js"}},
		{"npx", []string{"playwright", "test", "--", "tests/foo.spec.js"}, []string{"playwright", "test", "--list", "--", "tests/foo.spec.js"}},
		{"playwright", []string{"test", "--project=chromium"}, []string{"test", "--project=chromium", "--list"}},
		{"npm", []string{"test"}, []string{"test", "--", "--list"}},
		{"npm", []string{"test", "--", "--", "tests/foo.spec.js"}, []string{"test", "--", "--list", "--", "tests/foo.spec.js"}},
		{"npm", []string{"exec", "playwright", "test"}, []string{"exec", "playwright", "test", "--", "--list"}},
		{"npm", []string{"exec", "--", "playwright", "test", "--", "tests/foo.spec.js"}, []string{"exec", "--", "playwright", "test", "--list", "--", "tests/foo.spec.js"}},
	} {
		require.Equal(t, tc.want, playwrightArgs(tc.command, tc.args, "--list"), "%s %v", tc.command, tc.args)
	}
}
