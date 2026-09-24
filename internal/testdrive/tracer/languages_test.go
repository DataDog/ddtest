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
	executor := &fakeCommandExecutor{responses: []commandResponse{{}, {}}}
	installer := &Python{Interpreter: "/customer/venv/bin/python", command: "/customer/venv/bin/python", executor: executor}
	path, err := installer.Install(t.Context(), directory)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "python"), path.Path)
	require.Equal(t, "/customer/venv/bin/python", executor.commands[1].name)
	require.Equal(t, []string{"-m", "pip", "install", "--disable-pip-version-check", "--target", filepath.Join(directory, "python-packages"), "ddtrace"}, executor.commands[1].args)
	contents, err := os.ReadFile(filepath.Join(path.Path, "sitecustomize.py"))
	require.NoError(t, err)
	require.Contains(t, string(contents), filepath.Join(directory, "python-packages"))
	require.Contains(t, string(contents), "sys.path.append(")
	executor = &fakeCommandExecutor{responses: []commandResponse{{}, {output: []byte("pip unavailable"), err: errors.New("exit 1")}}}
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
		installer := NewPythonForCommand(test.command, test.args, "python", "latest")
		require.Equal(t, test.wantCommand, installer.command)
		require.Equal(t, test.wantPrefix, installer.prefixArgs)
	}
}

func TestPythonTracerVersions(t *testing.T) {
	for _, tt := range []struct{ version, spec string }{
		{"", "ddtrace"}, {"latest", "ddtrace"}, {"4.15.1", "ddtrace==4.15.1"},
		{"4.16.0rc1", "ddtrace==4.16.0rc1"},
		{"git:abc1234", "ddtrace @ git+https://github.com/DataDog/dd-trace-py.git@abc1234"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{}, {}}}
			installer := NewPythonForCommand("uv", []string{"run", "pytest"}, "python", tt.version)
			installer.executor = executor
			_, err := installer.Install(t.Context(), t.TempDir())
			require.NoError(t, err)
			require.Equal(t, "uv", executor.commands[1].name)
			require.Equal(t, []string{"run", "python", "-m", "pip"}, executor.commands[1].args[:4])
			require.Equal(t, tt.spec, executor.commands[1].args[len(executor.commands[1].args)-1])
		})
	}
	_, err := NewPython("python", "git:").Install(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "git ref must not be empty")
}

func TestPythonReusesProjectTracer(t *testing.T) {
	for _, version := range []string{"latest", "4.15.1", "git:abc1234"} {
		t.Run(version, func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{output: []byte("3.0.0\n")}}}
			installer := NewPythonForCommand("uv", []string{"run", "pytest"}, "python", version)
			installer.executor = executor
			directory := t.TempDir()
			result, err := installer.Install(t.Context(), directory)
			require.NoError(t, err)
			require.Equal(t, Installation{Project: true}, result)
			require.Len(t, executor.commands, 1)
			require.Equal(t, "uv", executor.commands[0].name)
			require.Equal(t, []string{"run", "python", "-c"}, executor.commands[0].args[:3])
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestPythonProbeFailureDoesNotInstall(t *testing.T) {
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("interpreter unavailable")}}}
	installer := NewPython("python", "latest")
	installer.executor = executor
	_, err := installer.Install(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "interpreter unavailable")
	require.Len(t, executor.commands, 1)
}
