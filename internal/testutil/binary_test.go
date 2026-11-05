package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDDTestBuildIsSharedAcrossWorkingDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Go executable")
	}
	previous := compiled
	compiled = new(binaryCache)
	t.Cleanup(func() { compiled = previous })
	t.Setenv("DDTEST_TEST_BINARY", "")
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	log := filepath.Join(directory, "builds")
	t.Setenv("BUILD_LOG", log)
	goCommand := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" >> \"$BUILD_LOG\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$3\"\nchmod +x \"$3\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "go"), []byte(goCommand), 0755))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(t.TempDir())
	first := DDTest(t)
	t.Chdir(t.TempDir())
	require.Equal(t, first, DDTest(t))
	data, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Len(t, strings.Split(strings.TrimSpace(string(data)), "\n"), 1)
	require.FileExists(t, filepath.Join(strings.TrimSpace(string(data)), "go.mod"))
	info, err := os.Stat(first)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
}

func TestDDTestUsesTheProvidedCIExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ddtest")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0755))
	t.Setenv("DDTEST_TEST_BINARY", path)
	t.Setenv("PATH", t.TempDir()) // A provided executable never needs Go installed.
	t.Chdir(t.TempDir())
	require.Equal(t, path, DDTest(t))
}
