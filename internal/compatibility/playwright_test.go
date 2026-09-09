package compatibility

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestPlaywrightAdapterIntegration(t *testing.T) {
	binary := requireEnv(t, "DDTEST_PLAYWRIGHT_BINARY")
	nodeModules := requireEnv(t, "DDTEST_PLAYWRIGHT_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	t.Chdir(root)
	projectRoot := filepath.Join(root, "apps", "web")
	if err := os.MkdirAll(filepath.Join(projectRoot, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	projects := `[{ name: 'one' }, { name: 'two' }]`
	lifecycleFiles := []string{}
	if playwrightVersionAtLeast(t, binary, 1, 31) {
		projects = `[
    { name: 'setup', testMatch: '**/setup.spec.ts' },
    { name: 'one', testIgnore: /(?:setup|ignored)\.spec\.ts/, dependencies: ['setup'] },
    { name: 'two', testIgnore: /(?:setup|ignored)\.spec\.ts/, dependencies: ['setup'] },
  ]`
		lifecycleFiles = []string{"setup.spec.ts"}
	}
	// Project teardown was added after project dependencies. Older versions
	// ignore the teardown property and treat the named project as a normal
	// project, so only exercise teardown filtering where Playwright supports it.
	if playwrightVersionAtLeast(t, binary, 1, 38) {
		projects = `[
    { name: 'setup', testMatch: '**/setup.spec.ts', teardown: 'teardown' },
    { name: 'teardown', testMatch: '**/teardown.spec.ts' },
    { name: 'one', testIgnore: /(?:setup|teardown|ignored)\.spec\.ts/, dependencies: ['setup'] },
    { name: 'two', testIgnore: /(?:setup|teardown|ignored)\.spec\.ts/, dependencies: ['setup'] },
  ]`
		lifecycleFiles = []string{"setup.spec.ts", "teardown.spec.ts"}
	}
	config := fmt.Sprintf(`module.exports = {
  testDir: './tests',
  testMatch: '**/*.@(spec|test).ts',
  testIgnore: '**/ignored.*',
  projects: %s,
}
`, projects)
	writeFixture(t, projectRoot, "playwright.config.js", config)
	for _, name := range []string{"a.spec.ts", "b.test.ts", "ignored.spec.ts", "not-a-test.ts"} {
		content := "const { test } = require('@playwright/test'); test('works', () => {});\n"
		if name == "b.test.ts" {
			content = "const { test } = require('@playwright/test'); test('must not run', () => { throw new Error('unassigned file ran') });\n"
		}
		writeFixture(t, projectRoot, filepath.Join("tests", name), content)
	}
	for _, name := range lifecycleFiles {
		writeFixture(t, projectRoot, filepath.Join("tests", name), "const { test } = require('@playwright/test'); test('shared lifecycle', () => {});\n")
	}

	baseCommand := []string{binary, "test", "--config", "apps/web/playwright.config.js"}
	configureFramework(shellCommand(baseCommand...), "")
	playwright := framework.NewPlaywright()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	files, err := playwright.DiscoverTestFiles(ctx, discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apps/web/tests/a.spec.ts", "apps/web/tests/b.test.ts"}
	requireFiles(t, files, want)

	projectCommand := append(append([]string{}, baseCommand...), "--project", "one")
	configureFramework(shellCommand(projectCommand...), "")
	projectPlaywright := framework.NewPlaywright()
	if err := projectPlaywright.RunTests(ctx, []string{"apps/web/tests/a.spec.ts"}, nil); err != nil {
		t.Fatalf("running one assigned file failed: %v", err)
	}
	if source, ok := playwright.SourceFileForSuite("a.spec.ts"); !ok || source != "apps/web/tests/a.spec.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}

	emptyCommand := append(append([]string{}, baseCommand...), "__ddtest_no_match__")
	configureFramework(shellCommand(emptyCommand...), "")
	emptyPlaywright := framework.NewPlaywright()
	if files, err := emptyPlaywright.DiscoverTestFiles(ctx, discovery.TestFileSet{}); err != nil || len(files) != 0 {
		t.Fatalf("empty native discovery = %v, %v", files, err)
	}

	writeFixture(t, projectRoot, "tests/broken.spec.ts", "throw new Error('collection exploded')\n")
	brokenCommand := append(append([]string{}, baseCommand...), "broken.spec.ts")
	configureFramework(shellCommand(brokenCommand...), "")
	brokenPlaywright := framework.NewPlaywright()
	if _, err := brokenPlaywright.DiscoverTestFiles(ctx, discovery.TestFileSet{}); err == nil {
		t.Fatal("collection failure was accepted as an empty discovery")
	}
}

func playwrightVersionAtLeast(t *testing.T, binary string, wantedMajor, wantedMinor int) bool {
	t.Helper()
	output, err := exec.Command(binary, "--version").Output() // no-dd-sa:go-security/command-injection
	if err != nil {
		t.Fatalf("failed to read Playwright version: %v", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) == 0 {
		t.Fatalf("unexpected Playwright version output: %q", output)
	}
	parts := strings.Split(strings.TrimPrefix(fields[len(fields)-1], "v"), ".")
	if len(parts) < 2 {
		t.Fatalf("unexpected Playwright version output: %q", output)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		t.Fatalf("unexpected Playwright version output: %q", output)
	}
	return major > wantedMajor || major == wantedMajor && minor >= wantedMinor
}
