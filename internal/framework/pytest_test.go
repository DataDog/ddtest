package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/stretchr/testify/require"
)

type pytestSkippingPlatform struct {
	testPlatform
	level settings.TestSkippingLevel
}

func (p *pytestSkippingPlatform) TestSkippingLevel() settings.TestSkippingLevel { return p.level }

func TestPyTest_RunTests_EmptyCollection(t *testing.T) {
	// Use real subprocess exit errors, including wrapping, rather than matching
	// error messages that might also come from test failures.
	for _, level := range []settings.TestSkippingLevel{settings.TestSkippingLevelTest, settings.TestSkippingLevelSuite} {
		for _, code := range []int{0, 1, 2, 3, 4, 5} {
			t.Run(fmt.Sprintf("%s/exit-%d", level, code), func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestPyTest_ExitHelper$")
				cmd.Env = append(os.Environ(), fmt.Sprintf("DDTEST_PYTEST_EXIT_HELPER=%d", code))
				processErr := cmd.Run()
				if code != 0 {
					var exitErr *exec.ExitError
					require.ErrorAs(t, processErr, &exitErr)
					require.Equal(t, code, exitErr.ExitCode())
					processErr = fmt.Errorf("pytest execution: %w", processErr)
				}
				pytest := &PyTest{
					executor: &mockCommandExecutor{err: processErr},
					platform: &pytestSkippingPlatform{level: level},
				}
				err := pytest.RunTests(t.Context(), []string{"test_case.py"}, nil)
				if code == 0 || (level == settings.TestSkippingLevelSuite && code == 5) {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, processErr)
				}
				if code == 5 {
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					require.Error(t, pytest.RunTests(ctx, []string{"test_case.py"}, nil))
				}
			})
		}
	}
	for _, failure := range []error{context.Canceled, context.DeadlineExceeded, errors.New("failed to start pytest"), errors.New("exit status 5")} {
		pytest := &PyTest{
			executor: &mockCommandExecutor{err: failure},
			platform: &pytestSkippingPlatform{level: settings.TestSkippingLevelSuite},
		}
		require.ErrorIs(t, pytest.RunTests(t.Context(), []string{"test_case.py"}, nil), failure)
	}
}

func TestPyTest_ExitHelper(t *testing.T) {
	if code := os.Getenv("DDTEST_PYTEST_EXIT_HELPER"); code != "" {
		var status int
		_, err := fmt.Sscanf(code, "%d", &status)
		require.NoError(t, err)
		os.Exit(status)
	}
}

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

	pytest := &PyTest{executor: mockExecutor, platform: &testPlatform{env: map[string]string{}}}
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

	pytest := &PyTest{executor: mockExecutor, platform: &testPlatform{env: map[string]string{}}}
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

	pytest := &PyTest{executor: mockExecutor, platform: &testPlatform{env: map[string]string{}}}
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

	pytest := &PyTest{executor: mockExecutor, platform: &testPlatform{env: map[string]string{}}}
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

func TestPyTest_Metadata(t *testing.T) {
	pytest := NewPytest(&testPlatform{})
	if pytest.Name() != "pytest" {
		t.Errorf("expected framework name pytest, got %q", pytest.Name())
	}
	if !pytest.SupportsFullTestDiscovery() {
		t.Error("expected PyTest to support full test discovery")
	}

}

func TestPyTest_SupportsFullTestDiscovery(t *testing.T) {
	pytest := NewPytest(&testPlatform{})
	if !pytest.SupportsFullTestDiscovery() {
		t.Error("expected PyTest to support full test discovery")
	}
	if sourceFile, ok := pytest.SourceFileForSuite("tests/test_user.py"); ok || sourceFile != "" {
		t.Fatalf("SourceFileForSuite() = %q, %v; want unsupported", sourceFile, ok)
	}
}

