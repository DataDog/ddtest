package framework

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

const pytestStaticEnv = "DD_TEST_OPTIMIZATION_RUNNER_PYTEST_STATIC_DISCOVERY"

func staticPython(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("static pytest discovery tests require python3")
	}
	return python
}

func staticPytestFixture(t *testing.T, source string) *PyTest {
	t.Helper()
	python := staticPython(t)
	t.Chdir(t.TempDir())
	t.Setenv("PYTEST_ADDOPTS", "")
	t.Setenv("PYTEST_PLUGINS", "")
	t.Setenv("PYTHONPATH", "")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	if err := os.Mkdir("tests", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("tests/test_sample.py", []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	return &PyTest{executor: &ext.DefaultCommandExecutor{}, commandOverride: []string{python, "-m", "pytest"}}
}

func TestPytestStaticInventory(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
		want   []testoptimization.Test
	}{
		{
			name: "plain and nested classes",
			source: `def test_plain(): pass

def testwithoutunderscore(): pass

class TestOuter:
    def test_method(self): pass
    class TestInner:
        def test_nested(self): pass
`,
			want: []testoptimization.Test{{Name: "test_plain"}, {Name: "testwithoutunderscore"}, {Name: "TestOuter::test_method"}, {Name: "TestOuter::TestInner::test_nested"}},
		},
		{
			name: "literal parametrization",
			source: `import pytest
@pytest.mark.parametrize("value", [1, "hello", None, [1, 2]])
def test_case(value): pass
`,
			want: []testoptimization.Test{
				{Name: "test_case[1]", Parameters: `{"arguments": {"value": "1"}, "metadata": {}}`},
				{Name: "test_case[hello]", Parameters: `{"arguments": {"value": "'hello'"}, "metadata": {}}`},
				{Name: "test_case[None]", Parameters: `{"arguments": {"value": "None"}, "metadata": {}}`},
				{Name: "test_case[value3]", Parameters: `{"arguments": {"value": "[1, 2]"}, "metadata": {}}`},
			},
		},
		{
			name: "multiple arguments explicit ids and pytest param",
			source: `import pytest
@pytest.mark.parametrize("x, y", [(1, True), pytest.param(2, False, id="named")], ids=["first", None])
def test_case(x, y): pass
`,
			want: []testoptimization.Test{
				{Name: "test_case[first]", Parameters: `{"arguments": {"x": "1", "y": "True"}, "metadata": {}}`},
				{Name: "test_case[named]", Parameters: `{"arguments": {"x": "2", "y": "False"}, "metadata": {}}`},
			},
		},
		{
			name: "stacked decorators",
			source: `import pytest
@pytest.mark.parametrize("x", [1, 2])
@pytest.mark.parametrize("y", [False, True])
def test_case(x, y): pass
`,
			want: []testoptimization.Test{
				{Name: "test_case[False-1]", Parameters: `{"arguments": {"x": "1", "y": "False"}, "metadata": {}}`},
				{Name: "test_case[False-2]", Parameters: `{"arguments": {"x": "2", "y": "False"}, "metadata": {}}`},
				{Name: "test_case[True-1]", Parameters: `{"arguments": {"x": "1", "y": "True"}, "metadata": {}}`},
				{Name: "test_case[True-2]", Parameters: `{"arguments": {"x": "2", "y": "True"}, "metadata": {}}`},
			},
		},
		{
			name: "empty and duplicate ids",
			source: `import pytest
@pytest.mark.parametrize("x", ["", 1, 1])
def test_case(x): pass
`,
			want: []testoptimization.Test{
				{Name: "test_case[]", Parameters: `{"arguments": {"x": "''"}, "metadata": {}}`},
				{Name: "test_case[1_0]", Parameters: `{"arguments": {"x": "1"}, "metadata": {}}`},
				{Name: "test_case[1_1]", Parameters: `{"arguments": {"x": "1"}, "metadata": {}}`},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, tt.source)
			tests, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{Pattern: "tests/**/*.py"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := range tt.want {
				tt.want[i].Module = "tests"
				tt.want[i].Suite = "test_sample.py"
				tt.want[i].SuiteSourceFile = "tests/test_sample.py"
			}
			if !reflect.DeepEqual(tests, tt.want) {
				t.Fatalf("inventory = %#v; want %#v", tests, tt.want)
			}
		})
	}
}

func writeStaticExtras(t *testing.T, conftest, config string) {
	t.Helper()
	if conftest != "" {
		if err := os.WriteFile(filepath.Join("tests", "conftest.py"), []byte(conftest), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if config != "" {
		if err := os.WriteFile("pytest.ini", []byte(config), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPytestStaticCollectionInventory(t *testing.T) {
	for _, tt := range []struct {
		name     string
		source   string
		conftest string
		config   string
		want     []testoptimization.Test
	}{
		{
			name: "plain and unknown fixtures",
			source: `import pytest
@pytest.fixture
def plain(): pass

def test_builtin(tmp_path, monkeypatch, capsys): pass

def test_unknown(nonexistent): pass

def test_plain(plain): pass
`,
			want: []testoptimization.Test{{Name: "test_builtin"}, {Name: "test_unknown"}, {Name: "test_plain"}},
		},
		{
			name: "parametrized fixtures",
			source: `import pytest
@pytest.fixture(params=["a", "b"])
def thing(request): return request.param

def test_thing(thing): pass

@pytest.fixture(params=[1, 2], ids=["uno", "dos"])
def named(request): return request.param

def test_named(named): pass

@pytest.fixture(name="renamed")
def _underlying(): pass

def test_renamed(renamed): pass

@pytest.mark.parametrize("p", [10, 20])
def test_combo(p, thing): pass
`,
			want: []testoptimization.Test{
				{Name: "test_thing[a]", Parameters: `{"arguments": {"thing": "'a'"}, "metadata": {}}`},
				{Name: "test_thing[b]", Parameters: `{"arguments": {"thing": "'b'"}, "metadata": {}}`},
				{Name: "test_named[uno]", Parameters: `{"arguments": {"named": "1"}, "metadata": {}}`},
				{Name: "test_named[dos]", Parameters: `{"arguments": {"named": "2"}, "metadata": {}}`},
				{Name: "test_renamed"},
				{Name: "test_combo[a-10]", Parameters: `{"arguments": {"p": "10", "thing": "'a'"}, "metadata": {}}`},
				{Name: "test_combo[a-20]", Parameters: `{"arguments": {"p": "20", "thing": "'a'"}, "metadata": {}}`},
				{Name: "test_combo[b-10]", Parameters: `{"arguments": {"p": "10", "thing": "'b'"}, "metadata": {}}`},
				{Name: "test_combo[b-20]", Parameters: `{"arguments": {"p": "20", "thing": "'b'"}, "metadata": {}}`},
			},
		},
		{
			name: "module autouse parametrized fixture",
			source: `import pytest
@pytest.fixture(params=["u1", "u2"], autouse=True)
def auto_f(request): return request.param

def test_alone(): pass

@pytest.mark.parametrize("x", [1])
def test_with(x): pass
`,
			want: []testoptimization.Test{
				{Name: "test_alone[u1]", Parameters: `{"arguments": {"auto_f": "'u1'"}, "metadata": {}}`},
				{Name: "test_alone[u2]", Parameters: `{"arguments": {"auto_f": "'u2'"}, "metadata": {}}`},
				{Name: "test_with[u1-1]", Parameters: `{"arguments": {"auto_f": "'u1'", "x": "1"}, "metadata": {}}`},
				{Name: "test_with[u2-1]", Parameters: `{"arguments": {"auto_f": "'u2'", "x": "1"}, "metadata": {}}`},
			},
		},
		{
			name:     "conftest fixtures",
			conftest: "import pytest\n\n@pytest.fixture\ndef plain_conf(): pass\n\n@pytest.fixture(params=[\"p1\", \"p2\"])\ndef param_conf(request): return request.param\n",
			source: `def test_conf_param(param_conf): pass

def test_conf_plain(plain_conf): pass
`,
			want: []testoptimization.Test{
				{Name: "test_conf_param[p1]", Parameters: `{"arguments": {"param_conf": "'p1'"}, "metadata": {}}`},
				{Name: "test_conf_param[p2]", Parameters: `{"arguments": {"param_conf": "'p2'"}, "metadata": {}}`},
				{Name: "test_conf_plain"},
			},
		},
		{
			name:     "local fixture shadows conftest",
			conftest: "import pytest\n\n@pytest.fixture(params=[\"p1\", \"p2\"])\ndef param_conf(request): return request.param\n",
			source: `import pytest
@pytest.fixture
def param_conf(): pass

def test_shadow(param_conf): pass
`,
			want: []testoptimization.Test{{Name: "test_shadow"}},
		},
		{
			name: "skip marks",
			source: `import pytest
@pytest.mark.skip
def test_skip(): pass

@pytest.mark.skipif(False, reason="never")
def test_skipif_false(): pass

@pytest.mark.skipif(True, reason="always")
def test_skipif_true(): pass

@pytest.mark.xfail
def test_xfail(): pass

@pytest.mark.slow
def test_inert(): pass

@pytest.mark.parametrize("x", [pytest.param(1, marks=pytest.mark.skip), 2])
def test_one_skipped(x): pass
`,
			want: []testoptimization.Test{
				{Name: "test_skipif_false"},
				{Name: "test_xfail"},
				{Name: "test_inert"},
				{Name: "test_one_skipped[2]", Parameters: `{"arguments": {"x": "2"}, "metadata": {}}`},
			},
		},
		{
			name: "local inheritance",
			source: `class Base:
    def test_inherited(self): pass

class TestDerived(Base):
    def test_own(self): pass
`,
			want: []testoptimization.Test{{Name: "TestDerived::test_inherited"}, {Name: "TestDerived::test_own"}},
		},
		{
			name: "collection details",
			source: `import pytest
CONST = [1, 2]
ANN: list = [3]

@pytest.mark.parametrize("x", CONST)
def test_const(x): pass

@pytest.mark.parametrize("y", ANN)
def test_ann(y): pass

class TestStatic:
    @staticmethod
    def test_static(): pass

    @classmethod
    def test_cls(cls): pass

class TestVarargs:
    def test_var(self, *args): pass

class TestInit:
    def __init__(self): pass
    def test_not_collected(self): pass

class TestOff:
    __test__ = False
    def test_off(self): pass

class TestClsFixture:
    @pytest.fixture(params=["m1", "m2"])
    def cls_param(self, request): return request.param
    def test_m(self, cls_param): pass

@pytest.mark.parametrize("x", [])
def test_empty(x): pass
`,
			want: []testoptimization.Test{
				{Name: "test_const[1]", Parameters: `{"arguments": {"x": "1"}, "metadata": {}}`},
				{Name: "test_const[2]", Parameters: `{"arguments": {"x": "2"}, "metadata": {}}`},
				{Name: "test_ann[3]", Parameters: `{"arguments": {"y": "3"}, "metadata": {}}`},
				{Name: "TestStatic::test_cls"},
				{Name: "TestStatic::test_static"},
				{Name: "TestVarargs::test_var"},
				{Name: "TestClsFixture::test_m[m1]", Parameters: `{"arguments": {"cls_param": "'m1'"}, "metadata": {}}`},
				{Name: "TestClsFixture::test_m[m2]", Parameters: `{"arguments": {"cls_param": "'m2'"}, "metadata": {}}`},
			},
		},
		{
			name:   "module pytestmark skip",
			source: "import pytest\npytestmark = pytest.mark.skip\n\ndef test_case(): pass\n",
			want:   []testoptimization.Test{},
		},
		{
			name:   "custom function patterns",
			config: "[pytest]\npython_functions = check_*\n",
			source: `def check_thing(): pass

def test_thing(): pass
`,
			want: []testoptimization.Test{{Name: "check_thing"}},
		},
		{
			name: "__test__ override",
			source: `class TestOff:
    __test__ = False
    def test_base(self): pass

class TestOn(TestOff):
    __test__ = True
    def test_on(self): pass
`,
			want: []testoptimization.Test{{Name: "TestOn::test_base"}, {Name: "TestOn::test_on"}},
		},
		{
			name: "inherited nested classes",
			source: `class TestBaseNest:
    class TestNested:
        def test_nested(self): pass

class TestDerivedNest(TestBaseNest):
    def test_own_nest(self): pass
`,
			want: []testoptimization.Test{
				{Name: "TestBaseNest::TestNested::test_nested"},
				{Name: "TestDerivedNest::test_own_nest"},
				{Name: "TestDerivedNest::TestNested::test_nested"},
			},
		},
		{
			name: "usefixtures marks",
			source: `import pytest
@pytest.fixture(params=[1, 2])
def backend(request): return request.param
@pytest.fixture(params=["x", "y"])
def arg_f(request): return request.param

@pytest.mark.usefixtures("backend")
def test_marked(): pass

@pytest.mark.usefixtures("backend")
def test_mark_and_arg(arg_f): pass
`,
			want: []testoptimization.Test{
				{Name: "test_marked[1]", Parameters: `{"arguments": {"backend": "1"}, "metadata": {}}`},
				{Name: "test_marked[2]", Parameters: `{"arguments": {"backend": "2"}, "metadata": {}}`},
				{Name: "test_mark_and_arg[1-x]", Parameters: `{"arguments": {"arg_f": "'x'", "backend": "1"}, "metadata": {}}`},
				{Name: "test_mark_and_arg[1-y]", Parameters: `{"arguments": {"arg_f": "'y'", "backend": "1"}, "metadata": {}}`},
				{Name: "test_mark_and_arg[2-x]", Parameters: `{"arguments": {"arg_f": "'x'", "backend": "2"}, "metadata": {}}`},
				{Name: "test_mark_and_arg[2-y]", Parameters: `{"arguments": {"arg_f": "'y'", "backend": "2"}, "metadata": {}}`},
			},
		},
		{
			name: "class pytestmark before decorators",
			source: `import pytest
@pytest.fixture(params=["d1", "d2"])
def dec_f(request): return request.param
@pytest.fixture(params=["p1", "p2"])
def pm_f(request): return request.param

@pytest.mark.usefixtures("dec_f")
class TestBothMarks:
    pytestmark = pytest.mark.usefixtures("pm_f")
    def test_both(self): pass
`,
			want: []testoptimization.Test{
				{Name: "TestBothMarks::test_both[p1-d1]", Parameters: `{"arguments": {"dec_f": "'d1'", "pm_f": "'p1'"}, "metadata": {}}`},
				{Name: "TestBothMarks::test_both[p1-d2]", Parameters: `{"arguments": {"dec_f": "'d2'", "pm_f": "'p1'"}, "metadata": {}}`},
				{Name: "TestBothMarks::test_both[p2-d1]", Parameters: `{"arguments": {"dec_f": "'d1'", "pm_f": "'p2'"}, "metadata": {}}`},
				{Name: "TestBothMarks::test_both[p2-d2]", Parameters: `{"arguments": {"dec_f": "'d2'", "pm_f": "'p2'"}, "metadata": {}}`},
			},
		},
		{
			name:   "module pytestmark usefixtures",
			source: "import pytest\npytestmark = pytest.mark.usefixtures(\"mark_f\")\n@pytest.fixture(params=[\"m1\", \"m2\"])\ndef mark_f(request): return request.param\n\ndef test_module_mark(): pass\n",
			want: []testoptimization.Test{
				{Name: "test_module_mark[m1]", Parameters: `{"arguments": {"mark_f": "'m1'"}, "metadata": {}}`},
				{Name: "test_module_mark[m2]", Parameters: `{"arguments": {"mark_f": "'m2'"}, "metadata": {}}`},
			},
		},
		{
			name: "literal unpacking",
			source: `import pytest
BASE = {"a": 1}
LIST_BASE = [1, 2]

@pytest.mark.parametrize("x", [{**BASE, "b": 2}])
def test_dict(x): pass

@pytest.mark.parametrize("y", [[*LIST_BASE, 3]])
def test_list(y): pass
`,
			want: []testoptimization.Test{
				{Name: "test_dict[x0]", Parameters: `{"arguments": {"x": "{'a': 1, 'b': 2}"}, "metadata": {}}`},
				{Name: "test_list[y0]", Parameters: `{"arguments": {"y": "[1, 2, 3]"}, "metadata": {}}`},
			},
		},
		{
			name: "inherited class pytestmark",
			source: `import pytest
class MarkedBase:
    pytestmark = pytest.mark.skip
    def test_hidden(self): pass

class TestDerivedSkip(MarkedBase):
    def test_own(self): pass
`,
			want: []testoptimization.Test{},
		},
		{
			name: "foreign-based helper classes are inert",
			source: `import datetime

class FixedOffset(datetime.tzinfo):
    def utcoffset(self, dt): pass

class MyError(Exception): pass

class Base:
    def helper(self): pass

def test_case(): pass
`,
			want: []testoptimization.Test{{Name: "test_case"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, tt.source)
			writeStaticExtras(t, tt.conftest, tt.config)
			tests, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{Pattern: "tests/**/*.py"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i := range tt.want {
				tt.want[i].Module = "tests"
				tt.want[i].Suite = "test_sample.py"
				tt.want[i].SuiteSourceFile = "tests/test_sample.py"
			}
			if !reflect.DeepEqual(tests, tt.want) {
				t.Fatalf("inventory = %#v; want %#v", tests, tt.want)
			}
		})
	}
}

func TestPytestStaticFallback(t *testing.T) {
	for _, tt := range []struct {
		name     string
		source   string
		conftest string
		config   string
	}{
		{"dynamic values", "import pytest\n@pytest.mark.parametrize('x', range(3))\ndef test_case(x): pass\n", "", ""},
		{"callable ids", "import pytest\n@pytest.mark.parametrize('x', [1], ids=str)\ndef test_case(x): pass\n", "", ""},
		{"indirect", "import pytest\n@pytest.mark.parametrize('x', [1], indirect=True)\ndef test_case(x): pass\n", "", ""},
		{"version-dependent trailing comma", "import pytest\n@pytest.mark.parametrize('x,', [1, 2])\ndef test_case(x): pass\n", "", ""},
		{"custom decorator", "import functools\n@functools.cache\ndef test_case(): pass\n", "", ""},
		{"poison fixture chain", "import pytest\n@pytest.fixture(params=[1])\ndef p(request): return request.param\n@pytest.fixture\ndef mid(p): return 1\ndef test_case(mid): pass\n", "", ""},
		{"multiple module autouse", "import pytest\n@pytest.fixture(params=[1], autouse=True)\ndef a(request): pass\n@pytest.fixture(params=[2], autouse=True)\ndef b(request): pass\n\ndef test_case(): pass\n", "", ""},
		{"class autouse parametrized", "import pytest\nclass TestC:\n    @pytest.fixture(params=[1], autouse=True)\n    def a(self, request): pass\n    def test_case(self): pass\n", "", ""},
		{"non-literal skipif", "import sys\n@pytest.mark.skipif(sys.platform == 'darwin', reason='local')\ndef test_case(): pass\n", "", ""},
		{"parametrize in pytestmark", "import pytest\npytestmark = pytest.mark.parametrize('x', [1])\ndef test_case(): pass\n", "", ""},
		{"property test", "class TestP:\n    @property\n    def test_case(self): pass\n", "", ""},
		{"metaclass keyword", "class TestM(metaclass=type):\n    def test_case(self): pass\n", "", ""},
		{"hook", "def pytest_generate_tests(metafunc): pass\ndef test_case(): pass\n", "", ""},
		{"inheritance outside module", "class TestCase(Base):\n def test_case(self): pass\n", "", ""},
		{"module level test flag", "__test__ = False\ndef test_case(): pass\n", "", ""},
		{"imported tests", "from somewhere import test_imported\n", "", ""},
		{"wildcard imports", "from somewhere import *\n", "", ""},
		{"duplicate definitions", "def test_case(): pass\ndef test_case(): pass\n", "", ""},
		{"executable module", "raise RuntimeError('must not import tests')\ndef test_case(): pass\n", "", ""},
		{"malformed source", "def test_case(\n", "", ""},
		{"conftest hook", "", "def pytest_collection_modifyitems(items): pass\n", ""},
		{"conftest autouse parametrized", "", "import pytest\n@pytest.fixture(params=[1], autouse=True)\ndef a(request): pass\n", ""},
		{"python_modules config", "", "", "[pytest]\npython_modules = mymodule\n"},
		{"usefixtures non-literal name", "import pytest\n@pytest.mark.usefixtures(backend)\ndef test_case(): pass\n", "", ""},
		{"usefixtures kwargs", "import pytest\n@pytest.mark.usefixtures(fixture='backend')\ndef test_case(): pass\n", "", ""},
		{"bare usefixtures", "import pytest\n@pytest.mark.usefixtures\ndef test_case(): pass\n", "", ""},
		{"pytest private api import", "from _pytest.monkeypatch import notset\ndef test_case(): pass\n", "", ""},
		{"collected class through foreign ancestry", "import datetime\nclass Base(datetime.tzinfo):\n    def helper(self): pass\nclass TestD(Base):\n    def test_case(self): pass\n", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, "def test_before_uncertainty(): pass\n"+tt.source)
			writeStaticExtras(t, tt.conftest, tt.config)
			tests, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{ExplicitFiles: []string{"tests/test_sample.py"}}, nil)
			if err == nil || tests != nil {
				t.Fatalf("got %#v, %v; expected no partial inventory and a fallback reason", tests, err)
			}
		})
	}
}

func TestPytestStaticOptionsAndConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options string
		args    []string
		config  string
		content string
		wantOK  bool
	}{
		{name: "safe env options", options: "-q -vv -s --ddtrace --tb=short --color auto -ra --disable-warnings", wantOK: true},
		{name: "safe command options", args: []string{"tests/test_sample.py", "--ddtrace", "-q"}, wantOK: true},
		{name: "replaced positional scope", args: []string{"-q", "--", "discarded.py"}, wantOK: true},
		{name: "safe config", config: "pytest.ini", content: "[pytest]\naddopts = -q --ddtrace\npython_functions = test\npython_files = *.py\n", wantOK: true},
		{name: "safe TOML", config: "pyproject.toml", content: "[tool.pytest.ini_options]\naddopts = ['-v', '--ddtrace']\n", wantOK: true},
		{name: "safe conftest", config: "tests/conftest.py", content: "import pytest\n\n@pytest.fixture\ndef helper():\n    return 1\n", wantOK: true},
		{name: "pattern globs", config: "pytest.ini", content: "[pytest]\npython_functions = it_*\npython_classes = *Suite\n", wantOK: true},
		{name: "python_modules", config: "pytest.ini", content: "[pytest]\npython_modules = mymodule\n", wantOK: false},
		{name: "coverage options", options: "--cov=myapp --cov-report term-missing:skip-covered --cov-fail-under=90 --cov-branch --no-cov", wantOK: true},
		{name: "coverage option cannot hide a selector", options: "--cov -k safe", wantOK: false},
		{name: "xdist and execution options", options: "-n auto --dist loadscope -p no:cacheprovider -x --maxfail=2 --durations 5 --junitxml=r.xml -W ignore --strict-markers", wantOK: true},
		{name: "attached short options", options: "-n4 -pno:cacheprovider -Werror", wantOK: true},
		{name: "invalid worker count", options: "-nfoo", wantOK: false},
		{name: "plugin load via -p", options: "-p cacheprovider", wantOK: false},
		{name: "selection env", options: "-k safe"},
		{name: "marker env", options: "-m slow"},
		{name: "unknown env", options: "--unknown"},
		{name: "positional env", options: "tests/test_sample.py"},
		{name: "malformed env", options: "'"},
		{name: "node id", args: []string{"tests/test_sample.py::test_plain"}},
		{name: "selection command", args: []string{"--lf"}},
		{name: "selection config", config: "pytest.ini", content: "[pytest]\naddopts = -k selected\n"},
		{name: "custom function names", config: "pytest.ini", content: "[pytest]\npython_functions = check_*\n", wantOK: true},
		{name: "nested config", config: "tests/pytest.ini", content: "[pytest]\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, "def test_plain(): pass\n")
			t.Setenv("PYTEST_ADDOPTS", tt.options)
			pytest.commandOverride = append(pytest.commandOverride, tt.args...)
			if tt.config != "" {
				if err := os.WriteFile(tt.config, []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{Pattern: "tests/**/*.py"}, nil)
			if tt.name == "safe TOML" && err != nil && strings.Contains(err.Error(), "Python 3.11+") {
				t.Skip("TOML static discovery requires Python 3.11+")
			}
			if (err == nil) != tt.wantOK {
				t.Fatalf("error = %v, want success = %v", err, tt.wantOK)
			}
		})
	}
}

func staticPluginDir(t *testing.T, name string) string {
	t.Helper()
	metadata := filepath.Join(t.TempDir(), "plugin-1.0.dist-info")
	if err := os.MkdirAll(metadata, 0755); err != nil {
		t.Fatal(err)
	}
	for filename, content := range map[string]string{
		"METADATA":         "Metadata-Version: 2.1\nName: " + name + "\nVersion: 1.0\n",
		"entry_points.txt": "[pytest11]\ncustom = must_not_import:plugin\n",
	} {
		if err := os.WriteFile(filepath.Join(metadata, filename), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Dir(metadata)
}

func TestPytestStaticFallbackReasonDetails(t *testing.T) {
	for _, tt := range []struct {
		name     string
		source   string
		conftest string
		config   string
		contains []string
	}{
		{
			name:     "module hook",
			source:   "def pytest_generate_tests(metafunc): pass\n",
			contains: []string{"pytest_generate_tests", "tests/test_sample.py:2"},
		},
		{
			name:     "inheritance outside module",
			source:   "class TestCase(Base):\n def test_case(self): pass\n",
			contains: []string{"TestCase", "Base"},
		},
		{
			name:     "custom decorator",
			source:   "import functools\n@functools.cache\ndef test_case(): pass\n",
			contains: []string{"functools.cache"},
		},
		{
			name:     "duplicate definition",
			source:   "def test_case(): pass\ndef test_case(): pass\n",
			contains: []string{"duplicate definition", "test_case"},
		},
		{
			name:     "assignment",
			source:   "pytest_plugins = ['plug']\n",
			contains: []string{"pytest_plugins"},
		},
		{
			name:     "pytest private api",
			source:   "from _pytest.monkeypatch import notset\n",
			contains: []string{"_pytest", "tests/test_sample.py"},
		},
		{
			name:     "conftest hook",
			conftest: "def pytest_collection_modifyitems(items): pass\n",
			contains: []string{"pytest_collection_modifyitems", "tests/conftest.py:1"},
		},
		{
			name:     "config key",
			config:   "[pytest]\nxfail_strict = true\n",
			contains: []string{"xfail_strict", "pytest.ini"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, "def test_before(): pass\n"+tt.source)
			writeStaticExtras(t, tt.conftest, tt.config)
			_, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{ExplicitFiles: []string{"tests/test_sample.py"}}, nil)
			if err == nil {
				t.Fatal("expected fallback")
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("reason %q does not mention %q", err.Error(), want)
				}
			}
		})
	}
}

func TestPytestStaticPluginDetection(t *testing.T) {
	// Fake dist-info metadata in PYTHONPATH is scanned before the host
	// interpreter's site-packages, so the fallback case stays hermetic.
	t.Run("unknown plugin triggers fallback", func(t *testing.T) {
		pytest := staticPytestFixture(t, "def test_plain(): pass\n")
		t.Setenv("PYTHONPATH", staticPluginDir(t, "custom-plugin"))
		t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
		_, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{Pattern: "tests/**/*.py"}, nil)
		if err == nil || !strings.Contains(err.Error(), "custom-plugin") {
			t.Fatalf("unknown plugin must trigger fallback without being imported: %v", err)
		}
	})

	t.Run("inventory-neutral plugins are allowed", func(t *testing.T) {
		// "PyTest_Randomly" also covers PEP 503 name normalization.
		for _, name := range []string{"pytest-cov", "PyTest_Randomly"} {
			t.Run(name, func(t *testing.T) {
				pytest := staticPytestFixture(t, "def test_plain(): pass\n")
				t.Setenv("PYTHONPATH", staticPluginDir(t, name))
				t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "")
				_, err := pytest.discoverTestsStatically(context.Background(), discovery.TestFileSet{Pattern: "tests/**/*.py"}, nil)
				// A host interpreter may carry extra plugins outside the
				// allowlist; our allowlisted names may never be the reason.
				if err != nil && strings.Contains(err.Error(), name) {
					t.Fatalf("inventory-neutral plugin %q must not trigger fallback: %v", name, err)
				}
			})
		}
	})
}

