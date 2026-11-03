package onboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func pythonWorkflow(t *testing.T, contents string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".github/workflows/test.yml"), []byte(contents), 0644))
	return root
}

func pythonRuntimeCheck(t *testing.T, root string) RuntimeCheck {
	t.Helper()
	return checkPythonCIRuntimes(t.Context(), root, "4.16.0", "", func(_ context.Context, version string) (PythonRelease, error) {
		return PythonRelease{version, ">=3.9,<3.15"}, nil
	}, func(context.Context, string) (string, error) {
		t.Fatal("explicit version must not resolve action default")
		return "", nil
	})
}

func TestPythonRetainsApplicationEnvironment(t *testing.T) {
	root := pythonWorkflow(t, `env:
  DD_ENV: ci
  DD_SERVICE: application
  DD_CIVISIBILITY_FLAKY_RETRY_COUNT: '${{ matrix.mode }}'
  DD_GIT_BRANCH: fixture
  DD_API_KEY: '${{ secrets.DD_API_KEY }}'
  DD_TRACE_AGENT_URL: '${{ env.ACTION_AGENT_URL }}'
jobs:
  tests:
    strategy:
      matrix: {mode: [1, 2]}
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
		require.Equal(t, []string{"1", "2"}[index], command.Environment["DD_CIVISIBILITY_FLAKY_RETRY_COUNT"])
		require.Equal(t, "fixture", command.Environment["DD_GIT_BRANCH"])
		require.NotContains(t, command.Environment, "DD_API_KEY")
		require.NotContains(t, command.Environment, "DD_TRACE_AGENT_URL")
	}
}

func TestPythonMatrixPreservationExcludesUnrelatedJobs(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
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

func TestPythonAddoptsPreservesParentOptionsLocallyAndInCIChecks(t *testing.T) {
	workflow := ciWorkflow{Env: map[string]string{"PYTEST_ADDOPTS": "-k selected"}}
	job := runtimeJob{Env: map[string]string{"PYTEST_ADDOPTS": "${{env.PYTEST_ADDOPTS}} --cov=app"}}
	step := runtimeStep{Run: "python -m pytest", Env: map[string]string{"PYTEST_ADDOPTS": "${{ env.PYTEST_ADDOPTS }} -p ddtrace -q"}}
	value, err := pythonAddopts(workflow, job, step, nil)
	require.NoError(t, err)
	require.Equal(t, "--ddtrace -k selected --cov=app -p ddtrace -q", value)
	_, environment, err := resolveVariantStep(workflow, job, step, nil, "pytest")
	require.NoError(t, err)
	require.Equal(t, value, environment["PYTEST_ADDOPTS"])
}

func TestPythonScopeIgnoresUnrelatedNodeVersion(t *testing.T) {
	root := pythonWorkflow(t, `jobs:
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

// Outside GitHub Actions a pytest job without the provider's setup is not
// instrumented, as without the action.
func TestPythonStaticChecksNameTheProviderSetup(t *testing.T) {
	useCIProvider(t, "gitlab")
	root := t.TempDir()
	writeFile(t, root, ".gitlab-ci.yml", "tests:\n  image: python:3.12\n  script: python -m pytest\n")
	require.NoError(t, CheckCIProviderSupport(root, "python", "pytest"))
	check := pythonRuntimeCheck(t, root)
	require.Equal(t, "incompatible", check.Status)
	require.Len(t, check.Jobs, 1)
	require.Equal(t, "missing_instrumentation", check.Jobs[0].Code)
	require.Contains(t, check.Jobs[0].Reason, "No Datadog setup script selecting python runs before this pytest command")
}
