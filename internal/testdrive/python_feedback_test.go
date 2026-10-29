package testdrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPytestCoverageRequiresApplicationFiles(t *testing.T) {
	root := t.TempDir()
	for _, file := range []string{"test_app.py", "conftest.py", "tests/helpers.py", ".venv/lib/dependency.py", "app.py", "checks.py"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755))
		requireWriteFile(t, filepath.Join(root, file), "# fixture\n")
	}
	native := []nativeTest{{File: filepath.Join(root, "test_app.py")}, {File: filepath.Join(root, "checks.py")}}
	for _, file := range []string{"/test_app.py", "/checks.py", "/conftest.py", "/tests/helpers.py", "/.venv/lib/dependency.py", "/app.py", "/missing.py"} {
		facts := intake.Facts{TestCount: 1, CoveredTestCount: 1, CoverageLevel: "test", Tests: []intake.Test{{CoveredFiles: []string{file}}}}
		want := "not observed"
		switch file {
		case "/app.py":
			want = "observed"
		case "/missing.py":
			want = "invalid"
		}
		require.Equal(t, want, pytestSourceCoverage(root, facts, native).Status, file)
		require.Equal(t, []string{file}, facts.Tests[0].CoveredFiles, "raw evidence must stay unchanged")
	}
}

func TestPythonValidationProbesRespectTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	python := filepath.Join(root, "python")
	requireWriteFile(t, python, "#!/bin/sh\nexec /bin/sleep 30\n")
	require.NoError(t, os.Chmod(python, 0755))
	drive := &Testdrive{repositoryRoot: root, command: python, args: []string{"-m", "pytest"}, commandTimeout: 100 * time.Millisecond}
	for _, probe := range []string{"initial", "refresh", "preflight", "verification"} {
		t.Run(probe, func(t *testing.T) {
			started := time.Now()
			var err error
			switch probe {
			case "initial":
				_, _, _, finding := (pythonLanguage{}).inspectEnvironment(t.Context(), environmentRequest{root: root, command: python, args: drive.args, explicit: true, timeout: drive.commandTimeout})
				require.NotNil(t, finding)
				require.Contains(t, finding.Context, "100ms per-command limit")
			case "refresh":
				(pythonLanguage{}).refreshProjectTracer(drive, t.Context())
				require.NotNil(t, drive.environmentFailure)
				require.Contains(t, drive.environmentFailure.Context, "100ms per-command limit")
			case "preflight":
				err = drive.checkPytestPreflight(t.Context(), io.Discard, &validationResult{})
			case "verification":
				err = drive.verifyPytestTracer(t.Context(), "", "4.16.0")
			}
			if probe == "preflight" || probe == "verification" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				var timeout *ext.CommandTimeoutError
				require.True(t, errors.As(err, &timeout))
			}
			require.Less(t, time.Since(started), 3*time.Second)
		})
	}
}

func feedbackPython(t *testing.T) string {
	t.Helper()
	python := os.Getenv("DDTEST_PYTEST_PYTHON")
	if python == "" {
		python, _ = exec.LookPath("python3")
	}
	if python == "" {
		t.Skip("Python required")
	}
	if output, err := exec.CommandContext(t.Context(), python, "-c", `import pytest, ddtrace; from packaging.version import Version; assert Version(ddtrace.__version__) >= Version("4.11.0") and Version(pytest.__version__) >= Version("7.2")`).CombinedOutput(); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" || os.Getenv("DDTEST_PYTEST_PYTHON") != "" {
			t.Fatalf("pytest and ddtrace required: %s: %v", output, err)
		}
		t.Skip("pytest and ddtrace required")
	}
	return python
}

