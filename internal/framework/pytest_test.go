package framework

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestPyTest_DiscoverTests_NoFileArgsWithDefaultPattern(t *testing.T) {
	// When --tests-location is not set, pytest should receive no file arguments.
	// Pytest's own discovery should be left in charge.
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery dir: %v", err)
	}
	defer cleanupDiscoveryDir()

	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedArgs = args
			f, _ := os.Create(TestsDiscoveryFilePath)
			_ = f.Close()
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	_, _ = pytest.DiscoverTests(context.Background())

	for _, arg := range capturedArgs {
		if strings.HasSuffix(arg, ".py") {
			t.Errorf("unexpected .py file arg when no --tests-location is set: %v", capturedArgs)
		}
	}

	for _, expected := range []string{"-m", "pytest"} {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected base arg %q in pytest args, got %v", expected, capturedArgs)
		}
	}
}

func TestPyTest_DiscoverTests_PassesResolvedFilesForCustomTestsLocation(t *testing.T) {
	// When --tests-location is set, the resolved files must be appended to the
	// pytest --collect-only command. Without this, the setting would be silently
	// ignored during discovery while being honoured by DiscoverTestFiles.
	tmpDir := t.TempDir()
	testFile1 := filepath.Join(tmpDir, "test_foo.py")
	testFile2 := filepath.Join(tmpDir, "test_bar.py")
	nonMatchingFile := filepath.Join(tmpDir, "helper.py")
	for _, f := range []string{testFile1, testFile2, nonMatchingFile} {
		if err := os.WriteFile(f, []byte(""), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", f, err)
		}
	}

	setTestsLocation(t, filepath.Join(tmpDir, "test_*.py"))

	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery dir: %v", err)
	}
	defer cleanupDiscoveryDir()

	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedArgs = args
			f, _ := os.Create(TestsDiscoveryFilePath)
			_ = f.Close()
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	_, _ = pytest.DiscoverTests(context.Background())

	for _, expected := range []string{testFile1, testFile2} {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected resolved file %q in pytest args, got %v", expected, capturedArgs)
		}
	}

	if slices.Contains(capturedArgs, nonMatchingFile) {
		t.Errorf("non-matching file %q should not appear in pytest args", nonMatchingFile)
	}
}

func TestPyTest_DiscoverTests_Success(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(TestsDiscoveryFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery directory: %v", err)
	}
	defer cleanupDiscoveryDir()

	testData := []testoptimization.Test{
		{
			Name:            "test_user_is_valid",
			Suite:           "TestUser",
			Module:          "tests.test_user",
			Parameters:      "",
			SuiteSourceFile: "tests/test_user.py",
		},
		{
			Name:            "test_login_success",
			Suite:           "TestAuth",
			Module:          "tests.test_auth",
			Parameters:      "",
			SuiteSourceFile: "tests/test_auth.py",
		},
	}

	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			file, err := os.Create(TestsDiscoveryFilePath)
			if err != nil {
				t.Fatalf("mock failed to create discovery file: %v", err)
			}
			defer func() { _ = file.Close() }()

			encoder := json.NewEncoder(file)
			for _, test := range testData {
				if err := encoder.Encode(test); err != nil {
					t.Fatalf("mock failed to encode test data: %v", err)
				}
			}
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	tests, err := pytest.DiscoverTests(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTests failed: %v", err)
	}

	if len(tests) != len(testData) {
		t.Fatalf("expected %d tests, got %d", len(testData), len(tests))
	}

	for i, expected := range testData {
		actual := tests[i]
		if actual.Name != expected.Name {
			t.Errorf("test[%d].Name: expected %q, got %q", i, expected.Name, actual.Name)
		}
		if actual.Suite != expected.Suite {
			t.Errorf("test[%d].Suite: expected %q, got %q", i, expected.Suite, actual.Suite)
		}
		if actual.Module != expected.Module {
			t.Errorf("test[%d].Module: expected %q, got %q", i, expected.Module, actual.Module)
		}
		if actual.SuiteSourceFile != expected.SuiteSourceFile {
			t.Errorf("test[%d].SuiteSourceFile: expected %q, got %q", i, expected.SuiteSourceFile, actual.SuiteSourceFile)
		}
	}
}
