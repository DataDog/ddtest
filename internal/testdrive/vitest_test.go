package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestVitestTelemetryUsesTitlePartsWithoutChangingRunnerIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title, fullName, tracerName string
		ancestors                         []string
	}{
		{name: "empty title", ancestors: []string{"encodeHash"}, title: "", fullName: "encodeHash", tracerName: "encodeHash "},
		{name: "empty ancestor", ancestors: []string{"outer", "", "inner"}, title: "test", fullName: "outer  inner test", tracerName: "outer inner test"},
		{name: "meaningful whitespace", ancestors: []string{" suite "}, title: " test \n", fullName: " suite   test \n", tracerName: " suite   test \n"},
		{name: "top level", ancestors: []string{}, title: "test", fullName: "test", tracerName: "test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertion := map[string]any{"ancestorTitles": tc.ancestors, "title": tc.title, "fullName": tc.fullName, "status": "passed"}
			data, err := json.Marshal(map[string]any{"testResults": []any{map[string]any{
				"name": "/repo/test.ts", "assertionResults": []any{assertion, assertion},
			}}})
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "results.json")
			requireWriteFile(t, path, string(data))
			for _, label := range []string{"Vitest", "Jest"} {
				var baseline, instrumented validationRun
				readRunnerResults(path, label, &baseline)
				readRunnerResults(path, label, &instrumented)
				require.Equal(t, tc.fullName, instrumented.Tests[0].Name)
				name := tc.fullName
				if label == "Vitest" {
					name = tc.tracerName
				}
				instrumented.Facts = intake.Facts{TestEventCount: 2, Tests: []intake.Test{{
					Name: name, SourceFile: "/repo/test.ts", Attempts: []intake.TestRun{{Status: "pass"}, {Status: "pass"}},
				}}}
				require.Equal(t, "compatible", compareJest(baseline, instrumented).Status, label)
				instrumented.Facts.Tests[0].Name = name + " "
				require.Equal(t, "inconclusive", compareJest(baseline, instrumented).Status, "must not trim whitespace")
				instrumented.Facts.Tests[0].Name = name
				instrumented.Facts.Tests[0].Attempts = instrumented.Facts.Tests[0].Attempts[:1]
				require.Equal(t, "inconclusive", compareJest(baseline, instrumented).Status, "duplicate test counts must match")
			}
		})
	}
}

type vitestErrorExecutor struct{ t *testing.T }

func (e vitestErrorExecutor) CombinedOutput(_ context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	require.Contains(e.t, args, "--reporter=default")
	require.Contains(e.t, args, "--reporter=json")
	if separator := slices.Index(args, "--"); separator >= 0 {
		require.Less(e.t, slices.Index(args, "--reporter=json"), separator, "validation flags must precede the argument terminator")
	}

	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, "--outputFile="); ok {
			requireWriteFile(e.t, path, `{"success":true,"testResults":[{"name":"probe.test.ts","assertionResults":[{"fullName":"ddtest validation probe","status":"passed"}]}]}`)
		}
	}
	return []byte("\x1b[31mUnhandled Errors\x1b[0m\nUnhandled Rejection\nError: Unable to deserialize cloned data\n" +
		" Test Files 1 passed\n % Coverage report from v8\n" + strings.Repeat("coverage row\n", 1000)), errors.New("exit status 1")
}

func TestVitestRetainsUnhandledErrorsDespitePassingJSONAndCoverageOutput(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.command, drive.args = "pnpm", []string{"vitest", "--coverage"}
	drive.executor = vitestErrorExecutor{t}
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	run, err := drive.runVitest(t.Context(), &bytes.Buffer{}, session, "/trace/ci/init.js", runSpec{Name: "early-flake-detection", Role: roleProbeFeature, Instrumented: true, ProbePath: "probe.test.ts", ProbeMode: "pass"})
	require.NoError(t, err)
	require.NotZero(t, run.ExitCode)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
	require.Equal(t, "passed", run.Tests[0].Status)
	require.Contains(t, run.summary().Diagnostic, "Error: Unable to deserialize cloned data")
	require.NotContains(t, run.Diagnostic, "coverage row")
	require.NotContains(t, run.Diagnostic, "\x1b")
	require.LessOrEqual(t, len([]rune(run.Diagnostic)), 1024)
	drive.framework = &framework.Vitest{}
	require.NoError(t, drive.writeHTMLReport(&bytes.Buffer{}, &validationResult{}, run))
	page, err := os.ReadFile(htmlReportPath(drive.repositoryRoot))
	require.NoError(t, err)
	require.Equal(t, 1000, strings.Count(string(page), "coverage row"))
	require.Contains(t, string(page), "Unable to deserialize cloned data")
}

