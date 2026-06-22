package planner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	ciUtils "github.com/DataDog/ddtest/internal/utils"
)

func TestDiscoveryCacheMetadata_AppendedAndParsed(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "tests.json")
	initial := strings.Join([]string{
		`{"module":"rspec","suite":"Cart","name":"adds item","suiteSourceFile":"spec/cart_spec.rb"}`,
		`{"module":"rspec","suite":"Order","name":"checks out","suiteSourceFile":"spec/order_spec.rb"}`,
		"",
	}, "\n")
	if err := os.WriteFile(filePath, []byte(initial), 0644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	metadata := testDiscoveryCacheMetadata("abc123", "ruby", "rspec", "spec/**/*_spec.rb", "spec/system/**/*_spec.rb")
	if err := appendDiscoveryCacheMetadata(filePath, metadata); err != nil {
		t.Fatalf("appendDiscoveryCacheMetadata() failed: %v", err)
	}

	restored, err := readDiscoveryCacheMetadata(filePath)
	if err != nil {
		t.Fatalf("readDiscoveryCacheMetadata() failed: %v", err)
	}
	if restored != metadata {
		t.Fatalf("metadata = %+v, want %+v", restored, metadata)
	}

	tests, err := parseCachedDiscoveryTests(filePath)
	if err != nil {
		t.Fatalf("parseCachedDiscoveryTests() failed: %v", err)
	}
	if len(tests) != 2 {
		t.Fatalf("parsed %d tests, want 2", len(tests))
	}
	if tests[0].Suite != "Cart" || tests[1].Suite != "Order" {
		t.Fatalf("parsed tests = %+v", tests)
	}
}

func TestReadDiscoveryCacheMetadata_LargeFileFindsFinalMetadata(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "tests.json")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create discovery file: %v", err)
	}
	for i := 0; i < 20000; i++ {
		if _, err := file.WriteString(`{"module":"rspec","suite":"Suite","name":"test","suiteSourceFile":"spec/suite_spec.rb"}` + "\n"); err != nil {
			t.Fatalf("failed to write discovery record: %v", err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close discovery file: %v", err)
	}

	metadata := testDiscoveryCacheMetadata("tail-sha", "ruby", "rspec", "spec/**/*_spec.rb", "")
	if err := appendDiscoveryCacheMetadata(filePath, metadata); err != nil {
		t.Fatalf("appendDiscoveryCacheMetadata() failed: %v", err)
	}

	restored, err := readDiscoveryCacheMetadata(filePath)
	if err != nil {
		t.Fatalf("readDiscoveryCacheMetadata() failed: %v", err)
	}
	if restored.SourceCommit != "tail-sha" {
		t.Fatalf("source commit = %q, want tail-sha", restored.SourceCommit)
	}
}

func TestReadDiscoveryCacheMetadataErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := readDiscoveryCacheMetadata(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("expected missing file error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "tests.json")
		if err := os.WriteFile(filePath, []byte("{"), 0644); err != nil {
			t.Fatalf("write discovery cache: %v", err)
		}

		_, err := readDiscoveryCacheMetadata(filePath)
		if err == nil {
			t.Fatal("expected invalid metadata JSON error")
		}
	})

	t.Run("last record is not metadata", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "tests.json")
		if err := os.WriteFile(filePath, []byte(`{"module":"rspec","suite":"Cart","name":"adds item"}`+"\n"), 0644); err != nil {
			t.Fatalf("write discovery cache: %v", err)
		}

		_, err := readDiscoveryCacheMetadata(filePath)
		if err == nil || !strings.Contains(err.Error(), "does not contain") {
			t.Fatalf("readDiscoveryCacheMetadata() error = %v, want missing metadata", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "tests.json")
		if err := os.WriteFile(filePath, []byte("\n\n"), 0644); err != nil {
			t.Fatalf("write discovery cache: %v", err)
		}

		_, err := readDiscoveryCacheMetadata(filePath)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("readDiscoveryCacheMetadata() error = %v, want EOF", err)
		}
	})
}

func TestParseCachedDiscoveryTestsErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := parseCachedDiscoveryTests(filepath.Join(t.TempDir(), "missing.json"))
		if err == nil {
			t.Fatal("expected missing file error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "tests.json")
		if err := os.WriteFile(filePath, []byte("{"), 0644); err != nil {
			t.Fatalf("write discovery cache: %v", err)
		}

		_, err := parseCachedDiscoveryTests(filePath)
		if err == nil {
			t.Fatal("expected invalid cached test JSON error")
		}
	})
}

func TestDiscoveryCacheHitUsesCachedTests(t *testing.T) {
	t.Chdir(t.TempDir())
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{
		diffOutput:   "",
		statusOutput: "",
	})

	pattern := filepath.Join("spec", "**", "*_spec.rb")
	cachedTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "Cart",
		Name:            "adds item",
		SuiteSourceFile: "spec/cart_spec.rb",
	}
	writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{cachedTest})

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: pattern,
		Tests:            []testoptimization.Test{{Module: "rspec", Suite: "Live", Name: "test", SuiteSourceFile: "spec/live_spec.rb"}},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			cachedTest.DatadogTestId(): true,
		},
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(context.Background()); err != nil {
		t.Fatalf("PreparePlanningData() failed: %v", err)
	}
	if len(mockFramework.DiscoverTestsFiles) != 0 {
		t.Fatalf("expected cache hit to avoid full discovery, got %d calls", len(mockFramework.DiscoverTestsFiles))
	}
	aggregate := runner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: "Cart"}]
	if aggregate.NumTests != 1 || aggregate.NumTestsSkipped != 1 {
		t.Fatalf("cached aggregate = %+v, want one skipped test", aggregate)
	}
}

func TestDiscoveryCacheMissRunsFullDiscoveryAndStoresMetadata(t *testing.T) {
	t.Chdir(t.TempDir())
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{head: "head-sha"})

	pattern := filepath.Join("spec", "**", "*_spec.rb")
	discoveredTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "Order",
		Name:            "checks out",
		SuiteSourceFile: "spec/order_spec.rb",
	}
	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: pattern,
		Tests:            []testoptimization.Test{discoveredTest},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			discoveredTest.DatadogTestId(): true,
		},
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(context.Background()); err != nil {
		t.Fatalf("PreparePlanningData() failed: %v", err)
	}
	if len(mockFramework.DiscoverTestsFiles) != 1 {
		t.Fatalf("expected full discovery once, got %d calls", len(mockFramework.DiscoverTestsFiles))
	}
	metadata, err := readDiscoveryCacheMetadata(discovery.TestsFilePath)
	if err != nil {
		t.Fatalf("readDiscoveryCacheMetadata() failed: %v", err)
	}
	if metadata.SourceCommit != "head-sha" || metadata.TestsLocation != pattern {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestDiscoveryCacheImportsExternalCacheBeforeValidation(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{})

	pattern := filepath.Join("spec", "**", "*_spec.rb")
	cachedTest := testoptimization.Test{
		Module:          "rspec",
		Suite:           "Imported",
		Name:            "test",
		SuiteSourceFile: "spec/imported_spec.rb",
	}
	externalCachePath := filepath.Join(root, "restored-cache.json")
	writeDiscoveryCacheFile(t, externalCachePath, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{cachedTest})
	setPlannerTestDiscoveryCache(t, externalCachePath)

	mockFramework := &MockFramework{
		FrameworkName:    "rspec",
		TestPatternValue: pattern,
		Tests:            []testoptimization.Test{{Module: "rspec", Suite: "Live", Name: "test", SuiteSourceFile: "spec/live_spec.rb"}},
	}
	mockPlatform := &MockPlatform{
		PlatformName: "ruby",
		Tags:         map[string]string{"platform": "ruby"},
		Framework:    mockFramework,
	}
	mockOptimizationClient := &MockTestOptimizationClient{
		Settings: testOptimizationSettings(true, true, false),
		SkippableTests: map[string]bool{
			cachedTest.DatadogTestId(): true,
		},
	}
	runner := NewWithDependencies(
		&MockPlatformDetector{Platform: mockPlatform},
		mockOptimizationClient,
		newDefaultMockCIProviderDetector(),
	)

	if err := runner.PreparePlanningData(context.Background()); err != nil {
		t.Fatalf("PreparePlanningData() failed: %v", err)
	}
	if len(mockFramework.DiscoverTestsFiles) != 0 {
		t.Fatalf("expected imported cache to avoid full discovery, got %d calls", len(mockFramework.DiscoverTestsFiles))
	}
	if _, err := os.Stat(discovery.TestsFilePath); err != nil {
		t.Fatalf("expected imported cache at internal path: %v", err)
	}
}

