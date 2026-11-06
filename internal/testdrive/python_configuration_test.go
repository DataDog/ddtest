package testdrive

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRebasePytestPathsPreservesExpressionsAndNodeSelectors(t *testing.T) {
	root, copied := t.TempDir(), t.TempDir()
	file := filepath.Join(root, "test_app.py")
	config := filepath.Join(root, "pytest.ini")
	requireWriteFile(t, file, "")
	requireWriteFile(t, config, "")
	outside := filepath.Join(t.TempDir(), "outside.py")
	output := filepath.Join(root, "new-report.xml")
	args := []string{"-k", file, "-o", "addopts=" + config, "-c", config, "--config-file=" + config, "-c" + config, "-c=" + config, "--junitxml=" + output, "--", file + "::test_app[a/b]", outside}
	wantFile, wantConfig := filepath.Join(copied, "test_app.py"), filepath.Join(copied, "pytest.ini")
	want := []string{"-k", file, "-o", "addopts=" + config, "-c", wantConfig, "--config-file=" + wantConfig, "-c" + wantConfig, "-c" + wantConfig, "--junitxml=" + filepath.Join(copied, "new-report.xml"), "--", wantFile + "::test_app[a/b]", outside}
	require.Equal(t, want, rebasePytestPaths(args, root, copied))
}

func TestRebasePytestCoverageAndLogPaths(t *testing.T) {
	original, copied := t.TempDir(), t.TempDir()
	for _, option := range []string{"--cov", "--cov-config", "--log-file"} {
		for _, attached := range []bool{false, true} {
			path := filepath.Join(original, "path with spaces")
			args, want := []string{option, path}, []string{option, filepath.Join(copied, "path with spaces")}
			if attached {
				args, want = []string{option + "=" + args[1]}, []string{option + "=" + want[1]}
			}
			require.Equal(t, want, rebasePytestPaths(args, original, copied))
		}
	}
	args := []string{"--cov-report=xml:" + filepath.Join(original, "coverage.xml"), "--cov-report", "json:" + filepath.Join(original, "coverage.json"), "--cov-report=term-missing:skip-covered", "--cov-report=", "--cov=app", "--log-file=relative.log"}
	want := []string{"--cov-report=xml:" + filepath.Join(copied, "coverage.xml"), "--cov-report", "json:" + filepath.Join(copied, "coverage.json"), "--cov-report=term-missing:skip-covered", "--cov-report=", "--cov=app", "--log-file=relative.log"}
	require.Equal(t, want, rebasePytestPaths(args, original, copied))
}

func TestUVDirectoryRebasingPreservesEnvironmentSelectors(t *testing.T) {
	original, copied := t.TempDir(), t.TempDir()
	interpreter := filepath.Join(original, ".venv", "bin", "python")
	words := []string{"uv", "run", "--python", interpreter, "--group", "python3.10", "--directory", original, "--project=" + original, "python", "-m", "pytest", "--directory", original}
	want := []string{"uv", "run", "--python", interpreter, "--group", "python3.10", "--directory", copied, "--project=" + copied, "python", "-m", "pytest", "--directory", original}
	require.Equal(t, want, rebaseUVDirectories(words, original, copied))
}

func TestPytestConfigUsesUVWorkingDirectory(t *testing.T) {
	original, copied := t.TempDir(), t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(original, "suite"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(copied, "suite"), 0755))
	args := filepath.Join(copied, "suite", "args.txt")
	requireWriteFile(t, args, filepath.Join(original, "test_app.py")+"\n")
	requireWriteFile(t, filepath.Join(original, "suite", "args.txt"), filepath.Join(original, "test_app.py")+"\n")
	requireWriteFile(t, filepath.Join(copied, "suite", "pytest.ini"), "[pytest]\naddopts = @"+filepath.Join(original, "suite", "args.txt")+"\n")
	drive := &Testdrive{repositoryRoot: copied, configurationRoot: copied, configurationSourceRoot: original, command: "uv", args: []string{"run", "--directory", filepath.Join(original, "suite"), "python", "-m", "pytest"}, configurationEnvironment: map[string]string{}}
	require.NoError(t, drive.rebasePytestConfiguration(copied))
	require.Contains(t, drive.configurationEnvironment["PYTEST_ADDOPTS"], filepath.Join(copied, "test_app.py"))
	require.NotContains(t, drive.configurationEnvironment["PYTEST_ADDOPTS"], original)
}

