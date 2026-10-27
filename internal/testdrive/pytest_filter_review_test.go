package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPytestProbeClearsFiltersWithoutChangingSelectedSuite(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	if err := exec.CommandContext(t.Context(), python, "-c", "import pytest, ddtrace").Run(); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" {
			t.Fatal("pytest and ddtrace required")
		}
		t.Skip("pytest and ddtrace required")
	}
	for _, tc := range []struct {
		name, config, environment string
		args                      []string
	}{
		{name: "keyword", args: []string{"-k", "test_add"}},
		{name: "marker", args: []string{"-m", "slow"}},
		{name: "config", config: "-k test_add -m slow"},
		{name: "environment", environment: "-k test_add -m slow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, "ddtest_pytest_observer.py", pytestObserver)
			requireWriteFile(t, "pytest.ini", "[pytest]\nmarkers = slow\naddopts = "+tc.config+"\n")
			requireWriteFile(t, "test_app.py", "import pytest\n@pytest.mark.slow\ndef test_add():\n    assert 1 + 1 == 2\ndef test_subtract():\n    assert 2 - 1 == 1\n")
			probe := filepath.Join(root, "probe.py")
			requireWriteFile(t, probe, pytestProbeSource)
			t.Setenv("PYTHONPATH", root)
			t.Setenv("PYTEST_ADDOPTS", tc.environment)
			t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
			t.Setenv("DD_TRACE_ENABLED", "false")
			t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
			t.Setenv("DDTEST_PROBE_MODE", "")
			t.Setenv("DDTEST_PYTEST_EXECUTIONS", filepath.Join(root, "executions.txt"))
			for _, synthetic := range []bool{false, true} {
				t.Setenv("DDTEST_PYTEST_PROBE", "")
				if synthetic {
					t.Setenv("DDTEST_PYTEST_PROBE", probe)
				}
				path := filepath.Join(root, "results.json")
				t.Setenv("DDTEST_PYTEST_RESULTS", path)
				args := append([]string{"-m", "pytest", "test_app.py", "-p", "ddtest_pytest_observer", "-q"}, tc.args...)
				output, err := exec.CommandContext(t.Context(), python, args...).CombinedOutput()
				require.NoError(t, err, "%s", output)
				run := validationRun{}
				readPytestResults(path, &run)
				require.Empty(t, run.ResultError)
				require.Len(t, run.Tests, 1)
				expected := "test_add"
				if synthetic {
					expected = pytestProbeName
				}
				require.Equal(t, expected, run.Tests[0].Name)
			}
		})
	}
}

func TestPytestConfigPluginBlockerFailsBeforeExecution(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	if err := exec.CommandContext(t.Context(), python, "-c", "import pytest").Run(); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" {
			t.Fatal("pytest required")
		}
		t.Skip("pytest required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PYTEST_ADDOPTS", "")
	requireWriteFile(t, "pytest.ini", "[pytest]\naddopts = -p no:ddtrace\n")
	drive := &Testdrive{repositoryRoot: root, command: python, args: []string{"-m", "pytest"}}
	result := validationResult{}
	require.ErrorContains(t, drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result), "block the Datadog plugin")
	require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
	require.Nil(t, result.PythonSelection)
}

func TestPytestAmbiguousConfigDoesNotGateFullExecution(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	if err := exec.CommandContext(t.Context(), python, "-c", "import pytest, ddtrace").Run(); err != nil {
		t.Skip("pytest and ddtrace required")
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PYTEST_ADDOPTS", "")
	requireWriteFile(t, "pytest.ini", "[pytest]\n")
	require.NoError(t, os.Mkdir("tests", 0755))
	requireWriteFile(t, "tests/pytest.ini", "[pytest]\naddopts = -q\n")
	for _, checkOnly := range []bool{false, true} {
		drive := &Testdrive{repositoryRoot: root, command: python, args: []string{"-m", "pytest", "tests"}, checkOnly: checkOnly}
		result := validationResult{}
		err := drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result)
		if checkOnly {
			require.ErrorContains(t, err, "select its config explicitly")
			require.Equal(t, "inconclusive", result.Preflight.Verdict.Status)
		} else {
			require.NoError(t, err)
			require.Equal(t, "compatible", result.Preflight.Verdict.Status)
		}
	}
}

func TestPytestPreflightReadsConfigFromSelectedEnvironmentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, blocked := range []bool{false, true} {
		root := t.TempDir()
		directory := filepath.Join(root, "selected")
		require.NoError(t, os.Mkdir(directory, 0755))
		requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\naddopts = -p no:ddtrace\n")
		options := "-q"
		if blocked {
			options = "-p no:ddtrace"
		}
		requireWriteFile(t, filepath.Join(directory, "pytest.ini"), "[pytest]\naddopts = "+options+"\n")
		metadata, err := json.Marshal(map[string]string{"pytest": "8.4.2", "python": "3.12.8", "tracer": "4.16.0", "requiresPython": ">=3.9", "directory": directory})
		require.NoError(t, err)
		requireWriteFile(t, filepath.Join(root, "poetry"), "#!/bin/sh\nfor last; do :; done\nprintf '%s' '"+string(metadata)+"' > \"$last\"\n")
		require.NoError(t, os.Chmod(filepath.Join(root, "poetry"), 0755))
		t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("PYTEST_ADDOPTS", "")
		drive := &Testdrive{repositoryRoot: root, command: "poetry", args: []string{"run", "pytest"}, checkOnly: true}
		result := validationResult{}
		err = drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result)
		if blocked {
			require.ErrorContains(t, err, "block the Datadog plugin")
			require.Nil(t, result.PythonSelection)
		} else {
			require.NoError(t, err)
			require.Equal(t, "compatible", result.Preflight.Verdict.Status)
		}
	}
}
