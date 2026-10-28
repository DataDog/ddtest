// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/buildinfo"
	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func vitestIntegrationRoot(t *testing.T) string {
	t.Helper()
	modules := os.Getenv("DDTEST_VITEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_VITEST_NODE_MODULES")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	t.Chdir(root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"type":"module"}`)
	return root
}

func TestVitestValidationArgumentsIntegration(t *testing.T) {
	root := vitestIntegrationRoot(t)
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), `throw new Error('mode rejection must happen before config loading')`)
	script := framework.VitestValidationScript + `
 import assert from 'node:assert/strict'
 import {createRequire} from 'node:module'
 import {resolve} from 'node:path'
 import {pathToFileURL} from 'node:url'
 const require=createRequire(resolve('package.json'))
 const {parseCLI}=await import(pathToFileURL(require.resolve('vitest/node')))
 for (const output of [['--reporter=junit','--outputFile=reports/junit.xml'],['--reporter','junit','--outputFile.junit','reports/junit.xml']]) {
  const original=['run',...output,'--config','ci.ts','src','--coverage.reportsDirectory=customer']
  const result=validationArguments(original,parseCLI)
  assert.deepEqual(result.arguments,['run','--config','ci.ts','src'])
  assert.deepEqual(result.reporters,['junit'])
 }
 const filtered=['run','src','--shard=1/3','--config','src','--testNamePattern','old','--project','customer']
 assert.deepEqual(validationArguments(filtered,parseCLI).arguments,filtered)
 assert.deepEqual(validationArguments(filtered,parseCLI,'probe.test.js').arguments,['run','--config','src','probe.test.js'])
 assert.deepEqual(validationArguments(['run','--','src'],parseCLI,'probe.test.js').arguments,['run','probe.test.js'])
 for (const mode of ['bench','watch','dev','list','related']) {
  assert.throws(()=>validationArguments(['--config','ci.ts',mode,'--run'],parseCLI),/mode is not supported/)
 }
 assert.throws(()=>validationArguments(['run','--watch'],parseCLI),/watch mode/)
 assert.deepEqual(validationCoverage('reports',true),{reportsDirectory:'reports',thresholds:false})
 assert.deepEqual(validationCoverage('reports',false),{reportsDirectory:'reports',thresholds:{autoUpdate:false}})
 `
	data, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput()
	require.NoError(t, err, string(data))
	drive := &Testdrive{framework: framework.NewVitest(platform.NewJavaScript()), command: "npx", args: []string{"vitest", "--config", "vitest.config.mjs", "bench", "--run"}, executor: &ext.DefaultCommandExecutor{}}
	_, _, _, err = drive.inspectVitest(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "bench mode is not supported")
	require.NotContains(t, err.Error(), "mode rejection must happen")
}

func TestJestDDTestCommandDoesNotUseVitestRunner(t *testing.T) {
	drive := &Testdrive{framework: framework.NewJest(platform.NewJavaScript()), command: "ddtest", vitestRunner: true}
	require.False(t, drive.usesVitestRunner())
}

func TestUnhandledVitestErrorsParticipateInOutcomeComparison(t *testing.T) {
	baseline := validationRun{ExitCode: 1, Tests: []jestTest{{File: "a.test.js", Name: "old failure", Status: "failed", Failure: "existing failure"}}}
	instrumented := baseline
	instrumented.SuiteErrors = []string{vitestUnhandledErrors([]byte("\x1b[31mUnhandled Errors\x1b[0m\nUnhandled Rejection\nError: new instrumentation failure\n at worker.js:1\n Test Files 1 failed\n Duration 2s"))}
	require.Equal(t, "suspected regression", compareJest(baseline, instrumented).Status)
	require.Contains(t, instrumented.SuiteErrors[0], "new instrumentation failure")
	require.NotContains(t, instrumented.SuiteErrors[0], "Duration")
	require.Empty(t, vitestUnhandledErrors([]byte("ordinary test failure")))
}

func TestResolvedVitestNodeControlsInstrumentation(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewVitest(platform.NewJavaScript())
	drive.command, drive.args = "/explicit/node", []string{"/project/vitest.mjs", "run"}
	preload := filepath.Join(t.TempDir(), "ci/init.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(preload), 0700))
	requireWriteFile(t, filepath.Join(filepath.Dir(filepath.Dir(preload)), "register.js"), "")
	drive.nodeVersion = func() string { return "18.0.0" }
	require.NotContains(t, drive.environment(preload, "http://local", "session")["NODE_OPTIONS"], "--import")
	drive.executor = vitestConfigurationExecutor{version: "4.1.11"}
	_, _, _, err := drive.inspectVitest(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "24.14.1", drive.nodeVersion())
	require.Contains(t, drive.environment(preload, "http://local", "session")["NODE_OPTIONS"], "--import")
}

func TestVitestRunnerResolvesRelativePackageBinary(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "packages/app"), 0700))
	binary := filepath.Join(root, "bin/ddtest")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0700))
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0700))
	actual, err := resolveVitestRunnerBinary(root, "packages/app", "../../bin/ddtest")
	require.NoError(t, err)
	require.Equal(t, workspace.CanonicalRoot(binary), workspace.CanonicalRoot(actual))
	drive := preparedTestdrive(t)
	drive.command, err = os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), "env: {DDTEST_VERSION: 'v0.0.1'}\njobs: {}\n")
	info := drive.inspectVitestRunnerBinary(t.Context())
	require.Equal(t, buildinfo.CurrentVersion(), info.Version)
	require.Equal(t, drive.command, info.Path)
	require.Equal(t, []string{"v0.0.1"}, info.CIPins)
	require.Len(t, info.Warnings, 1)
}

func TestVitestSettingsTranslateWithCommandPrecedence(t *testing.T) {
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })

	drive := preparedTestdrive(t)
	settings.Get().VitestConfig = "settings.ts"
	drive.framework = framework.NewVitest(platform.NewJavaScript())
	drive.command = "vitest"
	drive.args = []string{"run"}
	require.NoError(t, drive.resolveVitestSettings())
	require.Equal(t, []string{"run", "--config", "settings.ts"}, drive.args)
	drive.args = []string{"run", "--config=command.ts"}
	require.NoError(t, drive.resolveVitestSettings())
	require.Equal(t, []string{"run", "--config=command.ts"}, drive.args)
}

func TestVitestRejectedCommandPublishesCurrentFailure(t *testing.T) {
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"devDependencies":{"vitest":"4"},"scripts":{"test":"unknown-wrapper"}}`)
	viper.Set("framework", "vitest")
	viper.Set("command", "npm test")
	settings.Init()
	drive, err := Prepare(PrepareOptions{CheckOnly: true})
	require.NoError(t, err)
	require.Error(t, drive.Run(t.Context(), &bytes.Buffer{}))
	data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
	require.NoError(t, err)
	var result validationResult
	require.NoError(t, json.Unmarshal(data, &result))
	require.NotEmpty(t, result.Error)
	require.False(t, result.ChecksPassed)
	require.Empty(t, result.Runs)
}

