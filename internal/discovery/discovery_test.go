package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBaseEnv(t *testing.T) {
	env := BaseEnv()

	expectedVars := map[string]string{
		"DD_CIVISIBILITY_ENABLED":                "1",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":      "true",
		"DD_API_KEY":                             "dummy_key",
		"DD_TEST_OPTIMIZATION_DISCOVERY_ENABLED": "1",
		"DD_TEST_OPTIMIZATION_DISCOVERY_FILE":    TestsFilePath,
	}

	for key, expectedValue := range expectedVars {
		if actualValue, exists := env[key]; !exists {
			t.Errorf("expected %q to be present in BaseEnv", key)
		} else if actualValue != expectedValue {
			t.Errorf("expected %q=%q, got %q", key, expectedValue, actualValue)
		}
	}

	if len(env) != len(expectedVars) {
		t.Errorf("expected %d env vars, got %d", len(expectedVars), len(env))
	}
}

func TestDiscoverTestFiles(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := DiscoverTestFiles(filepath.Join("test", "**", "*_test.rb"), "")
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"test/system/payment_test.rb",
		"test/system/users_test.rb",
		"test/unit/order_test.rb",
		"test/unit/user_test.rb",
	}
	if !slices.Equal(files, expected) {
		t.Fatalf("expected %v, got %v", expected, files)
	}
}

func TestDiscoverTestFilesWithExcludePattern(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := DiscoverTestFiles(
		filepath.Join("test", "**", "*_test.rb"),
		filepath.Join("test", "system", "**", "*_test.rb"),
	)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"test/unit/order_test.rb",
		"test/unit/user_test.rb",
	}
	if !slices.Equal(files, expected) {
		t.Fatalf("expected %v, got %v", expected, files)
	}
}

func TestDiscoverTestFilesWithExcludeDirectory(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := DiscoverTestFiles(
		filepath.Join("test", "**", "*_test.rb"),
		filepath.Join("test", "system"),
	)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"test/unit/order_test.rb",
		"test/unit/user_test.rb",
	}
	if !slices.Equal(files, expected) {
		t.Fatalf("expected %v, got %v", expected, files)
	}
}

func TestDiscoverTestFilesNormalizesPaths(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := DiscoverTestFiles(filepath.Join(".", "test", "unit", "*_test.rb"), "")
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"test/unit/order_test.rb",
		"test/unit/user_test.rb",
	}
	if !slices.Equal(files, expected) {
		t.Fatalf("expected %v, got %v", expected, files)
	}
}

func TestDiscoverTestFilesWithInvalidIncludePattern(t *testing.T) {
	_, err := DiscoverTestFiles("test/[", "")
	if err == nil {
		t.Fatal("expected error for invalid include pattern")
	}
}

func TestDiscoverTestFilesWithInvalidExcludePattern(t *testing.T) {
	_, err := DiscoverTestFiles(filepath.Join("test", "**", "*_test.rb"), "test/[")
	if err == nil {
		t.Fatal("expected error for invalid exclude pattern")
	}
}

