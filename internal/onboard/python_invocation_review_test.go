package onboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonCIInstrumentationIsCheckedPerInvocation(t *testing.T) {
	for _, tc := range []struct {
		name, command, toxCommands, autoload, addopts, status string
	}{
		{"direct missing second plugin", "pytest -p ddtrace tests/unit\npytest tests/integration", "", "1", "--ddtrace", "incompatible"},
		{"direct missing first plugin", "pytest tests/unit\npytest -p ddtrace tests/integration", "", "1", "--ddtrace", "incompatible"},
		{"direct both load plugin", "pytest -p ddtrace tests/unit\npytest -p ddtrace tests/integration", "", "1", "--ddtrace", "compatible"},
		{"command disables first autoload", "pytest --disable-plugin-autoload\npytest -p ddtrace", "", "", "--ddtrace", "incompatible"},
		{"command disables second autoload", "pytest -p ddtrace\npytest --disable-plugin-autoload", "", "", "--ddtrace", "incompatible"},
		{"autoload flag stays in its process", "pytest --disable-plugin-autoload -p ddtrace\npytest", "", "", "--ddtrace", "compatible"},
		{"tracing flag stays in its process", "pytest --ddtrace -p ddtrace\npytest -p ddtrace", "", "1", "", "incompatible"},
		{"tox missing second plugin", "tox -e py312", `[["pytest", "-p", "ddtrace"], ["pytest"]]`, "1", "--ddtrace", "incompatible"},
		{"tox missing first plugin", "tox -e py312", `[["pytest"], ["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "incompatible"},
		{"tox both load plugin", "tox -e py312", `[["pytest", "-p", "ddtrace"], ["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "compatible"},
		{"direct then tox", "pytest -p ddtrace\ntox -e py312", `[["pytest"]]`, "1", "--ddtrace", "incompatible"},
		{"tox then direct", "tox -e py312\npytest", `[["pytest", "-p", "ddtrace"]]`, "1", "--ddtrace", "incompatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    env: {PYTEST_DISABLE_PLUGIN_AUTOLOAD: '%s', PYTEST_ADDOPTS: '%s'}
    steps:
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: python, python-tracer-version: '4.16.0'}
      - run: |
          %s
`, tc.autoload, tc.addopts, strings.ReplaceAll(tc.command, "\n", "\n          ")))
			if tc.toxCommands != "" {
				config := "[tool.tox.env_run_base]\npass_env = ['DD_*', 'PYTHONPATH', 'PYTEST_ADDOPTS']\ncommands = " + tc.toxCommands + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte(config), 0644))
			}
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			if tc.status == "incompatible" {
				reason := "autoload is disabled"
				if tc.addopts == "" {
					reason = "preserve --ddtrace"
				}
				require.Contains(t, check.Jobs[0].Reason, reason)
			}
		})
	}
}

func TestPythonCIVersionedExecutableAliases(t *testing.T) {
	for _, tc := range []struct{ python, executable, status string }{
		{"3.12.8", "python", "compatible"},
		{"3.12.8", "python3", "compatible"},
		{"3.12.8", "python3.12", "compatible"},
		{"3.12.8", "python3.12.exe", "compatible"},
		{"3.12.8", "python3.12.8", "compatible"},
		{"3.12.8", "python3.12.9", "inconclusive"},
		{"3.12.8", "python3.1", "inconclusive"},
		{"3.12.8", "python3.11", "inconclusive"},
		{"3.12", "python3.12", "compatible"},
		{"3.12", "python3.12.8", "inconclusive"},
		{"3.12.8", ".venv/bin/python3.12", "inconclusive"},
	} {
		t.Run(tc.python+"/"+tc.executable, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '%s'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s -m pytest\n", tc.python, tc.executable))
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
		})
	}
}
