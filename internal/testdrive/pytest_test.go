package testdrive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

func TestPytestKeepsWrapperSeparatorsAndCIOptions(t *testing.T) {
	t.Setenv("PYTEST_ADDOPTS", "-k ignored")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
	session := workspace.PlanSession()
	require.NoError(t, session.Create())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	executor := &fakeTestdriveExecutor{}
	drive := &Testdrive{repositoryRoot: t.TempDir(), language: "python", command: "uv", args: []string{"run", "--", "python", "-m", "pytest", "--", "test_app.py"}, executor: executor, configurationEnvironment: map[string]string{"PYTEST_ADDOPTS": "--ddtrace -q --cov=app", "PYTHONPATH": "custom-packages", "APP_MODE": "ci"}, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil }}
	_, err := drive.runPytest(t.Context(), io.Discard, session, "", runSpec{Name: "baseline", Role: roleBaseline})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--no-sync", "--", "python", "-m", "pytest", "--no-ddtrace", "--", "test_app.py"}, executor.args)
	require.Contains(t, executor.env["PYTEST_ADDOPTS"], "-q --cov=app")
	require.NotContains(t, executor.env["PYTEST_ADDOPTS"], "ignored")
	require.Contains(t, executor.env["PYTEST_ADDOPTS"], "--no-ddtrace")
	require.Contains(t, executor.env["PYTHONPATH"], "custom-packages")
	require.Equal(t, "ci", executor.env["APP_MODE"])
	require.Equal(t, "false", executor.env["DD_TRACE_ENABLED"])
}

func TestPytestAttachedModuleKeepsWrapperAndSuiteSeparators(t *testing.T) {
	session := workspace.PlanSession()
	require.NoError(t, session.Create())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	executor := &fakeTestdriveExecutor{}
	drive := &Testdrive{repositoryRoot: t.TempDir(), command: "uv", args: []string{"run", "--", "python", "-mpytest", "--", "test_app.py"}, executor: executor, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil }}
	_, err := drive.runPytest(t.Context(), io.Discard, session, "", runSpec{Name: "baseline", Role: roleBaseline})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--no-sync", "--", "python", "-mpytest", "--no-ddtrace", "--", "test_app.py"}, executor.args)
}

func TestPytestResultsRequireCompleteNativeEvidence(t *testing.T) {
	for _, contents := range []string{"", `{}`, `{"complete":false,"tests":[]}`, `{"complete":true,"tests":null}`} {
		run := validationRun{}
		path := filepath.Join(t.TempDir(), "results.json")
		require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
		readPytestResults(path, &run)
		require.NotEmpty(t, run.ResultError)
	}
	path := filepath.Join(t.TempDir(), "results.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"complete":true,"tests":[{"file":"/project/test_math.py","name":"TestMath::test_add[one]","status":"passed"},{"file":"/project/test_math.py","name":"TestMath::test_add[two]","status":"failed","failure":"AssertionError: two"}],"errors":["collection failed"]}`), 0600))
	run := validationRun{}
	readPytestResults(path, &run)
	require.Empty(t, run.ResultError)
	require.Len(t, run.Tests, 2)
	require.NotEqual(t, run.Tests[0].Name, run.Tests[1].Name)
	require.Equal(t, []string{"collection failed"}, run.SuiteErrors)
}

func TestPytestBaselinePreservesCustomerOptions(t *testing.T) {
	options, err := stripPytestInstrumentation(`--ddtrace --ddtrace-patch-all -p ddtrace.contrib.pytest.plugin -v -k 'some test' --cov=app`)
	require.NoError(t, err)
	require.Equal(t, `-p ddtrace.contrib.pytest.plugin -v -k 'some test' --cov=app`, options)
	_, err = stripPytestInstrumentation(`-k 'unfinished`)
	require.Error(t, err)
}

func TestPytestInstrumentationStrippingRespectsOptionValuesAndSeparator(t *testing.T) {
	words := []string{"--ddtrace", "-k", "--ddtrace", "-m", "--no-ddtrace", "-o", "addopts=--ddtrace", "--ddtrace-patch-all", "--", "--ddtrace"}
	require.Equal(t, []string{"-k", "--ddtrace", "-m", "--no-ddtrace", "-o", "addopts=--ddtrace", "--", "--ddtrace"}, stripPytestArguments(words))
}

func TestPytestTracerVerificationChecksImportedVersion(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "ddtrace.py"), "__version__ = '0.0.0'\n")
	drive := &Testdrive{language: "python", command: python, args: []string{"-m", "pytest"}}
	require.ErrorContains(t, drive.verifyPytestTracer(t.Context(), root, "4.16.0"), "test environment loads ddtrace@0.0.0")
}