func TestPyTest_UnskippableMarkers(t *testing.T) {
	for _, location := range []string{"file", "conftest", "ancestor conftest", "missing file", "unreadable conftest", "plain"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			if err := os.MkdirAll("tests/unit", 0755); err != nil {
				t.Fatal(err)
			}
			file := "tests/unit/test_case.py"
			if location != "missing file" {
				if err := os.WriteFile(file, []byte("def test_case(): pass\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			markerFile := map[string]string{
				"file": file, "conftest": "tests/conftest.py", "ancestor conftest": "conftest.py",
			}[location]
			if markerFile != "" {
				if err := os.WriteFile(markerFile, []byte("# datadog_itr_unskippable\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if location == "unreadable conftest" {
				// A directory gives a deterministic read error, including when
				// tests run as root and filesystem permissions cannot deny reads.
				if err := os.Mkdir("tests/conftest.py", 0755); err != nil {
					t.Fatal(err)
				}
			}
			if got := NewPytest(&testPlatform{}).HasUnskippableMarker(file); got != (location != "plain") {
				t.Fatalf("HasUnskippableMarker = %v for %s", got, location)
			}
		})
	}
}

func TestPyTest_FileDiscoveryFiltersNonPythonFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, file := range []string{"tests/test_case.py", "tests/__pycache__/test_case.pyc", "tests/test_case.txt"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	pytest := NewPytest(&testPlatform{})
	for _, selection := range []discovery.TestFileSet{
		{Pattern: "tests/**/test_*"},
		{ExplicitFiles: []string{"tests/test_case.py", "tests/__pycache__/test_case.pyc", "tests/test_case.txt"}},
	} {
		original := slices.Clone(selection.ExplicitFiles)
		files, err := pytest.DiscoverTestFiles(t.Context(), selection)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(files, []string{"tests/test_case.py"}) {
			t.Fatalf("DiscoverTestFiles(%+v) = %v", selection, files)
		}
		if !slices.Equal(original, selection.ExplicitFiles) {
			t.Fatal("file discovery mutated the caller's selection")
		}
	}
}

func TestPyTest_FileDiscoveryFiltersNonTestDirectories(t *testing.T) {
	t.Chdir(t.TempDir())
	// Virtualenvs conventionally live inside the repository root and
	// installed packages ship test_* modules that match discovery patterns.
	for _, file := range []string{
		"tests/test_case.py",
		".venv/lib/python3.12/site-packages/ddtrace/testing/internal/test_data.py",
		"venv/lib/python3.12/site-packages/pkg/test_helper.py",
		"tests/__pycache__/test_case.py",
		".tox/py312/lib/python3.12/site-packages/pkg/test_helper.py",
		"node_modules/pkg/test_case.py",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	pytest := NewPytest(&testPlatform{})
	for _, selection := range []discovery.TestFileSet{
		{Pattern: "**/test_*"},
		{ExplicitFiles: []string{
			"tests/test_case.py",
			".venv/lib/python3.12/site-packages/ddtrace/testing/internal/test_data.py",
			"venv/lib/python3.12/site-packages/pkg/test_helper.py",
			"node_modules/pkg/test_case.py",
		}},
	} {
		files, err := pytest.DiscoverTestFiles(t.Context(), selection)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(files, []string{"tests/test_case.py"}) {
			t.Fatalf("DiscoverTestFiles(%+v) = %v, want only tests/test_case.py", selection, files)
		}
	}
}

func TestPyTest_RunTests_WithCommandOverride(t *testing.T) {
	testFiles := []string{"tests/test_user.py"}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		capturedEnvMap: map[string]string{},
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	pytest := &PyTest{
		executor:        mockExecutor,
		platform:        &testPlatform{env: map[string]string{}},
		commandOverride: []string{"pytest"},
	}
	if err := pytest.RunTests(context.Background(), testFiles, nil); err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != "pytest" {
		t.Fatalf("expected command 'pytest' (from commandOverride), got %q", capturedName)
	}
	expectedArgs := []string{"tests/test_user.py"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Fatalf("expected args %v (no -m pytest), got %v", expectedArgs, capturedArgs)
	}
}

func TestPyTest_DiscoverTests_WithCommandOverride(t *testing.T) {
	if err := os.MkdirAll(filepath.Dir(discovery.TestsFilePath), 0755); err != nil {
		t.Fatalf("failed to create discovery dir: %v", err)
	}
	defer cleanupDiscoveryDir()

	explicitFiles := []string{"tests/test_foo.py"}
	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
			f, _ := os.Create(discovery.TestsFilePath)
			_ = f.Close()
		},
	}

	pytest := &PyTest{
		executor:        mockExecutor,
		platform:        &testPlatform{env: map[string]string{}},
		commandOverride: []string{"pytest"},
	}
	testFiles := discovery.TestFileSet{ExplicitFiles: explicitFiles}
	_, _ = pytest.DiscoverTests(context.Background(), testFiles)

	if capturedName != "pytest" {
		t.Fatalf("expected command 'pytest' (from commandOverride), got %q", capturedName)
	}
	for _, expected := range explicitFiles {
		if !slices.Contains(capturedArgs, expected) {
			t.Errorf("expected file %q in pytest args, got %v", expected, capturedArgs)
		}
	}
	for _, unexpected := range []string{"-m", "pytest"} {
		if slices.Contains(capturedArgs, unexpected) {
			t.Errorf("unexpected arg %q in pytest args (commandOverride should not add -m pytest)", unexpected)
		}
	}
}

func TestPyTest_RunTests(t *testing.T) {
	testFiles := []string{"tests/test_user.py", "tests/test_auth.py"}
	envMap := map[string]string{
		"APP_ENV":    "test",
		"SHARED_VAR": "override",
	}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = args
		},
	}

	pytest := &PyTest{
		executor: mockExecutor,
		platform: &testPlatform{env: map[string]string{
			"PYTEST_ADDOPTS": "--ddtrace",
			"SHARED_VAR":     "platform",
		}},
	}
	if err := pytest.RunTests(context.Background(), testFiles, envMap); err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != "python" {
		t.Fatalf("expected command python, got %q", capturedName)
	}
	expectedArgs := []string{"-m", "pytest", "tests/test_user.py", "tests/test_auth.py"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Fatalf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
	if mockExecutor.capturedEnvMap["PYTEST_ADDOPTS"] != "--ddtrace" {
		t.Errorf("expected PYTEST_ADDOPTS from platform env, got %q", mockExecutor.capturedEnvMap["PYTEST_ADDOPTS"])
	}
	if mockExecutor.capturedEnvMap["APP_ENV"] != "test" {
		t.Errorf("expected APP_ENV from run env, got %q", mockExecutor.capturedEnvMap["APP_ENV"])
	}
	if mockExecutor.capturedEnvMap["SHARED_VAR"] != "override" {
		t.Errorf("expected run env to override platform env, got %q", mockExecutor.capturedEnvMap["SHARED_VAR"])
	}
}

func TestIsUnderExcludedDir(t *testing.T) {
	for _, tt := range []struct {
		name string
		file string
		want bool
	}{
		{name: "virtualenv layout", file: ".venv/lib/python3.12/site-packages/pkg/test_case.py", want: true},
		{name: "bare venv", file: "venv/test_case.py", want: true},
		{name: "tox environment", file: ".tox/py312/lib/test_case.py", want: true},
		{name: "bytecode cache", file: "tests/__pycache__/test_case.py", want: true},
		{name: "javascript dependencies", file: "node_modules/pkg/test_case.py", want: true},
		{name: "project test file", file: "tests/test_case.py", want: false},
		{name: "file whose basename embeds an excluded name", file: "tests/test_venv_case.py", want: false},
		{name: "directory that only starts with an excluded name", file: "my-venv/test_case.py", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUnderExcludedDir(tt.file); got != tt.want {
				t.Fatalf("isUnderExcludedDir(%q) = %v, want %v", tt.file, got, tt.want)
			}
		})
	}
}
