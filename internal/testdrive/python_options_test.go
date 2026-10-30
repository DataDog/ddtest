package testdrive

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPytestArgumentFileNormalizationTracksEverySource(t *testing.T) {
	for _, source := range []string{"command", "environment", "configuration", "command override", "environment override"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "args.txt"), "test_app.py\n")
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
			drive := &Testdrive{repositoryRoot: root, configurationSourceRoot: root, command: "python", args: []string{"-m", "pytest"}, configurationEnvironment: map[string]string{}}
			switch source {
			case "command":
				drive.args = append(drive.args, "@args.txt")
			case "environment":
				drive.configurationEnvironment["PYTEST_ADDOPTS"] = "@args.txt"
			case "configuration":
				requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\naddopts=@args.txt\n")
			case "command override":
				drive.args = append(drive.args, "-o", "addopts=@args.txt")
			case "environment override":
				drive.configurationEnvironment["PYTEST_ADDOPTS"] = "-o addopts=@args.txt"
			}
			require.NoError(t, drive.rebasePytestConfiguration(root))
			require.Equal(t, "true", drive.configurationEnvironment[pytestArgumentFilesEnvironment])
		})
	}
}

func TestPytestArgumentFilesRequireNativeVersionSupport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	for _, version := range []string{"7.2.0", "8.1.2", "8.2.0", "9.2.0"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			python := filepath.Join(root, "python")
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\n")
			metadata, err := json.Marshal(map[string]string{"pytest": version, "python": "3.12.8", "tracer": "4.16.1", "requiresPython": ">=3.9", "executable": python, "directory": root})
			require.NoError(t, err)
			requireWriteFile(t, python, "#!/bin/sh\nfor last; do :; done\ncat >\"$last\" <<'DDTEST_METADATA'\n"+string(metadata)+"\nDDTEST_METADATA\n")
			require.NoError(t, os.Chmod(python, 0755))
			drive := &Testdrive{repositoryRoot: root, command: python, args: []string{"-m", "pytest"}, configurationEnvironment: map[string]string{pytestArgumentFilesEnvironment: "true"}}
			var result validationResult
			err = drive.checkPytestPreflight(t.Context(), io.Discard, &result)
			if version == "7.2.0" || version == "8.1.2" {
				require.ErrorContains(t, err, "argument files require pytest >=8.2")
				require.Equal(t, "incompatible", result.Preflight.Verdict.Status)
			} else {
				require.NoError(t, err)
				require.Equal(t, "compatible", result.Preflight.Verdict.Status)
			}
			drive.configurationEnvironment[pytestArgumentFilesEnvironment] = "false"
			require.NoError(t, drive.checkPytestPreflight(t.Context(), io.Discard, &result), "older supported pytest must still accept ordinary selectors")
		})
	}
}
