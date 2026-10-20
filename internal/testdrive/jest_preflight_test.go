// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestJestPrerequisitesCoverLuxonAndTSyringe(t *testing.T) {
	for _, tc := range []struct{ jest, runner, tracer, node, engine, status string }{
		{"24.9.0", "/node_modules/jest-jasmine2/build/index.js", "6.16.0", "24.14.1", ">=22", "incompatible"},
		{"24.9.0", "/node_modules/jest-circus/runner.js", "6.16.0", "24.14.1", ">=22", "incompatible"},
		{"24.9.0", "/node_modules/jest-circus/runner.js", "5.128.0", "24.14.1", ">=18", "compatible"},
		{"24.7.0", "jest-circus/runner", "5.128.0", "24.14.1", ">=18", "incompatible"},
		{"24.8.0", "jest-circus/runner", "5.128.0", "18.0.0", ">=18", "compatible"},
		{"27.5.1", "jest-circus/runner", "6.16.0", "24.14.1", ">=22", "incompatible"},
		{"27.5.1", "jest-circus/runner", "5.86.0", "22.23.2", ">=18 <26", "compatible"},
		{"30.5.1", "jest-circus/runner", "5.86.0", "26.0.0", ">=18 <26", "incompatible"},
		{"28.0.0", "jest-circus/runner", "6.16.0", "24.14.1", ">=22", "compatible"},
		{"30.2.0", "jest-circus/runner", "6.16.0", "20.20.1", ">=22", "incompatible"},
		{"30.2.0", "jest-circus/runner", "6.16.0", "24.14.1", ">=22", "compatible"},
		{"30.2.0", "jest-circus/runner", "7.0.0", "24.14.1", ">=22", "inconclusive"},
	} {
		result := checkJestSupport(jestPreflight{Version: tc.jest, Node: tc.node, Projects: []jestProject{{Runner: tc.runner}}}, platform.JSSelection{Version: tc.tracer, Node: tc.engine})
		require.Equal(t, tc.status, result.Status, "%+v: %s", tc, result.Reason)
	}
}

type preflightExecutor struct {
	calls  int
	args   []string
	config string
}

func (e *preflightExecutor) CombinedOutput(_ context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	e.calls++
	e.args = args
	return []byte("package script banner\n" + e.config), nil
}

func preflightFixture(t *testing.T, version, runner string) *Testdrive {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is required to exercise project tracer resolution")
	}
	root := t.TempDir()
	writeJestManifest(t, root)
	t.Chdir(root)
	tracerRoot := filepath.Join(root, "node_modules/dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracerRoot, "ci"), 0755))
	requireWriteFile(t, filepath.Join(tracerRoot, "ci/init.js"), "")
	requireWriteFile(t, filepath.Join(tracerRoot, "package.json"), `{"version":"6.16.0","engines":{"node":">=22"}}`)
	run, err := Prepare("latest-node18")
	require.NoError(t, err)
	run.command = "npx"
	run.args = []string{"jest", "--config", "test/jest.config.js"}
	data, err := json.Marshal(map[string]any{"version": version, "configs": []map[string]string{{"testRunner": "/node_modules/" + runner, "testEnvironment": "/jest-environment-node/build/index.js", "rootDir": root}}})
	require.NoError(t, err)
	run.executor = &preflightExecutor{config: string(data)}
	run.nodeVersion = func() string { return "24.14.1" }
	run.resolveJSTracer = func(_ context.Context, major string) (platform.JSSelection, error) {
		if major == "5" {
			return platform.JSSelection{Version: "5.128.0", Node: ">=18"}, nil
		}
		return platform.JSSelection{Version: "6.17.0", Node: ">=22"}, nil
	}

	return run
}

