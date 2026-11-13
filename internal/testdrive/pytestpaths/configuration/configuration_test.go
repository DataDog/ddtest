package configuration

import (
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/pytestpaths"
)

func TestMain(m *testing.M) { pytestpaths.Main(m) }

func TestPytestLoggingExecutesOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"logging pytest.ini", "logging tox.ini", "logging pyproject.toml", "logging command override", "logging environment override", "logging command option"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestCoverageExecutesOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"coverage command", "coverage environment", "coverage configuration", "coverage inline override"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestCoverageDataExecutesOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"coverage ambient data", "coverage configured data", "coverage unset data"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestNativeConfigurationPathsExecuteOnlyInsideCopy(t *testing.T) {
	for _, mode := range []string{"native pytest.ini", "native pyproject.toml", "native command override", "native environment override"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}

func TestPytestConfiguredActivationIsControlled(t *testing.T) {
	for _, mode := range []string{"activation pytest.ini", "activation tox.ini", "activation pyproject.toml", "activation command override", "activation environment override"} {
		t.Run(mode, func(t *testing.T) { pytestpaths.Check(t, mode) })
	}
}
