package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonCIReadsEffectiveConfigAddopts(t *testing.T) {
	for _, tc := range []struct {
		name, command, directory, status string
		files                            map[string]string
	}{
		{"ini plugin blocker", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"ini tracer disabled", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts = --no-ddtrace\n"}},
		{"multiline options", "pytest", ".", "incompatible", map[string]string{"pytest.ini": "[pytest]\naddopts =\n    -q\n    -p no:ddtrace\n"}},
		{"toml options", "pytest", ".", "incompatible", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = '-p no:ddtrace'\n"}},
		{"toml array", "pytest", ".", "incompatible", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\naddopts = ['-p', 'no:ddtrace']\n"}},
		{"tox ini", "pytest", ".", "incompatible", map[string]string{"tox.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"setup cfg", "pytest", ".", "incompatible", map[string]string{"setup.cfg": "[tool:pytest]\naddopts = -p no:ddtrace\n"}},
		{"ordinary filters", "pytest -k test_add", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -q\n"}},
		{"empty ini wins", "pytest", ".", "compatible", map[string]string{"pytest.ini": "", "setup.cfg": "[tool:pytest]\naddopts = -p no:ddtrace\n"}},
		{"working directory", "pytest", "package", "incompatible", map[string]string{"package/pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"explicit config", "pytest -c ci.ini", ".", "incompatible", map[string]string{"ci.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"explicit toml config", "pytest -c ci.toml", ".", "incompatible", map[string]string{"ci.toml": "[tool.pytest.ini_options]\naddopts = '-p no:ddtrace'\n"}},
		{"explicit native toml config", "pytest -c ci.toml", ".", "inconclusive", map[string]string{"ci.toml": "[pytest]\naddopts = ['-p', 'no:ddtrace']\n"}},
		{"explicit config overrides discovery", "pytest --config-file=ci.ini", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n", "ci.ini": "[pytest]\naddopts = -q\n"}},
		{"override clears file options", "pytest -o addopts=", ".", "compatible", map[string]string{"pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"missing config", "pytest -c missing.ini", ".", "inconclusive", nil},
		{"malformed toml", "pytest", ".", "inconclusive", map[string]string{"pyproject.toml": "[broken\n"}},
		{"duplicate options", "pytest", ".", "inconclusive", map[string]string{"pytest.ini": "[pytest]\naddopts = -q\naddopts = -p no:ddtrace\n"}},
		{"nested config selection", "pytest tests", ".", "inconclusive", map[string]string{"pytest.ini": "[pytest]\n", "tests/pytest.ini": "[pytest]\naddopts = -p no:ddtrace\n"}},
		{"version dependent config", "pytest", ".", "inconclusive", map[string]string{"pytest.toml": "[pytest]\naddopts = ['-p', 'no:ddtrace']\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n        working-directory: %s\n", tc.command, tc.directory))
			for name, body := range tc.files {
				path := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
				require.NoError(t, os.WriteFile(path, []byte(body), 0644))
			}
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
		})
	}
}

func TestPythonCIIgnoresReadOnlySteps(t *testing.T) {
	for _, command := range []string{"python -m pytest", "echo before\npython -m pytest", "python -m pytest\necho after"} {
		root := pythonReviewWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    steps:
      - run: echo preparing
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          %s
      - run: printf 'done\n'
`, strings.ReplaceAll(command, "\n", "\n          ")))
		check := pythonReviewRuntime(t, root)
		require.Equal(t, "compatible", check.Status, "%+v", check)
		require.Len(t, check.Jobs, 1)
	}
}

func TestPythonCIWrapperEnvironmentIsNotEstablishedBySetupPython(t *testing.T) {
	for _, command := range []string{"uv run --no-sync python -m pytest", "uv run pytest", "poetry run pytest"} {
		root := pythonReviewWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n", command))
		check := pythonReviewRuntime(t, root)
		require.Equal(t, "inconclusive", check.Status, "%s: %+v", command, check)
		require.Contains(t, check.Jobs[0].Reason, "selects its own Python interpreter")
	}
}

func TestPytestConfigSelectionStaysInsideRepository(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	require.NoError(t, os.Mkdir(root, 0755))
	outside := filepath.Join(parent, "outside.ini")
	require.NoError(t, os.WriteFile(outside, []byte("[pytest]\naddopts = -p no:ddtrace\n"), 0644))
	_, err := PytestConfigOptions(root, root, "", []string{"pytest", "-c", "../outside.ini"})
	require.ErrorContains(t, err, "leaves the repository")
	if err := os.Symlink(outside, filepath.Join(root, "pytest.ini")); err != nil {
		t.Skip("symlinks unavailable")
	}
	_, err = PytestConfigOptions(root, root, "", []string{"pytest"})
	require.ErrorContains(t, err, "leaves the repository")
}

func TestPythonCIConfigSelectionUsesCommandOverEnvironment(t *testing.T) {
	for _, tc := range []struct{ config, status string }{{"good.ini", "compatible"}, {"bad.ini", "incompatible"}} {
		root := pythonReviewWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: pytest -c %s
        env: {PYTEST_ADDOPTS: '--ddtrace -c good.ini'}
`, tc.config))
		require.NoError(t, os.WriteFile(filepath.Join(root, "good.ini"), []byte("[pytest]\naddopts = -q\n"), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "bad.ini"), []byte("[pytest]\naddopts = -p no:ddtrace\n"), 0644))
		check := pythonReviewRuntime(t, root)
		require.Equal(t, tc.status, check.Status, "%+v", check)
	}
}

func TestPythonCILoggingDoesNotHideUnknownCommands(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          python -m pytest
          echo done
          custom-test-wrapper
`)
	check := pythonReviewRuntime(t, root)
	require.Equal(t, "inconclusive", check.Status, "%+v", check)
	require.Contains(t, check.Jobs[0].Reason, "Unresolved Python entry point")
}