func TestCheckOnlyReportsNoTestExecutionAndProjectPrecedence(t *testing.T) {
	run := preflightFixture(t, "30.2.0", "jest-circus/runner.js")
	run.checkOnly = true
	require.NoError(t, run.Run(t.Context(), &bytes.Buffer{}))
	e := run.executor.(*preflightExecutor)
	require.Equal(t, 1, e.calls)
	require.Equal(t, []string{"jest", "--config", "test/jest.config.js", "--showConfig"}, e.args)
	data, err := os.ReadFile(validationPath(run.repositoryRoot))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.True(t, report.ChecksPassed)
	require.False(t, report.Success)
	require.False(t, report.LocalSuccess)
	require.Equal(t, "not exercised", report.Compatibility.Status)
	require.Equal(t, "not exercised", report.CIExecution.Status)
	require.Empty(t, report.Runs)
	require.Equal(t, "latest-node18", report.Selection.Requested)
	require.Equal(t, "6.16.0", report.Selection.Version)
	require.Equal(t, "project", report.Selection.Source)
}

func TestCheckOnlyStopsUnsupportedJestBeforeInstallOrTests(t *testing.T) {
	run := preflightFixture(t, "24.9.0", "jest-circus/runner.js")
	run.checkOnly = true
	var output bytes.Buffer
	require.ErrorContains(t, run.Run(t.Context(), &output), "dd-trace 6 requires Jest >=28")
	require.Equal(t, 1, run.executor.(*preflightExecutor).calls)
	data, err := os.ReadFile(validationPath(run.repositoryRoot))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.False(t, report.ChecksPassed)
	require.False(t, report.Success)
	require.Empty(t, report.Runs)
	require.Equal(t, "5.128.0", report.Preflight.Recommendation.Selection.Version)
}

func TestJestCheckOnlyIntegration(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES to installed Jest and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	root := t.TempDir()
	writeJestManifest(t, root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	requireWriteFile(t, filepath.Join(root, "jest.config.js"), "module.exports={testEnvironment:'node',testRunner:'jest-circus/runner'}")
	requireWriteFile(t, filepath.Join(root, "never.test.js"), "require('fs').writeFileSync('test-executed','unexpected'); test('must not run',()=>{throw new Error('must not run')})")
	t.Chdir(root)
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	run, err := Prepare("latest", true)
	require.NoError(t, err)
	run.startIntake = func(string) (localIntake, error) { t.Fatal("check-only started an intake"); return nil, nil }
	var output bytes.Buffer
	require.NoError(t, run.Run(t.Context(), &output), output.String())
	require.NoFileExists(t, filepath.Join(root, "test-executed"))
	require.NoDirExists(t, filepath.Join(root, ".testoptimization", "testdrive"))
	require.NoFileExists(t, filepath.Join(root, ".testoptimization", "report.html"))
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.True(t, report.ChecksPassed)
	require.False(t, report.Success)
	require.Empty(t, report.Runs)
	require.Equal(t, "project", report.Selection.Source)
	require.Equal(t, "compatible", report.Preflight.Verdict.Status)
}

func TestCheckOnlyDiscoveryFailureReplacesEarlierPassingReport(t *testing.T) {
	root := t.TempDir()
	writeJestManifest(t, root)
	t.Chdir(root)
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), `jobs:
  first:
    steps:
      - run: npm test -- --config first.js
  second:
    steps:
      - run: npm test -- --config second.js
`)
	require.NoError(t, os.MkdirAll(filepath.Dir(validationPath(root)), 0755))
	previous := `{"checks_passed":true,"success":false}`
	requireWriteFile(t, validationPath(root), previous)
	run, err := Prepare("latest", true)
	require.NoError(t, err)
	require.Error(t, run.discoveryError)
	executor := &preflightExecutor{}
	run.executor = executor
	run.startIntake = func(string) (localIntake, error) { t.Fatal("discovery failure started an intake"); return nil, nil }
	var output bytes.Buffer
	run.Preview(&output)
	before, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	require.Equal(t, previous, string(before), "preview must not replace reports")
	require.Error(t, run.Run(t.Context(), &output))
	require.Zero(t, executor.calls)
	data, err := os.ReadFile(validationPath(root))
	require.NoError(t, err)
	var report validationResult
	require.NoError(t, json.Unmarshal(data, &report))
	require.False(t, report.ChecksPassed)
	require.False(t, report.Success)
	require.True(t, report.CheckOnly)
	require.Equal(t, "jest", report.Framework)
	require.NotEmpty(t, report.Error)
	require.Empty(t, report.Runs)
}
