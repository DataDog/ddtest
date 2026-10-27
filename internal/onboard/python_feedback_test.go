package onboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPythonFeedbackRejectsInterpreterWrappers(t *testing.T) {
	for _, command := range []string{"python-wrapper -m pytest", "uv run python-wrapper -m pytest", ".venv/bin/python-helper -m pytest"} {
		t.Run(command, func(t *testing.T) {
			root := pythonReviewWorkflow(t, "jobs:\n  tests:\n    steps:\n      - run: "+command+"\n")
			scope, err := DiscoverValidationScope(root, "pytest")
			require.NoError(t, err)
			require.Empty(t, scope.Commands)
			require.Contains(t, strings.Join(scope.Unresolved, "\n"), "Unresolved Python entry point")
			check := pythonReviewRuntime(t, root)
			require.Equal(t, "inconclusive", check.Status)
			require.Len(t, check.Jobs, 1)
		})
	}
}

func TestPythonFeedbackUVEnvironmentSelectsRuntime(t *testing.T) {
	for _, tc := range []struct{ name, workflowEnv, jobEnv, stepEnv, command, status string }{
		{name: "workflow selector", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "job selector", jobEnv: "    env: {UV_PYTHON: '3.8'}\n", command: "uv run pytest", status: "inconclusive"},
		{name: "step selector", stepEnv: "        env: {UV_PYTHON: '3.8'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "cleared selector", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", stepEnv: "        env: {UV_PYTHON: ''}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "direct Python unaffected", workflowEnv: "env: {UV_PYTHON: '3.8'}\n", command: "python -m pytest", status: "compatible"},
		{name: "workflow project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "job project environment", jobEnv: "    env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run pytest", status: "inconclusive"},
		{name: "step project environment", stepEnv: "        env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "cleared project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", stepEnv: "        env: {UV_PROJECT_ENVIRONMENT: ''}\n", command: "uv run python -m pytest", status: "inconclusive"},
		{name: "direct Python ignores project environment", workflowEnv: "env: {UV_PROJECT_ENVIRONMENT: '.venv38'}\n", command: "python -m pytest", status: "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pythonReviewWorkflow(t, fmt.Sprintf("%sjobs:\n  tests:\n%s    steps:\n      - uses: actions/setup-python@v6\n        with: {python-version: '3.12'}\n      - uses: datadog/test-visibility-github-action@v3\n        with: {languages: python, python-tracer-version: '4.16.0'}\n      - run: %s\n%s", tc.workflowEnv, tc.jobEnv, tc.command, tc.stepEnv))
			check := pythonReviewRuntime(t, root)
			require.Equal(t, tc.status, check.Status, "%+v", check)
			require.Len(t, check.Jobs, 1)
			if tc.status == "inconclusive" {
				require.Contains(t, check.Jobs[0].Reason, "selects its own Python interpreter")
			}
		})
	}
}

func TestPythonFeedbackObjectCommandMatrixStaysUnresolved(t *testing.T) {
	for _, command := range []string{"python -m pytest", "python3.12 -m pytest", "uv run python -m pytest"} {
		root := pythonReviewWorkflow(t, fmt.Sprintf(`jobs:
  tests:
    strategy:
      matrix:
        config:
          - {python: '3.12', command: '%s'}
    steps:
      - run: ${{ matrix.config.command }}
  docs:
    strategy:
      matrix:
        config:
          - {label: pytest, command: 'mkdocs build'}
    steps:
      - run: ${{ matrix.config.command }}
`, command))
		scope, err := DiscoverValidationScope(root, "pytest")
		require.NoError(t, err)
		require.Empty(t, scope.Commands)
		require.Len(t, scope.Unresolved, 1)
		require.Contains(t, scope.Unresolved[0], " / tests:")
		require.Contains(t, scope.Unresolved[0], "non-scalar value")
		check := pythonReviewRuntime(t, root)
		require.Equal(t, "inconclusive", check.Status)
		require.Len(t, check.Jobs, 1)
		require.Equal(t, "tests", check.Jobs[0].Job)
		_, err = CIMatrixRows(root, "pytest")
		require.ErrorContains(t, err, "non-scalar value")
	}
}
