package compatibility

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
)

// The file as the plan saw it: test_gone and test_old exist, test_new and
// TestD::test_z don't, test_p has two cases, and test_db and test_db2 none.
const splitFileAtPlan = `import pytest

def test_a(): pass
def test_ab(): pass
def test_gone(): pass
def test_old(): pass
def test_db(db): pass
def test_db2(db): pass

@pytest.mark.parametrize("n", [1, 2])
def test_p(n): pass

class TestC:
    def test_m(self): pass
    def test_m2(self): pass

class TestD:
    def test_x(self): pass
    def test_y(self): pass
`

// The file the runners get, a commit later: test_gone removed, test_old
// renamed, test_new and TestD::test_z added, and test_p given a third case;
// the conftest.py of the run gives test_db and test_db2 two cases each.
const splitFileAtRun = `import pytest

def test_a(): pass
def test_ab(): pass
def test_new(): pass
def test_renamed(): pass
def test_db(db): pass
def test_db2(db): pass

@pytest.mark.parametrize("n", [1, 2, 3])
def test_p(n): pass

class TestC:
    def test_m(self): pass
    def test_m2(self): pass

class TestD:
    def test_x(self): pass
    def test_y(self): pass
    def test_z(self): pass
`

// The conftest.py of the run: it records which runner ran each test, and
// parametrizes the db fixture, which the plan saw without cases.
const splitConftestAtRun = `import os
import pytest

def pytest_generate_tests(metafunc):
    if "db" in metafunc.fixturenames:
        metafunc.parametrize("db", ["sqlite", "mysql"])

@pytest.fixture(autouse=True)
def _record(request):
    with open(os.environ["DDTEST_SPLIT_RUN_LOG"], "a") as log:
        log.write(os.environ["DDTEST_SPLIT_RUNNER"] + " " + request.node.nodeid + "\n")
`