func TestVitestDiagnosticsStayBoundedAndRetainOtherFailures(t *testing.T) {
	output := []byte("Unhandled Errors\nError: first cause\n" + strings.Repeat("long stack\n", 1000))
	diagnostic := vitestDiagnostic(output)
	require.Contains(t, diagnostic, "Error: first cause")
	require.LessOrEqual(t, len([]rune(diagnostic)), 1024)
	require.Contains(t, diagnostic, "[truncated]")
	output = []byte("test output\nERROR: Coverage threshold was not met")
	require.Equal(t, commandDiagnostic(output), vitestDiagnostic(output))
}

func TestVitestPreflightScope(t *testing.T) {
	check := jestPreflight{Node: "24.14.1", Version: "4.1.6", VitestProjects: []vitestProject{{Pool: "forks"}}}
	selection := platform.JSSelection{Version: "6.17.0", Node: ">=22"}
	require.Equal(t, "compatible", checkVitestSupport(check, selection).Status)
	check.Node = "20.0.0"
	require.Equal(t, "incompatible", checkVitestSupport(check, selection).Status)
	check.Node = "24.14.1"
	check.VitestProjects[0].Browser = true
	require.Equal(t, "inconclusive", checkVitestSupport(check, selection).Status)
	check.VitestProjects[0].Browser = false
	check.VitestProjects = append(check.VitestProjects, check.VitestProjects[0])
	require.Equal(t, "inconclusive", checkVitestSupport(check, selection).Status)
	check.VitestProjects = check.VitestProjects[:1]
	check.Version = "5.0.0"
	require.Equal(t, "compatible", checkVitestSupport(check, selection).Status)
}

func TestVitestSkippingReleaseBoundaries(t *testing.T) {
	check := jestPreflight{Node: "24.14.1", Version: "4.1.6", VitestProjects: []vitestProject{{Pool: "forks"}}}
	for _, version := range []string{"4.99.0", "6.3.0", "6.8.99", "5.119.99"} {
		result := checkVitestSupport(check, platform.JSSelection{Version: version, Node: ">=22"})
		require.Equal(t, "incompatible", result.Status)
		require.Contains(t, result.Reason, "does not support Vitest test skipping")
		require.Contains(t, result.Reason, "explicit dependency upgrade")
	}
	for _, version := range []string{"5.120.0", "5.128.0", "6.9.0", "6.17.0"} {
		result := checkVitestSupport(check, platform.JSSelection{Version: version, Node: ">=22"})
		require.Equal(t, "compatible", result.Status)
		require.Contains(t, result.Reason, "feature checks are still required")
	}
	for _, version := range []string{"6.9.0-beta.1", "7.0.0", "git:custom"} {
		result := checkVitestSupport(check, platform.JSSelection{Version: version, Node: ">=22"})
		require.Equal(t, "compatible", result.Status)
		require.Contains(t, result.Reason, "feature checks are still required")
	}
}

func TestVitestVersionChecksFollowKnownTracerSupport(t *testing.T) {
	for _, tc := range []struct{ vitest, tracer, status string }{
		{"1.5.9", "6.18.0", "incompatible"},
		{"1.6.0", "6.18.0", "compatible"},
		{"2.1.0", "6.18.0", "compatible"},
		{"3.2.0", "6.18.0", "compatible"},
		{"4.1.11", "6.13.0", "compatible"},
		{"5.0.1", "6.13.99", "incompatible"},
		{"5.0.1", "6.14.0", "compatible"},
		{"5.0.1", "5.124.99", "incompatible"},
		{"5.0.1", "5.125.0", "compatible"},
		{"5.0.1", "6.18.0", "compatible"},
		{"6.0.0", "7.0.0", "compatible"},
		{"5.0.0-beta.1", "6.18.0", "compatible"},
		{"custom", "6.18.0", "compatible"},
	} {
		t.Run(tc.vitest+"/"+tc.tracer, func(t *testing.T) {
			check := jestPreflight{Node: "24.14.1", Version: tc.vitest, VitestProjects: []vitestProject{{Pool: "forks"}}}
			result := checkVitestSupport(check, platform.JSSelection{Version: tc.tracer, Node: ">=22"})
			require.Equal(t, tc.status, result.Status)
			if tc.status == "compatible" {
				require.Contains(t, result.Reason, "Paired execution and feature checks are still required")
			}
		})
	}
}

