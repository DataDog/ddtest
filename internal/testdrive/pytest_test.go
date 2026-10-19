package testdrive

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPytestKeepsWrapperSeparatorsAndCIOptions(t *testing.T) {
	t.Setenv("PYTEST_ADDOPTS", "-k ignored")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
	session := planSession()
	require.NoError(t, session.create())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	executor := &fakeTestdriveExecutor{}
	drive := &Testdrive{repositoryRoot: t.TempDir(), language: "python", command: "uv", args: []string{"run", "--", "python", "-m", "pytest", "--", "test_app.py"}, executor: executor, pytestEnvironment: map[string]string{"PYTEST_ADDOPTS": "--ddtrace -q --cov=app", "PYTHONPATH": "custom-packages", "APP_MODE": "ci"}, startIntake: func(string, intake.Scenario) (localIntake, error) { return &fakeIntake{url: "http://127.0.0.1:1"}, nil }}
	_, err := drive.runPytest(t.Context(), io.Discard, session, "", "baseline", false, intake.Scenario{}, "", "")
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--no-sync", "--", "python", "-m", "pytest", "--no-ddtrace", "--", "test_app.py"}, executor.args)
	require.Contains(t, executor.env["PYTEST_ADDOPTS"], "-q --cov=app")
	require.NotContains(t, executor.env["PYTEST_ADDOPTS"], "ignored")
	require.Contains(t, executor.env["PYTEST_ADDOPTS"], "--no-ddtrace")
	require.Contains(t, executor.env["PYTHONPATH"], "custom-packages")
	require.Equal(t, "ci", executor.env["APP_MODE"])
	require.Equal(t, "false", executor.env["DD_TRACE_ENABLED"])
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
	require.Equal(t, `-v -k 'some test' --cov=app`, options)
	_, err = stripPytestInstrumentation(`-k 'unfinished`)
	require.Error(t, err)
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
	session := planSession()
	require.NoError(t, session.create())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: python, args: []string{"-m", "pytest"}, executor: git, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
		return intake.StartScenario(dir, scenario)
	}}
	result := validationResult{Framework: "pytest"}
	var output bytes.Buffer
	require.NoError(t, drive.runFrameworkValidation(t.Context(), &output, session, "", &result), output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, "%+v", result.Compatibility)
	for _, feature := range result.Features {
		if parameters && feature.Name == "early-flake-detection" {
			require.Contains(t, []string{"passed", "failed"}, feature.Status, feature.Reason)
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
