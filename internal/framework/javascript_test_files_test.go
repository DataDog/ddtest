package framework

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
)

type noJavaScriptCommands struct{ t testing.TB }

func (e noJavaScriptCommands) CombinedOutput(context.Context, string, []string, map[string]string) ([]byte, error) {
	e.t.Fatal("file discovery must not start a process")
	return nil, nil
}
func (e noJavaScriptCommands) Output(context.Context, string, []string, map[string]string) ([]byte, []byte, error) {
	e.t.Fatal("file discovery must not start a process")
	return nil, nil, nil
}
func (e noJavaScriptCommands) Run(context.Context, string, []string, map[string]string) error {
	e.t.Fatal("empty assignments must not start a process")
	return nil
}

func filesystemFrameworks(t testing.TB) []Framework {
	e := noJavaScriptCommands{t}
	return []Framework{
		&Jest{executor: e}, &Vitest{executor: e}, &Mocha{executor: e},
		&Cypress{executor: e}, &Playwright{executor: e}, &Cucumber{executor: e},
	}
}

func writeJavaScriptPath(t testing.TB, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("throw new Error('discovery loaded a file')\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestJavaScriptFileDiscoveryDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	// Neither configs, source files, dependency trees, nor test bodies may run.
	files := []string{
		"vitest.config.ts", "cypress.config.js", "playwright.config.js", "cucumber.js",
		"src/a.test.js", "src/b.spec.tsx", "src/c.test.mts", "src/d.spec.cts",
		"src/__tests__/plain.js", "src/__tests__/nested/plain.ts", "src/helper.js",
		"test/a.js", "test/nested/b.mjs", "test/c.cjs", "test/notes.txt",
		"cypress/e2e/a.cy.ts", "cypress/e2e/nested/b.cy.jsx",
		"features/a.feature", "features/nested/b.feature.md",
		"node_modules/dependency/a.test.js", "packages/a/node_modules/dependency/b.spec.ts",
		".git/hidden.test.js",
	}
	for _, file := range files {
		writeJavaScriptPath(t, file)
	}
	common := []string{"src/a.test.js", "src/b.spec.tsx", "src/c.test.mts", "src/d.spec.cts"}
	expected := map[string][]string{
		"jest":   append(slices.Clone(common), "src/__tests__/plain.js", "src/__tests__/nested/plain.ts"),
		"vitest": common, "playwright": common,
		"mocha":    {"test/a.js", "test/nested/b.mjs", "test/c.cjs"},
		"cypress":  {"cypress/e2e/a.cy.ts", "cypress/e2e/nested/b.cy.jsx"},
		"cucumber": {"features/a.feature", "features/nested/b.feature.md"},
	}
	for _, f := range filesystemFrameworks(t) {
		t.Run(f.Name(), func(t *testing.T) {
			got, err := f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: f.TestPattern()})
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(expected[f.Name()])
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("files = %v, want %v", got, want)
			}
		})
	}
}