func TestVitestArgumentsDoNotDropWrapperSetup(t *testing.T) {
	args, err := vitestArguments("pnpm", []string{"vitest", "--config", "custom.ts", "--coverage"})
	require.NoError(t, err)
	require.Equal(t, []string{"--config", "custom.ts", "--coverage"}, args)
	for _, command := range []string{"npm", "sh"} {
		_, err = vitestArguments(command, []string{"test"})
		require.Error(t, err)
	}
}

func TestVitestFinalResultStillRequiresRetryEvidence(t *testing.T) {
	for _, feature := range []string{"early-flake-detection", "attempt-to-fix"} {
		identity := intake.Test{Suite: "a.test.ts", Name: probeName, Module: "vitest"}
		tags := map[string]string{"test.module": identity.Module, "test.suite": identity.Suite, "test.name": identity.Name, "test.status": "pass", "test.test_management.is_attempt_to_fix": "true"}
		retry := map[string]string{}
		for k, v := range tags {
			retry[k] = v
		}
		retry["test.is_retry"] = "true"
		retry["test.retry_reason"] = map[string]string{"early-flake-detection": "early_flake_detection", "attempt-to-fix": "attempt_to_fix"}[feature]
		run := validationRun{framework: "vitest", Tests: []jestTest{{Name: probeName, Status: "passed"}}, Facts: intake.Facts{Events: []intake.Event{{Type: "test", Tags: tags}, {Type: "test", Tags: retry}}}}
		require.Equal(t, "passed", evaluateFeature(feature, run, identity).Status)
		run.Facts.Events = run.Facts.Events[:1]
		require.Equal(t, "failed", evaluateFeature(feature, run, identity).Status)
	}
}

func TestVitestJSONAndProbeStayInOriginalSuite(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "test", "real.test.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(original), 0700))
	require.NoError(t, os.WriteFile(original, []byte("original"), 0600))
	result := filepath.Join(root, "result.json")
	require.NoError(t, os.WriteFile(result, []byte(`{"testResults":[{"name":"`+original+`","status":"passed","assertionResults":[{"fullName":"suite test","status":"passed"}]}]}`), 0600))
	var run validationRun
	readRunnerResults(result, "Vitest", &run)
	require.Empty(t, run.ResultError)
	probe, err := createProbe(root, run, "import {test} from 'vitest';\n"+probeSource)
	require.NoError(t, err)
	physical, err := filepath.EvalSymlinks(filepath.Dir(original))
	require.NoError(t, err)
	require.Equal(t, physical, filepath.Dir(probe))
	data, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
	require.NoError(t, removeProbe(root, probe))
}

func TestVitestMissingSkippingNegotiationIsNotAPass(t *testing.T) {
	run := validationRun{framework: "vitest", ExitCode: 1}
	result := evaluateFeature("skipping", run, intake.Test{SourceFile: "test/probe.test.ts"})
	require.Equal(t, "inconclusive", result.Status)
	require.Contains(t, result.Reason, "did not request skippable suites")
}

func TestVitestPairedExecutionCanBeRetained(t *testing.T) {
	exit := 0
	result := validationResult{Framework: "vitest", Runs: []runSummary{
		{Role: roleBaseline, Name: "baseline", Command: "vitest run", ExitCode: &exit},
		{Role: roleReporting, Name: "reporting-only", Command: "vitest run", ExitCode: &exit, Instrumented: true},
	}}
	require.True(t, hasPairedExecution(result))
	result.CheckOnly = true
	require.False(t, hasPairedExecution(result))
}

func TestVitestBenchmarkPreflightDoesNotExecuteOrdinaryTests(t *testing.T) {
	for _, command := range []string{"vitest", "pnpm", "npx", "yarn"} {
		drive := preparedTestdrive(t)
		drive.command = command
		drive.args = []string{"bench", "--run"}
		if command != "vitest" {
			drive.args = append([]string{"vitest"}, drive.args...)
		}
		// No executor is supplied: benchmark rejection happens before loading config.
		drive.executor = nil
		_, _, _, err := drive.inspectVitest(t.Context(), t.TempDir())
		require.ErrorContains(t, err, "benchmark mode is not validated")
	}
}

