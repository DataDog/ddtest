// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonInstallUsesSelectedInterpreterAndIsolatedTarget(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeCommandExecutor{responses: []commandResponse{{}}}
	installer := &Python{Interpreter: "/customer/venv/bin/python", executor: executor}
	path, err := installer.Install(t.Context(), directory)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "python"), path)
	require.Equal(t, "/customer/venv/bin/python", executor.commands[0].name)
	require.Equal(t, []string{"-m", "pip", "install", "--disable-pip-version-check", "--target", path, "ddtrace==" + PythonVersion}, executor.commands[0].args)
	executor = &fakeCommandExecutor{responses: []commandResponse{{output: []byte("pip unavailable"), err: errors.New("exit 1")}}}
	installer.executor = executor
	_, err = installer.Install(t.Context(), directory)
	require.ErrorContains(t, err, "pip unavailable")
}