func TestVitestCIMatrixUsesCompatibleTracerFloors(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/ci.yml")
	require.NoError(t, err)
	var ci struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct {
					Include []struct {
						Vitest string
						Tracer string
					}
				}
			}
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &ci))
	rows := ci.Jobs["testdrive-vitest-workflow"].Strategy.Matrix.Include
	require.Len(t, rows, 4)
	seen := map[int]bool{}
	for _, row := range rows {
		if row.Vitest == "latest" {
			continue // Unpinned; it catches releases newer than the pinned floors.
		}
		major, _, known := releaseParts(row.Vitest)
		require.True(t, known)
		seen[major] = true
		check := jestPreflight{Version: row.Vitest, Node: "24.14.1", ExecutionPath: vitestNodeAPIPath, VitestProjects: []vitestProject{{Pool: "forks"}}}
		require.Equal(t, "compatible", checkVitestSupport(check, platform.JSSelection{Version: row.Tracer, Node: ">=18"}).Status, row.Vitest+" / "+row.Tracer)
	}
	require.Equal(t, map[int]bool{3: true, 4: true, 5: true}, seen)

	// The CLI compatibility job installs one pinned tracer for every Vitest row.
	var compatibility struct {
		Jobs map[string]struct {
			Strategy struct {
				Matrix struct{ Vitest []string }
			}
			Steps []struct{ Run string }
		}
	}
	require.NoError(t, yaml.Unmarshal(data, &compatibility))
	job := compatibility.Jobs["vitest-compatibility"]
	require.NotEmpty(t, job.Strategy.Matrix.Vitest)
	var tracer string
	for _, step := range job.Steps {
		if match := regexp.MustCompile(`"dd-trace@([0-9.]+)"`).FindStringSubmatch(step.Run); match != nil {
			tracer = match[1]
		}
	}
	require.NotEmpty(t, tracer, "vitest-compatibility must pin dd-trace")
	for _, version := range job.Strategy.Matrix.Vitest {
		check := jestPreflight{Version: version, Node: "22.0.0", ExecutionPath: vitestCLIPath, VitestProjects: []vitestProject{{Pool: "forks"}}}
		require.Equal(t, "compatible", checkVitestSupport(check, platform.JSSelection{Version: tracer, Node: ">=18"}).Status, version+" / "+tracer)
	}
}

