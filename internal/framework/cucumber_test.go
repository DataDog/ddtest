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
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

type cucumberCommandExecutor struct {
	output       []byte
	combinedErr  error
	runErr       error
	capturedName string
	capturedArgs []string
	capturedEnv  map[string]string
	messages     []cucumberEnvelope
}

func (e *cucumberCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	e.capture(name, args, envMap)
	if e.combinedErr == nil && e.messages != nil {
		for i, arg := range args {
			if i == 0 || args[i-1] != "--format" || !strings.HasPrefix(arg, "message:") {
				continue
			}
			var output strings.Builder
			for _, envelope := range e.messages {
				encoded, err := json.Marshal(envelope)
				if err != nil {
					return nil, err
				}
				output.Write(encoded)
				output.WriteByte('\n')
			}
			if err := os.WriteFile(strings.TrimPrefix(arg, "message:"), []byte(output.String()), 0644); err != nil {
				return nil, err
			}
		}
	}
	return e.output, e.combinedErr
}

func (e *cucumberCommandExecutor) Run(_ context.Context, name string, args []string, envMap map[string]string) error {
	e.capture(name, args, envMap)
	return e.runErr
}

func (e *cucumberCommandExecutor) capture(name string, args []string, envMap map[string]string) {
	e.capturedName = name
	e.capturedArgs = slices.Clone(args)
	e.capturedEnv = make(map[string]string)
	for key, value := range envMap {
		e.capturedEnv[key] = value
	}
}

func cucumberPickleEnvelope(id, uri string) cucumberEnvelope {
	envelope := cucumberEnvelope{}
	envelope.Pickle = &struct {
		ID  string `json:"id"`
		URI string `json:"uri"`
	}{ID: id, URI: uri}
	return envelope
}

func cucumberTestCaseEnvelope(pickleID string) cucumberEnvelope {
	envelope := cucumberEnvelope{}
	envelope.TestCase = &struct {
		PickleID string `json:"pickleId"`
	}{PickleID: pickleID}
	return envelope
}

func TestCucumberBasics(t *testing.T) {
	cucumber := NewCucumber()
	if cucumber.Name() != "cucumber" {
		t.Fatalf("Name() = %q, want cucumber", cucumber.Name())
	}
	if cucumber.SupportsFullTestDiscovery() {
		t.Fatal("Cucumber should use suite-level discovery")
	}
	if got := cucumber.TestPattern(); got != "features/**/*.{feature,feature.md}" {
		t.Fatalf("TestPattern() = %q", got)
	}
	if source, ok := cucumber.SourceFileForSuite(" features/a.feature "); !ok || source != "features/a.feature" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
	if source, ok := cucumber.SourceFileForSuite(" "); ok || source != "" {
		t.Fatalf("empty SourceFileForSuite() = %q, %v", source, ok)
	}
	if _, err := cucumber.DiscoverTests(context.Background(), discovery.TestFileSet{}); !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("DiscoverTests() error = %v", err)
	}
}