func TestDiscoveryCacheRestoreRejectsEmptyCache(t *testing.T) {
	t.Chdir(t.TempDir())
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{})

	pattern := filepath.Join("spec", "**", "*_spec.rb")
	writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", nil)
	cache := newDiscoveryCache("ruby", &MockFramework{FrameworkName: "rspec", TestPatternValue: pattern})

	tests, ok := cache.restore()
	if ok || tests != nil {
		t.Fatalf("expected empty cache restore to fail, got ok=%v tests=%v", ok, tests)
	}
}

func TestDiscoveryCacheStoreSkipsWhenGitHeadFails(t *testing.T) {
	t.Chdir(t.TempDir())
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{outputErr: errors.New("git failed")})

	cache := newDiscoveryCache("ruby", &MockFramework{FrameworkName: "rspec", TestPatternValue: "spec/**/*_spec.rb"})
	cache.store()

	if _, err := os.Stat(discovery.TestsFilePath); !os.IsNotExist(err) {
		t.Fatalf("expected no discovery cache file to be created, got err=%v", err)
	}
}

func TestDiscoveryCacheStoreHandlesAppendError(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{head: "head-sha"})
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(discovery.TestsFilePath)), 0755); err != nil {
		t.Fatalf("failed to create discovery parent path: %v", err)
	}
	if err := os.WriteFile(filepath.Dir(discovery.TestsFilePath), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("failed to create broken discovery path: %v", err)
	}

	cache := newDiscoveryCache("ruby", &MockFramework{FrameworkName: "rspec", TestPatternValue: "spec/**/*_spec.rb"})
	cache.store()
}

func TestDiscoveryCacheValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	ciUtils.ResetCwdSubdirPrefixForTesting()
	t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)
	pattern := filepath.Join("spec", "**", "*_spec.rb")
	framework := &MockFramework{FrameworkName: "rspec", TestPatternValue: pattern}

	t.Run("test_root_change_invalidates", func(t *testing.T) {
		mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{diffOutput: "M\x00spec/cart_spec.rb\x00"})
		writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{{
			Module: "rspec", Suite: "Cart", Name: "adds item", SuiteSourceFile: "spec/cart_spec.rb",
		}})

		cache := newDiscoveryCache("ruby", framework)
		err := cache.validate()

		if err == nil || !strings.Contains(err.Error(), "test discovery file changed") {
			t.Fatalf("validation error = %v; want test-root invalidation", err)
		}
	})

	t.Run("app_change_reuses", func(t *testing.T) {
		mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{diffOutput: "M\x00app/models/cart.rb\x00"})
		writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{{
			Module: "rspec", Suite: "Cart", Name: "adds item", SuiteSourceFile: "spec/cart_spec.rb",
		}})

		cache := newDiscoveryCache("ruby", framework)
		err := cache.validate()

		if err != nil {
			t.Fatalf("validation error = %v; want usable cache", err)
		}
	})

	t.Run("custom_test_location_support_file_change_invalidates", func(t *testing.T) {
		setPlannerTestsLocation(t, pattern)
		mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{diffOutput: "M\x00spec/support/shared_examples.rb\x00"})
		writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{{
			Module: "rspec", Suite: "Cart", Name: "adds item", SuiteSourceFile: "spec/cart_spec.rb",
		}})

		cache := newDiscoveryCache("ruby", framework)
		err := cache.validate()

		if err == nil || !strings.Contains(err.Error(), "test discovery file changed") {
			t.Fatalf("validation error = %v; want custom test location root invalidation", err)
		}
	})

	t.Run("custom_test_location_other_project_change_reuses", func(t *testing.T) {
		repoRoot := t.TempDir()
		initGitRepo(t, repoRoot)
		coreDir := filepath.Join(repoRoot, "core")
		if err := os.MkdirAll(coreDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(coreDir)
		ciUtils.ResetCwdSubdirPrefixForTesting()
		t.Cleanup(ciUtils.ResetCwdSubdirPrefixForTesting)
		setPlannerTestsLocation(t, pattern)
		mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{diffOutput: "M\x00other/spec/support/shared_examples.rb\x00"})
		writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{{
			Module: "rspec", Suite: "Cart", Name: "adds item", SuiteSourceFile: "spec/cart_spec.rb",
		}})

		cache := newDiscoveryCache("ruby", framework)
		err := cache.validate()

		if err != nil {
			t.Fatalf("validation error = %v; want usable cache for another project", err)
		}
	})

	t.Run("exclude_pattern_mismatch_invalidates", func(t *testing.T) {
		setPlannerTestsExcludePattern(t, filepath.Join("spec", "system", "**", "*_spec.rb"))
		mockDiscoveryCacheGit(t, mockDiscoveryCacheGitRunner{})
		writePlannerDiscoveryCache(t, "base-sha", "ruby", "rspec", pattern, "", []testoptimization.Test{{
			Module: "rspec", Suite: "Cart", Name: "adds item", SuiteSourceFile: "spec/cart_spec.rb",
		}})

		cache := newDiscoveryCache("ruby", framework)
		err := cache.validate()

		if err == nil || !strings.Contains(err.Error(), "tests exclude pattern mismatch") {
			t.Fatalf("validation error = %v; want exclude mismatch", err)
		}
	})
}

func TestDiscoveryCacheParseGitDiffNameStatus(t *testing.T) {
	output := []byte(strings.Join([]string{
		"M",
		"app/models/cart.rb",
		"R100",
		"spec/old_cart_spec.rb",
		"spec/new_cart_spec.rb",
		"C75",
		"lib/source.rb",
		"lib/copied.rb",
		"R100",
		"spec/dangling_old_spec.rb",
	}, "\x00") + "\x00")

	got := discoveryCacheParseGitDiffNameStatus(output)
	want := []string{
		"app/models/cart.rb",
		"spec/old_cart_spec.rb",
		"spec/new_cart_spec.rb",
		"lib/source.rb",
		"lib/copied.rb",
		"spec/dangling_old_spec.rb",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("discoveryCacheParseGitDiffNameStatus() = %#v, want %#v", got, want)
	}
}

func TestDiscoveryCacheParseGitStatusPorcelain(t *testing.T) {
	output := []byte(strings.Join([]string{
		" M app/models/cart.rb",
		"?? spec/new_cart_spec.rb",
		"R  spec/old_cart_spec.rb",
		"spec/new_cart_spec.rb",
		"C  lib/source.rb",
		"lib/copied.rb",
		"bad",
		" M ",
	}, "\x00") + "\x00")

	got := discoveryCacheParseGitStatusPorcelain(output)
	want := []string{
		"app/models/cart.rb",
		"spec/new_cart_spec.rb",
		"spec/old_cart_spec.rb",
		"spec/new_cart_spec.rb",
		"lib/source.rb",
		"lib/copied.rb",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("discoveryCacheParseGitStatusPorcelain() = %#v, want %#v", got, want)
	}
}

func TestDiscoveryPathMatchesFailsClosedForInvalidPattern(t *testing.T) {
	if !discoveryPathMatches("[", "app/models/cart.rb") {
		t.Fatal("discoveryPathMatches() should match when the pattern is invalid")
	}
}