func TestPytestObserverKeepsDuplicateExecutions(t *testing.T) {
	python := feedbackPython(t)
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	t.Chdir(root)
	requireWriteFile(t, "ddtest_pytest_observer.py", pytestObserver)
	requireWriteFile(t, "test_app.py", "import os\nattempts = 0\ndef test_duplicate():\n    global attempts\n    attempts += 1\n    assert attempts != 1, os.environ['FAILURE_TEXT']\n")
	t.Setenv("PYTHONPATH", root)
	t.Setenv("PYTEST_ADDOPTS", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	t.Setenv("DD_TRACE_ENABLED", "false")
	t.Setenv("DD_CIVISIBILITY_ENABLED", "false")
	var runs []validationRun
	for _, failure := range []string{"before", "after"} {
		t.Setenv("FAILURE_TEXT", failure)
		path := filepath.Join(root, failure+".json")
		t.Setenv("DDTEST_PYTEST_RESULTS", path)
		output, err := exec.CommandContext(t.Context(), python, "-m", "pytest", "test_app.py", "test_app.py", "-p", "ddtest_pytest_observer", "-q").CombinedOutput()
		require.Error(t, err, "%s", output)
		run := validationRun{framework: "pytest", root: root, ExitCode: 1}
		readPytestResults(path, &run)
		require.Empty(t, run.ResultError)
		require.Len(t, run.Tests, 2)
		require.Equal(t, "failed", run.Tests[0].Status)
		require.Contains(t, run.Tests[0].Failure, failure)
		require.Equal(t, "passed", run.Tests[1].Status)
		counts := nativeExecutionCounts(run)
		require.Equal(t, 2, counts.TestExecutions)
		require.Nil(t, counts.SourceTestIdentities)
		require.Contains(t, counts.Reason, "identities repeat")
		runs = append(runs, run)
	}
	require.Equal(t, "suspected regression", compareJest(runs[0], runs[1]).Status)
}

func TestPytestNestedCopyPreservesAncestorFixturesAndNaming(t *testing.T) {
	python := feedbackPython(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.CommandContext(t.Context(), "git", "init", "-q").Run())
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\npython_functions = check_*\npythonpath = .\n")
	requireWriteFile(t, filepath.Join(root, "conftest.py"), "import pytest\n@pytest.fixture\ndef ancestor():\n    return 2\n")
	requireWriteFile(t, filepath.Join(root, "app.py"), "def add(a,b):\n    return a+b\n")
	nested := filepath.Join(root, "tests")
	require.NoError(t, os.MkdirAll(nested, 0755))
	requireWriteFile(t, filepath.Join(nested, "test_app.py"), "from app import add\ndef check_app(ancestor):\n    assert add(ancestor, 1) == 3\n")
	t.Chdir(nested)
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.com/ddtest-python-fixture.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "0123456789012345678901234567890123456789")
	t.Setenv("DD_GIT_BRANCH", "main")
	t.Setenv("DD_GIT_COMMIT_MESSAGE", "pytest fixture")
	var reporting *intake.Server
	drive := &Testdrive{repositoryRoot: nested, language: "python", framework: framework.NewPytest(platform.NewPython()), command: python, args: []string{"-m", "pytest"}, executor: &ext.DefaultCommandExecutor{}, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
		server, err := intake.StartScenario(dir, scenario)
		if filepath.Base(dir) == "reporting-only" {
			reporting = server
		}
		return server, err
	}}
	drive.preflight = drive.frameworkPreflight()
	var output bytes.Buffer
	result, err := drive.runPreparedConfiguration(t.Context(), &output, &configurationResult{TestCommand: onboard.TestCommand{Directory: "."}})
	require.NoError(t, err, output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, output.String())
	require.Len(t, result.Features, 6)
	for _, feature := range result.Features {
		require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
	}
	facts, err := reporting.Facts()
	require.NoError(t, err)
	require.Equal(t, "observed", result.Runs[1].Coverage.Status, "%+v", facts.Tests)
	probes, err := filepath.Glob(filepath.Join(nested, "ddtest-pytest-*"))
	require.NoError(t, err)
	require.Empty(t, probes)
}

func TestPytestNestedCheckKeepsCheckoutCIFindings(t *testing.T) {
	python := feedbackPython(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.CommandContext(t.Context(), "git", "init", "-q").Run())
	requireWriteFile(t, "pytest.ini", "[pytest]\n")
	require.NoError(t, os.MkdirAll(".github/workflows", 0755))
	requireWriteFile(t, ".github/workflows/test.yml", "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.8'}\n      - run: python -m pytest\n        working-directory: packages/api\n")
	require.NoError(t, os.MkdirAll("packages/api", 0755))
	drive := &Testdrive{repositoryRoot: root, workflowRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: python, args: []string{"-m", "pytest"}, checkOnly: true}
	drive.preflight = drive.frameworkPreflight()
	result, err := drive.runPreparedConfiguration(t.Context(), io.Discard, &configurationResult{TestCommand: onboard.TestCommand{Directory: "packages/api"}})
	require.NoError(t, err)
	require.NotNil(t, result.CIRuntime)
	require.NotEqual(t, "not applicable", result.CIRuntime.Status)
	require.NotEqual(t, "compatible", result.CIRuntime.Status)
	require.Len(t, result.CIRuntime.Jobs, 1)
}
