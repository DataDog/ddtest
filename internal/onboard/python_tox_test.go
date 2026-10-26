package onboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const toxProject = `[tool.tox]
env_list = ["py3.12", "typing"]
[tool.tox.env_run_base]
commands = [["pytest", "-v", "--basetemp={env_tmp_dir}", {replace="posargs", default=[], extend=true}]]
%s
[tool.tox.env.typing]
commands = [["mypy", "src"], ["pyright"]]
`

func writeToxProject(t *testing.T, root, settings string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(strings.Replace(toxProject, "%s", settings, 1)), 0644))
}

func TestPythonToxDiscoversItsDangerousMatrix(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    runs-on: ${{ matrix.os || 'ubuntu-latest' }}
    strategy:
      matrix:
        include:
          - {python: '3.13'}
          - {python: '3.13', os: windows-latest}
          - {python: '3.13', os: macos-latest}
          - {python: '3.12'}
          - {python: '3.11'}
          - {python: '3.10'}
          - {python: 'pypy-3.11', tox: pypy311}
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '${{ matrix.python }}'}
      - run: uv run --locked tox run -e ${{ matrix.tox || format('py{0}', matrix.python) }}
  typing:
    steps:
      - run: uv run --locked tox run -e typing
`)
	writeToxProject(t, root, "")
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.NotEmpty(t, scope.Unresolved)
	require.Len(t, scope.Commands, 5)
	locations := 0
	for _, command := range scope.Commands {
		require.Contains(t, strings.Join(command.UnvalidatedSetup, "\n"), "Local tox execution is not supported")
		require.Contains(t, command.Command, "uv run --locked tox run -e ")
		require.NotContains(t, command.Command, "${{")
		locations += len(command.Locations)
	}
	require.Equal(t, 7, locations)
	check := pythonReviewRuntime(t, root)
	require.Len(t, check.Jobs, 7)
	for _, finding := range check.Jobs {
		require.Equal(t, "missing_instrumentation", finding.Code, "%+v", finding)
	}
	for _, command := range []string{"python -m pytest && tox -e typing", "tox -e typing && python -m pytest"} {
		resolution := resolvePythonStep(root, ciWorkflow{}, runtimeJob{}, runtimeStep{Run: command})
		require.True(t, resolution.Matched)
		require.Len(t, resolution.Tests, 1)
		require.Equal(t, "python -m pytest", resolution.Tests[0].Command)
	}
}

func TestPythonToxChecksEffectiveInstrumentation(t *testing.T) {
	const forwarded = `pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]`
	for _, tc := range []struct{ name, settings, command, status, reason string }{
		{"not forwarded", "", "tox run -e py3.12", "incompatible", "filters"},
		{"forwarded", forwarded, "tox run -e py3.12", "compatible", "satisfies"},
		{"partial forwarding", `pass_env = ["DD_*", "PYTHONPATH"]`, "tox run -e py3.12", "incompatible", "PYTEST_ADDOPTS"},
		{"overridden environment", forwarded + "\nset_env = { PYTEST_ADDOPTS = '--no-ddtrace' }", "tox run -e py3.12", "inconclusive", "set_env"},
		{"excluded variable", forwarded + "\ndisallow_pass_env = ['PYTHONPATH']", "tox run -e py3.12", "inconclusive", "disallow_pass_env"},
		{"posargs disable tracer", forwarded, "uv run --locked tox run -e py3.12 -- --no-ddtrace", "incompatible", "disables"},
		{"other interpreter", forwarded, "tox -e py311", "inconclusive", "tox selects Python 3.11"},
		{"pypy", forwarded, "tox -e pypy311", "inconclusive", "PyPy"},
		{"custom interpreter", forwarded, "tox -e custom", "inconclusive", "Cannot identify"},
		{"config override", forwarded, "tox -c other.ini -e py312", "inconclusive", "unsupported tox option"},
		{"environment group", forwarded, "tox -e py311,py312", "inconclusive", "one literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, "jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: "+tc.command+"\n")
			writeToxProject(t, root, tc.settings)
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
}

func TestPythonToxUnknownConfigurationStaysUnresolved(t *testing.T) {
	for _, config := range []string{
		`[tool.tox]`,
		`[tool.tox.env_run_base]
commands = [["pytest", {replace="env", name="OPTIONS"}]]`,
		`[tool.tox.env_run_base]
commands = [["pytest"]]
base = ["custom"]`,
		`[tool.tox.env_run_base]
commands = [["make", "test"]]`,
	} {
		root := pythonReviewWorkflow(t, "jobs:\n  tests:\n    steps:\n      - run: tox -e py312\n")
		require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(config), 0644))
		_, err := readPythonTox(root, []string{"-e", "py312"})
		require.Error(t, err)
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.NotEmpty(t, scope.Unresolved)
		check := pythonReviewRuntime(t, root)
		require.Equal(t, "inconclusive", check.Status)
	}
	root := t.TempDir()
	writeToxProject(t, root, "")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tox.ini"), []byte("[testenv]\ncommands=pytest --no-ddtrace\n"), 0644))
	_, err := readPythonTox(root, []string{"-e", "py312"})
	require.ErrorContains(t, err, "tox.ini")
}

func TestPythonToxEnvironmentOverridesBaseForwarding(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: tox -e py312
`)
	writeToxProject(t, root, `pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]`)
	file, err := os.OpenFile(filepath.Join(root, "pyproject.toml"), os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, err = file.WriteString("\n[tool.tox.env.py312]\npass_env = ['PYTHONPATH', 'PYTEST_ADDOPTS']\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	check := pythonReviewRuntime(t, root)
	require.Equal(t, "incompatible", check.Status)
	require.Contains(t, check.Jobs[0].Reason, "DD_*")
}

func TestPythonToxChecksConfigurationAfterCD(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: cd sub && tox -e py312
`)
	writeToxProject(t, root, `pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]`)
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0755))
	writeToxProject(t, filepath.Join(root, "sub"), "")
	check := pythonReviewRuntime(t, root)
	require.Equal(t, "incompatible", check.Status)
	require.Contains(t, check.Jobs[0].Reason, "filters")
}

func TestMatrixExpressionInterpolationRemainsBounded(t *testing.T) {
	for _, tc := range []struct{ expression, expected string }{
		{`${{ matrix.tox || format('py{0}', matrix.python) }}`, "py3.12"},
		{`${{ format('{{{0}}}', matrix.python) }}`, ""}, // Braces require shell review.
		{`${{ secrets.COMMAND }}`, ""},
		{`${{ matrix.unsafe || 'pytest' }}`, ""},
		{`${{ matrix.python }}-${{ matrix.python }}`, "3.12-3.12"},
	} {
		actual, err := interpolateMatrix(tc.expression, map[string]any{"python": "3.12", "unsafe": "x; echo bad"})
		if tc.expected == "" {
			require.Error(t, err, tc.expression)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.expected, actual)
		}
	}
	actual, err := interpolateMatrix(`${{ matrix.tox || format('py{0}', matrix.python) }}`, map[string]any{"python": "pypy-3.11", "tox": "pypy311"})
	require.NoError(t, err)
	require.Equal(t, "pypy311", actual)
}
