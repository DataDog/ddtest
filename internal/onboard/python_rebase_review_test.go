package onboard

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonInlineAddoptsInstrumentation(t *testing.T) {
	for _, tc := range []struct{ command, status, reason string }{
		{`python -m pytest -o addopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest -oaddopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest -o=addopts=--no-ddtrace`, "incompatible", "disables"},
		{`pytest --override-ini addopts=--no-ddtrace`, "incompatible", "disables"},
		{`uv run -- python -m pytest --override-ini=addopts=--no-ddtrace`, "inconclusive", "selects its own Python interpreter"},
		{`pytest -o 'addopts=-p no:ddtrace'`, "incompatible", "block"},
		{`pytest -o addopts=--disable-plugin-autoload`, "incompatible", "autoload"},
		{`pytest -o 'addopts=--disable-plugin-autoload -p ddtrace'`, "compatible", "satisfies"},
		{`pytest -o addopts=--no-ddtrace --override-ini=addopts=`, "compatible", "satisfies"},
		{`pytest -o addopts= --override-ini=addopts=--no-ddtrace`, "incompatible", "disables"},
		{`tox -e py312 -- -o addopts=--no-ddtrace`, "incompatible", "disables"},
		{`tox -e py312 -- --override-ini 'addopts=-p no:ddtrace'`, "incompatible", "block"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf("jobs:\n  tests:\n    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n", tc.command))
			writeToxProject(t, root, `pass_env = ["DD_*", "PYTHONPATH", "PYTEST_ADDOPTS"]`)
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			require.Contains(t, check.Jobs[0].Reason, tc.reason)
		})
	}
}

func TestPythonScopeIgnoresUnrelatedNodeVersion(t *testing.T) {
	root := pythonReviewWorkflow(t, `jobs:
  tests:
    steps:
      - uses: actions/setup-node@v6
        with: {node-version: '16'}
      - uses: actions/setup-python@v6
        with: {python-version: '3.12'}
      - run: python -m pytest
`)
	scope, err := DiscoverValidationScope(root, "pytest")
	require.NoError(t, err)
	require.Len(t, scope.Commands, 1)
	require.False(t, scope.Commands[0].CIInstrumentationExcluded)
	require.False(t, scope.Commands[0].UnsupportedNode)
}
