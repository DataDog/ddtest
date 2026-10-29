package testdrive

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/stretchr/testify/require"
)

func TestUVDirectoryRetainsPreparedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable fixtures")
	}
	root, tools := t.TempDir(), t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir(".git", 0755))
	requireWriteFile(t, "pytest.ini", "[pytest]\n")
	availability, err := json.Marshal(map[string]any{"executable": filepath.Join(tools, "prepared", "bin", "python"), "version": "3.12.8", "pytest": true})
	require.NoError(t, err)
	metadata, err := json.Marshal(map[string]string{"Pytest": "9.2.0", "Python": "3.12.8", "Tracer": "4.16.1", "RequiresPython": ">=3.9"})
	require.NoError(t, err)
	command := filepath.Join(tools, "uv")
	requireWriteFile(t, command, `#!/bin/sh
case "$*" in
*"--directory $ORIGINAL "*) ;;
*) [ "$UV_PROJECT_ENVIRONMENT" = "$PREPARED" ] || exit 3;;
esac
for last; do :; done
case "$*" in
*'result = {"executable"'*) printf '%s' '`+string(availability)+`' > "$last";;
*) printf '%s' '`+string(metadata)+`' > "$last";;
esac
`)
	require.NoError(t, os.Chmod(command, 0755))
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: command, args: []string{"run", "--directory", root, "python", "-m", "pytest"}, executor: &ext.DefaultCommandExecutor{}, checkOnly: true}
	drive.preflight = drive.frameworkPreflight()
	result, err := drive.runPreparedConfiguration(t.Context(), io.Discard, &configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: map[string]string{"ORIGINAL": root, "PREPARED": filepath.Join(tools, "prepared")}}})
	require.NoError(t, err)
	require.NotNil(t, result.Preflight)
	require.Equal(t, "compatible", result.Preflight.Verdict.Status)
}
