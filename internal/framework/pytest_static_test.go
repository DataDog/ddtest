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

func TestPytestStaticFallback(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
	}{
		{"dynamic values", "import pytest\n@pytest.mark.parametrize('x', range(3))\ndef test_case(x): pass\n"},
		{"callable ids", "import pytest\n@pytest.mark.parametrize('x', [1], ids=str)\ndef test_case(x): pass\n"},
		{"parameter marks", "import pytest\n@pytest.mark.parametrize('x', [pytest.param(1, marks=pytest.mark.skip)])\ndef test_case(x): pass\n"},
		{"skip marker", "import pytest\n@pytest.mark.skip\ndef test_case(): pass\n"},
		{"indirect", "import pytest\n@pytest.mark.parametrize('x', [1], indirect=True)\ndef test_case(x): pass\n"},
		{"empty values", "import pytest\n@pytest.mark.parametrize('x', [])\ndef test_case(x): pass\n"},
		{"version-dependent trailing comma", "import pytest\n@pytest.mark.parametrize('x,', [1, 2])\ndef test_case(x): pass\n"},
		{"fixture", "def test_case(tmp_path): pass\n"},
		{"fixture decorator", "import pytest\n@pytest.fixture(params=[1, 2], autouse=True)\ndef fixture(request): pass\ndef test_case(): pass\n"},
		{"hook", "def pytest_generate_tests(metafunc): pass\ndef test_case(): pass\n"},
		{"inheritance", "class TestCase(Base):\n def test_case(self): pass\n"},
		{"test disabled", "__test__ = False\ndef test_case(): pass\n"},
		{"class constructor", "class TestCase:\n def __init__(self): pass\n def test_case(self): pass\n"},
		{"imported tests", "from somewhere import test_imported\n"},
		{"dynamic annotations", "def test_case() -> configure_collection(): pass\n"},
		{"wildcard imports", "from somewhere import *\n"},
		{"duplicate definitions", "def test_case(): pass\ndef test_case(): pass\n"},
		{"executable module", "raise RuntimeError('must not import tests')\ndef test_case(): pass\n"},
		{"malformed source", "def test_case(\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pytest := staticPytestFixture(t, "def test_before_uncertainty(): pass\n"+tt.source)
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
		{name: "coverage options", options: "--cov=myapp --cov-report term-missing:skip-covered --cov-fail-under=90 --cov-branch --no-cov", wantOK: true},
		{name: "coverage option cannot hide a selector", options: "--cov -k safe", wantOK: false},
		{name: "selection env", options: "-k safe"},
		{name: "marker env", options: "-m slow"},
		{name: "unknown env", options: "--unknown"},
		{name: "positional env", options: "tests/test_sample.py"},
		{name: "malformed env", options: "'"},
		{name: "node id", args: []string{"tests/test_sample.py::test_plain"}},
		{name: "selection command", args: []string{"--lf"}},
		{name: "selection config", config: "pytest.ini", content: "[pytest]\naddopts = -k selected\n"},
		{name: "custom function names", config: "pytest.ini", content: "[pytest]\npython_functions = check_*\n"},
		{name: "conftest", config: "tests/conftest.py", content: "# Local collection may depend on this plugin\n"},
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
