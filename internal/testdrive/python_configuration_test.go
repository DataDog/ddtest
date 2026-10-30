package testdrive

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/kballard/go-shellquote"
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

func TestPytestAbsolutePathsExecuteOnlyInsideCopy(t *testing.T) {
	testPytestPathsExecuteOnlyInsideCopy(t, "direct")
}

func TestPytestEqualsConfigPathExecutesOnlyInsideCopy(t *testing.T) {
	testPytestPathsExecuteOnlyInsideCopy(t, "equals config")
}

func TestPytestNativeConfigurationPathsExecuteOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"native pytest.ini", "native pyproject.toml", "native command override", "native environment override"} {
		t.Run(mode, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, mode) })
	}
}

func TestPytestConfiguredActivationIsControlled(t *testing.T) {
	for _, mode := range []string{"activation pytest.ini", "activation tox.ini", "activation pyproject.toml", "activation command override", "activation environment override"} {
		t.Run(mode, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, mode) })
	}
}

func TestPytestArgumentFilesExecuteOnlyInsideCopy(t *testing.T) {
	testPytestPathsExecuteOnlyInsideCopy(t, "argument files")
}

func TestPytestConfiguredArgumentFilesExecuteOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"config pytest.ini", "config tox.ini", "config pyproject.toml", "inline override", "environment override"} {
		t.Run(mode, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, mode) })
	}
}

func TestPytestInheritedEndOfOptions(t *testing.T) {
	for _, mode := range []string{"environment separator", "config separator"} {
		t.Run(mode, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, mode) })
	}
}

func TestPytestControlledModeWithDisabledAutoload(t *testing.T) {
	testPytestPathsExecuteOnlyInsideCopy(t, "disabled autoload")
}

func TestPytestObserverWithEnvironmentIgnored(t *testing.T) {
	for _, flag := range []string{"-I", "-E", "-bE"} {
		t.Run(flag, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, flag) })
	}
}

