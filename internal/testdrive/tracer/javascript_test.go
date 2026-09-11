// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type command struct {
	name string
	args []string
}

type commandResponse struct {
	output []byte
	err    error
}

type fakeCommandExecutor struct {
	commands  []command
	responses []commandResponse
}

func (e *fakeCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, _ map[string]string) ([]byte, error) {
	e.commands = append(e.commands, command{name: name, args: args})
	response := e.responses[len(e.commands)-1]
	return response.output, response.err
}

func TestJavaScriptInstall(t *testing.T) {
	sessionDirectory := t.TempDir()
	resolvedPath := filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init.js")
	executor := &fakeCommandExecutor{
		responses: []commandResponse{
			{},
			{output: []byte(resolvedPath)},
		},
	}
	javascript := &JavaScript{executor: executor}

	ciInitPath, err := javascript.Install(context.Background(), sessionDirectory)
	require.NoError(t, err)
	require.Equal(t, resolvedPath, ciInitPath)
	require.True(t, filepath.IsAbs(ciInitPath))
	require.Equal(t, []command{
		{
			name: "npm",
			args: []string{
				"install",
				"--prefix", sessionDirectory,
				"--no-save",
				"--package-lock=false",
				"--no-audit",
				"--no-fund",
				"dd-trace@" + JavaScriptVersion,
			},
		},
		{
			name: "node",
			args: []string{
				"-e",
				resolveJavaScriptModule,
				filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init"),
			},
		},
	}, executor.commands)
}

func TestJavaScriptInstallReportsNPMError(t *testing.T) {
	executor := &fakeCommandExecutor{
		responses: []commandResponse{{
			output: []byte("registry unavailable"),
			err:    errors.New("exit status 1"),
		}},
	}
	javascript := &JavaScript{executor: executor}

	_, err := javascript.Install(context.Background(), t.TempDir())
	require.ErrorContains(t, err, "install dd-trace@"+JavaScriptVersion)
	require.ErrorContains(t, err, "registry unavailable")
}

func TestJavaScriptInstallEndToEnd(t *testing.T) {
	if os.Getenv("DDTEST_RUN_NPM_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_NPM_INTEGRATION_TEST=1 to install the pinned tracer from npm")
	}

	sessionDirectory := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	ciInitPath, err := NewJavaScript().Install(ctx, sessionDirectory)
	require.NoError(t, err)
	require.FileExists(t, ciInitPath)
	resolvedSessionDirectory, err := filepath.EvalSymlinks(sessionDirectory)
	require.NoError(t, err)
	require.Equal(t, resolvedSessionDirectory, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(ciInitPath)))))
}
