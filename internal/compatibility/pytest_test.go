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
	writeFixture(t, root, "pytest.ini", "[pytest]\naddopts = -q\npython_functions = test_* check_*\n")
	writeFixture(t, root, "side_effect.py", "from pathlib import Path\nPath('imported-test-module').touch()\n")
	writeFixture(t, root, "test_root.py", "def test_root(): pass\n")
	writeFixture(t, root, "tests/test_excluded.py", "raise AssertionError('excluded file imported')\n")
	writeFixture(t, root, "tests/conftest.py", `import pytest

CONF_CONST = ["c1", "c2"]

@pytest.fixture
def plain_conf(): return 1

@pytest.fixture(params=["p1", "p2"])
def param_conf(request): return request.param

@pytest.fixture(params=CONF_CONST)
def const_conf(request): return request.param

@pytest.fixture(params=[1, 2], ids=["uno", "dos"])
def ids_conf(request): return request.param

@pytest.fixture(name="conf_renamed")
def _conf_renamed(): return 1

@pytest.fixture(params=["u1", "u2"])
def use_fixture(request): return request.param
`)
	writeFixture(t, root, "tests/test_cases.py", `import pytest
import side_effect
from _pytest import monkeypatch

def check_collected(): pass

def test_builtin_fixtures(tmp_path, monkeypatch, capsys): pass

def test_unknown_fixture(nonexistent): pass

@pytest.fixture
def plain_local(): return 2

def test_local_fixture(plain_local): pass

@pytest.fixture(params=["s1", "s2"])
def local_param(request): return request.param

def test_local_param(local_param): pass

@pytest.fixture(params=["au1", "au2"], autouse=True)
def auto_f(request): return request.param

def test_autouse_only(): pass

@pytest.mark.parametrize("z", [1, 2])
def test_autouse_param(z): pass

def test_conftest_plain(plain_conf): pass

def test_conftest_param(param_conf): pass

def test_conftest_const(const_conf): pass

def test_conftest_ids(ids_conf): pass

def test_renamed(conf_renamed): pass

@pytest.mark.parametrize("p", [10, 20])
def test_combo(p, param_conf): pass

@pytest.fixture(params=[9])
def param_conf(request): return request.param

def test_local_shadow(param_conf): pass

@pytest.mark.parametrize("marked_local", [9])
def test_param_over_fixture(marked_local): pass

CONST = [1, 2]
ANN: list[int] = [3, 4]
DICT_BASE = {"a": 1}

@pytest.mark.parametrize("x", CONST)
def test_constant(x): pass

@pytest.mark.parametrize("y", ANN)
def test_annotated(y): pass

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

@pytest.mark.skip
def test_skip(): pass

@pytest.mark.skipif(False, reason="never")
def test_skipif_false(): pass

@pytest.mark.skipif(True, reason="always")
def test_skipif_true(): pass

@pytest.mark.xfail
def test_xfail(): pass

@pytest.mark.slow
def test_inert_mark(): pass

@pytest.mark.skip
@pytest.mark.parametrize("x", [1, 2])
def test_param_skip(x): pass

@pytest.mark.parametrize("x", [pytest.param(1, marks=pytest.mark.skip), 2])
def test_param_mark_skip(x): pass

@pytest.mark.parametrize("x", [pytest.param(1, id="dup", marks=pytest.mark.skip), pytest.param(2, id="dup")])
def test_dup_skip(x): pass

@pytest.mark.skip
class TestSkipClass:
    def test_a(self): pass
    def test_b(self): pass

class TestHalfSkip:
    @pytest.mark.skip
    def test_skipped_method(self): pass
    def test_ok_method(self): pass

class TestClsMark:
    pytestmark = pytest.mark.skip
    def test_c(self): pass

class Base:
    def test_inherited(self): pass

class TestDerived(Base):
    def test_own(self): pass

class MixinBase:
    @pytest.mark.parametrize("x", [1, 2])
    def test_inherited_param(self, x): pass

class TestWithParam(MixinBase):
    def test_own2(self): pass

class TestStatic:
    @staticmethod
    def test_staticmethod(): pass
    @classmethod
    def test_classmethod(cls): pass

class TestVarargs:
    def test_varargs(self, *args): pass

class TestInit:
    def __init__(self): pass
    def test_not_collected(self): pass

class TestClassFixture:
    @pytest.fixture
    def cls_fixture(self): return 1
    def test_cls_fixture(self, cls_fixture): pass
    @pytest.fixture(params=["m1", "m2"])
    def cls_param(self, request): return request.param
    def test_cls_param(self, cls_param): pass

@pytest.mark.parametrize("x", [])
def test_empty(x): pass

class TestOff:
    __test__ = False
    def test_base_off(self): pass

class TestOn(TestOff):
    __test__ = True
    def test_on(self): pass

class TestBaseNest:
    class TestNested:
        def test_nested(self): pass

class TestDerivedNest(TestBaseNest):
    def test_own_nest(self): pass

@pytest.mark.usefixtures("use_fixture")
def test_usefixtures(): pass

@pytest.mark.parametrize("u", [1, 2])
@pytest.mark.usefixtures("use_fixture")
def test_usefixtures_and_param(u): pass

class MarkedBase:
    pytestmark = pytest.mark.skip
    def test_hidden(self): pass

class TestDerivedSkip(MarkedBase):
    def test_derived_own(self): pass

@pytest.mark.parametrize("d", [{**DICT_BASE, "b": 2}])
def test_dict_unpack(d): pass

@pytest.mark.parametrize("l", [[*[3, 4], 5]])
def test_list_unpack(l): pass
`)
	t.Chdir(root)
	t.Setenv("PYTEST_ADDOPTS", "--ddtrace -vv --tb=short -p no:cacheprovider")
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
