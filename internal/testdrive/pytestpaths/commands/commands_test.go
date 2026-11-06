package commands

import (
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/pytestpaths"
)

func TestMain(m *testing.M) { pytestpaths.Main(m) }

func TestPytestIgnoredConfigurationUsesRoot(t *testing.T) {
	pytestpaths.Check(t, "ignored config")
}

func TestPytestAbsolutePathsExecuteOnlyInsideCopy(t *testing.T) {
	pytestpaths.Check(t, "direct")
}

func TestPytestEqualsConfigPathExecutesOnlyInsideCopy(t *testing.T) {
	pytestpaths.Check(t, "equals config")
}

func TestPytestArgumentFilesExecuteOnlyInsideCopy(t *testing.T) {
	pytestpaths.Check(t, "argument files")
}

func TestPytestConfiguredArgumentFilesExecuteOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"config pytest.ini", "config tox.ini", "config pyproject.toml", "inline override", "environment override"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestInheritedEndOfOptions(t *testing.T) {
	for _, mode := range []string{"environment separator", "config separator"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestControlledModeWithDisabledAutoload(t *testing.T) {
	pytestpaths.Check(t, "disabled autoload")
}

func TestPytestObserverWithEnvironmentIgnored(t *testing.T) {
	for _, flag := range []string{"-I", "-E", "-bE", "-Im", "-IBum"} {
		t.Run(flag, func(t *testing.T) { pytestpaths.Check(t, flag) })
	}
}

func TestPytestUVDirectoryExecutesOnlyInsideCopy(t *testing.T) {
	for _, option := range []string{"--directory", "--project", "uv pytest group"} {
		t.Run(option, func(t *testing.T) { pytestpaths.Check(t, option) })
	}
}

func TestPytestApplicationPythonPathIsIsolated(t *testing.T) {
	for _, source := range []string{"ambient", "configured"} {
		t.Run(source, func(t *testing.T) { pytestpaths.Check(t, "pythonpath "+source) })
	}
}
