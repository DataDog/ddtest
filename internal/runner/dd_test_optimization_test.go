package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
)

func TestTestRunner_PrepareTestOptimization_Success(t *testing.T) {
	ctx := context.Background()

	// Setup mocks
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite1", Name: "test2", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
			{Suite: "TestSuite2", Name: "test3", Parameters: "", SuiteSourceFile: "test/file2_test.rb"},
			{Suite: "TestSuite3", Name: "test4", Parameters: "", SuiteSourceFile: "test/file3_test.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"platform": "ruby",
			"version":  "3.0",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			"TestSuite1.test2.": true, // Skip test2
			"TestSuite3.test4.": true, // Skip test4
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Verify tags were passed to optimization client
	if mockOptimizationClient.Tags["platform"] != "ruby" {
		t.Error("PrepareTestOptimization() should pass platform tags to optimization client")
	}

	// Verify optimization client was shut down
	if !mockOptimizationClient.ShutdownCalled {
		t.Error("PrepareTestOptimization() should shutdown optimization client")
	}

	// Verify test files were calculated correctly (should include file1 and file2, but not file3)
	expectedFiles := map[string]bool{
		"test/file1_test.rb": true, // test1 is not skipped
		"test/file2_test.rb": true, // test3 is not skipped
	}

	if len(runner.testFiles) != 2 {
		t.Errorf("PrepareTestOptimization() should result in 2 test files, got %d", len(runner.testFiles))
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file: %s", file)
		}
	}

	// Verify skippable percentage was calculated correctly (2 out of 4 tests skipped = 50%)
	expectedPercentage := 50.0
	if runner.skippablePercentage != expectedPercentage {
		t.Errorf("PrepareTestOptimization() should calculate skippable percentage as %.2f, got %.2f",
			expectedPercentage, runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_PlatformDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatformDetector := &MockPlatformDetector{
		Err: errors.New("platform detection failed"),
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when platform detection fails")
	}

	expectedMsg := "failed to detect platform"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_TagsCreationError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		TagsErr: errors.New("tags creation failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when tags creation fails")
	}

	expectedMsg := "failed to create platform tags"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_OptimizationClientInitError(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{Suite: "", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		InitializeErr: errors.New("client initialization failed"),
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when optimization client initialization fails")
	}

	expectedMsg := "failed to initialize optimization client"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_FrameworkDetectionError(t *testing.T) {
	ctx := context.Background()

	mockPlatform := &MockPlatform{
		Tags:         map[string]string{"platform": "ruby"},
		FrameworkErr: errors.New("framework detection failed"),
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when framework detection fails")
	}

	expectedMsg := "failed to detect framework"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_TestDiscoveryError(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Err: errors.New("test discovery failed"),
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when test discovery fails")
	}

	expectedMsg := "test discovery failed"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}
}

func TestTestRunner_PrepareTestOptimization_EmptyTests(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{}, // Empty test list
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{SkippableTests: map[string]bool{}}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should handle empty tests, got: %v", err)
	}

	if len(runner.testFiles) != 0 {
		t.Errorf("PrepareTestOptimization() should result in 0 test files for empty tests, got %d", len(runner.testFiles))
	}

	// Division by zero should be handled gracefully
	if runner.skippablePercentage != 0.0 {
		t.Logf("Skippable percentage for empty tests: %f", runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_AllTestsSkipped(t *testing.T) {
	ctx := context.Background()

	mockFramework := &MockFramework{
		Tests: []testoptimization.Test{
			{Suite: "", Name: "test1", Parameters: "", SuiteSourceFile: "file1.rb"},
			{Suite: "", Name: "test2", Parameters: "", SuiteSourceFile: "file2.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		Tags:      map[string]string{"platform": "ruby"},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{
			".test1.": true,
			".test2.": true,
		},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should handle all tests skipped, got: %v", err)
	}

	if len(runner.testFiles) != 0 {
		t.Errorf("PrepareTestOptimization() should result in 0 test files when all tests are skipped, got %d", len(runner.testFiles))
	}

	if runner.skippablePercentage != 100.0 {
		t.Errorf("PrepareTestOptimization() should calculate 100%% skippable when all tests are skipped, got %.2f", runner.skippablePercentage)
	}
}

func TestTestRunner_PrepareTestOptimization_RuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Set runtime tags override via environment variable - only override some tags
	overrideTags := `{"os.platform":"linux","runtime.version":"3.2.0"}`
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", overrideTags)
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")
	}()

	// Reinitialize settings to pick up the environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	// Platform tags should have more tags than the override
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"os.platform":     "darwin",
			"os.architecture": "arm64",
			"runtime.name":    "ruby",
			"runtime.version": "3.3.0",
			"language":        "ruby",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Check that override tags replaced the detected values
	if mockOptimizationClient.Tags["os.platform"] != "linux" {
		t.Errorf("Expected os.platform to be 'linux' from override, got %q", mockOptimizationClient.Tags["os.platform"])
	}

	if mockOptimizationClient.Tags["runtime.version"] != "3.2.0" {
		t.Errorf("Expected runtime.version to be '3.2.0' from override, got %q", mockOptimizationClient.Tags["runtime.version"])
	}

	// Check that detected tags NOT in override were preserved
	if mockOptimizationClient.Tags["os.architecture"] != "arm64" {
		t.Errorf("Expected os.architecture to be 'arm64' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["os.architecture"])
	}

	if mockOptimizationClient.Tags["runtime.name"] != "ruby" {
		t.Errorf("Expected runtime.name to be 'ruby' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["runtime.name"])
	}

	if mockOptimizationClient.Tags["language"] != "ruby" {
		t.Errorf("Expected language to be 'ruby' from detected tags (not overridden), got %q", mockOptimizationClient.Tags["language"])
	}
}

func TestTestRunner_PrepareTestOptimization_RuntimeTagsOverrideInvalidJSON(t *testing.T) {
	ctx := context.Background()

	// Set invalid JSON as runtime tags override
	_ = os.Setenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS", `{invalid json}`)
	defer func() {
		_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")
	}()

	// Reinitialize settings to pick up the environment variable
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests:         []testoptimization.Test{},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err == nil {
		t.Error("PrepareTestOptimization() should return error when runtime tags JSON is invalid")
	}

	expectedMsg := "failed to parse runtime tags override"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("PrepareTestOptimization() error should contain '%s', got: %v", expectedMsg, err)
	}

	// Optimization client should not be initialized when there's a parse error
	if mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should not initialize optimization client when runtime tags JSON is invalid")
	}
}

func TestTestRunner_PrepareTestOptimization_NoRuntimeTagsOverride(t *testing.T) {
	ctx := context.Background()

	// Ensure no runtime tags override is set
	_ = os.Unsetenv("DD_TEST_OPTIMIZATION_RUNNER_RUNTIME_TAGS")

	// Reinitialize settings to ensure clean state
	settings.Init()

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "TestSuite1", Name: "test1", Parameters: "", SuiteSourceFile: "test/file1_test.rb"},
		},
	}

	// Platform tags that should be used when no override is provided
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags: map[string]string{
			"os.platform":     "darwin",
			"runtime.version": "3.3.0",
			"language":        "ruby",
		},
		Framework: mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{
		Platform: mockPlatform,
	}

	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)

	if err != nil {
		t.Errorf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Verify optimization client was initialized with platform tags
	if !mockOptimizationClient.InitializeCalled {
		t.Error("PrepareTestOptimization() should initialize optimization client")
	}

	// Check that platform tags were used (not override)
	if mockOptimizationClient.Tags["os.platform"] != "darwin" {
		t.Errorf("Expected os.platform to be 'darwin' from platform, got %q", mockOptimizationClient.Tags["os.platform"])
	}

	if mockOptimizationClient.Tags["runtime.version"] != "3.3.0" {
		t.Errorf("Expected runtime.version to be '3.3.0' from platform, got %q", mockOptimizationClient.Tags["runtime.version"])
	}
}