// Configuration discovery must work across the same versions as the existing
// Vitest runner adapter and must not execute any test body.
func TestVitestConfigurationIntegration(t *testing.T) {
	modules := os.Getenv("DDTEST_VITEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_VITEST_NODE_MODULES")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"type":"module"}`)
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), `export default {test:{pool:'forks',include:['*.test.js']}}`)
	requireWriteFile(t, filepath.Join(root, "one.test.js"), `throw new Error('configuration inspection must not execute tests')`)
	drive := &Testdrive{command: "npx", args: []string{"vitest", "run"}, executor: &ext.DefaultCommandExecutor{}}
	version, projects, files, err := drive.inspectVitest(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, version)
	require.Len(t, projects, 1)
	require.Equal(t, "forks", projects[0].Pool)
	require.Len(t, files, 1)
	require.Equal(t, "one.test.js", filepath.Base(files[0]))

	probe := filepath.Join(root, "controlled.test.js")
	requireWriteFile(t, probe, `throw new Error('probe discovery must not execute tests')`)
	drive.args = []string{"vitest", "run", "--config", "vitest.config.mjs", "one.test.js"}
	_, _, files, err = drive.inspectVitest(t.Context(), t.TempDir(), probe)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "controlled.test.js", filepath.Base(files[0]))
	require.NotContains(t, drive.vitestProbeArguments, "one.test.js")
}

func TestVitestManagerShorthandDoesNotIgnorePackageScripts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"vitest":"cross-env MODE=ci vitest --config ci.ts"}}`)
	for _, manager := range []string{"pnpm", "yarn"} {
		_, err := vitestArguments(manager, []string{"vitest", "--coverage"})
		require.ErrorContains(t, err, "--all")
		args, err := vitestArguments(manager, []string{"exec", "vitest", "--config", "ci.ts"})
		require.NoError(t, err)
		require.Equal(t, []string{"--config", "ci.ts"}, args)
	}
}

type vitestConfigurationExecutor struct{ version string }

func (e vitestConfigurationExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return []byte(`__DDTEST_VITEST_CONFIG__{"version":"` + e.version + `","node":"24.14.1","projects":[{"pool":"forks"}],"files":[]}`), nil
}

func TestVitestSourceBuildDefersOnlyUntilInstalledMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, version      string
		checkOnly, blocked bool
	}{
		{"full source build", "4.1.11", false, false}, {"check-only needs metadata", "4.1.11", true, true}, {"known incompatible framework", "1.5.0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			drive := preparedTestdrive(t)
			drive.framework = framework.NewVitest(platform.NewJavaScript())
			drive.platform = platform.NewJavaScript()
			drive.command, drive.args = "vitest", []string{"run"}
			drive.tracerVersion, drive.checkOnly = "git:local-source", tc.checkOnly
			drive.executor = vitestConfigurationExecutor{version: tc.version}
			drive.nodeVersion = func() string { return "24.14.1" }
			t.Setenv("DD_TRACE_PACKAGE", "")
			t.Setenv("NODE_OPTIONS", "")
			result := validationResult{CIRuntime: &onboard.RuntimeCheck{Status: "not applicable"}}
			err := drive.checkVitestPreflight(t.Context(), &bytes.Buffer{}, &result)
			require.Equal(t, tc.blocked, err != nil)
			if tc.blocked {
				return
			}
			require.Equal(t, "inconclusive", result.Preflight.Verdict.Status)
			tracer := filepath.Join(t.TempDir(), "dd-trace")
			require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci"), 0700))
			requireWriteFile(t, filepath.Join(tracer, "package.json"), `{"version":"6.20.1","engines":{"node":">=22"}}`)
			require.NoError(t, drive.verifyInstalledSelection(t.Context(), filepath.Join(tracer, "ci/init.js"), &result))
			require.Equal(t, "compatible", checkVitestSupport(*result.Preflight, *result.Selection).Status)
		})
	}
}

func TestVitestValidationFlagsPrecedeArgumentTerminator(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.command, drive.args = "npx", []string{"vitest", "run", "--", "src"}
	drive.executor = vitestErrorExecutor{t}
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}
	session, err := NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	run, err := drive.runVitest(t.Context(), &bytes.Buffer{}, session, "/trace/ci/init.js", runSpec{Name: "baseline", Role: roleBaseline})
	require.NoError(t, err)
	require.Empty(t, run.ResultError)
}
