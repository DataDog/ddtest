package onboard

import (
	"context"
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