func TestDiscoverTestFilesEmptyAndMissingRoot(t *testing.T) {
	t.Chdir(t.TempDir())

	files, err := DiscoverTestFiles("", "")
	if err != nil {
		t.Fatalf("empty pattern returned error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("empty pattern returned files: %v", files)
	}

	files, err = DiscoverTestFiles(filepath.Join("missing", "**", "*_test.rb"), "")
	if err != nil {
		t.Fatalf("missing root returned error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("missing root returned files: %v", files)
	}
}

func TestResolveTestFilesWithoutExcludePattern(t *testing.T) {
	pattern := filepath.Join("test", "**", "*_test.rb")

	files, err := ResolveTestFiles(pattern, "")
	if err != nil {
		t.Fatal(err)
	}

	if files.Pattern != pattern {
		t.Fatalf("Pattern = %q, want %q", files.Pattern, pattern)
	}
	if files.UseExplicitFiles() {
		t.Fatalf("expected discovery pattern to be used, got explicit files %#v", files.ExplicitFiles)
	}
	if files.Empty() {
		t.Fatal("pattern-based discovery should not be empty")
	}
}

func TestResolveTestFilesWithExcludePattern(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := ResolveTestFiles(
		filepath.Join("test", "**", "*_test.rb"),
		filepath.Join("test", "system", "**", "*_test.rb"),
	)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"test/unit/order_test.rb",
		"test/unit/user_test.rb",
	}
	if !files.UseExplicitFiles() {
		t.Fatal("expected filtered discovery to use explicit files")
	}
	if !slices.Equal(files.ExplicitFiles, expected) {
		t.Fatalf("expected %v, got %v", expected, files.ExplicitFiles)
	}
	if files.Empty() {
		t.Fatal("filtered discovery should not be empty")
	}
}

func TestResolveTestFilesWithExcludePatternEmptyResult(t *testing.T) {
	root := createDiscoveryFixture(t)
	t.Chdir(root)

	files, err := ResolveTestFiles(
		filepath.Join("test", "**", "*_test.rb"),
		filepath.Join("test", "**", "*_test.rb"),
	)
	if err != nil {
		t.Fatal(err)
	}

	if !files.UseExplicitFiles() {
		t.Fatal("expected empty filtered discovery to use explicit files")
	}
	if !files.Empty() {
		t.Fatalf("expected empty explicit files, got %#v", files.ExplicitFiles)
	}
}

func TestCleanupRemovesDiscoveryFile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Dir(TestsFilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TestsFilePath, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}

	Cleanup()

	if _, err := os.Stat(TestsFilePath); !os.IsNotExist(err) {
		t.Fatalf("expected discovery file to be removed, stat error: %v", err)
	}
	Cleanup()
}

