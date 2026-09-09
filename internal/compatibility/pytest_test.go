package compatibility

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

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
	pytest := framework.NewPytest()
	pytest.SetPlatformEnv(map[string]string{"PYTEST_ADDOPTS": "--ddtrace"})
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
