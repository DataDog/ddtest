package testutil

import (
	"os"
	"os/exec"
	"testing"
)

// PytestPython returns the Python that real pytest validations use. A required
// runtime that is missing fails the test; otherwise it skips.
func PytestPython(t *testing.T) string {
	t.Helper()
	python := os.Getenv("DDTEST_PYTEST_PYTHON")
	if python == "" {
		python, _ = exec.LookPath("python3")
	}
	if python == "" {
		t.Skip("Python required")
	}
	probe := exec.CommandContext(t.Context(), python, "-c", `import pytest, ddtrace; from packaging.version import Version; assert Version(ddtrace.__version__) >= Version("4.11.0") and Version(pytest.__version__) >= Version("7.2")`)
	// This process only checks installed versions. The real validation processes
	// still exercise telemetry and all feature controls against the local intake.
	probe.Env = append(os.Environ(), "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false", "DD_TRACE_ENABLED=false", "DD_CIVISIBILITY_ENABLED=false")
	if output, err := probe.CombinedOutput(); err != nil {
		if os.Getenv("DDTEST_REQUIRE_PROJECT_RUNTIMES") == "1" || os.Getenv("DDTEST_PYTEST_PYTHON") != "" {
			t.Fatalf("pytest and ddtrace required: %s: %v", output, err)
		}
		t.Skip("pytest and ddtrace required")
	}
	return python
}
