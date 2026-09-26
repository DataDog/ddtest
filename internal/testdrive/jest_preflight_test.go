// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
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

func TestPreflightStopsUnsupportedJestBeforeInstallOrTests(t *testing.T) {
	for _, runner := range []string{"jest-jasmine2/build/index.js", "jest-circus/runner.js"} {
		t.Run(runner, func(t *testing.T) {
			run := preflightFixture(t, "24.9.0", runner)
			var output bytes.Buffer
			require.ErrorContains(t, run.Run(t.Context(), &output), "dd-trace 6 requires Jest >=28")
			require.Equal(t, 1, run.executor.(*preflightExecutor).calls)
			data, err := os.ReadFile(validationPath(run.repositoryRoot))
			require.NoError(t, err)
			var report validationResult
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, "incompatible", report.Preflight.Verdict.Status)
			require.Empty(t, report.Runs)
			require.False(t, report.Success)
			require.Equal(t, "project", report.Selection.Source)
		})
	}
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

func TestTracerMismatchCannotClaimOverallSuccess(t *testing.T) {
	for _, tc := range []struct{ version, status, want string }{
		{"5.128.0", "compatible", "compatible"}, {"5.127.0", "compatible", "incompatible"}, {"", "inconclusive", "inconclusive"},
	} {
		check := onboard.RuntimeCheck{Status: tc.status, Jobs: []onboard.RuntimeFinding{{Status: tc.status}}}
		if tc.version != "" {
			check.Jobs[0].Tracer = "dd-trace@" + tc.version
		}
		agreement := compareCISelection(check, "5.128.0")
		require.Equal(t, tc.want, agreement.Status)
		root := t.TempDir()
		err := finishValidation(&bytes.Buffer{}, root, validationResult{Compatibility: verdict{Status: "compatible"}, CIRuntime: &check, CISelection: agreement})
		if tc.want == "compatible" {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		data, err := os.ReadFile(validationPath(root))
		require.NoError(t, err)
		var report validationResult
		require.NoError(t, json.Unmarshal(data, &report))
		require.True(t, report.LocalSuccess)
		require.Equal(t, tc.want == "compatible", report.Success)
	}
}

func TestJestInstallsExactPreflightSelection(t *testing.T) {
	run := preparedTestdrive(t)
	run.tracerVersion = "latest-node18"
	run.preflight = func(_ context.Context, _ io.Writer, result *validationResult) error {
		result.Selection = &platform.JSSelection{Requested: "latest-node18", Version: "5.128.0", Source: "fallback"}
		return nil
	}
	installer := &fakeTracer{err: errors.New("installation stopped for assertion")}
	run.platform = installer
	require.ErrorContains(t, run.Run(t.Context(), &bytes.Buffer{}), "installation stopped for assertion")
	require.Equal(t, "5.128.0", installer.options.Version)
	require.Equal(t, run.command, installer.options.Command)
	require.Equal(t, run.args, installer.options.Args)
	require.NoDirExists(t, installer.options.Directory)
}
