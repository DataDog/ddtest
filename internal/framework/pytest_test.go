package framework

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestPyTest_DiscoverTests_WithExplicitFiles(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery dir: %v", err)
	}
	defer cleanupDiscoveryDir()

	explicitFiles := []string{"tests/test_foo.py", "tests/test_bar.py"}
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedArgs = args
			f, _ := os.Create(discovery.TestsFilePath)
			_ = f.Close()
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	testFiles := discovery.TestFileSet{ExplicitFiles: explicitFiles}
	_, _ = pytest.DiscoverTests(context.Background(), testFiles)

	for _, expected := range explicitFiles {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected file %q in pytest args, got %v", expected, capturedArgs)
		}
	}

	for _, expected := range []string{"-m", "pytest"} {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected base arg %q in pytest args, got %v", expected, capturedArgs)
		}
	}
}

func TestPyTest_DiscoverTests_WithPatternGlobsFiles(t *testing.T) {
	tmpDir := t.TempDir()
	testFile1 := filepath.Join(tmpDir, "test_foo.py")
	testFile2 := filepath.Join(tmpDir, "test_bar.py")
	nonMatchingFile := filepath.Join(tmpDir, "helper.py")
	for _, f := range []string{testFile1, testFile2, nonMatchingFile} {
		if err := os.WriteFile(f, []byte(""), 0644); err != nil {
			t.Fatalf("failed to create test file %s: %v", f, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery dir: %v", err)
	}
	defer cleanupDiscoveryDir()

	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedArgs = args
			f, _ := os.Create(discovery.TestsFilePath)
			_ = f.Close()
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	// Pattern-based (no explicit files): pytest.go will glob the pattern
	pattern := filepath.Join(tmpDir, "test_*.py")
	testFiles := discovery.TestFileSet{Pattern: pattern}
	_, _ = pytest.DiscoverTests(context.Background(), testFiles)

	// Both test files should be discovered and passed as args
	for _, expected := range []string{testFile1, testFile2} {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected resolved file %q in pytest args, got %v", expected, capturedArgs)
		}
	}

	if slices.Contains(capturedArgs, nonMatchingFile) {
		t.Errorf("non-matching file %q should not appear in pytest args", nonMatchingFile)
	}
}

func TestPyTest_DiscoverTests_EmptyFileSet(t *testing.T) {
	executorCalled := false
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			executorCalled = true
		},
	}

	pytest := &PyTest{executor: mockExecutor, platformEnv: map[string]string{}}
	tests, err := pytest.DiscoverTests(context.Background(), discovery.TestFileSet{ExplicitFiles: []string{}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tests) != 0 {
		t.Errorf("expected no tests, got %d", len(tests))
	}
	if executorCalled {
		t.Error("executor should not be called for an empty file set")
	}
}

func TestPyTest_DiscoverTests_Success(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
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
			file, err := os.Create(discovery.TestsFilePath)
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
	testFiles := discovery.TestFileSet{ExplicitFiles: []string{"tests/test_user.py", "tests/test_auth.py"}}
	tests, err := pytest.DiscoverTests(context.Background(), testFiles)
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

func TestPyTest_SupportsFullTestDiscovery(t *testing.T) {
	pytest := NewPytest()
	if !pytest.SupportsFullTestDiscovery() {
		t.Error("expected PyTest to support full test discovery")
	}
}