func TestVitestMultiProjectRejectionKeepsConfigurationEvidenceIntegration(t *testing.T) {
	root := vitestIntegrationRoot(t)
	// test.projects arrived in Vitest 3.2; older releases ignore it.
	data, err := os.ReadFile(filepath.Join(root, "node_modules", "vitest", "package.json"))
	require.NoError(t, err)
	var installed struct{ Version string }
	require.NoError(t, json.Unmarshal(data, &installed))
	if major, minor, ok := releaseParts(installed.Version); !ok || major < 3 || major == 3 && minor < 2 {
		t.Skipf("Vitest %s has no test.projects", installed.Version)
	}
	requireWriteFile(t, filepath.Join(root, "vitest.config.mjs"), `export default {test:{projects:[{test:{name:'a',include:['*.test.js'],pool:'forks'}},{test:{name:'b',include:['*.test.js'],pool:'forks'}}]}}`)
	requireWriteFile(t, filepath.Join(root, "one.test.js"), `throw new Error('unsupported projects must not execute')`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  test:\n    steps:\n      - run: vitest run --config vitest.config.mjs\n      - run: vitest run --config vitest.config.mjs --coverage\n")
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	viper.Set("framework", "vitest")
	settings.Init()
	drive, err := Prepare(PrepareOptions{All: true})
	require.NoError(t, err)
	var output bytes.Buffer
	require.Error(t, drive.Run(t.Context(), &output))
	data, err = os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.Len(t, report.Configurations, 2)
	require.Empty(t, report.Runs)
	for _, config := range report.Configurations {
		require.NotNil(t, config.Preflight)
		require.Equal(t, "inconclusive", config.Preflight.Verdict.Status)
		require.Equal(t, "incomplete", config.Status)
	}
	coverage, _ := summarizeConfigurationCoverage(report)
	require.Equal(t, 2, coverage.Discovered)
	require.Equal(t, coverage.Discovered, coverage.Validated+coverage.Blocked+coverage.NotExercised+coverage.Incomplete)
	require.Contains(t, output.String(), "2 incomplete")
}

type vitestRegressionExecutor struct {
	calls int
	t     *testing.T
	file  string
}

func (e *vitestRegressionExecutor) CombinedOutput(_ context.Context, _ string, args []string, env map[string]string) ([]byte, error) {
	e.calls++
	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, "--outputFile="); ok {
			data, err := json.Marshal(map[string]any{"testResults": []any{map[string]any{"name": e.file, "assertionResults": []any{map[string]any{"fullName": "existing failure", "status": "failed", "failureMessages": []string{"old failure"}}}}}})
			require.NoError(e.t, err)
			requireWriteFile(e.t, path, string(data))
		}
	}
	if env["DD_CIVISIBILITY_ENABLED"] == "false" {
		return []byte("old failure"), errors.New("exit status 1")
	}
	return []byte("old failure\nUnhandled Errors\nUnhandled Rejection\nError: instrumentation regression\nTest Files 1 failed"), errors.New("exit status 1")
}