func TestJavaScriptFileDiscoverySelections(t *testing.T) {
	for _, f := range filesystemFrameworks(t) {
		t.Run(f.Name(), func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, file := range []string{"checks/a.check.js", "checks/nested/b.check.js", "checks/ignored/c.check.js", "src/other.test.js"} {
				writeJavaScriptPath(t, file)
			}
			setTestsLocation(t, "checks/**/*.check.js")
			setTestsExcludePattern(t, "checks/ignored/**")
			resolved, err := discovery.ResolveTestFiles(f.TestPattern(), "checks/ignored/**")
			if err != nil {
				t.Fatal(err)
			}
			for _, selection := range []discovery.TestFileSet{{Pattern: f.TestPattern()}, resolved, {}} {
				got, err := f.DiscoverTestFiles(context.Background(), selection)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(got, []string{"checks/a.check.js", "checks/nested/b.check.js"}) {
					t.Fatalf("files = %v", got)
				}
			}
			original := []string{"checks/nested/b.check.js"}
			got, err := f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{ExplicitFiles: original})
			if err != nil || !slices.Equal(got, original) {
				t.Fatalf("explicit selection = %v, %v", got, err)
			}
			got[0] = "changed"
			if original[0] != "checks/nested/b.check.js" {
				t.Fatal("mutated caller's explicit selection")
			}
			got, err = f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: f.TestPattern(), ExplicitFiles: []string{}})
			if err != nil || len(got) != 0 {
				t.Fatalf("empty explicit selection = %v, %v", got, err)
			}
			got, err = f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: "missing/**/*.js"})
			if err != nil || len(got) != 0 {
				t.Fatalf("unmatched pattern = %v, %v", got, err)
			}
			if _, err := f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: "checks/["}); err == nil {
				t.Fatal("invalid glob accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := f.DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: f.TestPattern()}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
			if err := f.RunTests(context.Background(), nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJavaScriptDiscoveryDoesNotFollowDirectorySymlinks(t *testing.T) {
	t.Chdir(t.TempDir())
	writeJavaScriptPath(t, "tests/a.test.js")
	if err := os.Symlink("..", "tests/loop"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files, err := NewJest().DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil || !slices.Equal(files, []string{"tests/a.test.js"}) {
		t.Fatalf("files = %v, %v", files, err)
	}
}

func TestPlaywrightSuitePathsWithoutLoadingConfiguration(t *testing.T) {
	p := &Playwright{suiteSourceFiles: indexPlaywrightSuiteFiles([]string{"apps/web/tests/a.spec.ts", "apps/web/tests/nested/b.spec.ts", "apps/other/tests/a.spec.ts"})}
	if source, ok := p.SourceFileForSuite("nested/b.spec.ts"); !ok || source != "apps/web/tests/nested/b.spec.ts" {
		t.Fatalf("source = %q, %v", source, ok)
	}
	if source, ok := p.SourceFileForSuite("a.spec.ts"); ok {
		t.Fatalf("ambiguous suite resolved to %q", source)
	}
}

// Include more than MaxExplicitTestFiles and a dependency tree to guard both
// the planner's large-selection fallback and the cost of irrelevant traversal.
func BenchmarkJavaScriptFileDiscovery(b *testing.B) {
	b.Chdir(b.TempDir())
	for i := 0; i < 10000; i++ {
		writeJavaScriptPath(b, fmt.Sprintf("packages/p%d/test/%d.test.js", i/100, i))
	}
	for i := 0; i < 1000; i++ {
		writeJavaScriptPath(b, fmt.Sprintf("packages/p0/node_modules/dependency/%d.test.js", i))
	}
	for _, f := range filesystemFrameworks(b) {
		b.Run(f.Name(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				files, err := f.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: "packages/**/*.test.js"})
				if err != nil || len(files) != 10000 {
					b.Fatalf("discovery = %d files, %v", len(files), err)
				}
			}
		})
	}
}

// ResolveTestFiles deliberately avoids passing more than 8,000 explicit paths.
// The filesystem fallback must still preserve the user's exclude pattern.
func TestJavaScriptDiscoveryLargeExcludeFallback(t *testing.T) {
	t.Chdir(t.TempDir())
	setTestsLocation(t, "tests/**/*.test.js")
	setTestsExcludePattern(t, "tests/ignored/**")
	for i := 0; i <= discovery.MaxExplicitTestFiles; i++ {
		writeJavaScriptPath(t, fmt.Sprintf("tests/%d.test.js", i))
	}
	writeJavaScriptPath(t, "tests/ignored/skipped.test.js")
	resolved, err := discovery.ResolveTestFiles("tests/**/*.test.js", "tests/ignored/**")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.UseExplicitFiles() {
		t.Fatal("expected large-selection pattern fallback")
	}
	files, err := (&Jest{executor: noJavaScriptCommands{t}}).DiscoverTestFiles(context.Background(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != discovery.MaxExplicitTestFiles+1 || slices.Contains(files, "tests/ignored/skipped.test.js") {
		t.Fatalf("large discovery returned %d files", len(files))
	}
}
