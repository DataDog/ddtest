package compatibility

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
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
    assert os.environ["_DD_CIVISIBILITY_ITR_SUITE_MODE"] == os.environ["DDTEST_EXPECTED_SUITE_MODE"]
    assert os.environ["_DD_CIVISIBILITY_ITR_PREVENT_TEST_SKIPPING"] == os.environ["DDTEST_EXPECTED_SUITE_MODE"]
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

	if err := pytest.RunTests(ctx, []string{"checks/check_selected.py"}, map[string]string{
		"DDTEST_PYTEST_WORKER": "selected", "DDTEST_EXPECTED_SUITE_MODE": "false",
		"_DD_CIVISIBILITY_ITR_PREVENT_TEST_SKIPPING": "false",
	}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}

	t.Run("suite mode", func(t *testing.T) {
		settings.Get().TestSkippingLevel = settings.TestSkippingLevelSuite
		suitePytest := framework.NewPytest(platform.NewPython())
		files, err := suitePytest.DiscoverTestFiles(ctx, testFiles)
		if err != nil {
			t.Fatal(err)
		}
		requireFiles(t, files, wantFiles)
		// Explicit full discovery must still work when suite mode is selected.
		tests, err := suitePytest.DiscoverTests(ctx, testFiles)
		if err != nil {
			t.Fatal(err)
		}
		requireTestSources(t, tests, wantFiles)
		if err := suitePytest.RunTests(ctx, []string{"checks/check_selected.py"}, map[string]string{
			"DDTEST_PYTEST_WORKER": "selected", "DDTEST_EXPECTED_SUITE_MODE": "true",
			"_DD_CIVISIBILITY_ITR_PREVENT_TEST_SKIPPING": "false",
		}); err != nil {
			t.Fatalf("suite-mode selected-file run failed: %v", err)
		}
	})
}
