package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonReleaseReadsOnlyPackageInfo(t *testing.T) {
	metadata, err := readPythonRelease(strings.NewReader(`{"info":{"version":"4.16.0","requires_python":">=3.9"},"releases":`))
	require.NoError(t, err)
	require.Equal(t, "4.16.0", metadata.Version)
	for _, text := range []string{`{}`, `{"info":{}}`, `{"info":null}`, `[]`, `{"info":`} {
		_, err := readPythonRelease(strings.NewReader(text))
		require.Error(t, err)
	}
}

func TestPythonRuntimeRequirement(t *testing.T) {
	for _, tc := range []struct{ python, requirement, status string }{
		{"3.12", "<3.15,>=3.9", "compatible"},
		{"3.8", ">=3.9", "incompatible"},
		{"3.15", ">=3.9,<3.15", "incompatible"},
		{"3", ">=3.9", "inconclusive"},
		{"pypy3.10", ">=3.9", "inconclusive"},
		{"3.12", ">=3.9,!=3.12.*", "inconclusive"},
	} {
		status, _ := ComparePythonRequirement(tc.python, tc.requirement)
		require.Equal(t, tc.status, status, "%s / %s", tc.python, tc.requirement)
	}
}

func TestPythonInvocationStopsAtTheExecutionTarget(t *testing.T) {
	for _, tc := range []struct {
		words []string
		match bool
	}{
		{[]string{"python", "-m", "pytest", "tests"}, true},
		{[]string{"python3.12", "-I", "-W", "error", "-Xdev", "-m", "pytest"}, true},
		{[]string{"python", "--check-hash-based-pycs", "always", "-m", "pytest"}, true},
		{[]string{"python", "wrapper.py", "-m", "pytest"}, false},
		{[]string{"python", "-c", "print('wrapper')", "-m", "pytest"}, false},
		{[]string{"python", "-", "-m", "pytest"}, false},
		{[]string{"python", "--", "-m", "pytest"}, false},
		{[]string{"python", "-W", "-m", "pytest"}, false},
		{[]string{"python", "-X"}, false},
		{[]string{"python", "--version", "-m", "pytest"}, false},
		{[]string{"uv", "run", "--locked", "python", "wrapper.py", "-m", "pytest"}, false},
	} {
		require.Equal(t, tc.match, pythonInvocation(tc.words), "%v", tc.words)
	}
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    steps:
      - run: python -m pytest
      - run: python wrapper.py -m pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Unresolved Python entry point: python wrapper.py -m pytest")
	check := pythonReviewRuntime(t, root)
	require.Len(t, check.Jobs, 2)
	require.Equal(t, "inconclusive", check.Jobs[1].Status)
}

func TestPythonScopeRetainsCommandsAndUnresolvedWrappers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(`jobs:
  test:
    strategy:
      matrix:
        python: ['3.11', '3.12']
    steps:
      - run: python -m pip install -r requirements.txt
      - run: python -m pytest tests -q --cov=app
      - run: uv run --no-sync --no-default-groups --group test python -m pytest -c integration.ini
      - run: make custom-tests
`), 0644))
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 2)
	require.Equal(t, "python -m pytest tests -q --cov=app", scope.Commands[0].Command)
	require.Len(t, scope.Commands[0].Locations, 2)
	require.Contains(t, scope.Unresolved[0], "make custom-tests")
	require.NotEmpty(t, scope.Review)
}

func TestPythonCIReleaseAndMatrixValidation(t *testing.T) {
	for _, tc := range []struct{ name, python, version, options, status string }{
		{"compatible", "3.12", "4.16.0", "--ddtrace -q", "compatible"},
		{"old runtime", "3.8", "4.16.0", "--ddtrace", "incompatible"},
		{"different tracer", "3.12", "4.15.0", "--ddtrace", "incompatible"},
		{"bootstrap overwritten", "3.12", "4.16.0", "-q", "incompatible"},
		{"runtime alias", "3.x", "4.16.0", "--ddtrace", "inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
			contents := "jobs:\n  test:\n    strategy:\n      matrix:\n        python: ['" + tc.python + "']\n    steps:\n      - uses: actions/setup-python@v6\n        with:\n          python-version: ${{ matrix.python }}\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: python\n          python-tracer-version: '" + tc.version + "'\n      - run: python -m pytest\n        env:\n          PYTEST_ADDOPTS: '" + tc.options + "'\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(contents), 0644))
			check := checkPythonCIRuntimes(t.Context(), root, "4.16.0", func(_ context.Context, version string) (PythonRelease, error) {
				return PythonRelease{version, ">=3.9,<3.15"}, nil
			}, func(context.Context, string) (string, error) {
				t.Fatal("explicit version must not resolve action default")
				return "", nil
			})
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Equal(t, tc.python, check.Jobs[0].Python)
		})
	}
}

