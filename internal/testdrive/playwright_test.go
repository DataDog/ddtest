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
	result.Features[0] = unsupported
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

func TestPlaywrightReporterUsesDeviceBrowserDefault(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for reporter regression")
	}
	root := t.TempDir()
	reporter := filepath.Join(root, "reporter.cjs")
	result := filepath.Join(root, "results.json")
	requireWriteFile(t, reporter, playwrightReporter)
	script := `const Reporter=require(process.argv[1]);
 const reporter=new Reporter();
 const project={name:'iPhone',testDir:'/repo/test',use:{defaultBrowserType:'webkit'}};
 const test={parent:{project:()=>project},titlePath:()=>['','iPhone','a.spec.ts','opens'],title:'opens',location:{file:'/repo/test/a.spec.ts'},results:[],repeatEachIndex:0,expectedStatus:'passed'};
 reporter.onBegin({version:'1.41.2',projects:[project]},{allTests:()=>[test]});reporter.onEnd();`
	command := exec.CommandContext(t.Context(), node, "-e", script, reporter)
	command.Env = append(os.Environ(), "DDTEST_PLAYWRIGHT_REPORT="+result)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var run validationRun
	_, err = readPlaywrightResults(result, &run)
	require.NoError(t, err)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "webkit", run.Tests[0].Playwright.Browser)
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
	require.Equal(t, "inconclusive", got.Status)
	require.Contains(t, got.Reason, "1 test identities have recurring differences")
	require.Contains(t, got.Reason, "1 vary between repeats")
	require.Contains(t, got.Differences[0], `project="iPhone"`)
	require.Contains(t, got.Differences[1], `project="Pixel"`)
}
