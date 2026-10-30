package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestPytestInlineAddoptsFailsBeforeExecution(t *testing.T) {
	for _, command := range []string{
		`python -m pytest -o addopts=--no-ddtrace`,
		`pytest --override-ini 'addopts=-p no:ddtrace'`,
		`uv run -- python -m pytest --override-ini=addopts=--no-ddtrace`,
	} {
		t.Run(command, func(t *testing.T) {
			t.Setenv("PYTEST_ADDOPTS", "")
			words, err := shellquote.Split(command)
			require.NoError(t, err)
			drive := &Testdrive{repositoryRoot: t.TempDir(), command: words[0], args: words[1:]}
			result := validationResult{}
			require.Error(t, drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result))
			require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
			require.Empty(t, result.Tracer)
		})
	}
	for _, command := range []string{
		`pytest --no-ddtrace`,
		`pytest -o addopts=--no-ddtrace -o addopts=`,
		`uv run -p no:ddtrace -- python -m pytest`,
		`uv run -- python -m pytest -- --no-ddtrace`,
	} {
		words, err := shellquote.Split(command)
		require.NoError(t, err)
		require.NoError(t, checkPytestPluginOptions("", words, nil), command)
	}
}

func TestPytestAllDoesNotExecuteToxInventory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable fixture")
	}
	for _, checkOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "all", true: "check-only"}[checkOnly], func(t *testing.T) {
			drive := preparedTestdrive(t)
			root := drive.repositoryRoot
			drive.framework = framework.NewPytest(platform.NewPython())
			drive.language, drive.checkOnly = "python", checkOnly
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			requireWriteFile(t, filepath.Join(root, ".github/workflows/ci.yml"), "jobs:\n  tests:\n    steps:\n      - run: tox -e py312\n")
			requireWriteFile(t, filepath.Join(root, "pyproject.toml"), "[tool.tox.env_run_base]\ncommands = [[\"pytest\"]]\n")
			sentinel := filepath.Join(root, "tox-executed")
			requireWriteFile(t, filepath.Join(root, "tox"), "#!/bin/sh\ntouch '"+sentinel+"'\nexit 1\n")
			require.NoError(t, os.Chmod(filepath.Join(root, "tox"), 0755))
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			require.ErrorContains(t, drive.runAllConfigurations(t.Context(), &bytes.Buffer{}), "Local tox execution is not supported")
			require.NoFileExists(t, sentinel)
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var result validationResult
			require.NoError(t, json.Unmarshal(data, &result))
			require.False(t, result.Success)
			require.NotEmpty(t, result.Scope.Unresolved)
			require.Len(t, result.Configurations, 1)
			require.Equal(t, "blocked", result.Configurations[0].Status)
			require.Nil(t, result.Configurations[0].Preflight)
			require.Empty(t, result.Runs)
		})
	}
}

func TestPytestObserverKeepsBodyAndTeardownFailures(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	if err := exec.CommandContext(t.Context(), python, "-c", "import pytest, ddtrace").Run(); err != nil {
		t.Skip("pytest and ddtrace required")
	}
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, "ddtest_pytest_observer.py", pytestObserver)
	requireWriteFile(t, "test_app.py", "import os, pytest\n@pytest.fixture\ndef teardown():\n    yield\n    raise RuntimeError('constant teardown failure')\ndef test_app(teardown):\n    assert False, 'body-' + os.environ['BODY_MODE']\n")
	t.Setenv("PYTHONPATH", root)
	t.Setenv("PYTEST_ADDOPTS", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	t.Setenv("DD_TRACE_ENABLED", "false")
	t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	var runs []validationRun
	for _, mode := range []string{"baseline", "instrumented"} {
		t.Setenv("BODY_MODE", mode)
		path := filepath.Join(root, mode+".json")
		t.Setenv("DDTEST_PYTEST_RESULTS", path)
		output, err := exec.CommandContext(t.Context(), python, "-m", "pytest", "-p", "ddtest_pytest_observer", "-q").CombinedOutput()
		require.Error(t, err, "%s", output)
		run := validationRun{framework: "pytest", root: root, ExitCode: 1}
		readPytestResults(path, &run)
		require.Empty(t, run.ResultError)
		require.Len(t, run.Tests, 1)
		require.Equal(t, "failed", run.Tests[0].Status)
		require.Contains(t, run.Tests[0].Failure, "body-"+mode)
		require.Contains(t, run.Tests[0].Failure, "constant teardown failure")
		runs = append(runs, run)
	}
	require.Equal(t, "suspected regression", compareJest(runs[0], runs[1]).Status)
}