// initGitRepo initializes a bare git repo in the given directory so that
// `git rev-parse --show-toplevel` resolves correctly during tests.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init git repo in %s: %v\n%s", dir, err, string(out))
	}
	// Need at least one commit for some git operations to work
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to create initial commit in %s: %v\n%s", dir, err, string(out))
	}
}

// TestPrepareTestOptimization_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative
// reproduces issue #33: full discovery returns repo-root-relative SuiteSourceFile paths
// (e.g. "core/spec/...") but workers run from subdirectory "core/", causing double-prefix.
func TestPrepareTestOptimization_ITRFullDiscovery_SubdirRootRelativePath_NormalizesToCwdRelative(t *testing.T) {
	ctx := context.Background()

	// Create a temp monorepo: repoRoot/core/spec/models/order_spec.rb
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	coreDir := filepath.Join(repoRoot, "core")
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "models", "order_spec.rb"), []byte("# spec"), 0644)
	_ = os.MkdirAll(filepath.Join(coreDir, "spec", "finders"), 0755)
	_ = os.WriteFile(filepath.Join(coreDir, "spec", "finders", "find_spec.rb"), []byte("# spec"), 0644)

	// chdir into the subdirectory (simulating: cd core && ddtest plan)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(coreDir)

	// Full discovery returns repo-root-relative paths (the bug scenario)
	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "Order", Name: "should be valid", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
			{Suite: "AddressFinder", Name: "finds addresses", Parameters: "", SuiteSourceFile: "core/spec/finders/find_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{}, // No tests skipped (ITR enabled but all tests need to run)
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// The key assertion: testFiles should contain CWD-relative paths, not repo-root-relative paths
	// i.e. "spec/models/order_spec.rb" not "core/spec/models/order_spec.rb"
	expectedFiles := map[string]bool{
		"spec/models/order_spec.rb": true,
		"spec/finders/find_spec.rb": true,
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q - should be CWD-relative, not repo-root-relative", file)
		}
		// Explicitly check for the double-prefix bug
		if strings.HasPrefix(file, "core/") {
			t.Errorf("Test file path %q still has repo-root prefix 'core/' - this is the bug from issue #33", file)
		}
	}
}

