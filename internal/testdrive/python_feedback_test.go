package testdrive

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/onboard"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPythonFeedbackPreflightRejectsWrapperExecutables(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"python-wrapper", []string{"-m", "pytest"}},
		{"uv", []string{"run", "python-wrapper", "-m", "pytest"}},
		{"poetry", []string{"run", "python3.12-helper", "-m", "pytest"}},
	} {
		_, _, err := pythonProbeCommand(tc.command, tc.args)
		require.Error(t, err, "must not execute unknown interpreter wrapper %s", tc.command)
	}
}

func TestPythonFeedbackAllChecksDoNotExecuteWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	wrapper := filepath.Join(root, "python-wrapper")
	marker := filepath.Join(root, "WRAPPER_RAN")
	requireWriteFile(t, wrapper, "#!/bin/sh\nprintf called > '"+marker+"'\nexit 99\n")
	require.NoError(t, os.Chmod(wrapper, 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".github/workflows"), 0755))
	requireWriteFile(t, filepath.Join(root, ".github/workflows/test.yml"), "jobs:\n  tests:\n    steps:\n      - run: "+wrapper+" -m pytest\n")
	drive := &Testdrive{repositoryRoot: root, language: "python", framework: framework.NewPytest(platform.NewPython()), checkOnly: true, tracerVersion: "4.16.0"}
	require.Error(t, drive.runAllConfigurations(t.Context(), io.Discard))
	require.NoFileExists(t, marker)
	report := readValidationReport(t, root)
	require.NotEmpty(t, report.Scope.Unresolved)
	require.Empty(t, report.Configurations)
}

func TestPythonFeedbackPreflightPreservesNestedCIScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	python := filepath.Join(root, "python")
	metadata, err := json.Marshal(map[string]string{"Pytest": "8.4.2", "Python": "3.12.8", "Tracer": "4.16.0", "RequiresPython": ">=3.9", "Executable": python})
	require.NoError(t, err)
	requireWriteFile(t, python, "#!/bin/sh\nfor last; do :; done\nprintf '%s' '"+string(metadata)+"' > \"$last\"\n")
	require.NoError(t, os.Chmod(python, 0755))
	drive := &Testdrive{repositoryRoot: root, language: "python", command: python, args: []string{"-m", "pytest"}}
	check := onboard.RuntimeCheck{Status: onboard.StatusInconclusive, Reason: "Checkout CI was not inspected", Review: []onboard.RuntimeFinding{{Code: onboard.CodeNestedCIScope, Status: onboard.StatusNotExercised}}}
	result := validationResult{CIRuntime: &check, CheckOnly: true}
	require.NoError(t, drive.checkPytestPreflight(t.Context(), &bytes.Buffer{}, &result))
	require.Equal(t, onboard.StatusInconclusive, result.CIRuntime.Status)
	require.Equal(t, check.Reason, result.CIRuntime.Reason)
	require.Len(t, result.CIRuntime.Review, 1)
	require.Equal(t, onboard.CodeNestedCIScope, result.CIRuntime.Review[0].Code)
	prepareValidationResult(root, &result)
	require.False(t, result.ChecksPassed)
}

func TestPythonFeedbackProbeRecoveryAfterAbruptExit(t *testing.T) {
	if root := os.Getenv("DDTEST_PYTEST_PROBE_CRASH_ROOT"); root != "" {
		source := filepath.Join(root, "test_app.py")
		session := planSession()
		session.directory = filepath.Join(root, "session")
		drive := &Testdrive{repositoryRoot: root, language: "python", startIntake: func(string, intake.Scenario) (localIntake, error) { os.Exit(0); return nil, nil }}
		err := drive.runPytestFeatures(t.Context(), io.Discard, session, "", validationRun{Tests: []jestTest{{File: source, Status: "passed"}}}, &validationResult{})
		t.Fatalf("expected abrupt exit with published probe: %v", err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "test_app.py")
	requireWriteFile(t, source, "def test_app():\n    assert True\n")
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestPythonFeedbackProbeRecoveryAfterAbruptExit$")
	child.Env = append(os.Environ(), "DDTEST_PYTEST_PROBE_CRASH_ROOT="+root)
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
	probes, err := filepath.Glob(filepath.Join(root, "ddtest-pytest-*/test_ddtest_probe.py"))
	require.NoError(t, err)
	require.Len(t, probes, 1)
	require.NoError(t, (&Testdrive{repositoryRoot: root}).recoverStartup(io.Discard))
	require.NoFileExists(t, probes[0], "startup must recover the probe after deferred cleanup was skipped")
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "def test_app():\n    assert True\n", string(data))
}