func TestPytestUnresolvedNestedConfigurationRequiresExplicitConfig(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			original, copied := t.TempDir(), t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(copied, "tests"), 0755))
			requireWriteFile(t, filepath.Join(copied, "pytest.ini"), "[pytest]\n")
			requireWriteFile(t, filepath.Join(copied, "tests", "pytest.ini"), "[pytest]\npythonpath="+filepath.Join(original, "src")+"\n")
			drive := &Testdrive{repositoryRoot: copied, configurationSourceRoot: original, command: "python", args: []string{"-m", "pytest", "tests"}, configurationEnvironment: map[string]string{}}
			if override {
				drive.args = append(drive.args, "-o", "addopts=")
			}
			err := drive.rebasePytestConfiguration(copied)
			require.ErrorContains(t, err, "cannot isolate pytest configuration")
			require.ErrorContains(t, err, "explicitly with -c")
		})
	}
}

func TestPytestAddoptsOverridesDoNotReadUnusedDefaults(t *testing.T) {
	for _, source := range []string{"command", "environment", "command shadows environment", "last command override", "last environment override"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "pytest.ini"), "[pytest]\naddopts = @/missing-unused-args.txt\n")
			drive := &Testdrive{repositoryRoot: root, configurationSourceRoot: root, command: "python", args: []string{"-m", "pytest"}, configurationEnvironment: map[string]string{}}
			if source == "command" || source == "command shadows environment" || source == "last command override" {
				switch source {
				case "command shadows environment":
					drive.configurationEnvironment["PYTEST_ADDOPTS"] = "-o addopts=@/missing-unused-env.txt"
				case "last command override":
					drive.args = append(drive.args, "-oaddopts=@/missing-unused-command.txt")
				}
				drive.args = append(drive.args, "-o", "addopts=")
			} else {
				drive.configurationEnvironment["PYTEST_ADDOPTS"] = "--override-ini=addopts="
				if source == "last environment override" {
					drive.configurationEnvironment["PYTEST_ADDOPTS"] = "-o addopts=@/missing-unused-env.txt --override-ini=addopts="
				}
			}
			require.NoError(t, drive.rebasePytestConfiguration(root))
		})
	}
}

func TestPytestArgumentFileCyclesAreRejected(t *testing.T) {
	for _, content := range []string{"@args.txt\n", "-o\naddopts=@args.txt\n"} {
		root := t.TempDir()
		requireWriteFile(t, filepath.Join(root, "args.txt"), content)
		_, _, err := isolatePytestOptions([]string{"@args.txt"}, root, root, root, map[string]bool{}, nil)
		require.ErrorContains(t, err, "recursive pytest")
	}
}

func TestPytestArgumentFilesCannotModifyExternalFiles(t *testing.T) {
	copied, external := t.TempDir(), t.TempDir()
	file := filepath.Join(external, "args.txt")
	requireWriteFile(t, file, "/original/test_app.py\n")
	for _, mode := range []string{"absolute", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			argument := "@" + file
			if mode == "symlink" {
				if runtime.GOOS == "windows" {
					t.Skip("requires POSIX symlink privileges")
				}
				link := filepath.Join(copied, "args.txt")
				require.NoError(t, os.Symlink(file, link))
				argument = "@args.txt"
			}
			_, err := expandPytestArgumentFiles([]string{argument}, copied, "/original", copied, map[string]bool{}, nil)
			require.ErrorContains(t, err, "outside checkout")
			contents, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, "/original/test_app.py\n", string(contents))
		})
	}
}