func TestCucumberHasUnskippableMarker(t *testing.T) {
	marked := filepath.Join(t.TempDir(), "marked.feature")
	if err := os.WriteFile(marked, []byte("@datadog:unskippable\nFeature: guarded\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !NewCucumber().HasUnskippableMarker(marked) {
		t.Fatal("expected @datadog:unskippable feature to be guarded")
	}
}

func TestCucumberCommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "local", command: "node_modules/.bin/cucumber-js", args: []string{"--tags", "@smoke"}, want: []string{"--tags", "@smoke"}},
		{name: "npx", command: "npx", args: []string{"cucumber-js", "--profile", "ci"}, want: []string{"--profile", "ci"}},
		{name: "pnpm", command: "pnpm", args: []string{"exec", "cucumber-js", "features/a.feature"}, want: []string{"features/a.feature"}},
		{name: "missing", command: "npm", args: []string{"test"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cucumberCLIArgs(test.command, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("cucumberCLIArgs() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("cucumberCLIArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCucumberArgsWithoutPaths(t *testing.T) {
	baseArgs := []string{
		"exec", "cucumber-js",
		"--profile", "ci",
		"features/a.feature:12",
		"--tags=@smoke",
		"--publish-token", "secret-token",
		"--world-parameters", `{"browser":"firefox"}`,
		"@rerun.txt",
		"--", "features/b.feature",
	}
	cliArgs, err := cucumberCLIArgs("pnpm", baseArgs)
	if err != nil {
		t.Fatal(err)
	}
	got := cucumberArgsWithoutPaths(baseArgs, cliArgs)
	want := []string{
		"exec", "cucumber-js",
		"--profile", "ci",
		"--tags=@smoke",
		"--publish-token", "secret-token",
		"--world-parameters", `{"browser":"firefox"}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("cucumberArgsWithoutPaths() = %v, want %v", got, want)
	}
}

func TestRedactCucumberArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "separate value",
			args: []string{"--publish-token", "secret-token", "--profile", "ci"},
			want: []string{"--publish-token", cucumberRedactedValue, "--profile", "ci"},
		},
		{
			name: "equals value",
			args: []string{"--publish-token=secret-token", "--profile", "ci"},
			want: []string{"--publish-token=" + cucumberRedactedValue, "--profile", "ci"},
		},
		{
			name: "no token",
			args: []string{"--profile", "ci"},
			want: []string{"--profile", "ci"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := slices.Clone(test.args)
			got := redactCucumberArgs(test.args)
			if !slices.Equal(got, test.want) {
				t.Fatalf("redactCucumberArgs() = %v, want %v", got, test.want)
			}
			if !slices.Equal(test.args, original) {
				t.Fatalf("redactCucumberArgs() mutated input: %v", test.args)
			}
		})
	}
}

func TestCucumberDiscoverTestFilesUsesSelectedTestCases(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, filename := range []string{"features/a.feature", "features/b.feature", "features/filtered.feature"} {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("Feature: test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	absB, _ := filepath.Abs("features/b.feature")
	executor := &cucumberCommandExecutor{messages: []cucumberEnvelope{
		cucumberPickleEnvelope("filtered", "features/filtered.feature"),
		cucumberPickleEnvelope("b", absB),
		cucumberTestCaseEnvelope("b"),
		cucumberPickleEnvelope("a", "features/a.feature"),
		cucumberTestCaseEnvelope("a"),
		cucumberTestCaseEnvelope("a"),
	}}
	cucumber := &Cucumber{
		executor:        executor,
		commandOverride: []string{"pnpm", "exec", "cucumber-js", "features/**/*.feature", "--tags", "@smoke"},
		platformEnv: map[string]string{
			"NODE_OPTIONS": "-r dd-trace/ci/init --max-old-space-size=4096",
			"CUSTOM":       "value",
		},
	}

	files, err := cucumber.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: cucumber.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"features/a.feature", "features/b.feature"}) {
		t.Fatalf("files = %v", files)
	}
	if executor.capturedName != "pnpm" {
		t.Fatalf("command = %q", executor.capturedName)
	}
	for _, expected := range []string{"features/**/*.feature", "--dry-run", "--parallel", "0"} {
		if !slices.Contains(executor.capturedArgs, expected) {
			t.Fatalf("discovery args %v do not contain %q", executor.capturedArgs, expected)
		}
	}
	if strings.Contains(executor.capturedEnv[nodeOptionsEnvVar], ddTraceCIInitModule) {
		t.Fatalf("discovery NODE_OPTIONS still contains dd-trace init: %q", executor.capturedEnv[nodeOptionsEnvVar])
	}
	if executor.capturedEnv[cucumberPublishEnabled] != "false" {
		t.Fatalf("%s = %q", cucumberPublishEnabled, executor.capturedEnv[cucumberPublishEnabled])
	}
}

func TestCucumberDiscoverTestFilesFiltersLocationAndExclude(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, filename := range []string{"features/a.feature", "features/excluded.feature", "other/outside.feature"} {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("Feature: test\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	viper.Reset()
	viper.Set("tests_location", "features/**/*.feature")
	viper.Set("tests_exclude_pattern", "**/excluded.feature")
	settings.Init()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})

	executor := &cucumberCommandExecutor{messages: []cucumberEnvelope{
		cucumberPickleEnvelope("a", "features/a.feature"), cucumberTestCaseEnvelope("a"),
		cucumberPickleEnvelope("excluded", "features/excluded.feature"), cucumberTestCaseEnvelope("excluded"),
		cucumberPickleEnvelope("outside", "other/outside.feature"), cucumberTestCaseEnvelope("outside"),
	}}
	cucumber := &Cucumber{executor: executor, commandOverride: []string{"cucumber-js"}, platformEnv: map[string]string{}}
	files, err := cucumber.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: cucumber.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"features/a.feature"}) {
		t.Fatalf("files = %v", files)
	}
}

func TestCucumberDiscoverTestFilesEmptyGlobCandidatesStillUsesCucumber(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("custom", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("custom/from-config.feature", []byte("Feature: test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	viper.Set("tests_exclude_pattern", "**/never.feature")
	settings.Init()
	t.Cleanup(func() {
		viper.Reset()
		settings.Init()
	})

	executor := &cucumberCommandExecutor{messages: []cucumberEnvelope{
		cucumberPickleEnvelope("configured", "custom/from-config.feature"), cucumberTestCaseEnvelope("configured"),
	}}
	cucumber := &Cucumber{executor: executor, commandOverride: []string{"cucumber-js"}, platformEnv: map[string]string{}}
	files, err := cucumber.DiscoverTestFiles(context.Background(), discovery.TestFileSet{
		Pattern:       cucumber.TestPattern(),
		ExplicitFiles: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom/from-config.feature"}) {
		t.Fatalf("files = %v", files)
	}
	if executor.capturedName == "" {
		t.Fatal("expected Cucumber discovery command to run")
	}
}

func TestCucumberDiscoverTestFilesReportsCommandError(t *testing.T) {
	executor := &cucumberCommandExecutor{output: []byte("invalid profile"), combinedErr: errors.New("exit status 1")}
	cucumber := &Cucumber{executor: executor, commandOverride: []string{"cucumber-js"}, platformEnv: map[string]string{}}
	_, err := cucumber.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: cucumber.TestPattern()})
	if err == nil || !strings.Contains(err.Error(), "invalid profile") {
		t.Fatalf("error = %v", err)
	}
}

func TestCucumberRunTestsReplacesConfiguredPaths(t *testing.T) {
	executor := &cucumberCommandExecutor{}
	cucumber := &Cucumber{
		executor: executor,
		commandOverride: []string{
			"pnpm", "exec", "cucumber-js", "features/v1/*.feature", "--profile", "ci", "--tags", "not @slow",
		},
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "SHARED": "platform"},
	}
	err := cucumber.RunTests(context.Background(), []string{"features/a.feature", "features/b.feature"}, map[string]string{"SHARED": "worker"})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"exec", "cucumber-js", "--profile", "ci", "--tags", "not @slow",
		"features/a.feature", "features/b.feature",
	}
	if !slices.Equal(executor.capturedArgs, wantArgs) {
		t.Fatalf("run args = %v, want %v", executor.capturedArgs, wantArgs)
	}
	if executor.capturedEnv["SHARED"] != "worker" {
		t.Fatalf("worker environment did not override platform environment: %v", executor.capturedEnv)
	}
	if !strings.Contains(executor.capturedEnv[nodeOptionsEnvVar], ddTraceCIInitModule) {
		t.Fatalf("run NODE_OPTIONS = %q", executor.capturedEnv[nodeOptionsEnvVar])
	}
}

func TestParseCucumberMessagesRejectsMalformedLine(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "messages.ndjson")
	if err := os.WriteFile(filename, []byte("{invalid}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCucumberMessages(filename); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %v", err)
	}
}

func TestCucumberAdapterIntegration(t *testing.T) {
	cucumberBinary := os.Getenv("DDTEST_CUCUMBER_BINARY")
	if cucumberBinary == "" {
		t.Skip("DDTEST_CUCUMBER_BINARY is not set")
	}
	nodeModules := os.Getenv("DDTEST_CUCUMBER_NODE_MODULES")
	if nodeModules == "" {
		t.Fatal("DDTEST_CUCUMBER_NODE_MODULES is not set")
	}
	cucumberVersion := os.Getenv("DDTEST_CUCUMBER_VERSION")
	if cucumberVersion == "" {
		t.Fatal("DDTEST_CUCUMBER_VERSION is not set")
	}

	root := t.TempDir()
	t.Chdir(root)
	if err := os.Symlink(nodeModules, "node_modules"); err != nil {
		t.Fatal(err)
	}
	cucumberConfig := `module.exports = {
  default: {
    paths: ['features/**/*.feature'],
    tags: 'not @excluded',
    require: ['features/support/**/*.js']
  }
}
`
	if strings.HasPrefix(cucumberVersion, "7.") {
		// Cucumber 7 profiles are CLI argument strings. Object-based profiles were
		// introduced later and are silently treated as empty by Cucumber 7.
		cucumberConfig = `module.exports = {
  default: "--require 'features/support/**/*.js' --tags 'not @excluded' 'features/**/*.feature'"
}
`
	}
	files := map[string]string{
		"cucumber.js": cucumberConfig,
		"features/included.feature": `Feature: included
  Scenario: selected by the default profile
    Given a passing step
`,
		"features/excluded.feature": `@excluded
Feature: excluded
  Scenario: filtered by the default profile
    Given a passing step
`,
		"features/support/steps.js": `const { Given } = require('@cucumber/cucumber')
Given('a passing step', function () {})
`,
	}
	for filename, content := range files {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cucumber := &Cucumber{
		executor:        &ext.DefaultCommandExecutor{},
		commandOverride: []string{cucumberBinary},
		platformEnv:     map[string]string{},
	}
	discovered, err := cucumber.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: cucumber.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discovered, []string{"features/included.feature"}) {
		t.Fatalf("discovered = %v", discovered)
	}
}
