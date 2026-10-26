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
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestPytestDiscoveryFailuresProduceReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "check-only", true: "all"}[all], func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("PATH", root)
			t.Setenv("VIRTUAL_ENV", "")
			t.Setenv("CONDA_PREFIX", "")
			viper.Reset()
			settings.Init()
			t.Cleanup(func() { viper.Reset(); settings.Init() })
			requireWriteFile(t, "pyproject.toml", "[tool.pytest.ini_options]\n")
			require.NoError(t, os.MkdirAll(".github/workflows", 0755))
			requireWriteFile(t, ".github/workflows/test.yml", "jobs: [broken\n")
			require.NoError(t, os.WriteFile("python", []byte("#!/bin/sh\nfor last; do :; done\nprintf '%s' '{\"executable\":\"python\",\"version\":\"3.12.8\",\"pytest\":true}' > \"$last\"\n"), 0755))
			drive, err := Prepare(PrepareOptions{CheckOnly: true, All: all})
			require.NoError(t, err, "discovery must not abort before report creation")
			require.Equal(t, all, drive.allConfigurations)
			require.Error(t, drive.Run(t.Context(), &bytes.Buffer{}))
			report := readValidationReport(t, root)
			require.Contains(t, report.Error, "parse")
			require.False(t, report.Success)
			require.NotNil(t, report.Scope)
			require.NotEmpty(t, report.Scope.Unresolved)
		})
	}
}

func TestPytestDefaultDoesNotPromoteToAllConfigurations(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	viper.Reset()
	settings.Init()
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	requireWriteFile(t, "pyproject.toml", "[tool.pytest.ini_options]\n")
	require.NoError(t, os.MkdirAll(".github/workflows", 0755))
	requireWriteFile(t, ".github/workflows/test.yml", "jobs:\n  tests:\n    steps:\n      - run: python -m pytest\n")
	drive, err := Prepare(PrepareOptions{})
	require.NoError(t, err)
	require.False(t, drive.allConfigurations)
}

func TestPytestReportsAreReadOnlyHistoricalEvidence(t *testing.T) {
	for _, checkOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "execution", true: "retained"}[checkOnly], func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			validation := completeValidationFixture("pytest")
			root = canonicalWorkflowRoot(root)
			validation.Success, validation.LocalSuccess = true, true
			if checkOnly {
				previous := validation
				validation = validationResult{Framework: "pytest", CheckOnly: true, Retained: &retainedExecution{Result: &previous}}
			}
			before, err := json.Marshal(validation)
			require.NoError(t, err)
			require.NoError(t, writeValidation(validationPath(root), before))
			requireWriteFile(t, htmlReportPath(root), "saved HTML")
			t.Setenv("PATH", root) // Report readback must not require Node or Python.
			for _, format := range []string{"json", "verbose"} {
				var output bytes.Buffer
				require.NoError(t, RenderReport(t.Context(), &output, format))
				require.Contains(t, output.String(), "historical_report")
				require.Contains(t, output.String(), "freshness_unverified")
				require.NotContains(t, output.String(), "Local testdrive: PASSED")
				require.NotContains(t, output.String(), "--agent")
				if format == "json" {
					var result onboard.AgentResult
					require.NoError(t, json.Unmarshal(output.Bytes(), &result))
					require.Equal(t, validationPath(root), result.JSONReport)
					require.Equal(t, htmlReportPath(root), result.HTMLReport)
					require.NotEmpty(t, result.NextCommand)
				}
			}
			after, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			require.Equal(t, before, after)
			html, err := os.ReadFile(htmlReportPath(root))
			require.NoError(t, err)
			require.Equal(t, "saved HTML", string(html))
		})
	}
}

func TestPytestBlockedPluginFailsPreflight(t *testing.T) {
	for _, options := range []string{"-p no:ddtrace", "-pno:ddtrace.contrib.internal.pytest.plugin", "-p no:ddtrace.testing.internal.pytest.entry_point"} {
		for _, source := range []string{"environment", "command"} {
			t.Run(source+options, func(t *testing.T) {
				t.Setenv("PYTEST_ADDOPTS", "")
				drive := &Testdrive{repositoryRoot: t.TempDir(), framework: framework.NewPytest(platform.NewPython()), command: "python", args: []string{"-m", "pytest"}}
				if source == "environment" {
					t.Setenv("PYTEST_ADDOPTS", options)
				} else {
					drive.args = append(drive.args, "-p", "no:ddtrace")
				}
				result := validationResult{}
				require.ErrorContains(t, drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result), "block the Datadog plugin")
				require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
				require.Nil(t, result.PythonSelection, "must fail before tracer download or execution")
			})
		}
	}
	require.NoError(t, checkPytestPluginOptions("-p ddtrace", nil))
}

func TestPytestObserverResolvesPathsAgainstRootdir(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	if err := exec.CommandContext(t.Context(), python, "-c", "import pytest").Run(); err != nil {
		t.Skip("pytest required")
	}
	root := t.TempDir()
	t.Chdir(root)
	requireWriteFile(t, "ddtest_pytest_observer.py", pytestObserver)
	require.NoError(t, os.MkdirAll("project/tests", 0755))
	requireWriteFile(t, "project/tests/test_app.py", "def test_app():\n    assert True\n")
	resultPath := filepath.Join(root, "results.json")
	t.Setenv("PYTHONPATH", root)
	t.Setenv("PYTEST_ADDOPTS", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
	t.Setenv("DD_TRACE_ENABLED", "false")
	t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DDTEST_PYTEST_RESULTS", resultPath)
	output, err := exec.CommandContext(t.Context(), python, "-m", "pytest", "--rootdir=project", "project/tests", "-p", "ddtest_pytest_observer", "-q").CombinedOutput()
	require.NoError(t, err, "%s", output)
	run := validationRun{}
	readPytestResults(resultPath, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 1)
	expected, err := filepath.EvalSymlinks(filepath.Join(root, "project/tests/test_app.py"))
	require.NoError(t, err)
	require.Equal(t, expected, run.Tests[0].File)
	require.FileExists(t, run.Tests[0].File)
}