// TestPyTestSplitFilesRunEachTestOnce runs the parts of a file split by test
// with real pytest after the file and a conftest.py changed: every test of a
// full collection runs once, in the part that names it or a class or
// function above it, and otherwise in the last part, the rest of the file.
func TestPyTestSplitFilesRunEachTestOnce(t *testing.T) {
	python := requireEnv(t, "DDTEST_PYTHON_BINARY")
	resetSettingsAfterTest(t)

	parts := map[string][]string{
		// A part names a class alone, and a test the commit removes.
		"part-1": {"tests/test_long.py::TestD", "tests/test_long.py::test_a", "tests/test_long.py::test_db", "tests/test_long.py::test_gone"},
		// test_ab must not fall into this part, nor test_m2; test_old is
		// renamed.
		"part-2": {"tests/test_long.py::TestC::test_m", "tests/test_long.py::test_old", "tests/test_long.py::test_p"},
		// Every test of this part is gone: pytest collects nothing.
		"part-3": {"tests/test_long.py::test_gone"},
	}
	var listed []string
	for _, ids := range parts {
		listed = append(listed, ids...)
	}
	runners := map[string][]string{
		"runner-0": append(slices.Clone(parts["part-1"]), "tests/test_other.py"),
		"runner-1": parts["part-2"],
		"runner-2": parts["part-3"],
		"runner-3": {"tests/test_long.py"},
	}
	want := map[string]string{
		"tests/test_long.py::TestD::test_x":    "runner-0",
		"tests/test_long.py::TestD::test_y":    "runner-0",
		"tests/test_long.py::TestD::test_z":    "runner-0",
		"tests/test_long.py::test_a":           "runner-0",
		"tests/test_long.py::test_db[sqlite]":  "runner-0",
		"tests/test_long.py::test_db[mysql]":   "runner-0",
		"tests/test_other.py::test_o":          "runner-0",
		"tests/test_long.py::TestC::test_m":    "runner-1",
		"tests/test_long.py::test_p[1]":        "runner-1",
		"tests/test_long.py::test_p[2]":        "runner-1",
		"tests/test_long.py::test_p[3]":        "runner-1",
		"tests/test_long.py::TestC::test_m2":   "runner-3",
		"tests/test_long.py::test_ab":          "runner-3",
		"tests/test_long.py::test_new":         "runner-3",
		"tests/test_long.py::test_renamed":     "runner-3",
		"tests/test_long.py::test_db2[sqlite]": "runner-3",
		"tests/test_long.py::test_db2[mysql]":  "runner-3",
	}

	for _, addopts := range []string{"--ddtrace", "--ddtrace -n 2"} {
		t.Run(addopts, func(t *testing.T) {
			// The checkout is reached through a symlink: Go's working
			// directory keeps it, Python's resolves it.
			root := filepath.Join(t.TempDir(), "checkout")
			if err := os.Symlink(t.TempDir(), root); err != nil {
				t.Skipf("symlinks are unavailable: %v", err)
			}
			log := filepath.Join(root, "run.log")
			writeFixture(t, root, "conftest.py", "import pytest\n\n@pytest.fixture\ndef db():\n    return 'sqlite'\n")
			writeFixture(t, root, "tests/test_long.py", splitFileAtPlan)
			writeFixture(t, root, "tests/test_other.py", "def test_o(): pass\n")
			t.Chdir(root)
			configureFramework(shellCommand(python, "-m", "pytest"), "")
			data, err := json.Marshal(map[string][]string{"tests/test_long.py": listed})
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, root, constants.SplitFileTestsPath, string(data))
			writeFixture(t, root, "tests/test_long.py", splitFileAtRun)
			writeFixture(t, root, "conftest.py", splitConftestAtRun)

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			pytest := framework.NewPytest(platform.NewPython())
			for _, runner := range []string{"runner-0", "runner-1", "runner-2", "runner-3"} {
				if err := pytest.RunTests(ctx, runners[runner], map[string]string{
					"PYTEST_ADDOPTS":       addopts,
					"DDTEST_SPLIT_RUN_LOG": log,
					"DDTEST_SPLIT_RUNNER":  runner,
				}); err != nil {
					t.Fatalf("%s failed: %v", runner, err)
				}
			}

			data, err = os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				runner, nodeid, _ := strings.Cut(line, " ")
				if previous, ran := got[nodeid]; ran {
					t.Errorf("%s ran twice, in %s and %s", nodeid, previous, runner)
				}
				got[nodeid] = runner
			}
			for nodeid, runner := range want {
				if got[nodeid] != runner {
					t.Errorf("%s ran in %q, want %s", nodeid, got[nodeid], runner)
				}
			}
			for nodeid, runner := range got {
				if _, ok := want[nodeid]; !ok {
					t.Errorf("%s ran in %s, but isn't in the file", nodeid, runner)
				}
			}

			collect := exec.CommandContext(ctx, python, "-m", "pytest", "--collect-only", "-q", "-p", "no:cacheprovider")
			collect.Env = append(os.Environ(), "DDTEST_SPLIT_RUN_LOG="+os.DevNull, "DDTEST_SPLIT_RUNNER=collect")
			output, err := collect.Output()
			if err != nil {
				t.Fatalf("full collection failed: %v\n%s", err, output)
			}
			var collected []string
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "::") {
					collected = append(collected, strings.TrimSpace(line))
				}
			}
			ran := make([]string, 0, len(got))
			for nodeid := range got {
				ran = append(ran, nodeid)
			}
			slices.Sort(collected)
			slices.Sort(ran)
			if !slices.Equal(ran, collected) {
				t.Errorf("tests run = %v, want the full collection %v", ran, collected)
			}
		})
	}
}

// TestPyTestSplitFilterRefusesFilesOutsideItsArguments runs the plugin with a
// filter for a file pytest wasn't given: the run stops before collection
// instead of running that file's tests unfiltered.
func TestPyTestSplitFilterRefusesFilesOutsideItsArguments(t *testing.T) {
	python := requireEnv(t, "DDTEST_PYTHON_BINARY")
	plugin, err := os.ReadFile(filepath.Join("..", "framework", "scripts", "pytest_split_filter.py"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFixture(t, root, "plugin/ddtest_split_filter.py", string(plugin))
	writeFixture(t, root, "tests/test_long.py", "def test_a(): pass\n")
	writeFixture(t, root, "tests/test_other.py", "def test_o(): pass\n")
	filters, err := json.Marshal(map[string]map[string][]string{
		filepath.Join(root, "tests/test_long.py"): {"keep": {"tests/test_long.py::test_a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "filters.json", string(filters))

	cmd := exec.CommandContext(t.Context(), python, "-m", "pytest", "-p", "no:cacheprovider", "tests/test_other.py")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"DDTEST_SPLIT_FILTER="+filepath.Join(root, "filters.json"),
		"PYTEST_PLUGINS=ddtest_split_filter",
		"PYTHONPATH="+filepath.Join(root, "plugin"),
	)
	output, err := cmd.CombinedOutput()

	requirePytestExitCode(t, err, 4)
	if !strings.Contains(string(output), "aren't among pytest's arguments") {
		t.Errorf("output = %s, want the refused file", output)
	}
	if _, err := os.Stat(filepath.Join(root, "filters.json.applied")); !os.IsNotExist(err) {
		t.Errorf("the refused run marked the filter applied: %v", err)
	}
}