func TestPytestInstructionsPreserveEnvironmentAndEvidence(t *testing.T) {
	text := instructions("python", "pytest")
	for _, expected := range []string{"languages: python", "PYTHONPATH", "PYTEST_ADDOPTS", "tox/nox", "pass_env", "DD_*", "--check-only", "--all", "--command", "report.html", "testdrive.json", "historical evidence", "no Datadog credentials"} {
		require.Contains(t, text, expected)
	}
	require.NotContains(t, text, "NODE_OPTIONS")
}

func pythonReviewWorkflow(t *testing.T, contents string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(contents), 0644))
	return root
}

func pythonReviewRuntime(t *testing.T, root string) RuntimeCheck {
	t.Helper()
	return checkPythonCIRuntimes(t.Context(), root, "4.16.0", func(_ context.Context, version string) (PythonRelease, error) {
		return PythonRelease{version, ">=3.9,<3.15"}, nil
	}, func(context.Context, string) (string, error) {
		t.Fatal("explicit version must not resolve action default")
		return "", nil
	})
}

func TestPythonReviewExpandsCommandMatrixBeforeFiltering(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    strategy:
      matrix:
        command: ['python -m pytest tests', 'python3.12 -m pytest integration']
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: ${{ matrix.command }}
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	check := pythonReviewRuntime(t, root)
	require.Equal(t, "compatible", check.Status, "%+v", check)
	require.Len(t, check.Jobs, 2)
}

func TestPythonReviewRetainsApplicationEnvironment(t *testing.T) {
	root := pythonReviewWorkflow(t, `env:
  DD_ENV: ci
  DD_SERVICE: application
  DD_APPLICATION_SETTING: '${{ matrix.mode }}'
  DD_GIT_BRANCH: fixture
  DD_API_KEY: '${{ secrets.DD_API_KEY }}'
  DD_TRACE_AGENT_URL: '${{ env.ACTION_AGENT_URL }}'
jobs:
  tests:
    strategy:
      matrix: {mode: [one, two]}
    steps:
      - run: python -m pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Empty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 2)
	for index, command := range scope.Commands {
		require.Equal(t, "ci", command.Environment["DD_ENV"])
		require.Equal(t, "application", command.Environment["DD_SERVICE"])
		require.Equal(t, []string{"one", "two"}[index], command.Environment["DD_APPLICATION_SETTING"])
		require.Equal(t, "fixture", command.Environment["DD_GIT_BRANCH"])
		require.NotContains(t, command.Environment, "DD_API_KEY")
		require.NotContains(t, command.Environment, "DD_TRACE_AGENT_URL")
	}
}

func TestPythonReviewEffectiveInstrumentationOptions(t *testing.T) {
	for _, tc := range []struct{ name, parent, stepEnv, command, status string }{
		{"step overrides parent", "  PYTEST_ADDOPTS: '-q'\n", "          PYTEST_ADDOPTS: '--ddtrace -q'\n", "python -m pytest", "compatible"},
		{"action options extended", "", "          PYTEST_ADDOPTS: '${{ env.PYTEST_ADDOPTS }} -q'\n", "python -m pytest", "compatible"},
		{"environment disables tracing", "", "          PYTEST_ADDOPTS: '--ddtrace --no-ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload disabled without plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload disabled explicit command plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest -p ddtrace", "compatible"},
		{"autoload disabled explicit environment plugin", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace -p ddtrace'\n", "python -m pytest", "compatible"},
		{"plugin explicitly blocked", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_ADDOPTS: '--ddtrace -p no:ddtrace'\n", "python -m pytest", "incompatible"},
		{"autoload restored by step", "  PYTEST_DISABLE_PLUGIN_AUTOLOAD: '1'\n", "          PYTEST_DISABLE_PLUGIN_AUTOLOAD: ''\n          PYTEST_ADDOPTS: '--ddtrace'\n", "python -m pytest", "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf("env:\n%sjobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n        env:\n%s", tc.parent, tc.command, tc.stepEnv))
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
		})
	}
}

func TestPythonReviewMatrixPreservationExcludesUnrelatedJobs(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  docs:
    if: github.event_name == 'release'
    strategy:
      matrix: {config: [{target: web}]}
    steps:
      - run: make docs
  tests:
    strategy:
      matrix: {python: ['3.11', '3.12']}
    steps:
      - run: python -m pytest
`)
	rows, err := CIMatrixRows(root, "pytest")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Contains(t, row, " / tests / ")
	}
}