func TestVitestUnhandledRegressionTriggersRepeatedPair(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewVitest(platform.NewJavaScript())
	drive.command = "vitest"
	drive.args = []string{"run"}
	drive.vitestArguments, drive.vitestInspected = drive.args, true
	executor := &vitestRegressionExecutor{t: t, file: filepath.Join(drive.repositoryRoot, "missing.test.js")}
	drive.executor = executor
	drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
		return &fakeIntake{url: "http://127.0.0.1:1234"}, nil
	}
	session, err := workspace.NewSession()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	var result validationResult
	result.Framework = "vitest"
	require.NoError(t, drive.runFrameworkValidation(t.Context(), &bytes.Buffer{}, session, "/tracer/ci/init.js", &result))
	require.Equal(t, 4, executor.calls)
	require.Equal(t, "suspected regression", result.Compatibility.Status)
	require.Equal(t, []runRole{roleBaseline, roleReporting, roleRepeat, roleRepeat}, []runRole{result.Runs[0].Role, result.Runs[1].Role, result.Runs[2].Role, result.Runs[3].Role})
}

func TestFocusedRunnerPreservesSelectedEnvironmentDuringInspection(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.framework = framework.NewVitest(platform.NewJavaScript())
	drive.command = "ddtest"
	drive.args = []string{"run", "--framework", "vitest"}
	drive.configurationEnvironment = map[string]string{"NODE_OPTIONS": "--conditions=ddtest-focused", "SCOPED_SETUP": "selected"}
	executor := &fakeTestdriveExecutor{err: errors.New("stop after inspecting the selected environment")}
	drive.executor = executor
	require.Error(t, drive.Run(t.Context(), &bytes.Buffer{}))
	require.Contains(t, executor.env["NODE_OPTIONS"], "--conditions=ddtest-focused")
	require.Equal(t, "selected", executor.env["SCOPED_SETUP"])
}

type versionOutputExecutor string

func (e versionOutputExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return []byte(e), nil
}

func TestVitestRunnerReadsTheVersionDdtestPrints(t *testing.T) {
	drive := preparedTestdrive(t)
	drive.command = filepath.Join(t.TempDir(), "ddtest")
	require.NoError(t, os.MkdirAll(filepath.Join(drive.repositoryRoot, ".github/workflows"), 0700))
	requireWriteFile(t, filepath.Join(drive.repositoryRoot, ".github/workflows/test.yml"), "env: {DDTEST_VERSION: 'v0.0.0-20261021202218-9f1abb9a90c0'}\njobs: {}\n")
	for _, output := range []string{"v0.0.0-20261021202218-9f1abb9a90c0\n", "ddtest version v0.0.0-20261021202218-9f1abb9a90c0\n"} {
		drive.executor = versionOutputExecutor(output)
		info := drive.inspectVitestRunnerBinary(t.Context())
		require.Equal(t, "v0.0.0-20261021202218-9f1abb9a90c0", info.Version, output)
		require.Empty(t, info.Warnings, "a matching CI pin needs no warning")
	}
	drive.executor = versionOutputExecutor("unexpected banner text\n")
	require.Equal(t, "unverified", drive.inspectVitestRunnerBinary(t.Context()).Version)
}

func TestFocusedCopiedRunPublishesItsReportInTheOriginalRepository(t *testing.T) {
	root := t.TempDir()
	drive := &Testdrive{repositoryRoot: root, framework: framework.NewVitest(platform.NewJavaScript())}
	// The copied run retains one model per reporting run; in place, the last one wins.
	models := []reportModel{{Runtime: reportRuntime{Output: "first reporting run"}}, {Runtime: reportRuntime{Output: "repeated reporting run"}}}
	result := validationResult{HTMLReportCurrent: true}
	var output bytes.Buffer
	require.NoError(t, drive.publishCopiedRunReport(&output, &result, models))
	page, err := os.ReadFile(htmlReportPath(root))
	require.NoError(t, err)
	require.Contains(t, string(page), "repeated reporting run")
	require.NotContains(t, string(page), "first reporting run")
	require.True(t, result.HTMLReportCurrent)
	require.Contains(t, output.String(), "Open report:")

	blocked := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(blocked, constants.PlanDirectory), nil, 0600))
	drive.repositoryRoot = blocked
	require.Error(t, drive.publishCopiedRunReport(&bytes.Buffer{}, &result, models))
	require.False(t, result.HTMLReportCurrent, "a report that could not be published is not current")
}
