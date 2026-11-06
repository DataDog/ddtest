// Package pytestpaths validates real pytest commands from end to end, and
// checks that each validation leaves the original checkout untouched. Every
// case starts Python several times, so these cases hold most of testdrive's
// test time: they live in their own packages so that CI can spread them over
// its shards by package, and Datadog can backfill the coverage of skipped tests.
package pytestpaths

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/DataDog/ddtest/internal/testutil"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
)

// Main runs a package's cases with a temporary directory of their own, which it
// removes afterwards: the processes of a validation leave directories there.
func Main(m *testing.M) {
	if os.Getenv("DDTEST_ISOLATED_TEST_TMP") == "1" {
		os.Exit(m.Run())
	}
	root, err := os.MkdirTemp("", "ddtest-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("DDTEST_ISOLATED_TEST_TMP", "1")
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		_ = os.Setenv(key, root)
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

// Check validates the pytest command that mode describes, in a new repository.
func Check(t *testing.T, mode string) {
	t.Helper()
	python := testutil.PytestPython(t)
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
	coverageDataPath := ""
	var coverageData []byte
	if mode == "ignored config" {
		legacy := filepath.Join(root, "tests", "legacy")
		require.NoError(t, os.Mkdir(legacy, 0755))
		requireWriteFile(t, filepath.Join(legacy, "pytest.ini"), "[pytest]\naddopts=--ddtrace\n")
		args = []string{"-m", "pytest", "--ignore", legacy, file + "::test_app"}
	} else if strings.HasPrefix(mode, "logging ") {
		log := filepath.Join(root, "pytest log.txt")
		configuration = strings.TrimPrefix(mode, "logging ")
		configuredContents = "[pytest]\npythonpath=.\nlog_file=" + log + "\n"
		switch configuration {
		case "pytest.ini", "tox.ini":
		case "pyproject.toml":
			configuredContents = fmt.Sprintf("[tool.pytest.ini_options]\npythonpath=['.']\nlog_file=%q\n", log)
		default:
			configuration, configuredContents = "pytest.ini", "[pytest]\npythonpath=.\n"
			switch mode {
			case "logging command override":
				args = append(args, "-o", "log_file="+log)
			case "logging environment override":
				addopts += " " + shellquote.Join("--override-ini=log_file="+log)
			default:
				args = append(args, "--log-file="+log)
			}
		}
		requireWriteFile(t, configuration, configuredContents)
		args[3] = filepath.Join(root, configuration)
		requireWriteFile(t, file, "from pathlib import Path\nimport logging\nfrom app import add\ndef test_app(request):\n    log = request.config.getoption('log_file') or request.config.getini('log_file')\n    assert Path(log) == Path.cwd() / 'pytest log.txt'\n    logging.warning('copied suite')\n    assert add(1, 2) == 3\n")
	} else if strings.HasPrefix(mode, "coverage ") {
		if output, err := exec.CommandContext(t.Context(), python, "-c", "import pytest_cov").CombinedOutput(); err != nil {
			if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" {
				t.Fatalf("pytest-cov required: %s", output)
			}
			t.Skipf("pytest-cov required: %s", output)
		}
		requireWriteFile(t, ".coveragerc", "[run]\nbranch = True\n")
		options := []string{"--cov=" + root, "--cov-config=" + filepath.Join(root, ".coveragerc"), "--cov-report=xml:" + filepath.Join(root, "coverage.xml"), "--cov-report", "json:" + filepath.Join(root, "coverage.json"), "--cov-fail-under=1"}
		switch mode {
		case "coverage command":
			args = append(args, options...)
		case "coverage environment":
			addopts += " " + shellquote.Join(options...)
		case "coverage configuration":
			configuration, configuredContents = "pytest.ini", "[pytest]\npythonpath=.\naddopts="+shellquote.Join(options...)+"\n"
			requireWriteFile(t, configuration, configuredContents)
		case "coverage inline override":
			args = append(args, "-o", "addopts="+shellquote.Join(options...))
		case "coverage ambient data", "coverage configured data", "coverage unset data":
			args = append(args, options...)
			coverageDataPath = filepath.Join(root, "coverage data.sqlite")
			output, err := exec.CommandContext(t.Context(), python, "-c", "import coverage, sys; data=coverage.CoverageData(basename=sys.argv[1]); data.add_lines({'app.py': {1, 2}}); data.write()", coverageDataPath).CombinedOutput()
			require.NoError(t, err, string(output))
			coverageData, err = os.ReadFile(coverageDataPath)
			require.NoError(t, err)
			t.Setenv("COVERAGE_FILE", coverageDataPath)
			switch mode {
			case "coverage configured data":
				t.Setenv("COVERAGE_FILE", filepath.Join(root, "unused-coverage.sqlite"))
				environment["COVERAGE_FILE"] = coverageDataPath
			case "coverage unset data":
				environment["COVERAGE_FILE"] = ext.UnsetEnvironment
			}
		}
		requireWriteFile(t, file, "from pathlib import Path\nfrom app import add\ndef test_app(request):\n    options = request.config.option\n    assert options.cov_source == [str(Path.cwd())]\n    assert Path(options.cov_config) == Path.cwd() / '.coveragerc'\n    assert Path(options.cov_report['xml']) == Path.cwd() / 'coverage.xml'\n    assert Path(options.cov_report['json']) == Path.cwd() / 'coverage.json'\n    assert add(1, 2) == 3\n")
	} else if strings.HasPrefix(mode, "activation ") {
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
		if strings.HasSuffix(mode, "m") {
			// A clustered -Im replaces -m.
			args = append([]string{mode}, args[2:]...)
		}
		contents, err := os.ReadFile(file)
		require.NoError(t, err)
		requireWriteFile(t, file, string(contents)+"    import sys\n    assert sys.flags.ignore_environment\n")
	}
	environment["PYTEST_ADDOPTS"] = addopts
	var output bytes.Buffer
	result, err := testdrive.ValidatePytestCommand(t.Context(), &output, root, command, args, environment)
	require.NoError(t, err, output.String())
	require.Equal(t, "compatible", result.Compatibility.Status, output.String())
	require.Len(t, result.Features, 6)
	if strings.HasPrefix(mode, "activation ") {
		for _, run := range result.Runs {
			if run.Role == "baseline" {
				require.Zero(t, run.TestEventCount)
			}
		}
	}
	for _, feature := range result.Features {
		require.Equal(t, "passed", feature.Status, "%s: %s", feature.Name, feature.Reason)
	}
	require.NoFileExists(t, filepath.Join(root, "tests", "test_app.executed"))
	if strings.HasPrefix(mode, "logging ") {
		require.NoFileExists(t, filepath.Join(root, "pytest log.txt"))
	}
	if strings.HasPrefix(mode, "coverage ") {
		require.NoFileExists(t, filepath.Join(root, "coverage.xml"))
		require.NoFileExists(t, filepath.Join(root, "coverage.json"))
		if coverageDataPath != "" {
			after, err := os.ReadFile(coverageDataPath)
			require.NoError(t, err)
			require.Equal(t, coverageData, after, "feature probes must not overwrite existing suite coverage")
			require.NoFileExists(t, filepath.Join(root, "unused-coverage.sqlite"))
		}
	}
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

func requireWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
}