// TestPrepareTestOptimization_RepoRootRun_LeavesRepoRelativePathsUnchanged
// ensures that when running from the repo root (not a subdirectory), paths are not modified.
func TestPrepareTestOptimization_RepoRootRun_LeavesRepoRelativePathsUnchanged(t *testing.T) {
	ctx := context.Background()

	// Create a temp repo root with spec files
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	_ = os.MkdirAll(filepath.Join(repoRoot, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(repoRoot, "spec", "models", "user_spec.rb"), []byte("# spec"), 0644)

	// chdir to repo root (normal case)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(repoRoot)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			{Suite: "User", Name: "should be valid", Parameters: "", SuiteSourceFile: "spec/models/user_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Paths should remain unchanged when running from repo root
	if _, ok := runner.testFiles["spec/models/user_spec.rb"]; !ok {
		t.Errorf("Expected test file 'spec/models/user_spec.rb' to remain unchanged, got: %v", runner.testFiles)
	}
}

// TestPrepareTestOptimization_FastDiscovery_PathsRemainUnchanged
// ensures the fast discovery path (ITR disabled) does not modify paths.
func TestPrepareTestOptimization_FastDiscovery_PathsRemainUnchanged(t *testing.T) {
	ctx := context.Background()

	// Fast discovery returns CWD-relative paths directly from glob
	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		Tests:            nil,                                                                    // No full discovery results
		TestFiles:        []string{"spec/models/user_spec.rb", "spec/controllers/admin_spec.rb"}, // Fast discovery
		DiscoverTestsErr: errors.New("context canceled"),                                         // Simulate full discovery being cancelled
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// Fast discovery paths should be used as-is
	expectedFiles := map[string]bool{
		"spec/models/user_spec.rb":       true,
		"spec/controllers/admin_spec.rb": true,
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q in fast discovery", file)
		}
	}
}

// TestPrepareTestOptimization_ITRPathNormalization_PrefixMismatchUnchanged
// ensures that when SuiteSourceFile does not match the current subdir prefix,
// the path is not modified (conservative behavior).
func TestPrepareTestOptimization_ITRPathNormalization_PrefixMismatchUnchanged(t *testing.T) {
	ctx := context.Background()

	// Create monorepo with "api" and "core" subdirs
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)

	apiDir := filepath.Join(repoRoot, "api")
	_ = os.MkdirAll(filepath.Join(apiDir, "spec", "models"), 0755)
	_ = os.WriteFile(filepath.Join(apiDir, "spec", "models", "endpoint_spec.rb"), []byte("# spec"), 0644)

	// We're in "api/" subdir but discovery returns "core/" paths (shouldn't happen in practice,
	// but tests the safety of the normalization)
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	_ = os.Chdir(apiDir)

	mockFramework := &MockFramework{
		FrameworkName: "rspec",
		Tests: []testoptimization.Test{
			// Path with different prefix than CWD subdir - should be left unchanged
			{Suite: "Endpoint", Name: "should respond", Parameters: "", SuiteSourceFile: "core/spec/models/order_spec.rb"},
			// Path that does match CWD subdir prefix
			{Suite: "ApiEndpoint", Name: "should work", Parameters: "", SuiteSourceFile: "api/spec/models/endpoint_spec.rb"},
		},
	}

	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}

	mockPlatformDetector := &MockPlatformDetector{Platform: mockPlatform}
	mockOptimizationClient := &MockTestOptimizationClient{
		SkippableTests: map[string]bool{},
	}

	runner := NewWithDependencies(mockPlatformDetector, mockOptimizationClient, newDefaultMockCIProviderDetector())

	err := runner.PrepareTestOptimization(ctx)
	if err != nil {
		t.Fatalf("PrepareTestOptimization() should not return error, got: %v", err)
	}

	// "core/spec/..." doesn't match "api" subdir prefix, should be unchanged
	// "api/spec/..." matches "api" subdir prefix, should be normalized to "spec/..."
	expectedFiles := map[string]bool{
		"core/spec/models/order_spec.rb": true, // Mismatched prefix - unchanged
		"spec/models/endpoint_spec.rb":   true, // Matched "api/" prefix - stripped
	}

	if len(runner.testFiles) != 2 {
		t.Fatalf("Expected 2 test files, got %d: %v", len(runner.testFiles), runner.testFiles)
	}

	for file := range runner.testFiles {
		if !expectedFiles[file] {
			t.Errorf("Unexpected test file path %q", file)
		}
	}
}
