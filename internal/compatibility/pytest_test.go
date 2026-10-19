package compatibility

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
	"golang.org/x/sync/errgroup"
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

func TestPyTestSuiteModeSelectorBatches(t *testing.T) {
	python := requireEnv(t, "DDTEST_PYTHON_BINARY")
	resetSettingsAfterTest(t)
	root := t.TempDir()
	writeFixture(t, root, "pytest.ini", `[pytest]
markers = keep: tests selected for this run
`)
	writeFixture(t, root, "test_keep.py", `import os
import pytest

@pytest.mark.keep
def test_keep():
    assert os.environ.get("DDTEST_FAIL_SELECTED") != "true"
`)
	writeFixture(t, root, "test_other.py", `def test_other():
    raise AssertionError("deselected test ran")
`)
	t.Chdir(root)
	configureFramework(shellCommand(python, "-m", "pytest"), "")
	t.Setenv("PYTEST_ADDOPTS", "--ddtrace")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	for _, selector := range []string{"-k test_keep", "-m keep"} {
		t.Run(selector, func(t *testing.T) {
			settings.Get().TestSkippingLevel = settings.TestSkippingLevelSuite
			pytest := framework.NewPytest(platform.NewPython())
			for _, failSelected := range []string{"false", "true"} {
				var workers errgroup.Group
				for _, file := range []string{"test_keep.py", "test_other.py"} {
					workers.Go(func() error {
						return pytest.RunTests(ctx, []string{file}, map[string]string{
							"PYTEST_ADDOPTS":       "--ddtrace " + selector,
							"DDTEST_FAIL_SELECTED": failSelected,
						})
					})
				}
				err := workers.Wait()
				if failSelected == "false" {
					if err != nil {
						t.Fatalf("empty batch failed successful selected tests: %v", err)
					}
				} else {
					requirePytestExitCode(t, err, 1)
				}
			}
			// An entirely deselected suite-mode run is also an empty batch.
			if err := pytest.RunTests(ctx, []string{"test_other.py"}, map[string]string{"PYTEST_ADDOPTS": "--ddtrace " + selector}); err != nil {
				t.Fatalf("entirely deselected suite-mode batch failed: %v", err)
			}
			settings.Get().TestSkippingLevel = settings.TestSkippingLevelTest
			testPytest := framework.NewPytest(platform.NewPython())
			err := testPytest.RunTests(ctx, []string{"test_other.py"}, map[string]string{"PYTEST_ADDOPTS": "--ddtrace " + selector})
			requirePytestExitCode(t, err, 5)
		})
	}
}

func requirePytestExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
		t.Fatalf("expected pytest exit code %d, got %v", want, err)
	}
}
