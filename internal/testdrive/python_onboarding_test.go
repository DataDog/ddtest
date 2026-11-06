package testdrive

import (
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testutil"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

func TestPythonStandalonePreparationKeepsUnsupportedCIAdvisory(t *testing.T) {
	python := testutil.PytestPython(t)
	for _, mode := range []string{"default", "command", "all", "check-only"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
			requireWriteFile(t, filepath.Join(root, "test_app.py"), "def test_app():\n    pass\n")
			// ddtest does not validate hand-written Python setups yet.
			requireWriteFile(t, filepath.Join(root, ".gitlab-ci.yml"), "test:\n  image: python:3.12\n  variables:\n    PYTEST_ADDOPTS: --ddtrace\n    DD_CIVISIBILITY_AGENTLESS_ENABLED: \"true\"\n  script:\n    - pip install ddtrace==4.16.1 pytest\n    - python -m pytest\n")
			onboard.UseCIProvider("gitlab")
			t.Cleanup(func() { onboard.UseCIProvider("") })
			previous := settings.Get().Command
			t.Cleanup(func() { settings.Get().Command = previous })
			settings.Get().Command = ""
			if mode == "command" {
				settings.Get().Command = shellquote.Join(python, "-m", "pytest")
			}
			_, err := Prepare(PrepareOptions{All: mode == "all", CheckOnly: mode == "check-only", TracerVersion: "4.11.0"})
			if mode == "all" || mode == "check-only" {
				require.ErrorContains(t, err, "does not validate hand-written Python setups")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Before preflight knows the local release, pytest's CI placeholder is the
// pytest check, never the JavaScript one.
func TestPythonCIRuntimesUseThePytestCheck(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
	requireWriteFile(t, filepath.Join(root, ".gitlab-ci.yml"), "test:\n  image: python:3.12\n  script: python -m pytest\n")
	onboard.UseCIProvider("gitlab")
	t.Cleanup(func() { onboard.UseCIProvider("") })
	check := frameworkCIRuntimes(t.Context(), root, "pytest")
	require.Len(t, check.Jobs, 1)
	require.Equal(t, "missing_instrumentation", check.Jobs[0].Code)
	require.Contains(t, check.Jobs[0].Reason, "No Datadog setup script selecting python")
}
