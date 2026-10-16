package compatibility

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

// Compare the static inventory with the actual tracer, including both
// pytest's node IDs and ddtrace's parameter serialization. Runs in the existing
// pytest/ddtrace compatibility matrix rather than copying their implementation
// into the expected results.
func TestPyTestStaticDiscoveryParity(t *testing.T) {
	python := requireEnv(t, "DDTEST_PYTHON_BINARY")
	resetSettingsAfterTest(t)
	root := t.TempDir()
	writeFixture(t, root, "pytest.ini", "[pytest]\naddopts = -q\n")
	writeFixture(t, root, "side_effect.py", "from pathlib import Path\nPath('imported-test-module').touch()\n")
	writeFixture(t, root, "test_root.py", "def test_root(): pass\n")
	writeFixture(t, root, "tests/test_excluded.py", "raise AssertionError('excluded file imported')\n")
	writeFixture(t, root, "tests/test_cases.py", `import pytest
import side_effect

def test_plain(): pass

class TestOuter:
    def test_method(self): pass
    class TestInner:
        def test_nested(self): pass

@pytest.mark.parametrize("x", [0, -1, 1.5, True, False, None, "", "hello", "café", "line\nbreak", [1, 2], {"b": 2, "a": 1}])
def test_values(x): pass

@pytest.mark.parametrize("x", [1, 1, 2, 2, 2])
def test_duplicate_values(x): pass

@pytest.mark.parametrize("x", [1, 2, 3], ids=["same", "same", "same0"])
def test_duplicate_ids(x): pass

@pytest.mark.parametrize("x", [1, 2, 3], ids=["café", None, ""])
def test_explicit_ids(x): pass

@pytest.mark.parametrize("x, y", [(1, True), (2, None), pytest.param(3, False, id="café")])
def test_rows(x, y): pass

@pytest.mark.parametrize(["x"], [(1,), (2,)])
def test_sequence_names(x): pass

@pytest.mark.parametrize("x", [1, 2])
@pytest.mark.parametrize("y", ["a", "b"])
def test_stacked(x, y): pass

@pytest.mark.parametrize("x", ["object at 0x123ABC", (1, "a")])
def test_repr(x): pass
`)
	t.Chdir(root)
	t.Setenv("PYTEST_ADDOPTS", "--ddtrace -vv --tb=short")
	t.Setenv("PYTEST_PLUGINS", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
	configureFramework(shellCommand(python, "-m", "pytest", "-q"), "")
	pytest := framework.NewPytest(platform.NewPython())
	files, err := discovery.ResolveTestFiles("**/test_*.py", "tests/test_excluded.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const staticDiscoveryEnv = "DD_TEST_OPTIMIZATION_RUNNER_PYTEST_STATIC_DISCOVERY"
	t.Setenv(staticDiscoveryEnv, "false")
	collected, err := pytest.DiscoverTests(ctx, files)
	if err != nil || len(collected) == 0 {
		t.Fatalf("tracer discovery: %v (%d tests)", err, len(collected))
	}
	if err := os.Remove("imported-test-module"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(staticDiscoveryEnv, "true")
	static, err := pytest.DiscoverTests(ctx, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("imported-test-module"); !os.IsNotExist(err) {
		t.Fatalf("static discovery imported a test dependency or fell back: %v", err)
	}
	if _, err := os.Stat(discovery.TestsFilePath); !os.IsNotExist(err) {
		t.Fatalf("static discovery fell back to tracer collection: %v", err)
	}
	compare := func(a, b testoptimization.Test) int { return strings.Compare(a.DatadogTestId(), b.DatadogTestId()) }
	slices.SortFunc(collected, compare)
	slices.SortFunc(static, compare)
	if !reflect.DeepEqual(static, collected) {
		for i := range min(len(static), len(collected)) {
			if static[i] != collected[i] {
				t.Errorf("test %d: static %#v; collected %#v", i, static[i], collected[i])
			}
		}
		t.Fatalf("inventories differ (static %d, collected %d tests)", len(static), len(collected))
	}

	// Exercise the actual console-script interpreter and the existing --
	// selection replacement, not just the python -m pytest command form.
	configureFramework(shellCommand(filepath.Join(filepath.Dir(python), "pytest"), "-q", "--", "discarded.py"), "")
	console := framework.NewPytest(platform.NewPython())
	consoleTests, err := console.DiscoverTests(ctx, files)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(consoleTests, compare)
	if !reflect.DeepEqual(consoleTests, static) {
		t.Fatal("console-script static discovery differs from python -m pytest")
	}
	if _, err := os.Stat(discovery.TestsFilePath); !os.IsNotExist(err) {
		t.Fatalf("console-script discovery fell back to pytest: %v", err)
	}
}

func TestPyTestAdapterIntegration(t *testing.T) {
	python := requireEnv(t, "DDTEST_PYTHON_BINARY")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	writeFixture(t, root, "pytest.ini", `[pytest]
testpaths = checks
python_files = check_*.py
`)
	writeFixture(t, root, "checks/check_selected.py", `import os

def test_preserves_worker_environment():
    assert os.environ["DDTEST_PYTEST_WORKER"] == "selected"
`)
	writeFixture(t, root, "checks/check_unselected.py", `def test_must_not_run():
    raise AssertionError("unselected file ran")
`)
	t.Chdir(root)

	configureFramework(shellCommand(python, "-m", "pytest"), "")
	pytest := framework.NewPytest(platform.NewPython())
	t.Setenv("PYTEST_ADDOPTS", "--ddtrace")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: pytest.TestPattern()}
	files, err := pytest.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"checks/check_selected.py", "checks/check_unselected.py"}
	requireFiles(t, files, wantFiles)

	tests, err := pytest.DiscoverTests(ctx, testFiles)
	if err != nil {
		t.Fatalf("full discovery failed: %v", err)
	}
	requireTestSources(t, tests, wantFiles)

	if err := pytest.RunTests(ctx, []string{"checks/check_selected.py"}, map[string]string{"DDTEST_PYTEST_WORKER": "selected"}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
