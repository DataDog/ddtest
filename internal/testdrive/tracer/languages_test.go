// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonInstallUsesSelectedInterpreterAndIsolatedTarget(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeCommandExecutor{responses: []commandResponse{{}}}
	installer := &Python{Interpreter: "/customer/venv/bin/python", command: "/customer/venv/bin/python", executor: executor}
	path, err := installer.Install(t.Context(), directory)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "python"), path)
	require.Equal(t, "/customer/venv/bin/python", executor.commands[0].name)
	require.Equal(t, []string{"-m", "pip", "install", "--disable-pip-version-check", "--target", filepath.Join(directory, "python-packages"), "ddtrace==" + PythonVersion}, executor.commands[0].args)
	contents, err := os.ReadFile(filepath.Join(path, "sitecustomize.py"))
	require.NoError(t, err)
	require.Contains(t, string(contents), filepath.Join(directory, "python-packages"))
	executor = &fakeCommandExecutor{responses: []commandResponse{{output: []byte("pip unavailable"), err: errors.New("exit 1")}}}
	installer.executor = executor
	_, err = installer.Install(t.Context(), directory)
	require.ErrorContains(t, err, "pip unavailable")
}
func TestNewPythonForCommandUsesRunnerInterpreter(t *testing.T) {
	for _, test := range []struct {
		command     string
		args        []string
		wantCommand string
		wantPrefix  []string
	}{
		{command: "python3.12", args: []string{"-m", "pytest"}, wantCommand: "python3.12"},
		{command: ".venv/bin/pytest", wantCommand: filepath.Join(".venv", "bin", "python")},
		{command: "uv", args: []string{"run", "pytest"}, wantCommand: "uv", wantPrefix: []string{"run", "python"}},
		{command: "poetry", args: []string{"run", "pytest"}, wantCommand: "poetry", wantPrefix: []string{"run", "python"}},
	} {
		installer := NewPythonForCommand(test.command, test.args, "python")
		require.Equal(t, test.wantCommand, installer.command)
		require.Equal(t, test.wantPrefix, installer.prefixArgs)
	}
}
