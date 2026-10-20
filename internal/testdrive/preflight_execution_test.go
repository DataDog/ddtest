// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCheckOnlyRejectsOtherRunnerBeforeExecution(t *testing.T) {
	root := t.TempDir()
	writeJestManifest(t, root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"test":"mocha && jest"},"devDependencies":{"jest":"30.5.1"}}`), 0600))
	t.Chdir(root)
	viper.Reset()
	t.Cleanup(viper.Reset)
	run, err := Prepare("latest", true)
	require.NoError(t, err)
	executor := &preflightExecutor{config: `{"version":"30.5.1","configs":[{"testRunner":"jest-circus/runner"}]}`}
	run.executor = executor
	require.Error(t, run.Run(t.Context(), &bytes.Buffer{}))
	require.Zero(t, executor.calls, "check-only must reject mixed runners before any execution")
}

func TestCheckOnlyCannotPassUnresolvedPreparation(t *testing.T) {
	for _, scope := range []onboard.ValidationScope{
		{Unresolved: []string{"CI replaces Jest configuration"}},
		{Commands: []onboard.TestCommand{{Command: "jest", UnvalidatedSetup: []string{"cp ci.js jest.config.js"}}}},
	} {
		root := t.TempDir()
		result := validationResult{CheckOnly: true, Scope: &scope, Preflight: &jestPreflight{Verdict: verdict{Status: "compatible"}}}
		require.Error(t, finishValidation(&bytes.Buffer{}, root, result))
		data, err := os.ReadFile(validationPath(root))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &result))
		require.False(t, result.ChecksPassed)
	}
}

func TestJestPreflightChecksTheCapturedRuntime(t *testing.T) {
	run := preflightFixture(t, "30.5.1", "jest-circus/runner.js")
	run.executor.(*preflightExecutor).node = "v20.20.2"
	run.nodeVersion = func() string { return "v24.14.1" }
	result := validationResult{}
	require.Error(t, run.checkJestPreflight(t.Context(), &bytes.Buffer{}, &result))
	require.Equal(t, "v20.20.2", result.Preflight.Node)
	require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
}

func TestJestRuntimeCaptureUsesExecutingProcess(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for runtime capture")
	}
	root := t.TempDir()
	main := filepath.Join(root, "node_modules", "jest", "bin", "jest.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(main), 0700))
	require.NoError(t, os.WriteFile(main, []byte(`console.log(JSON.stringify({node:process.version,executable:process.execPath}))`), 0600))
	env := map[string]string{}
	directory, path, err := prepareJestRuntimeCapture(env)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(directory)) }()
	cmd := exec.Command(node, main, "--showConfig")
	cmd.Env = cmd.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	var actual jestRuntime
	require.NoError(t, json.Unmarshal(output, &actual))
	captured, err := readJestRuntimeCapture(path)
	require.NoError(t, err)
	require.Equal(t, actual.Node, captured.Node)
	require.Equal(t, actual.Executable, captured.Executable)
	require.Equal(t, []string{"--showConfig"}, captured.Arguments)
	option, err := strconv.Unquote(strings.TrimSpace(strings.TrimPrefix(env["NODE_OPTIONS"], "--require ")))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "runtime.cjs"), option)
	require.NoError(t, os.Remove(path))
	_, err = readJestRuntimeCapture(path)
	require.Error(t, err, "unknown runtimes must not fall back to unrelated PATH Node")
}