func TestPytestUVDirectoryExecutesOnlyInsideCopy(t *testing.T) {
	for _, option := range []string{"--directory", "--project", "uv pytest group"} {
		t.Run(option, func(t *testing.T) { testPytestPathsExecuteOnlyInsideCopy(t, option) })
	}
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

func testPytestPathsExecuteOnlyInsideCopy(t *testing.T, mode string) {
	t.Helper()
	python := feedbackPython(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, exec.CommandContext(t.Context(), "git", "init", "-q").Run())
	requireWriteFile(t, "pytest.ini", "[pytest]\npythonpath = .\n")
	requireWriteFile(t, "app.py", "def add(a,b):\n    return a+b\n")
	require.NoError(t, os.Mkdir("tests", 0755))
	file := filepath.Join(root, "tests", "test_app.py")
	requireWriteFile(t, file, "from pathlib import Path\nfrom app import add\ndef test_app():\n    Path(__file__).with_suffix('.executed').write_text('ran')\n    assert add(1, 2) == 3\n")
	t.Setenv("DD_GIT_REPOSITORY_URL", "https://example.com/ddtest-python-fixture.git")
	t.Setenv("DD_GIT_COMMIT_SHA", "0123456789012345678901234567890123456789")
	t.Setenv("DD_GIT_BRANCH", "main")
	t.Setenv("DD_GIT_COMMIT_MESSAGE", "pytest fixture")
	args := []string{"-m", "pytest", "-c", filepath.Join(root, "pytest.ini"), file + "::test_app"}
	command := python
	addopts := "--rootdir=" + root
	environment := map[string]string{}
	configuration, configuredContents := "", ""
	if strings.HasPrefix(mode, "activation ") {
		activation := "--ddtrace --ddtrace-patch-all"
		configuration = strings.TrimPrefix(mode, "activation ")
		configuredContents = "[pytest]\npythonpath=.\n"
		switch configuration {
		case "pytest.ini", "tox.ini":
			configuredContents += "addopts=" + activation + "\n"
		case "pyproject.toml":
			configuredContents = "[tool.pytest.ini_options]\npythonpath=['.']\naddopts=['--ddtrace', '--ddtrace-patch-all']\n"
		default:
			configuration = "pytest.ini"
			if mode == "activation command override" {
				args = append(args, "-o", "addopts="+activation)
			} else {
				addopts += " " + shellquote.Join("-o", "addopts="+activation)
			}
		}
		requireWriteFile(t, configuration, configuredContents)
		args[3] = filepath.Join(root, configuration)
		requireWriteFile(t, file, "import os\nfrom app import add\ndef test_app(request):\n    expected = os.environ['DDTEST_PYTEST_INSTRUMENTED'] == 'true'\n    assert request.config.getoption('ddtrace') == expected\n    assert not request.config.getoption('ddtrace-patch-all')\n    assert add(1, 2) == 3\n")
	} else if mode == "equals config" {
		args = []string{"-m", "pytest", "-c=" + filepath.Join(root, "pytest.ini"), file + "::test_app"}
	} else if strings.HasPrefix(mode, "native ") {
		require.NoError(t, os.Mkdir("src", 0755))
		require.NoError(t, os.Remove("app.py"))
		requireWriteFile(t, "src/app.py", "from pathlib import Path\ndef add(a,b):\n    Path(__file__).with_suffix('.executed').write_text('ran')\n    return a+b\n")
		args = []string{"-m", "pytest"}
		settings := []string{"testpaths=" + filepath.Dir(file), "pythonpath=" + filepath.Join(root, "src"), "cache_dir=" + filepath.Join(root, "suite-cache")}
		configuration = strings.TrimPrefix(mode, "native ")
		switch configuration {
		case "pytest.ini":
			configuredContents = "[pytest]\n" + strings.Join(settings, "\n") + "\n"
		case "pyproject.toml":
			configuredContents = fmt.Sprintf("[tool.pytest.ini_options]\ntestpaths=[%q]\npythonpath=[%q]\ncache_dir=%q\n", filepath.Dir(file), filepath.Join(root, "src"), filepath.Join(root, "suite-cache"))
			args = append(args, "-c", filepath.Join(root, configuration))
		default:
			configuration = "pytest.ini"
			configuredContents = "[pytest]\n"
			var overrides []string
			for _, setting := range settings {
				overrides = append(overrides, "-o", setting)
			}
			if mode == "native command override" {
				args = append(args, overrides...)
			} else {
				addopts += " " + shellquote.Join(overrides...)
			}
		}
		requireWriteFile(t, configuration, configuredContents)
	} else if mode == "environment separator" || mode == "config separator" {
		args = []string{"-m", "pytest"}
		if mode == "environment separator" {
			addopts = "-- " + file + "::test_app"
		} else {
			addopts = ""
			configuration = "pytest.ini"
			configuredContents = "[pytest]\npythonpath = .\naddopts = -- " + file + "::test_app\n"
			requireWriteFile(t, configuration, configuredContents)
		}
	} else if mode == "argument files" {
		requireWriteFile(t, "ci-args.txt", "@"+filepath.Join(root, "nested-args.txt")+"\n")
		requireWriteFile(t, "nested-args.txt", "-c\n"+filepath.Join(root, "pytest.ini")+"\n"+file+"::test_app\n")
		requireWriteFile(t, "addopts.txt", addopts+"\n")
		args = []string{"-m", "pytest", "@" + filepath.Join(root, "ci-args.txt")}
		addopts = "@addopts.txt"
	} else if strings.HasPrefix(mode, "config ") || strings.HasSuffix(mode, " override") {
		argumentFile := filepath.Join(root, "ci-args.txt")
		requireWriteFile(t, argumentFile, file+"::test_app\n")
		args = []string{"-m", "pytest"}
		if strings.HasPrefix(mode, "config ") {
			configuration = strings.TrimPrefix(mode, "config ")
			configuredContents = "[pytest]\npythonpath = .\naddopts = @" + argumentFile + "\n"
			if configuration == "pyproject.toml" {
				configuredContents = fmt.Sprintf("[tool.pytest.ini_options]\npythonpath = ['.']\naddopts = [%q]\n", "@"+argumentFile)
			}
			requireWriteFile(t, configuration, configuredContents)
			if configuration != "pytest.ini" {
				args = append(args, "-c", filepath.Join(root, configuration))
			}
		} else if mode == "inline override" {
			args = append(args, "-o", "addopts=@"+argumentFile)
		} else {
			addopts += " -o addopts=@" + argumentFile
		}
	} else if mode == "--directory" || mode == "--project" || mode == "uv pytest group" {
		uv, err := exec.LookPath("uv")
		if err != nil {
			t.Skip("uv required")
		}
		command = uv
		args = append([]string{"run", "--no-sync", mode, root, "python"}, args...)
		if mode == "uv pytest group" {
			args = append([]string{"run", "--no-sync", "--group", "pytest", "--", "python"}, args[5:]...)
		}
		requireWriteFile(t, "pyproject.toml", "[project]\nname='ddtest-uv-review'\nversion='0.1.0'\nrequires-python='>=3.9'\n[dependency-groups]\npytest=[]\n")
		environment["UV_PROJECT_ENVIRONMENT"] = filepath.Dir(filepath.Dir(python))
	} else if mode == "disabled autoload" {
		environment["PYTEST_DISABLE_PLUGIN_AUTOLOAD"] = "1"
	} else if strings.HasPrefix(mode, "pythonpath ") {
		require.NoError(t, os.Mkdir("src", 0755))
		require.NoError(t, os.Remove("app.py"))
		requireWriteFile(t, "src/app.py", "from pathlib import Path\nfrom external_dependency import EXTRA\ndef add(a,b):\n    Path(__file__).with_suffix('.executed').write_text('ran')\n    return a+b+EXTRA\n")
		requireWriteFile(t, "pytest.ini", "[pytest]\n")
		external := t.TempDir()
		requireWriteFile(t, filepath.Join(external, "external_dependency.py"), "EXTRA=0\n")
		path := filepath.Join(root, "src") + string(os.PathListSeparator) + external
		if mode == "pythonpath ambient" {
			t.Setenv("PYTHONPATH", path)
		} else {
			environment["PYTHONPATH"] = path
		}
	} else if mode != "direct" {
		args = append([]string{mode}, args...)
		contents, err := os.ReadFile(file)
		require.NoError(t, err)
		requireWriteFile(t, file, string(contents)+"    import sys\n    assert sys.flags.ignore_environment\n")
	}
	environment["PYTEST_ADDOPTS"] = addopts
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), command: command, args: args, executor: &ext.DefaultCommandExecutor{}, startIntake: func(dir string, scenario intake.Scenario) (localIntake, error) {
		return intake.StartScenario(dir, scenario)
	}}
	drive.preflight = drive.frameworkPreflight()
	var output bytes.Buffer
	result, err := drive.runPreparedConfiguration(t.Context(), &output, &configurationResult{TestCommand: onboard.TestCommand{Directory: ".", Environment: environment}})
	require.NoError(t, err, output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, output.String())
	require.Len(t, result.Features, 6)
	if strings.HasPrefix(mode, "activation ") {
		for _, run := range result.Runs {
			if run.Role == roleBaseline {
				require.Zero(t, run.TestEventCount)
			}
		}
	}
	for _, feature := range result.Features {
		require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
	}
	require.NoFileExists(t, filepath.Join(root, "tests", "test_app.executed"))
	if strings.HasPrefix(mode, "pythonpath ") {
		require.NoFileExists(t, filepath.Join(root, "src", "app.executed"))
	}
	if strings.HasPrefix(mode, "native ") {
		require.NoFileExists(t, filepath.Join(root, "src", "app.executed"))
		require.NoDirExists(t, filepath.Join(root, "suite-cache"))
	}
	if configuration != "" {
		contents, err := os.ReadFile(configuration)
		require.NoError(t, err)
		require.Equal(t, configuredContents, string(contents))
	}
	if mode == "argument files" {
		contents, err := os.ReadFile("nested-args.txt")
		require.NoError(t, err)
		require.Equal(t, "-c\n"+filepath.Join(root, "pytest.ini")+"\n"+file+"::test_app\n", string(contents))
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