func TestPytestStaticEnvironmentOverrides(t *testing.T) {
	pytest := staticPytestFixture(t, "def test_plain(): pass\n")
	t.Setenv("PYTEST_ADDOPTS", "-k selected")
	files := discovery.TestFileSet{ExplicitFiles: []string{"tests/test_sample.py"}}
	if _, err := pytest.discoverTestsStatically(context.Background(), files, map[string]string{"PYTEST_ADDOPTS": "--ddtrace -q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pytest.discoverTestsStatically(context.Background(), files, map[string]string{"PYTEST_PLUGINS": "custom_plugin"}); err == nil {
		t.Fatal("PYTEST_PLUGINS must force fallback")
	}
}

type staticDiscoveryExecutor struct {
	calls int
	run   func(int, context.Context, []string) ([]byte, error)
}

func (e *staticDiscoveryExecutor) CombinedOutput(ctx context.Context, _ string, args []string, _ map[string]string) ([]byte, error) {
	e.calls++
	return e.run(e.calls, ctx, args)
}

func (e *staticDiscoveryExecutor) Run(context.Context, string, []string, map[string]string) error {
	return errors.New("unexpected test execution")
}

func TestPytestStaticDiscoveryGateAndFallback(t *testing.T) {
	for _, mode := range []string{"disabled", "success", "unsupported", "helper error", "invalid JSON"} {
		t.Run(mode, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv(pytestStaticEnv, "true")
			if mode == "disabled" {
				t.Setenv(pytestStaticEnv, "false")
			}
			want := []testoptimization.Test{{Name: "test_plain", Suite: "test_sample.py", SuiteSourceFile: "test_sample.py"}}
			executor := &staticDiscoveryExecutor{run: func(call int, _ context.Context, args []string) ([]byte, error) {
				if mode != "disabled" && call == 1 {
					if len(args) < 3 || args[0] != "-I" || args[1] != "-c" {
						t.Fatalf("expected static helper, got %v", args)
					}
					switch mode {
					case "unsupported":
						return []byte(`{"reason":"unsafe pytest argument"}`), nil
					case "helper error":
						return nil, errors.New("failed helper")
					case "invalid JSON":
						return []byte("invalid"), nil
					default:
						return json.Marshal(map[string]any{"tests": want})
					}
				}
				if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(want[0])
				if err != nil {
					t.Fatal(err)
				}
				return nil, os.WriteFile(discovery.TestsFilePath, data, 0644)
			}}
			var logs bytes.Buffer
			logger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(logger) })
			pytest := &PyTest{executor: executor, platform: &testPlatform{}}
			got, err := pytest.DiscoverTests(context.Background(), discovery.TestFileSet{ExplicitFiles: []string{"test_sample.py"}})
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, want)
			}
			wantCalls := 2
			if mode == "disabled" || mode == "success" {
				wantCalls = 1
			} else if !strings.Contains(logs.String(), "WARN") || !strings.Contains(logs.String(), "falling back") {
				t.Fatalf("missing fallback warning: %s", logs.String())
			}
			if executor.calls != wantCalls {
				t.Fatalf("got %d calls, want %d", executor.calls, wantCalls)
			}
			if mode == "success" {
				if _, err := os.Stat(discovery.TestsFilePath); !os.IsNotExist(err) {
					t.Fatalf("static inventory must not be cached: %v", err)
				}
			}
		})
	}
}