func TestPytestObserverHandlesDisabledAutoload(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	check := exec.CommandContext(t.Context(), python, "-c", "import pytest, ddtrace; assert pytest.version_tuple >= (8, 4)")
	if output, err := check.CombinedOutput(); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" {
			t.Fatalf("pytest >=8.4 and ddtrace required: %s: %v", output, err)
		}
		t.Skip("pytest >=8.4 and ddtrace required")
	}
	for _, tc := range []struct {
		name, environment, config string
		args                      []string
	}{
		{name: "environment", environment: "1"},
		{name: "command", args: []string{"--disable-plugin-autoload"}},
		{name: "config", config: "[pytest]\naddopts=--disable-plugin-autoload\n"},
		{name: "explicit plugin", environment: "1", args: []string{"-p", "ddtrace.contrib.internal.pytest.plugin"}},
		{name: "explicit entry point", environment: "1", args: []string{"-p", "ddtrace.testing.internal.pytest.entry_point"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, "ddtest_pytest_observer.py", pytestObserver)
			requireWriteFile(t, "ddtest_hook_order.py", "import pytest\n@pytest.hookimpl(tryfirst=True)\ndef pytest_load_initial_conftests(early_config):\n    assert hasattr(early_config.option, 'ddtrace'), 'tracer registered too late'\n")
			requireWriteFile(t, "test_app.py", "def test_app():\n    assert True\n")
			if tc.config != "" {
				requireWriteFile(t, "pytest.ini", tc.config)
			}
			resultPath := filepath.Join(root, "results.json")
			t.Setenv("PYTHONPATH", root)
			t.Setenv("PYTEST_ADDOPTS", "")
			t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", tc.environment)
			t.Setenv("DD_TRACE_ENABLED", "false")
			t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
			t.Setenv("DDTEST_PYTEST_RESULTS", resultPath)
			args := append([]string{"-m", "pytest", "-p", "ddtest_pytest_observer", "-p", "ddtest_hook_order", "--no-ddtrace", "-q"}, tc.args...)
			output, err := exec.CommandContext(t.Context(), python, args...).CombinedOutput()
			require.NoError(t, err, "%s", output)
			run := validationRun{}
			readPytestResults(resultPath, &run)
			require.Empty(t, run.ResultError)
			require.Len(t, run.Tests, 1)
			require.Equal(t, "passed", run.Tests[0].Status)
		})
	}
}

func TestPythonReviewAllSelectsEachCommandEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable fixtures")
	}
	for _, tc := range []struct {
		name     string
		commands []string
		failure  bool
	}{
		{"versioned interpreters", []string{"python3.10 -m pytest", "python3.12 -m pytest"}, false},
		{"default unavailable", []string{".venv/bin/python -m pytest", "poetry run pytest"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			for _, command := range []string{"python3.10", "python3.12", ".venv/bin/python", "poetry"} {
				version := "3.12.8"
				if command == "python3.10" {
					version = "3.10.16"
				}
				metadata, err := json.Marshal(map[string]string{"Pytest": "8.4.2", "Python": version, "Tracer": "4.16.0", "RequiresPython": ">=3.9", "Executable": filepath.Join(root, command)})
				require.NoError(t, err)
				availability, err := json.Marshal(map[string]any{"pytest": true, "version": version, "executable": filepath.Join(root, command)})
				require.NoError(t, err)
				file := filepath.Join(root, command)
				require.NoError(t, os.MkdirAll(filepath.Dir(file), 0755))
				requireWriteFile(t, file, "#!/bin/sh\nfor last; do :; done\ncase \"$*\" in\n*'result = {\"executable\"'*) printf '%s' '"+string(availability)+"' > \"$last\";;\n*) printf '%s' '"+string(metadata)+"' > \"$last\";;\nesac\n")
				require.NoError(t, os.Chmod(file, 0755))
			}
			var steps strings.Builder
			for _, command := range tc.commands {
				fmt.Fprintf(&steps, "      - run: %s\n", command)
			}
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n"+steps.String())
			drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: "python", tracerVersion: "4.16.0", checkOnly: true,
				environmentInfo: environmentInfo{Executable: filepath.Join(root, "python3.12"), Version: "3.12.8"}}
			if tc.failure {
				drive.environmentFailure = environmentFinding("pytest-unavailable", "default pytest missing", "default environment is unavailable", "select a CI environment")
			}
			_ = drive.runAllConfigurations(t.Context(), io.Discard)
			data, err := os.ReadFile(validationPath(root))
			require.NoError(t, err)
			var result validationResult
			require.NoError(t, json.Unmarshal(data, &result))
			require.Equal(t, "ddtrace@4.16.0", result.Tracer)
			require.NotContains(t, result.Error, "tracer selection differs")
			require.Len(t, result.Configurations, len(tc.commands))
			for index, configuration := range result.Configurations {
				require.NotNil(t, configuration.Preflight, "%s: %s", configuration.Command, configuration.Error)
				require.Equal(t, tc.commands[index], configuration.Preflight.Command)
				require.Equal(t, "compatible", configuration.Preflight.Verdict.Status)
				if !tc.failure && index == 0 {
					require.Equal(t, "3.10.16", configuration.Preflight.Runtime)
				} else {
					require.Equal(t, "3.12.8", configuration.Preflight.Runtime)
				}
			}
		})
	}
}

func TestPythonReviewTracerVerificationKeepsSelectedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable fixture")
	}
	root := t.TempDir()
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	command := filepath.Join(root, "uv")
	requireWriteFile(t, command, "#!/bin/sh\n[ \"$UV_PROJECT_ENVIRONMENT\" = .venv-ci ] || exit 3\n[ \"$DD_ENV\" = application-ci ] || exit 4\n[ \"$PYTHONPATH\" = tracer-path:application-path ] || exit 5\nfor last; do :; done\nprintf '%s' '{\"Version\":\"4.16.0\"}' > \"$last\"\n")
	require.NoError(t, os.Chmod(command, 0755))
	drive := &Testdrive{language: "python", command: "uv", args: []string{"run", "pytest"}, configurationEnvironment: map[string]string{"UV_PROJECT_ENVIRONMENT": ".venv-ci", "DD_ENV": "application-ci", "PYTHONPATH": "application-path"}}
	require.NoError(t, drive.verifyPytestTracer(t.Context(), "tracer-path", "4.16.0"))
}

func TestPytestWorkflowPreservesStandaloneValidation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		options    WorkflowOptions
		standalone bool
	}{
		{"standalone", WorkflowOptions{Format: "verbose"}, true},
		{"agent coordinator", WorkflowOptions{Format: "agent"}, false},
		{"automatic CI edits", WorkflowOptions{Format: "json", Apply: true}, false},
		{"resume coordinator", WorkflowOptions{Format: "verbose", Resume: "previous-run"}, false},
		{"setup facts", WorkflowOptions{Format: "verbose", FactsFile: "facts.json"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), environmentFailure: environmentFinding("pytest-unavailable", "selected pytest environment is unavailable", "missing pytest", "prepare the environment")}
			var output bytes.Buffer
			err := drive.Workflow(t.Context(), &output, tc.options)
			if tc.standalone {
				require.ErrorContains(t, err, "Python environment")
				require.FileExists(t, validationPath(root))
			} else {
				require.ErrorContains(t, err, "framework_unsupported")
				require.NoFileExists(t, validationPath(root))
			}
		})
	}
}