func TestDiscoveryCacheDebugGitOutputTruncates(t *testing.T) {
	output := []byte(strings.Repeat("a", discoveryCacheDebugGitOutputMaxBytes+10))

	got := discoveryCacheDebugGitOutput(output)

	if len(got) != discoveryCacheDebugGitOutputMaxBytes {
		t.Fatalf("debug output length = %d, want %d", len(got), discoveryCacheDebugGitOutputMaxBytes)
	}
	if got != strings.Repeat("a", discoveryCacheDebugGitOutputMaxBytes) {
		t.Fatal("debug output did not preserve the expected prefix")
	}
}

func TestCopyFileSamePathDoesNotTruncate(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "tests.json")
	contents := []byte("cached discovery")
	if err := os.WriteFile(filePath, contents, 0644); err != nil {
		t.Fatalf("failed to write cache: %v", err)
	}

	if err := copyFile(filePath, filePath); err != nil {
		t.Fatalf("copyFile() failed: %v", err)
	}
	restored, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read cache: %v", err)
	}
	if !slices.Equal(restored, contents) {
		t.Fatalf("cache was truncated: %q", restored)
	}
}

type mockDiscoveryCacheGitRunner struct {
	head         string
	diffOutput   string
	statusOutput string
	outputErr    error
}

func (m mockDiscoveryCacheGitRunner) output(args ...string) ([]byte, error) {
	if m.outputErr != nil {
		return nil, m.outputErr
	}
	if len(args) == 0 {
		return nil, errors.New("missing git args")
	}
	switch args[0] {
	case "cat-file":
		return nil, nil
	case "rev-parse":
		head := m.head
		if head == "" {
			head = "base-sha"
		}
		return []byte(head + "\n"), nil
	case "diff":
		return []byte(m.diffOutput), nil
	case "status":
		return []byte(m.statusOutput), nil
	default:
		return nil, errors.New("unexpected git output: " + strings.Join(args, " "))
	}
}

func mockDiscoveryCacheGit(t *testing.T, runner mockDiscoveryCacheGitRunner) {
	t.Helper()
	originalGit := discoveryCacheGitOutput
	discoveryCacheGitOutput = runner.output
	t.Cleanup(func() {
		discoveryCacheGitOutput = originalGit
	})
}

func writePlannerDiscoveryCache(
	t *testing.T,
	sourceCommit string,
	platformName string,
	frameworkName string,
	testsLocation string,
	testsExcludePattern string,
	tests []testoptimization.Test,
) {
	t.Helper()
	writeDiscoveryCacheFile(t, discovery.TestsFilePath, sourceCommit, platformName, frameworkName, testsLocation, testsExcludePattern, tests)
}

func writeDiscoveryCacheFile(
	t *testing.T,
	filePath string,
	sourceCommit string,
	platformName string,
	frameworkName string,
	testsLocation string,
	testsExcludePattern string,
	tests []testoptimization.Test,
) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		t.Fatalf("failed to create discovery cache directory: %v", err)
	}
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create discovery cache file: %v", err)
	}
	encoder := json.NewEncoder(file)
	for _, test := range tests {
		if err := encoder.Encode(test); err != nil {
			t.Fatalf("failed to write discovery test: %v", err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatalf("failed to close discovery cache file: %v", err)
	}

	metadata := testDiscoveryCacheMetadata(sourceCommit, platformName, frameworkName, testsLocation, testsExcludePattern)
	if err := appendDiscoveryCacheMetadata(filePath, metadata); err != nil {
		t.Fatalf("appendDiscoveryCacheMetadata() failed: %v", err)
	}
}

func testDiscoveryCacheMetadata(sourceCommit, platformName, frameworkName, testsLocation, testsExcludePattern string) discoveryCacheMetadata {
	return discoveryCacheMetadata{
		SchemaVersion:       discoveryCacheSchemaVersion,
		SourceCommit:        sourceCommit,
		Platform:            platformName,
		Framework:           frameworkName,
		TestsLocation:       testsLocation,
		TestsExcludePattern: testsExcludePattern,
	}
}

func setPlannerTestDiscoveryCache(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(settings.Init)
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_TEST_DISCOVERY_CACHE", path)
	settings.Init()
}