func TestDiscoverTestsReturnsParseError(t *testing.T) {
	logs := captureDiscoveryLogs(t)
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Dir(TestsFilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TestsFilePath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests, err := DiscoverTests(context.Background(), successfulDiscoveryExecutor{}, "bundle", []string{"exec", "rspec"}, map[string]string{"APP_ENV": "test"})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if tests != nil {
		t.Fatalf("expected nil tests on parse error, got %+v", tests)
	}
	if !strings.Contains(logs.String(), "Error parsing JSON") {
		t.Fatalf("expected parse error log, got: %s", logs.String())
	}
}

func TestDiscoveryCommandLogValueShortCommand(t *testing.T) {
	got := discoveryCommandLogValue("bundle", []string{"exec", "rspec"})
	if got != "bundle exec rspec" {
		t.Fatalf("discoveryCommandLogValue() = %q, want bundle exec rspec", got)
	}
}

func TestDiscoverTestsLogsTruncatedCommand(t *testing.T) {
	logs := captureDiscoveryLogs(t)
	root := t.TempDir()
	t.Chdir(root)

	if err := os.MkdirAll(filepath.Dir(TestsFilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TestsFilePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	longArg := strings.Repeat("a", discoveryCommandLogMaxLength)
	args := []string{"exec", "rspec", "spec/example_spec.rb", longArg}
	_, err := DiscoverTests(context.Background(), successfulDiscoveryExecutor{}, "bundle", args, nil)
	if err != nil {
		t.Fatal(err)
	}

	loggedCommand := discoveryCommandLogValue("bundle", args)
	if len(loggedCommand) != discoveryCommandLogMaxLength {
		t.Fatalf("expected logged command to be %d characters, got %d", discoveryCommandLogMaxLength, len(loggedCommand))
	}
	if !strings.HasSuffix(loggedCommand, discoveryCommandLogTruncSuffix) {
		t.Fatalf("expected logged command to have truncation suffix, got %q", loggedCommand)
	}

	output := logs.String()
	if !strings.Contains(output, "Discovering tests with command") || !strings.Contains(output, loggedCommand) {
		t.Fatalf("expected discovery command to be logged, got: %s", output)
	}
	if strings.Contains(output, "args=") {
		t.Fatalf("expected discovery args not to be logged separately, got: %s", output)
	}
	if strings.Contains(output, longArg) {
		t.Fatalf("expected full long arg not to appear in logs, got: %s", output)
	}
}

func TestExecuteCommandLogsCancelledDiscoveryAtDebug(t *testing.T) {
	logs := captureDiscoveryLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := executeCommand(ctx, failingDiscoveryExecutor{err: errors.New("signal: killed")}, "bundle", []string{"exec", "rspec"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	output := logs.String()
	if !strings.Contains(output, "level=DEBUG") || !strings.Contains(output, "Test discovery was cancelled") {
		t.Fatalf("expected cancelled discovery to log at DEBUG, got: %s", output)
	}
	if strings.Contains(output, "level=WARN") || strings.Contains(output, "Failed to run test discovery") {
		t.Fatalf("expected cancelled discovery not to log a warning, got: %s", output)
	}
}

func TestExecuteCommandLogsUnexpectedFailureAtWarn(t *testing.T) {
	logs := captureDiscoveryLogs(t)

	err := executeCommand(context.Background(), failingDiscoveryExecutor{
		output: []byte("boom"),
		err:    errors.New("exit status 1"),
	}, "bundle", []string{"exec", "rspec"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	output := logs.String()
	if !strings.Contains(output, "level=WARN") || !strings.Contains(output, "Failed to run test discovery") {
		t.Fatalf("expected unexpected discovery failure to log at WARN, got: %s", output)
	}
	if strings.Contains(output, "Test discovery was cancelled") {
		t.Fatalf("expected unexpected discovery failure not to log cancellation, got: %s", output)
	}
}

func BenchmarkDiscoverTestFiles10000(b *testing.B) {
	root := b.TempDir()
	createBenchmarkTestFiles(b, root)

	includePattern := filepath.Join(root, "test", "**", "*_test.rb")
	excludePattern := filepath.Join(root, "test", "system", "**", "*_test.rb")
	excludeDirectory := filepath.Join(root, "test", "system")

	b.Run("no_exclude", func(b *testing.B) {
		for b.Loop() {
			files, err := DiscoverTestFiles(includePattern, "")
			if err != nil {
				b.Fatal(err)
			}
			if len(files) != 10_000 {
				b.Fatalf("expected 10000 files, got %d", len(files))
			}
		}
	})

	b.Run("exclude_half", func(b *testing.B) {
		for b.Loop() {
			files, err := DiscoverTestFiles(includePattern, excludePattern)
			if err != nil {
				b.Fatal(err)
			}
			if len(files) != 5_000 {
				b.Fatalf("expected 5000 files, got %d", len(files))
			}
		}
	})

	b.Run("exclude_directory", func(b *testing.B) {
		for b.Loop() {
			files, err := DiscoverTestFiles(includePattern, excludeDirectory)
			if err != nil {
				b.Fatal(err)
			}
			if len(files) != 5_000 {
				b.Fatalf("expected 5000 files, got %d", len(files))
			}
		}
	})
}

func createBenchmarkTestFiles(tb testing.TB, root string) {
	tb.Helper()

	for _, suite := range []string{"unit", "system"} {
		for dirIndex := range 50 {
			dir := filepath.Join(root, "test", suite, fmt.Sprintf("group_%02d", dirIndex))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				tb.Fatal(err)
			}
			for fileIndex := range 100 {
				path := filepath.Join(dir, fmt.Sprintf("example_%03d_test.rb", fileIndex))
				if err := os.WriteFile(path, []byte("# test\n"), 0o644); err != nil {
					tb.Fatal(err)
				}
			}
		}
	}
}

func createDiscoveryFixture(tb testing.TB) string {
	tb.Helper()

	root := tb.TempDir()
	files := []string{
		filepath.Join("test", "unit", "user_test.rb"),
		filepath.Join("test", "unit", "order_test.rb"),
		filepath.Join("test", "unit", "helper.rb"),
		filepath.Join("test", "system", "users_test.rb"),
		filepath.Join("test", "system", "payment_test.rb"),
		filepath.Join("test", "system", "support.rb"),
		filepath.Join("spec", "models", "user_spec.rb"),
	}

	for _, file := range files {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# test\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}

	return root
}

type failingDiscoveryExecutor struct {
	output []byte
	err    error
}

type successfulDiscoveryExecutor struct{}

func (successfulDiscoveryExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return nil, nil
}

func (successfulDiscoveryExecutor) Run(context.Context, string, []string, map[string]string) error {
	return nil
}

func (e failingDiscoveryExecutor) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	return e.output, e.err
}

func (e failingDiscoveryExecutor) Run(context.Context, string, []string, map[string]string) error {
	return e.err
}

func captureDiscoveryLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var logs bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})

	return &logs
}