func TestPytestStaticCancellation(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(pytestStaticEnv, "1")
	ctx, cancel := context.WithCancel(context.Background())
	executor := &staticDiscoveryExecutor{run: func(_ int, _ context.Context, _ []string) ([]byte, error) {
		cancel()
		return nil, ctx.Err()
	}}
	pytest := &PyTest{executor: executor, platform: &testPlatform{}}
	_, err := pytest.DiscoverTests(ctx, discovery.TestFileSet{ExplicitFiles: []string{"test_sample.py"}})
	if !errors.Is(err, context.Canceled) || executor.calls != 1 {
		t.Fatalf("got %v, %d calls; must not start fallback after cancellation", err, executor.calls)
	}
}

func TestStaticPytestCommand(t *testing.T) {
	for _, command := range []string{"python", "python3", "/venv/bin/python3.12", "python.exe"} {
		interpreter, options, err := staticPytestCommand(command, []string{"-m", "pytest", "-q"})
		if err != nil || interpreter != command || !reflect.DeepEqual(options, []string{"-q"}) {
			t.Fatalf("%s: got %q, %v, %v", command, interpreter, options, err)
		}
	}
	for _, command := range []string{"uv", "poetry", "sh"} {
		if _, _, err := staticPytestCommand(command, []string{"run", "pytest"}); err == nil {
			t.Fatalf("wrapper %s must fall back", command)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "pytest")
	if err := os.WriteFile(path, []byte("#!/venv/bin/python3\n# console script\n"), 0755); err != nil {
		t.Fatal(err)
	}
	interpreter, options, err := staticPytestCommand(path, []string{"-q"})
	if err != nil || interpreter != "/venv/bin/python3" || !reflect.DeepEqual(options, []string{"-q"}) {
		t.Fatalf("console script: got %q, %v, %v", interpreter, options, err)
	}
}
