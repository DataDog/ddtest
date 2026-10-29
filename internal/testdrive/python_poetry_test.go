package testdrive

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestPoetryEnvironmentSelectionDoesNotCreateStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable fixtures")
	}
	for _, mode := range []string{"prepared", "missing", "system", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			command := filepath.Join(root, "poetry")
			log := filepath.Join(root, "calls")
			requireWriteFile(t, log, "")
			requireWriteFile(t, command, `#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$*" in
*'env info --path')
  case "$MODE" in
    prepared) printf '%s\n' "$PREPARED";;
    timeout) sleep 30;;
    *) exit 1;;
  esac;;
*'config virtualenvs.create')
  if [ "$MODE" = system ]; then echo false; else echo true; fi;;
*) exit 99;;
esac
`)
			require.NoError(t, os.Chmod(command, 0755))
			env := map[string]string{"CALLS": log, "MODE": mode, "PREPARED": root}
			timeout := time.Second
			if mode == "timeout" {
				timeout = 300 * time.Millisecond
			}
			pinned, err := pythonPoetryEnvironment(t.Context(), command, root, env, timeout)
			switch mode {
			case "prepared", "system":
				require.NoError(t, err)
				require.Equal(t, "false", pinned["POETRY_VIRTUALENVS_CREATE"])
				if mode == "prepared" {
					require.Equal(t, root, pinned["VIRTUAL_ENV"])
				} else {
					require.NotContains(t, pinned, "VIRTUAL_ENV")
				}
				require.NotContains(t, env, "POETRY_VIRTUALENVS_CREATE")
			case "missing":
				require.ErrorContains(t, err, "prepare the Poetry environment")
			case "timeout":
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			calls, err := os.ReadFile(log)
			require.NoError(t, err)
			if mode != "timeout" {
				require.Contains(t, string(calls), "--directory "+root+" env info --path")
			}
			require.NotContains(t, string(calls), " run ")
			if mode == "timeout" {
				require.NotContains(t, string(calls), "config virtualenvs.create")
			}
		})
	}
}

func TestPoetryConfigurationPinsOriginalEnvironmentBeforeRefresh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable fixtures")
	}
	root, tools := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	original := filepath.Join(root, "suite")
	require.NoError(t, os.Mkdir(original, 0755))
	t.Chdir(original)
	requireWriteFile(t, "pytest.ini", "[pytest]\n")
	command := filepath.Join(tools, "poetry")
	available, err := json.Marshal(map[string]any{"executable": "/prepared/bin/python", "version": "3.12.8", "pytest": true})
	require.NoError(t, err)
	metadata, err := json.Marshal(map[string]string{"Pytest": "9.2.0", "Python": "3.12.8", "Tracer": "4.16.1", "RequiresPython": ">=3.9", "Executable": "/prepared/bin/python"})
	require.NoError(t, err)
	requireWriteFile(t, command, `#!/bin/sh
case "$*" in
*"--directory $ORIGINAL env info --path") printf '%s\n' "$PREPARED"; exit;;
*'env info --path')
  if [ "$PWD" = "$ORIGINAL" ]; then printf '%s\n' "$PREPARED"; exit; fi;;
esac
[ "$VIRTUAL_ENV" = "$PREPARED" ] && [ "$POETRY_VIRTUALENVS_CREATE" = false ] || exit 3
for last; do :; done
case "$*" in
*'env info --path') printf '%s\n' "$PREPARED";;
*'result = {"executable"'*) printf '%s' '`+string(available)+`' > "$last";;
*) printf '%s' '`+string(metadata)+`' > "$last";;
esac
`)
	require.NoError(t, os.Chmod(command, 0755))
	require.NoError(t, os.Mkdir(filepath.Join(tools, "bin"), 0755))
	data, err := os.ReadFile(command)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tools, "bin", "python"), data, 0755))
	env := map[string]string{"ORIGINAL": original, "PREPARED": tools}
	info, finding := pythonEnvironmentInfo(t.Context(), command, []string{"run", "python", "-I", "-m", "pytest"}, "Poetry", env, time.Second)
	require.Nil(t, finding)
	require.Equal(t, "/prepared/bin/python", info.Executable)
	drive := &Testdrive{repositoryRoot: original, language: "python", framework: framework.NewPytest(platform.NewPython()), command: command, args: []string{"run", "pytest"}, executor: &ext.DefaultCommandExecutor{}, checkOnly: true}
	drive.preflight = drive.frameworkPreflight()
	result, err := drive.runPreparedConfiguration(t.Context(), io.Discard, &configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: env}})
	require.NoError(t, err)
	require.NotNil(t, result.Preflight, "%+v", result)
	require.Equal(t, "compatible", result.Preflight.Verdict.Status)
	require.DirExists(t, tools)
}
