package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPythonProbePreservesEnvironmentSelectors(t *testing.T) {
	for _, tc := range []struct {
		command      string
		args, prefix []string
	}{
		{"uv", []string{"run", "--no-sync", "python", "-m", "pytest"}, []string{"run", "--no-sync", "python"}},
		{"uv", []string{"run", "--project", "project space", "--python", "python3.13", "--package=member", "pytest", "-k", "example"}, []string{"run", "--no-sync", "--project", "project space", "--python", "python3.13", "--package=member", "python"}},
		{"uv", []string{"run", "--locked", "--", "python", "-m", "pytest"}, []string{"run", "--no-sync", "--locked", "--", "python"}},
		{"uv", []string{"run", "--no-default-groups", "--group", "dev", "python", "-m", "pytest"}, []string{"run", "--no-sync", "--no-default-groups", "--group", "dev", "python"}},
		{"python3.13", []string{"-W", "error", "-m", "pytest"}, []string{"-W", "error"}},
		{"python3.13t", []string{"-m", "pytest"}, []string{}},
		{"python3.13d", []string{"-W", "error", "-m", "pytest"}, []string{"-W", "error"}},
		{"uv", []string{"run", "python3.13d", "-m", "pytest"}, []string{"run", "--no-sync", "python3.13d"}},
		{"python_d.exe", []string{"-m", "pytest"}, []string{}},
		{"python3", []string{"-Im", "pytest"}, []string{"-I"}},
		{"python3", []string{"-X", "dev", "-IBu", "-m", "pytest"}, []string{"-X", "dev", "-I", "-B", "-u"}},
		{"uv", []string{"run", "python", "-Im", "pytest"}, []string{"run", "--no-sync", "python", "-I"}},
		{"uv", []string{"run", "--only-group", "test", "--all-groups", "--no-group=docs", "--no-dev", "--all-extras", "--no-extra", "slow", "pytest"}, []string{"run", "--no-sync", "--only-group", "test", "--all-groups", "--no-group=docs", "--no-dev", "--all-extras", "--no-extra", "slow", "python"}},
		{"poetry", []string{"run", "python", "-m", "pytest"}, []string{"run", "python"}},
	} {
		command, prefix, err := pythonProbeCommand(tc.command, tc.args)
		require.NoError(t, err)
		require.Equal(t, tc.command, command)
		require.Equal(t, tc.prefix, prefix)
	}
	_, _, err := pythonProbeCommand("uv", []string{"run", "--with", "pytest", "pytest"})
	require.ErrorContains(t, err, "--with")
	_, _, err = pythonProbeCommand("python-wrapper", []string{"-m", "pytest"})
	require.ErrorContains(t, err, "cannot identify a Python interpreter")
	_, _, err = pythonProbeCommand("uv", []string{"run", "python-wrapper", "-m", "pytest"})
	require.ErrorContains(t, err, "python-wrapper")
}

// Isolated interpreters get the observer through a bootstrap, also when -I
// is clustered with -m or other flags.
func TestPytestObserverArgumentsReadClusteredFlags(t *testing.T) {
	for args, want := range map[string][]string{
		"-Im pytest -q":       {"-I", "-c", pytestObserverBootstrap, "observer", "tracer", "-q"},
		"-X dev -IBum pytest": {"-X", "dev", "-I", "-B", "-u", "-c", pytestObserverBootstrap, "observer", "tracer"},
		"-Bu -m pytest -q":    {"-Bu", "-m", "pytest", "-q"},
	} {
		require.Equal(t, want, pytestObserverArguments("python3", strings.Fields(args), "observer", "tracer"), args)
	}
}

func TestUVPreflightReportsBeforeInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable fixture")
	}
	for _, outcome := range []string{"uv missing", "runner error", "pytest missing", "ready"} {
		t.Run(outcome, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("VIRTUAL_ENV", "")
			t.Setenv("CONDA_PREFIX", "")
			t.Setenv("UV_PROJECT_ENVIRONMENT", "")
			t.Setenv("PATH", root)
			old := settings.Get().Command
			settings.Get().Command = ""
			t.Cleanup(func() { settings.Get().Command = old })
			requireWriteFile(t, "pyproject.toml", "[tool.pytest.ini_options]\n")
			requireWriteFile(t, "uv.lock", "")
			python := platform.PythonEnvironmentExecutable(filepath.Join(root, ".venv"))
			if outcome != "uv missing" {
				result, err := json.Marshal(map[string]any{"executable": python, "version": "3.13.15", "pytest": outcome == "ready", "error": "No module named pytest"})
				require.NoError(t, err)
				script := "#!/bin/sh\n[ \"$1\" = run ] || exit 2\nfor last; do :; done\nprintf '%s' '" + string(result) + "' > \"$last\"\n"
				if outcome == "runner error" {
					script = "#!/bin/sh\nprintf '%s' 'error: selected Python is unavailable' >&2\nexit 2\n"
				}
				require.NoError(t, os.WriteFile("uv", []byte(script), 0755))
			}
			drive, err := Prepare(PrepareOptions{TracerVersion: "latest"})
			require.NoError(t, err)
			require.NoDirExists(t, drive.session.Directory())
			installer := &fakeTracer{}
			drive.platform = installer
			executor := &fakeTestdriveExecutor{}
			drive.executor = executor
			drive.startIntake = func(string, intake.Scenario) (localIntake, error) {
				require.Equal(t, "ready", outcome, "blocked environments must not start an intake")
				return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
			}
			var output bytes.Buffer
			drive.Preview(&output)
			err = drive.Run(t.Context(), &output)
			if outcome == "ready" {
				require.Error(t, err, "availability alone is not paired validation evidence")
				require.False(t, readValidationReport(t, root).Success)
			} else {
				require.Error(t, err)
				require.Empty(t, installer.options.Directory)
				require.Empty(t, executor.command)
				require.NotContains(t, output.String(), "Installing ddtrace")
				require.Contains(t, output.String(), "Environment check:")
				if outcome == "runner error" {
					require.Contains(t, output.String(), "error: selected Python is unavailable")
				}
			}
			if outcome != "ready" {
				data, err := os.ReadFile(htmlReportPath(root))
				require.NoError(t, err)
				require.Contains(t, string(data), "uv project (uv.lock)")
				require.Contains(t, string(data), drive.environmentFailure.Title)
				require.Contains(t, string(data), "Troubleshooting")
			}
			require.NoDirExists(t, drive.session.Directory())

		})
	}
}

func TestEnvironmentPresentationIsShared(t *testing.T) {
	for _, name := range []string{"Python", "Node.js", "Ruby"} {
		info := environmentInfo{Runtime: name, Version: "1.2.3", Source: "Selected environment", Executable: "/example/runtime"}
		var output bytes.Buffer
		info.write(&output)
		require.Contains(t, output.String(), name+" 1.2.3: /example/runtime")
		model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1}, false, reportRuntime{Environment: info})
		require.Contains(t, model.Facts, reportFact{Label: name + " 1.2.3", Value: "/example/runtime"})
	}
}

func TestPythonFrameworkCommandUnaffectedByUV(t *testing.T) {
	t.Chdir(t.TempDir())
	requireWriteFile(t, "uv.lock", "")
	old := settings.Get().Command
	settings.Get().Command = ""
	t.Cleanup(func() { settings.Get().Command = old })
	command, args := framework.NewPytest(platform.NewPython()).Command()
	require.Equal(t, "python", command)
	require.Equal(t, []string{"-m", "pytest"}, args)
}
