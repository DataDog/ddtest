package framework

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
)

type cypressCommandExecutor struct {
	output       []byte
	err          error
	runCalls     int
	capturedName string
	capturedArgs []string
	capturedEnv  map[string]string
}

func (e *cypressCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	e.capture(name, args, env)
	return e.output, e.err
}

func (e *cypressCommandExecutor) Run(_ context.Context, name string, args []string, env map[string]string) error {
	e.runCalls++
	e.capture(name, args, env)
	return e.err
}

func (e *cypressCommandExecutor) capture(name string, args []string, env map[string]string) {
	e.capturedName = name
	e.capturedArgs = slices.Clone(args)
	e.capturedEnv = make(map[string]string, len(env))
	for key, value := range env {
		e.capturedEnv[key] = value
	}
}

func TestCypressFrameworkMetadata(t *testing.T) {
	cypress := NewCypress()
	if cypress.Name() != "cypress" {
		t.Fatalf("Name() = %q, want cypress", cypress.Name())
	}
	if cypress.SupportsFullTestDiscovery() {
		t.Fatal("Cypress should use suite-level discovery")
	}
	if cypress.TestPattern() != cypressDefaultE2EPattern {
		t.Fatalf("TestPattern() = %q", cypress.TestPattern())
	}
	if source, ok := cypress.SourceFileForSuite(" cypress/e2e/example.cy.ts "); !ok || source != "cypress/e2e/example.cy.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
	if _, ok := cypress.SourceFileForSuite(" "); ok {
		t.Fatal("blank suite should not resolve to a source file")
	}
	if tests, err := cypress.DiscoverTests(context.Background(), discovery.TestFileSet{}); tests != nil || !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("DiscoverTests() = %v, %v; want unsupported", tests, err)
	}
}

func TestCypressCLIArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "local", command: "node_modules/.bin/cypress", args: []string{"run", "--e2e"}, want: []string{"run", "--e2e"}},
		{name: "npx", command: "npx", args: []string{"cypress", "run"}, want: []string{"run"}},
		{name: "pnpm", command: "pnpm", args: []string{"exec", "cypress", "run"}, want: []string{"run"}},
		{name: "indirect", command: "npm", args: []string{"test"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cypressCLIArgs(test.command, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("cypressCLIArgs() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("cypressCLIArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCypressCommandConstruction(t *testing.T) {
	baseArgs := []string{
		"exec", "cypress", "open", "--component", "--project", "web",
		"--config-file", "custom.ts", "--config", "video=false", "--env", "target=ci",
		"--browser", "chrome", "--record", "--spec", "old.cy.ts",
	}
	discoveryArgs := cypressDiscoveryArgs("pnpm", baseArgs, "/tmp/discovery.ts")
	wantDiscovery := []string{
		"exec", "cypress", "run", "--component", "--project", "web", "--config", "video=false",
		"--env", "target=ci", "--config-file", "/tmp/discovery.ts",
		"--spec", cypressMissingDiscoverySpec,
	}
	if !slices.Equal(discoveryArgs, wantDiscovery) {
		t.Fatalf("cypressDiscoveryArgs() = %v, want %v", discoveryArgs, wantDiscovery)
	}

	runArgs := cypressRunArgs("pnpm", baseArgs, []string{"a.cy.ts", "b.cy.ts"})
	wantRun := []string{
		"exec", "cypress", "run", "--component", "--project", "web", "--config-file", "custom.ts",
		"--config", "video=false", "--env", "target=ci", "--browser", "chrome", "--record",
		"--spec", "a.cy.ts,b.cy.ts",
	}
	if !slices.Equal(runArgs, wantRun) {
		t.Fatalf("cypressRunArgs() = %v, want %v", runArgs, wantRun)
	}
}

func TestParseCypressDiscoveryOutput(t *testing.T) {
	output := []byte("config log\n" + cypressDiscoveryMarker + `{"projectRoot":"/repo","testingType":"component","specPattern":["src/**/*.cy.ts","lib/**/*.cy.ts"],"excludeSpecPattern":"**/ignored/**","otherTestingTypeSpecPattern":"cypress/e2e/**/*.cy.ts"}` + "\nmore logs")
	config, err := parseCypressDiscoveryOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if config.ProjectRoot != "/repo" || config.TestingType != "component" ||
		!slices.Equal(config.SpecPattern, []string{"src/**/*.cy.ts", "lib/**/*.cy.ts"}) ||
		!slices.Equal(config.ExcludeSpecPattern, []string{"**/ignored/**"}) ||
		!slices.Equal(config.OtherTestingTypeSpecPattern, []string{"cypress/e2e/**/*.cy.ts"}) {
		t.Fatalf("config = %#v", config)
	}

	if _, err := parseCypressDiscoveryOutput([]byte("noise")); err == nil {
		t.Fatal("expected missing marker error")
	}
	if _, err := parseCypressDiscoveryOutput([]byte(cypressDiscoveryMarker + `{}`)); err == nil {
		t.Fatal("expected testing type error")
	}
}

func TestDiscoverCypressSpecFilesUsesResolvedPatterns(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeCypressFixture(t, "cypress/e2e/login.cy.ts")
	writeCypressFixture(t, "src/button.cy.tsx")
	writeCypressFixture(t, "src/ignored.cy.tsx")
	writeCypressFixture(t, "src/app.tsx")

	e2eFiles, err := discoverCypressSpecFiles(cypressDiscoveryConfig{
		ProjectRoot:        root,
		TestingType:        "e2e",
		SpecPattern:        []string{"cypress/e2e/**/*.cy.{js,ts}"},
		ExcludeSpecPattern: []string{"*.hot-update.js"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(e2eFiles, []string{"cypress/e2e/login.cy.ts"}) {
		t.Fatalf("e2e files = %v", e2eFiles)
	}

	componentFiles, err := discoverCypressSpecFiles(cypressDiscoveryConfig{
		ProjectRoot:                 root,
		TestingType:                 "component",
		SpecPattern:                 []string{"**/*.cy.{ts,tsx}"},
		ExcludeSpecPattern:          []string{"**/ignored.cy.tsx"},
		OtherTestingTypeSpecPattern: []string{"cypress/e2e/**/*.cy.{js,ts}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(componentFiles, []string{"src/button.cy.tsx"}) {
		t.Fatalf("component files = %v", componentFiles)
	}
}

func TestCypressDiscoverTestFilesLoadsConfigAndFilters(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	projectRoot := filepath.Join(root, "web")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(projectRoot, "custom.config.ts")
	if err := os.WriteFile(configPath, []byte("export default {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeCypressFixture(t, "web/specs/a.cy.ts")
	writeCypressFixture(t, "web/specs/b.cy.ts")
	writeCypressFixture(t, "web/specs/ignored.cy.ts")

	payload, err := json.Marshal(map[string]any{
		"projectRoot":        projectRoot,
		"testingType":        "e2e",
		"specPattern":        "specs/**/*.cy.ts",
		"excludeSpecPattern": []string{"**/ignored.cy.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := &cypressCommandExecutor{
		output: append([]byte(cypressDiscoveryMarker), payload...),
		err:    errors.New("Cypress found no specs"),
	}
	cypress := &Cypress{
		executor: executor,
		commandOverride: []string{
			"pnpm", "exec", "cypress", "run", "--project", "web", "--config-file", "custom.config.ts", "--record",
		},
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init --max-old-space-size=2048", "CUSTOM": "value"},
	}

	files, err := cypress.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"web/specs/a.cy.ts", "web/specs/b.cy.ts"}) {
		t.Fatalf("files = %v", files)
	}
	if executor.capturedName != "pnpm" || slices.Contains(executor.capturedArgs, "--record") ||
		!slices.Contains(executor.capturedArgs, cypressMissingDiscoverySpec) {
		t.Fatalf("discovery command = %q %v", executor.capturedName, executor.capturedArgs)
	}
	if got := executor.capturedEnv["NODE_OPTIONS"]; got != "--max-old-space-size=2048" {
		t.Fatalf("discovery NODE_OPTIONS = %q", got)
	}
	if executor.capturedEnv["CUSTOM"] != "value" {
		t.Fatalf("discovery env = %v", executor.capturedEnv)
	}
	for _, entry := range mustReadDir(t, projectRoot) {
		if strings.HasPrefix(entry.Name(), ".ddtest-cypress-config-") {
			t.Fatalf("temporary discovery config was not removed: %s", entry.Name())
		}
	}
	if source, ok := cypress.SourceFileForSuite("specs/a.cy.ts"); !ok || source != "web/specs/a.cy.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
}

func TestPrepareCypressDiscoveryConfig(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "cypress.config.mjs")
	if err := os.WriteFile(original, []byte("export default {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	generated, err := prepareCypressDiscoveryConfig(root, original)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(generated) }()
	content, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `import originalConfig from "./cypress.config.mjs"`) ||
		!strings.Contains(string(content), cypressDiscoveryMarker) ||
		strings.Contains(string(content), cypressConfigImportToken) {
		t.Fatalf("generated config = %s", content)
	}
}

func TestCypressRunTests(t *testing.T) {
	executor := &cypressCommandExecutor{}
	cypress := &Cypress{
		executor: executor,
		commandOverride: []string{
			"npx", "cypress", "run", "--browser", "chrome", "--spec", "configured.cy.ts",
		},
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "BASE": "base"},
	}
	if err := cypress.RunTests(context.Background(), []string{"a.cy.ts", "b.cy.ts"}, map[string]string{"WORKER": "1"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"cypress", "run", "--browser", "chrome", "--spec", "a.cy.ts,b.cy.ts"}
	if executor.capturedName != "npx" || !slices.Equal(executor.capturedArgs, want) ||
		executor.capturedEnv["NODE_OPTIONS"] != "-r dd-trace/ci/init" || executor.capturedEnv["WORKER"] != "1" {
		t.Fatalf("run = %q %v env=%v", executor.capturedName, executor.capturedArgs, executor.capturedEnv)
	}

	if err := cypress.RunTests(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if executor.runCalls != 1 {
		t.Fatalf("empty assignment executed Cypress; calls = %d", executor.runCalls)
	}
}

func TestCypressRunTestsUsesPathsRelativeToSelectedProject(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join("apps", "web"), 0755); err != nil {
		t.Fatal(err)
	}
	executor := &cypressCommandExecutor{}
	cypress := &Cypress{
		executor: executor,
		commandOverride: []string{
			"npx", "cypress", "run", "--project", "apps/web",
		},
		platformEnv: make(map[string]string),
	}

	testFiles := []string{
		"apps/web/cypress/e2e/a.cy.ts",
		"apps/web/cypress/e2e/b.cy.ts",
	}
	if err := cypress.RunTests(context.Background(), testFiles, nil); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"cypress", "run", "--project", "apps/web", "--spec",
		"cypress/e2e/a.cy.ts,cypress/e2e/b.cy.ts",
	}
	if !slices.Equal(executor.capturedArgs, wantArgs) {
		t.Fatalf("run args = %v, want %v", executor.capturedArgs, wantArgs)
	}
	if !slices.Equal(testFiles, []string{
		"apps/web/cypress/e2e/a.cy.ts",
		"apps/web/cypress/e2e/b.cy.ts",
	}) {
		t.Fatalf("RunTests mutated planned test files: %v", testFiles)
	}
}

func TestCypressUnskippableMarker(t *testing.T) {
	file := filepath.Join(t.TempDir(), "marked.cy.ts")
	if err := os.WriteFile(file, []byte("// @datadog unskippable\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !NewCypress().HasUnskippableMarker(file) {
		t.Fatal("expected marker")
	}
}

func TestCypressAdapterIntegration(t *testing.T) {
	binary := os.Getenv("DDTEST_CYPRESS_BINARY")
	if binary == "" {
		t.Skip("DDTEST_CYPRESS_BINARY is not set")
	}
	nodeModules := os.Getenv("DDTEST_CYPRESS_NODE_MODULES")
	if nodeModules == "" {
		t.Fatal("DDTEST_CYPRESS_NODE_MODULES must be set with DDTEST_CYPRESS_BINARY")
	}

	root := t.TempDir()
	t.Chdir(root)
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	config := `export default {
  e2e: {
    supportFile: false,
    async setupNodeEvents(_on, config) {
      return { ...config, specPattern: "custom/**/*.cy.ts" }
    },
  },
}
`
	if err := os.WriteFile("cypress.config.ts", []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	writeCypressFixture(t, "custom/discovered.cy.ts")
	writeCypressFixture(t, "cypress/e2e/not-discovered.cy.ts")

	cypress := &Cypress{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: []string{binary, "run"},
		platformEnv:     make(map[string]string),
	}
	files, err := cypress.DiscoverTestFiles(context.Background(), discovery.TestFileSet{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom/discovered.cy.ts"}) {
		t.Fatalf("files = %v", files)
	}
}

func writeCypressFixture(t *testing.T, filename string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("describe('test', () => {})\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustReadDir(t *testing.T, dirname string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dirname)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
