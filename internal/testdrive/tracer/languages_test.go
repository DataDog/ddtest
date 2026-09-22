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

type rubyInstallExecutor struct{ env map[string]string }

func (e *rubyInstallExecutor) CombinedOutput(_ context.Context, _ string, _ []string, env map[string]string) ([]byte, error) {
	e.env = env
	return nil, nil
}

func TestRubyInstallDoesNotEditCustomerBundle(t *testing.T) {
	root := t.TempDir()
	directory := t.TempDir()
	original := "source 'https://rubygems.org'\ngemspec\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile"), []byte(original), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte("customer lock"), 0644))
	executor := &rubyInstallExecutor{}
	installer := &Ruby{root: root, executor: executor}
	path, err := installer.Install(t.Context(), directory)
	require.NoError(t, err)
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "eval_gemfile")
	require.Contains(t, string(contents), filepath.Join(root, "Gemfile"))
	require.Contains(t, string(contents), RubyVersion)
	require.Equal(t, path, executor.env["BUNDLE_GEMFILE"])
	require.Equal(t, filepath.Join(directory, "gems"), executor.env["BUNDLE_PATH"])
	contents, err = os.ReadFile(filepath.Join(root, "Gemfile"))
	require.NoError(t, err)
	require.Equal(t, original, string(contents))
	contents, err = os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, "customer lock", string(contents))
}

func TestRubyLockfileRelocatesOnlyLocalPathSources(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	lock := "PATH\n  remote: .\n  specs:\n    local (1.0.0)\n\nGIT\n  remote: https://example.com/gem.git\n\nGEM\n  remote: https://rubygems.org/\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "Gemfile.lock"), []byte(lock), 0644))
	require.NoError(t, copyRubyLockfile(root, session))
	contents, err := os.ReadFile(filepath.Join(session, "Gemfile.lock"))
	require.NoError(t, err)
	require.Contains(t, string(contents), "PATH\n  remote: "+root+"\n")
	require.Contains(t, string(contents), "GIT\n  remote: https://example.com/gem.git")
	original, err := os.ReadFile(filepath.Join(root, "Gemfile.lock"))
	require.NoError(t, err)
	require.Equal(t, lock, string(original))
}

func TestRubyReportsUnsupportedNativeBuildPathBeforeInstalling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project space")
	_, err := NewRuby(t.TempDir()).Install(t.Context(), path)
	require.ErrorContains(t, err, "checkout without spaces")
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
}
