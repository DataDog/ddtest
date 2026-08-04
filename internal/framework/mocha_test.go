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

type mochaCommandExecutor struct {
	output       []byte
	combinedErr  error
	runErr       error
	capturedName string
	capturedArgs []string
	capturedEnv  map[string]string
}

func (m *mochaCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	m.capture(name, args, envMap)
	return m.output, m.combinedErr
}

func (m *mochaCommandExecutor) Run(_ context.Context, name string, args []string, envMap map[string]string) error {
	m.capture(name, args, envMap)
	return m.runErr
}

func (m *mochaCommandExecutor) capture(name string, args []string, envMap map[string]string) {
	m.capturedName = name
	m.capturedArgs = slices.Clone(args)
	m.capturedEnv = make(map[string]string)
	for key, value := range envMap {
		m.capturedEnv[key] = value
	}
}

func TestMochaBasics(t *testing.T) {
	mocha := NewMocha()
	if mocha.Name() != "mocha" {
		t.Fatalf("Name() = %q, want mocha", mocha.Name())
	}
	if mocha.SupportsFullTestDiscovery() {
		t.Fatal("Mocha should use suite-level discovery")
	}
	if got := mocha.TestPattern(); got != "test/**/*.{js,cjs,mjs}" {
		t.Fatalf("TestPattern() = %q", got)
	}
	if source, ok := mocha.SourceFileForSuite(" test/unit.spec.js "); !ok || source != "test/unit.spec.js" {
		t.Fatalf("SourceFileForSuite() = %q, %v", source, ok)
	}
	if source, ok := mocha.SourceFileForSuite(" "); ok || source != "" {
		t.Fatalf("empty SourceFileForSuite() = %q, %v", source, ok)
	}
	if _, err := mocha.DiscoverTests(context.Background(), discovery.TestFileSet{}); !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("DiscoverTests() error = %v", err)
	}
}

func TestMochaCommandArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "local", command: "node_modules/.bin/mocha", args: []string{"--parallel"}, want: []string{"--parallel"}},
		{name: "npx", command: "npx", args: []string{"mocha", "--config", "custom.json"}, want: []string{"--config", "custom.json"}},
		{name: "pnpm", command: "pnpm", args: []string{"exec", "mocha", "--parallel"}, want: []string{"--parallel"}},
		{name: "missing", command: "npm", args: []string{"test"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := mochaCLIArgs(test.command, test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("mochaCLIArgs() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("mochaCLIArgs() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMochaDiscoverTestFiles(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, file := range []string{"test/a.spec.js", "test/b.spec.js"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	absA, _ := filepath.Abs("test/a.spec.js")
	absB, _ := filepath.Abs("test/b.spec.js")
	executor := &mochaCommandExecutor{
		output: []byte("config log\n" + mochaDiscoveryMarker + "[" + strconvQuote(absB) + "," + strconvQuote(absA) + "," + strconvQuote(absA) + "]\n"),
	}
	mocha := &Mocha{
		executor:        executor,
		commandOverride: []string{"pnpm", "exec", "mocha", "--parallel"},
		platformEnv:     map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init --max-old-space-size=4096", "CUSTOM": "value"},
	}
	files, err := mocha.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"test/a.spec.js", "test/b.spec.js"}) {
		t.Fatalf("files = %v", files)
	}
	if executor.capturedName != "node" || len(executor.capturedArgs) != 3 || executor.capturedArgs[0] != "--eval" {
		t.Fatalf("command = %q %v", executor.capturedName, executor.capturedArgs)
	}
	var request struct {
		Mode    string   `json:"mode"`
		CLIArgs []string `json:"cliArgs"`
	}
	if err := json.Unmarshal([]byte(executor.capturedArgs[2]), &request); err != nil {
		t.Fatal(err)
	}
	if request.Mode != "discover" || !slices.Equal(request.CLIArgs, []string{"--parallel"}) {
		t.Fatalf("request = %#v", request)
	}
	if executor.capturedEnv["NODE_OPTIONS"] != "--max-old-space-size=4096" || executor.capturedEnv["CUSTOM"] != "value" {
		t.Fatalf("discovery env = %v", executor.capturedEnv)
	}
}

func TestMochaRunTests(t *testing.T) {
	executor := &mochaCommandExecutor{}
	mocha := &Mocha{
		executor:        executor,
		commandOverride: []string{"npx", "mocha", "--parallel"},
		platformEnv:     map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "BASE": "base"},
	}
	files := []string{"test/a.spec.js"}
	if err := mocha.RunTests(context.Background(), files, map[string]string{"WORKER": "1"}); err != nil {
		t.Fatal(err)
	}
	if executor.capturedName != "node" || executor.capturedEnv["NODE_OPTIONS"] != "-r dd-trace/ci/init" || executor.capturedEnv["WORKER"] != "1" {
		t.Fatalf("run = %q env=%v", executor.capturedName, executor.capturedEnv)
	}
	var request struct {
		Mode    string   `json:"mode"`
		CLIArgs []string `json:"cliArgs"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(executor.capturedArgs[2]), &request); err != nil {
		t.Fatal(err)
	}
	if request.Mode != "run" || !slices.Equal(request.CLIArgs, []string{"--parallel"}) || !slices.Equal(request.Files, files) {
		t.Fatalf("request = %#v", request)
	}
}

func TestParseMochaDiscoveryOutputErrors(t *testing.T) {
	if _, err := parseMochaDiscoveryOutput([]byte("noise")); err == nil {
		t.Fatal("expected missing marker error")
	}
	if _, err := parseMochaDiscoveryOutput([]byte(mochaDiscoveryMarker + "not-json")); err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestMochaUnskippableMarker(t *testing.T) {
	file := filepath.Join(t.TempDir(), "marked.spec.js")
	if err := os.WriteFile(file, []byte("// @datadog unskippable\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if !NewMocha().HasUnskippableMarker(file) {
		t.Fatal("expected marker")
	}
}

func TestMochaAdapterIntegration(t *testing.T) {
	nodeModules := os.Getenv("DDTEST_MOCHA_NODE_MODULES")
	if nodeModules == "" {
		t.Skip("DDTEST_MOCHA_NODE_MODULES is not set")
	}

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeMochaFixture(t, root, ".mocharc.json", `{"spec":["test/**/*.spec.js"],"file":["setup.js"]}`)
	writeMochaFixture(t, root, "setup.js", "global.ddtestSetup = true\n")
	writeMochaFixture(t, root, "test/selected.spec.js", `const assert = require("assert"); describe("selected", () => { it("uses setup", () => assert.equal(global.ddtestSetup, true)) })`)
	writeMochaFixture(t, root, "test/unselected.spec.js", `describe("unselected", () => { it("must not run", () => { throw new Error("unselected file ran") }) })`)
	t.Chdir(root)

	mocha := &Mocha{executor: &ext.DefaultCommandExecutor{}, platformEnv: make(map[string]string)}
	files, err := mocha.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"test/selected.spec.js", "test/unselected.spec.js"}
	if !slices.Equal(files, want) {
		t.Fatalf("discovered files = %v, want %v", files, want)
	}
	if err := mocha.RunTests(context.Background(), []string{"test/selected.spec.js"}, nil); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}

	if err := os.Remove(filepath.Join(root, ".mocharc.json")); err != nil {
		t.Fatal(err)
	}
	files, err = mocha.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err != nil {
		t.Fatalf("default discovery failed: %v", err)
	}
	if !slices.Equal(files, want) {
		t.Fatalf("default discovered files = %v, want %v", files, want)
	}
}

func writeMochaFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestMochaDiscoveryFailureIncludesOutput(t *testing.T) {
	mocha := &Mocha{
		executor:        &mochaCommandExecutor{output: []byte("bad config"), combinedErr: errors.New("exit 1")},
		commandOverride: []string{"mocha"},
		platformEnv:     make(map[string]string),
	}
	_, err := mocha.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: mocha.TestPattern()})
	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("error = %v", err)
	}
}
