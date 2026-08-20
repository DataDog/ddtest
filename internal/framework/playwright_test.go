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
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
)

type playwrightCommandExecutor struct {
	output       []byte
	err          error
	runCalls     int
	capturedName string
	capturedArgs []string
	capturedEnv  map[string]string
}

type playwrightCommandExitError struct {
	code int
}

func (e playwrightCommandExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e playwrightCommandExitError) ExitCode() int { return e.code }

func (e *playwrightCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	e.capture(name, args, env)
	return e.output, e.err
}

func (e *playwrightCommandExecutor) Run(_ context.Context, name string, args []string, env map[string]string) error {
	e.runCalls++
	e.capture(name, args, env)
	return e.err
}

func (e *playwrightCommandExecutor) capture(name string, args []string, env map[string]string) {
	e.capturedName = name
	e.capturedArgs = slices.Clone(args)
	e.capturedEnv = make(map[string]string, len(env))
	for key, value := range env {
		e.capturedEnv[key] = value
	}
}

func TestPlaywrightFrameworkMetadata(t *testing.T) {
	playwright := NewPlaywright()
	if playwright.Name() != "playwright" || playwright.SupportsFullTestDiscovery() {
		t.Fatalf("unexpected metadata: %q, full=%v", playwright.Name(), playwright.SupportsFullTestDiscovery())
	}
	if playwright.TestPattern() != playwrightDefaultPattern {
		t.Fatalf("TestPattern() = %q", playwright.TestPattern())
	}
	matcher, err := discovery.NewTestFileSetMatcher(discovery.TestFileSet{Pattern: playwright.TestPattern()}, "")
	if err != nil || !matcher.Match("tests/example.spec.ts") || matcher.Match("tests/example.ts") {
		t.Fatalf("default Playwright pattern did not match expected test files: %v", err)
	}
	if source, ok := playwright.SourceFileForSuite(" tests/a.spec.ts "); !ok || source != "tests/a.spec.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
	if _, ok := playwright.SourceFileForSuite(" "); ok {
		t.Fatal("blank suite should not resolve to a source file")
	}
	if tests, err := playwright.DiscoverTests(context.Background(), discovery.TestFileSet{}); tests != nil || !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("DiscoverTests() = %v, %v; want unsupported", tests, err)
	}
}

func TestPlaywrightCLIArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "local", command: "node_modules/.bin/playwright", args: []string{"test", "--project=web"}, want: []string{"--project=web"}},
		{name: "Windows command shim", command: `node_modules\.bin\playwright.cmd`, args: []string{"test"}, want: []string{}},
		{name: "Windows PowerShell shim", command: "PLAYWRIGHT.PS1", args: []string{"test"}, want: []string{}},
		{name: "npx", command: "npx", args: []string{"playwright", "test"}, want: []string{}},
		{name: "pnpm", command: "pnpm", args: []string{"exec", "playwright", "test", "--grep", "smoke"}, want: []string{"--grep", "smoke"}},
		{name: "indirect", command: "npm", args: []string{"test"}, wantErr: true},
		{name: "wrong subcommand", command: "playwright", args: []string{"show-report"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := playwrightCLIArgs(test.command, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("playwrightCLIArgs() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("playwrightCLIArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlaywrightCommandConstruction(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	reporter := filepath.Join(root, "reporter.cjs")
	baseArgs := []string{
		"exec", "playwright", "test", "original.spec.ts", "--config", "apps/web/playwright.config.ts",
		"--project", "chromium", "firefox", "--grep", "smoke", "--reporter", "html",
		"--shard=1/2", "--ui", "--ui-port", "3000", "--debug",
	}
	discoveryArgs := playwrightDiscoveryArgs("pnpm", baseArgs, reporter)
	wantDiscovery := []string{
		"exec", "playwright", "test", "original.spec.ts", "--config", "apps/web/playwright.config.ts",
		"--project", "chromium", "firefox", "--grep", "smoke", "--list", "--reporter=" + reporter,
	}
	if !slices.Equal(discoveryArgs, wantDiscovery) {
		t.Fatalf("playwrightDiscoveryArgs() = %v, want %v", discoveryArgs, wantDiscovery)
	}

	runArgs := playwrightRunArgs("pnpm", baseArgs, []string{"apps/web/tests/a[1].spec.ts", "apps/web/tests/b.spec.ts"})
	wantRunOptions := []string{
		"--config", "apps/web/playwright.config.ts", "--project", "chromium", "firefox",
		"--grep", "smoke", "--reporter", "html", "--debug",
	}
	if len(runArgs) < len(wantRunOptions)+5 || !slices.Equal(runArgs[:3], []string{"exec", "playwright", "test"}) {
		t.Fatalf("playwrightRunArgs() = %v", runArgs)
	}
	if slices.Contains(runArgs, "original.spec.ts") || slices.Contains(runArgs, "--shard=1/2") || slices.Contains(runArgs, "--ui-port") {
		t.Fatalf("playwrightRunArgs() retained conflicting arguments: %v", runArgs)
	}
	if !strings.HasPrefix(runArgs[3], "^") || !strings.Contains(runArgs[3], `a\[1\]\.spec\.ts$`) {
		t.Fatalf("first file filter is not an exact escaped regex: %q", runArgs[3])
	}
	if !strings.HasPrefix(runArgs[4], "^") || !slices.Equal(runArgs[5:5+len(wantRunOptions)], wantRunOptions) {
		t.Fatalf("file filters must precede preserved options: %v", runArgs)
	}
	trailingProjectArgs := playwrightRunArgs("playwright", []string{"test", "--project", "chromium"}, []string{"tests/a.spec.ts"})
	if len(trailingProjectArgs) != 4 || !strings.HasPrefix(trailingProjectArgs[1], "^") ||
		!slices.Equal(trailingProjectArgs[2:], []string{"--project", "chromium"}) {
		t.Fatalf("trailing --project consumed the file filter: %v", trailingProjectArgs)
	}
}

func TestParsePlaywrightDiscoveryOutput(t *testing.T) {
	root := "/repo/" + playwrightDiscoveryMarker
	validPayload, err := json.Marshal(playwrightDiscoveryResult{
		RootDir: root,
		Files:   []string{"z.spec.ts", "a.test.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := []byte("config log\n" + playwrightDiscoveryMarker + string(validPayload) + "\n" +
		playwrightDiscoveryMarker + "not-json\nother output mentioning " + playwrightDiscoveryMarker)
	result, err := parsePlaywrightDiscoveryOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if result.RootDir != root || !slices.Equal(result.Files, []string{"z.spec.ts", "a.test.ts"}) {
		t.Fatalf("result = %#v", result)
	}
	if _, err := parsePlaywrightDiscoveryOutput([]byte("noise")); err == nil {
		t.Fatal("expected missing marker error")
	}
	if _, err := parsePlaywrightDiscoveryOutput([]byte(playwrightDiscoveryMarker + `not-json`)); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestPlaywrightDiscoverTestFilesUsesNativeListAndNormalizes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	files := []string{
		"apps/web/tests/b.test.ts",
		"apps/web/tests/a.spec.ts",
		"apps/web/tests/a.spec.ts",
	}
	if err := os.MkdirAll(filepath.Join(root, "apps/web/tests"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, file := range files[:2] {
		if err := os.WriteFile(filepath.Join(root, file), []byte("test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(playwrightDiscoveryResult{RootDir: root, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	payload := playwrightDiscoveryMarker + string(encoded)
	executor := &playwrightCommandExecutor{output: []byte("noise\n" + payload)}
	playwright := &Playwright{
		executor: executor,
		commandOverride: []string{
			"pnpm", "exec", "playwright", "test", "--config", "apps/web/playwright.config.ts", "--project", "chromium", "--reporter", "html",
		},
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init --max-old-space-size=2048", "CUSTOM": "value"},
	}
	discovered, err := playwright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discovered, []string{"apps/web/tests/a.spec.ts", "apps/web/tests/b.test.ts"}) {
		t.Fatalf("files = %v", discovered)
	}
	if executor.capturedName != "pnpm" || !slices.Contains(executor.capturedArgs, "--list") || slices.Contains(executor.capturedArgs, "html") {
		t.Fatalf("discovery command = %q %v", executor.capturedName, executor.capturedArgs)
	}
	if got := executor.capturedEnv["NODE_OPTIONS"]; got != "--max-old-space-size=2048" {
		t.Fatalf("discovery NODE_OPTIONS = %q", got)
	}
	if executor.capturedEnv["CUSTOM"] != "value" {
		t.Fatalf("discovery env = %v", executor.capturedEnv)
	}
	for _, arg := range executor.capturedArgs {
		if strings.HasPrefix(arg, "--reporter=") {
			if _, err := os.Stat(strings.TrimPrefix(arg, "--reporter=")); !os.IsNotExist(err) {
				t.Fatalf("temporary reporter was not removed: %v", err)
			}
			return
		}
	}
	t.Fatal("discovery reporter argument missing")
}

func TestPlaywrightDiscoveryAcceptsOnlyTheNoTestsExit(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	executor := &playwrightCommandExecutor{
		output: []byte(playwrightDiscoveryMarker + `{"rootDir":"` + root + `","files":[]}` + "\n" +
			playwrightErrorMarker + `{"message":"Error: No tests found"}`),
		err: playwrightCommandExitError{code: 1},
	}
	playwright := &Playwright{executor: executor, commandOverride: []string{"playwright", "test"}, platformEnv: map[string]string{}}
	files, err := playwright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil || len(files) != 0 {
		t.Fatalf("empty discovery = %v, %v", files, err)
	}

	tests := []struct {
		name   string
		output string
		err    error
	}{
		{
			name: "collection failure followed by no tests",
			output: playwrightDiscoveryMarker + `{"rootDir":"` + root + `","files":[]}` + "\n" +
				playwrightErrorMarker + `{"message":"Error: collection exploded"}` + "\n" +
				playwrightErrorMarker + `{"message":"Error: No tests found"}`,
			err: playwrightCommandExitError{code: 1},
		},
		{
			name: "wrong exit code",
			output: playwrightDiscoveryMarker + `{"rootDir":"` + root + `","files":[]}` + "\n" +
				playwrightErrorMarker + `{"message":"Error: No tests found"}`,
			err: playwrightCommandExitError{code: 2},
		},
		{
			name: "non-exit error",
			output: playwrightDiscoveryMarker + `{"rootDir":"` + root + `","files":[]}` + "\n" +
				playwrightErrorMarker + `{"message":"Error: No tests found"}`,
			err: errors.New("command failed"),
		},
		{
			name: "malformed file payload",
			output: playwrightDiscoveryMarker + `not-json` + "\n" +
				playwrightErrorMarker + `{"message":"Error: No tests found"}`,
			err: playwrightCommandExitError{code: 1},
		},
		{
			name: "failed listing with files",
			output: playwrightDiscoveryMarker + `{"rootDir":"` + root + `","files":["` + filepath.Join(root, "a.spec.ts") + `"]}` + "\n" +
				playwrightErrorMarker + `{"message":"Error: No tests found"}`,
			err: playwrightCommandExitError{code: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor.output = []byte(test.output)
			executor.err = test.err
			if _, err := playwright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{}); err == nil {
				t.Fatal("expected failed Playwright listing to return its command error")
			}
		})
	}

	// Playwright 1.18 reports no tests before onBegin, so there is no file
	// marker. The structured reporter error and exit code are still enough to
	// identify the expected empty-suite result.
	executor.output = []byte(playwrightErrorMarker + `{"message":"=================\n no tests found.\n================="}`)
	executor.err = playwrightCommandExitError{code: 1}
	files, err = playwright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil || len(files) != 0 {
		t.Fatalf("legacy empty discovery = %v, %v", files, err)
	}
}

func TestPlaywrightRunTestsMergesEnvironmentAndSkipsEmptyAssignments(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	executor := &playwrightCommandExecutor{}
	playwright := &Playwright{
		executor:        executor,
		commandOverride: []string{"playwright", "test", "old.spec.ts", "--shard", "1/3"},
		platformEnv:     map[string]string{"SHARED": "platform", "PLATFORM": "yes"},
	}
	if err := playwright.RunTests(context.Background(), nil, nil); err != nil || executor.runCalls != 0 {
		t.Fatalf("empty RunTests() = %v, calls = %d", err, executor.runCalls)
	}
	if err := playwright.RunTests(context.Background(), []string{"tests/a.spec.ts"}, map[string]string{"SHARED": "worker", "WORKER": "yes"}); err != nil {
		t.Fatal(err)
	}
	if executor.runCalls != 1 || slices.Contains(executor.capturedArgs, "old.spec.ts") || slices.Contains(executor.capturedArgs, "--shard") {
		t.Fatalf("run command = %q %v", executor.capturedName, executor.capturedArgs)
	}
	if executor.capturedEnv["SHARED"] != "worker" || executor.capturedEnv["PLATFORM"] != "yes" || executor.capturedEnv["WORKER"] != "yes" {
		t.Fatalf("run env = %v", executor.capturedEnv)
	}
}

func TestPlaywrightSourceFileForSuiteUsesConfigDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("apps/web", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("apps/web/playwright.config.ts", []byte("export default {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	playwright := &Playwright{commandOverride: []string{"playwright", "test", "--config", "apps/web/playwright.config.ts"}}
	if source, ok := playwright.SourceFileForSuite("tests/a.spec.ts"); !ok || source != "apps/web/tests/a.spec.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
}

func TestPlaywrightAdapterIntegration(t *testing.T) {
	binary := os.Getenv("DDTEST_PLAYWRIGHT_BINARY")
	nodeModules := os.Getenv("DDTEST_PLAYWRIGHT_NODE_MODULES")
	if binary == "" || nodeModules == "" {
		t.Skip("DDTEST_PLAYWRIGHT_BINARY and DDTEST_PLAYWRIGHT_NODE_MODULES are required")
	}
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
	if err := os.WriteFile(filepath.Join(projectRoot, "playwright.config.js"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.spec.ts", "b.test.ts", "ignored.spec.ts", "not-a-test.ts"} {
		content := "const { test } = require('@playwright/test'); test('works', () => {});\n"
		if name == "b.test.ts" {
			content = "const { test } = require('@playwright/test'); test('must not run', () => { throw new Error('unassigned file ran') });\n"
		}
		if err := os.WriteFile(filepath.Join(projectRoot, "tests", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range lifecycleFiles {
		content := "const { test } = require('@playwright/test'); test('shared lifecycle', () => {});\n"
		if err := os.WriteFile(filepath.Join(projectRoot, "tests", name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	playwright := &Playwright{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: []string{binary, "test", "--config", "apps/web/playwright.config.js"},
		platformEnv:     map[string]string{},
	}
	files, err := playwright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"apps/web/tests/a.spec.ts", "apps/web/tests/b.test.ts"}
	if !slices.Equal(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	playwright.commandOverride = []string{binary, "test", "--config", "apps/web/playwright.config.js", "--project", "one"}
	if err := playwright.RunTests(context.Background(), []string{"apps/web/tests/a.spec.ts"}, nil); err != nil {
		t.Fatalf("running one assigned file failed: %v", err)
	}
	if source, ok := playwright.SourceFileForSuite("a.spec.ts"); !ok || source != "apps/web/tests/a.spec.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}

	emptyPlaywright := &Playwright{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: []string{binary, "test", "--config", "apps/web/playwright.config.js", "__ddtest_no_match__"},
		platformEnv:     map[string]string{},
	}
	if files, err := emptyPlaywright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{}); err != nil || len(files) != 0 {
		t.Fatalf("empty native discovery = %v, %v", files, err)
	}

	brokenFile := filepath.Join(projectRoot, "tests", "broken.spec.ts")
	if err := os.WriteFile(brokenFile, []byte("throw new Error('collection exploded')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	brokenPlaywright := &Playwright{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: []string{binary, "test", "--config", "apps/web/playwright.config.js", "broken.spec.ts"},
		platformEnv:     map[string]string{},
	}
	if _, err := brokenPlaywright.DiscoverTestFiles(context.Background(), discovery.TestFileSet{}); err == nil {
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
