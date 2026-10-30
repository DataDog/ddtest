package testdrive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestPreparePythonInspectsEffectiveCommandEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, mode := range []string{"assignment", "CI environment", "assignment overrides CI"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("PATH", root)
			t.Setenv("UV_PROJECT_ENVIRONMENT", "")
			requireWriteFile(t, "pyproject.toml", "[tool.pytest.ini_options]\n")
			python := filepath.Join(root, ".venv-ci", "bin", "python")
			metadata, err := json.Marshal(map[string]any{"executable": python, "version": "3.12.8", "pytest": true})
			require.NoError(t, err)
			script := "#!/bin/sh\n[ \"$UV_PROJECT_ENVIRONMENT\" = .venv-ci ] || exit 3\nfor last; do :; done\nprintf '%s' '" + string(metadata) + "' > \"$last\"\n"
			require.NoError(t, os.WriteFile("uv", []byte(script), 0755))
			command := "uv run pytest"
			if mode != "assignment" {
				value := ".venv-ci"
				if mode == "assignment overrides CI" {
					value = ".other-venv"
				}
				require.NoError(t, os.MkdirAll(".github/workflows", 0755))
				requireWriteFile(t, ".github/workflows/test.yml", "jobs:\n  test:\n    steps:\n      - run: uv run pytest\n        env: {UV_PROJECT_ENVIRONMENT: '"+value+"'}\n")
			}
			if mode != "CI environment" {
				command = "UV_PROJECT_ENVIRONMENT=.venv-ci " + command
			}
			old := settings.Get().Command
			settings.Get().Command = command
			t.Cleanup(func() { settings.Get().Command = old })
			drive, err := Prepare(PrepareOptions{CheckOnly: true, TracerVersion: "4.16.1"})
			require.NoError(t, err)
			require.Nil(t, drive.environmentFailure)
			require.Equal(t, python, drive.environmentInfo.Executable)
			require.Equal(t, ".venv-ci", drive.configurationEnvironment["UV_PROJECT_ENVIRONMENT"])
		})
	}
}