func TestPytestPreviewDescribesControlledValidation(t *testing.T) {
	drive := &Testdrive{repositoryRoot: t.TempDir(), language: "python", framework: framework.NewPytest(nil), command: "test", session: workspace.PlanSession()}
	var output bytes.Buffer
	drive.Preview(&output)
	for _, expected := range []string{"Python runtimes", "without instrumentation", "temporary probe", "early-flake-detection, skipping", "disable coverage thresholds only for isolated probes"} {
		require.Contains(t, output.String(), expected)
	}
	require.NotContains(t, output.String(), "Node runtimes")
	require.NotContains(t, output.String(), "reporting-only telemetry")
}

// Uses an explicitly supplied existing environment; never installs dependencies.
// DDTEST_PYTEST_PYTHON=/path/to/venv/bin/python go test ./internal/testdrive -run TestPytestRealValidation -v
func TestPytestRealValidation(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) { testPytestRealValidation(t, false) })
	t.Run("autouse parameters", func(t *testing.T) { testPytestRealValidation(t, true) })
}

func testPytestRealValidation(t *testing.T, parameters bool) {
	python := os.Getenv("DDTEST_PYTEST_PYTHON")
	if python == "" {
		python, _ = exec.LookPath("python3")
		if python == "" {
			t.Skip("Python is unavailable")
		}
		if err := exec.Command(python, "-c", `import pytest, ddtrace; from packaging.version import Version; assert Version(ddtrace.__version__) >= Version("4.11.0")`).Run(); err != nil {
			t.Skip("set DDTEST_PYTEST_PYTHON to an environment with pytest and ddtrace >=4.11")
		}
	}
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://github.com/example/ddtest-pytest-fixture")
	t.Setenv("DD_GIT_COMMIT_SHA", "0123456789012345678901234567890123456789")
	t.Setenv("DD_GIT_BRANCH", "main")
	t.Setenv("DD_GIT_COMMIT_MESSAGE", "pytest validation fixture")
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	requireWriteFile(t, filepath.Join(root, "app.py"), "def add(a, b):\n    return a + b\n")
	if parameters {
		requireWriteFile(t, filepath.Join(root, "conftest.py"), "import pytest\n@pytest.fixture(autouse=True, params=['native', 'extension'])\ndef implementation(request):\n    return request.param\n")
	}
	requireWriteFile(t, filepath.Join(root, "test_app.py"), "import pytest\nfrom app import add\n@pytest.mark.parametrize('value', [1, 2])\ndef test_add(value):\n    assert add(value, 0) == value\nclass TestMath:\n    def test_method(self):\n        assert add(1, 2) == 3\n@pytest.mark.skip(reason='fixture')\ndef test_skip():\n    pass\ndef test_existing_failure():\n    assert add(1, 1) == 3\n")
	git := &ext.DefaultCommandExecutor{}
	_, err := git.CombinedOutput(t.Context(), "git", []string{"init", "-q"}, nil)
	require.NoError(t, err)
	session := workspace.PlanSession()
	require.NoError(t, session.Create())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: python, args: []string{"-m", "pytest"}, executor: git, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
		return intake.StartScenario(dir, scenario)
	}}
	result := validationResult{Framework: "pytest"}
	var output bytes.Buffer
	require.NoError(t, drive.runFrameworkValidation(t.Context(), &output, session, "", &result), output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, "%+v", result.Compatibility)
	for _, feature := range result.Features {
		if parameters && feature.Name == "early-flake-detection" && feature.Status != "passed" {
			require.Equal(t, "known limitation", feature.Status, feature.Reason)
			require.Equal(t, tracerLimitation, feature.Code)
			require.Equal(t, tracerControlNotApplied, feature.ObservedCode)
			require.False(t, featureSatisfied("pytest", feature))
			continue
		}
		require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
	}
	count := 6
	if parameters {
		count = 12
	}
	require.Len(t, result.Features, count)
	require.True(t, result.HTMLReportCurrent)
	probes, err := filepath.Glob(filepath.Join(root, "ddtest-pytest-*"))
	require.NoError(t, err)
	require.Empty(t, probes)
}
