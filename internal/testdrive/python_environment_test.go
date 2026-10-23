package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPythonEnvironmentSelection(t *testing.T) {
	for _, tc := range []struct {
		name, active   string
		lock, explicit bool
		want           string
	}{
		{name: "PATH", want: "python"},
		{name: "uv lock", lock: true, want: "uv"},
		{name: "explicit overrides uv", lock: true, explicit: true, want: "custom-python"},
		{name: "activated venv", active: "VIRTUAL_ENV"},
		{name: "uv overrides activated venv", lock: true, active: "VIRTUAL_ENV", want: "uv"},
		{name: "activated conda", active: "CONDA_PREFIX"},
		{name: "uv overrides activated conda", lock: true, active: "CONDA_PREFIX", want: "uv"},
		{name: "explicit overrides uv and activated venv", lock: true, active: "VIRTUAL_ENV", explicit: true, want: "custom-python"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("VIRTUAL_ENV", "")
			t.Setenv("CONDA_PREFIX", "")
			if tc.lock {
				requireWriteFile(t, filepath.Join(root, "uv.lock"), "")
			}
			want := tc.want
			if tc.active != "" {
				t.Setenv(tc.active, root)
				if want == "" {
					want = environmentPython(root)
				}
			}
			command := "python"
			if tc.explicit {
				command = "custom-python"
			}
			got, args, _ := pythonTestdriveCommand(root, command, []string{"-m", "pytest"}, tc.explicit)
			require.Equal(t, want, got)
			if got == "uv" {
				require.Equal(t, []string{"run", "--no-sync", "python", "-m", "pytest"}, args)
			}
		})
	}
}

func TestUVLockDiscoveryStopsAtCheckoutBoundary(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "uv.lock"), "")
	member := filepath.Join(root, "packages", "member")
	require.NoError(t, os.MkdirAll(member, 0755))
	require.Equal(t, root, uvLockRoot(member))
	requireWriteFile(t, filepath.Join(member, ".git"), "gitdir: elsewhere")
	require.Empty(t, uvLockRoot(member))
}

func TestPythonProbePreservesEnvironmentSelectors(t *testing.T) {
	for _, tc := range []struct {
		command      string
		args, prefix []string
	}{
		{"uv", []string{"run", "--no-sync", "python", "-m", "pytest"}, []string{"run", "--no-sync", "python"}},
		{"uv", []string{"run", "--project", "project space", "--python", "python3.13", "--package=member", "pytest", "-k", "example"}, []string{"run", "--no-sync", "--project", "project space", "--python", "python3.13", "--package=member", "python"}},
		{"uv", []string{"run", "--locked", "--", "python", "-m", "pytest"}, []string{"run", "--no-sync", "--locked", "--", "python"}},
		{"python3.13", []string{"-W", "error", "-m", "pytest"}, []string{"-W", "error"}},
		{"poetry", []string{"run", "python", "-m", "pytest"}, []string{"run", "python"}},
	} {
		command, prefix, err := pythonProbeCommand(tc.command, tc.args)
		require.NoError(t, err)
		require.Equal(t, tc.command, command)
		require.Equal(t, tc.prefix, prefix)
	}
	_, _, err := pythonProbeCommand("uv", []string{"run", "--with", "pytest", "pytest"})
	require.ErrorContains(t, err, "--with")
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
			python := environmentPython(filepath.Join(root, ".venv"))
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
			drive.startIntake = func(string) (localIntake, error) {
				require.Equal(t, "ready", outcome, "blocked environments must not start an intake")
				return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
			}
			var output bytes.Buffer
			drive.Preview(&output)
			err = drive.Run(t.Context(), &output)
			if outcome == "ready" {
				require.NoError(t, err)
				require.Equal(t, python, installer.options.Command)
				require.Equal(t, "uv", executor.command)
				require.Equal(t, []string{"run", "--no-sync", "python", "-m", "pytest"}, executor.args)
			} else {
				require.Error(t, err)
				require.Empty(t, installer.options.Directory)
				require.Empty(t, executor.command)
				require.NotContains(t, output.String(), "Installing ddtrace")
				require.Contains(t, output.String(), "Not run (environment check failed)")
				if outcome == "runner error" {
					require.Contains(t, output.String(), "error: selected Python is unavailable")
				}
			}
			var diagnostics runDiagnostics
			data, err := os.ReadFile(filepath.Join(drive.session.Directory(), diagnosticsFilename))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(data, &diagnostics))
			require.Equal(t, "uv project (uv.lock)", diagnostics.Environment.Source)
			if outcome != "ready" {
				require.False(t, diagnostics.CommandFailed)
				require.True(t, diagnostics.RunFailed)
				require.Len(t, diagnostics.Findings, 1)
				require.Len(t, diagnostics.Troubleshooting, 1)
				report, err := os.ReadFile(filepath.Join(drive.session.Directory(), reportFilename))
				require.NoError(t, err)
				require.Contains(t, string(report), diagnostics.Findings[0].Title)
				require.Contains(t, string(report), "Troubleshooting")
			}
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

func TestParentLockSelectsUVWithoutProbing(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "uv.lock"), "")
	child := filepath.Join(root, "packages", "member")
	require.NoError(t, os.MkdirAll(child, 0755))
	t.Setenv("PATH", "")
	t.Setenv("VIRTUAL_ENV", "")
	t.Setenv("CONDA_PREFIX", "")
	command, args, source := pythonTestdriveCommand(child, "python", []string{"-m", "pytest"}, false)
	require.Equal(t, "uv", command)
	require.Equal(t, []string{"run", "--no-sync", "python", "-m", "pytest"}, args)
	require.Equal(t, "uv project (uv.lock)", source)
}
