package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
