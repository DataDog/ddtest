package testdrive

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPytestApplicationPythonPathIsIsolated(t *testing.T) {
	for _, source := range []string{"ambient", "configured"} {
		t.Run(source, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, "pythonpath "+source) })
	}
}

func TestPytestTracerVerificationHonorsEmptyPythonPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable fixtures")
	}
	root := t.TempDir()
	python := filepath.Join(root, "python")
	requireWriteFile(t, python, "#!/bin/sh\n[ \"$PYTHONPATH\" = tracer-path ] || exit 3\nfor last; do :; done\nprintf '%s' '{\"version\":\"4.16.1\"}' > \"$last\"\n")
	require.NoError(t, os.Chmod(python, 0755))
	t.Setenv("PYTHONPATH", "unwanted-source-checkout")
	drive := &Testdrive{language: "python", command: python, args: []string{"-m", "pytest"}, configurationEnvironment: map[string]string{"PYTHONPATH": ""}}
	require.NoError(t, drive.verifyPytestTracer(t.Context(), "tracer-path", "4.16.1"))
}
