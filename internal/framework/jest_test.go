package framework

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
)

type jestCommandExecutor struct {
	output         []byte
	err            error
	onExecution    func(name string, args []string)
	capturedEnvMap map[string]string
}

func (m *jestCommandExecutor) CombinedOutput(ctx context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	m.capturedEnvMap = envMap
	if m.onExecution != nil {
		m.onExecution(name, args)
	}
	return m.output, m.err
}

func (m *jestCommandExecutor) Run(ctx context.Context, name string, args []string, envMap map[string]string) error {
	m.capturedEnvMap = envMap
	if m.onExecution != nil {
		m.onExecution(name, args)
	}
	return m.err
}

func TestNewJest(t *testing.T) {
	jest := NewJest()
	if jest == nil {
		t.Fatal("NewJest() returned nil")
	}
	if jest.executor == nil {
		t.Error("NewJest() created Jest with nil executor")
	}
}

func TestJest_Name(t *testing.T) {
	jest := NewJest()
	if jest.Name() != "jest" {
		t.Errorf("expected %q, got %q", "jest", jest.Name())
	}
}

func TestJest_DiscoverTests_Unsupported(t *testing.T) {
	jest := NewJest()
	tests, err := jest.DiscoverTests(context.Background(), discovery.TestFileSet{})

	if tests != nil {
		t.Errorf("expected nil tests, got %v", tests)
	}
	if !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("expected ErrFullTestDiscoveryUnsupported, got %v", err)
	}
	if jest.SupportsFullTestDiscovery() {
		t.Error("expected Jest to report unsupported full test discovery")
	}
}

func TestJest_SourceFileForSuite(t *testing.T) {
	jest := NewJest()

	sourceFile, ok := jest.SourceFileForSuite("src/example.test.js")
	if !ok || sourceFile != "src/example.test.js" {
		t.Fatalf("SourceFileForSuite() = %q, %v; want suite path", sourceFile, ok)
	}

	if sourceFile, ok := jest.SourceFileForSuite(" "); ok || sourceFile != "" {
		t.Fatalf("SourceFileForSuite(blank) = %q, %v; want unresolved", sourceFile, ok)
	}
}

func TestJest_HasUnskippableMarker(t *testing.T) {
	tempDir := t.TempDir()
	markedFile := filepath.Join(tempDir, "marked.test.js")
	if err := os.WriteFile(markedFile, []byte("// @datadog\n// unskippable"), 0644); err != nil {
		t.Fatal(err)
	}
	unmarkedFile := filepath.Join(tempDir, "unmarked.test.js")
	if err := os.WriteFile(unmarkedFile, []byte("// unskippable"), 0644); err != nil {
		t.Fatal(err)
	}

	jest := NewJest()
	if !jest.HasUnskippableMarker(markedFile) {
		t.Fatal("expected marker when @datadog and unskippable are present")
	}
	if jest.HasUnskippableMarker(unmarkedFile) {
		t.Fatal("expected no marker without @datadog")
	}
	if !jest.HasUnskippableMarker(filepath.Join(tempDir, "missing.test.js")) {
		t.Fatal("expected missing file to be treated as guarded")
	}
}

func TestJest_DiscoverTestFiles_UsesLocalJestListTests(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binJestPath), 0755); err != nil {
		t.Fatalf("failed to create jest bin dir: %v", err)
	}
	if err := os.WriteFile(binJestPath, []byte("#!/usr/bin/env node\n"), 0755); err != nil {
		t.Fatalf("failed to create jest bin: %v", err)
	}

	filesToCreate := []string{
		"src/b.test.ts",
		"src/foo.test.js",
	}
	for _, file := range filesToCreate {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", file, err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create %s: %v", file, err)
		}
	}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &jestCommandExecutor{
		output: []byte(filepath.Join(tempDir, "src", "b.test.ts") + "\n" +
			filepath.Join(tempDir, "src", "foo.test.js") + "\n" +
			"warning: ignored because it is not a file\n"),
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{
		executor:    mockExecutor,
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init"},
	}
	files, err := jest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: jest.TestPattern()})
	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	if capturedName != binJestPath {
		t.Errorf("expected command %q, got %q", binJestPath, capturedName)
	}
	expectedArgs := []string{"--listTests"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
	if mockExecutor.capturedEnvMap["NODE_OPTIONS"] != "-r dd-trace/ci/init" {
		t.Errorf("expected NODE_OPTIONS from platform env, got %q", mockExecutor.capturedEnvMap["NODE_OPTIONS"])
	}

	expectedFiles := []string{"src/b.test.ts", "src/foo.test.js"}
	if !slices.Equal(files, expectedFiles) {
		t.Errorf("expected files %v, got %v", expectedFiles, files)
	}
}

func TestJest_DiscoverTestFiles_WithTestsLocationUsesTestMatch(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	for _, file := range []string{"custom/a.check.js", "custom/b.check.js", "src/c.test.js"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", file, err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create %s: %v", file, err)
		}
	}

	setTestsLocation(t, "custom/*.check.js")

	var capturedName string
	var capturedArgs []string
	mockExecutor := &jestCommandExecutor{
		output: []byte(filepath.Join(tempDir, "custom", "b.check.js") + "\n" +
			filepath.Join(tempDir, "custom", "a.check.js") + "\n"),
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{executor: mockExecutor, platformEnv: make(map[string]string)}
	files, err := jest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: jest.TestPattern()})
	if err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	if capturedName != "npx" {
		t.Errorf("expected command %q, got %q", "npx", capturedName)
	}
	expectedArgs := []string{"jest", "--listTests", "--testMatch", "custom/*.check.js"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}

	expected := []string{"custom/a.check.js", "custom/b.check.js"}
	if !slices.Equal(files, expected) {
		t.Errorf("expected files %v, got %v", expected, files)
	}
}

func TestJest_DiscoverTestFiles_WithOverride(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	if err := os.MkdirAll("src", 0755); err != nil {
		t.Fatalf("failed to create src dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join("src", "a.test.js"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &jestCommandExecutor{
		output: []byte(filepath.Join(tempDir, "src", "a.test.js") + "\n"),
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{
		executor:        mockExecutor,
		commandOverride: []string{"pnpm", "jest", "--runInBand"},
		platformEnv:     make(map[string]string),
	}

	if _, err := jest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: jest.TestPattern()}); err != nil {
		t.Fatalf("DiscoverTestFiles failed: %v", err)
	}

	if capturedName != "pnpm" {
		t.Errorf("expected command %q, got %q", "pnpm", capturedName)
	}
	expectedArgs := []string{"jest", "--runInBand", "--listTests"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
}

func TestJest_DiscoverTestFiles_CommandError(t *testing.T) {
	mockExecutor := &jestCommandExecutor{
		output: []byte("invalid jest config"),
		err:    errors.New("exit status 1"),
	}
	jest := &Jest{executor: mockExecutor, platformEnv: make(map[string]string)}

	_, err := jest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: jest.TestPattern()})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to discover Jest test files") ||
		!strings.Contains(err.Error(), "invalid jest config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJest_RunTests_UsesLocalJestBinary(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binJestPath), 0755); err != nil {
		t.Fatalf("failed to create jest bin dir: %v", err)
	}
	if err := os.WriteFile(binJestPath, []byte("#!/usr/bin/env node\n"), 0755); err != nil {
		t.Fatalf("failed to create jest bin: %v", err)
	}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{
		executor:    mockExecutor,
		platformEnv: map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "SHARED": "platform"},
	}

	err := jest.RunTests(context.Background(), []string{"src/a.test.js", "src/b.test.ts"}, map[string]string{"SHARED": "worker", "DD_ENV": "ci"})
	if err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != binJestPath {
		t.Errorf("expected command %q, got %q", binJestPath, capturedName)
	}
	expectedArgs := []string{"--runTestsByPath", "src/a.test.js", "src/b.test.ts"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
	if mockExecutor.capturedEnvMap["NODE_OPTIONS"] != "-r dd-trace/ci/init" {
		t.Errorf("expected NODE_OPTIONS from platform env, got %q", mockExecutor.capturedEnvMap["NODE_OPTIONS"])
	}
	if mockExecutor.capturedEnvMap["SHARED"] != "worker" {
		t.Errorf("expected worker env to override platform env, got %q", mockExecutor.capturedEnvMap["SHARED"])
	}
	if mockExecutor.capturedEnvMap["DD_ENV"] != "ci" {
		t.Errorf("expected worker env DD_ENV, got %q", mockExecutor.capturedEnvMap["DD_ENV"])
	}
}

func TestJest_RunTests_UsesNpxFallback(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{executor: mockExecutor, platformEnv: make(map[string]string)}

	if err := jest.RunTests(context.Background(), []string{"src/a.test.js"}, nil); err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != "npx" {
		t.Errorf("expected command %q, got %q", "npx", capturedName)
	}
	expectedArgs := []string{"jest", "--runTestsByPath", "src/a.test.js"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
}

func TestJest_SetPlatformEnv(t *testing.T) {
	jest := NewJest()
	env := map[string]string{"NODE_OPTIONS": "-r dd-trace/ci/init", "FOO": "bar"}
	jest.SetPlatformEnv(env)

	got := jest.GetPlatformEnv()
	if got["NODE_OPTIONS"] != "-r dd-trace/ci/init" {
		t.Errorf("expected NODE_OPTIONS %q, got %q", "-r dd-trace/ci/init", got["NODE_OPTIONS"])
	}
	if got["FOO"] != "bar" {
		t.Errorf("expected FOO %q, got %q", "bar", got["FOO"])
	}
}

func TestJest_RunTests_WithOverride(t *testing.T) {
	var capturedName string
	var capturedArgs []string
	mockExecutor := &mockCommandExecutor{
		onExecution: func(name string, args []string) {
			capturedName = name
			capturedArgs = slices.Clone(args)
		},
	}
	jest := &Jest{
		executor:        mockExecutor,
		commandOverride: []string{"pnpm", "jest", "--runInBand"},
		platformEnv:     make(map[string]string),
	}

	if err := jest.RunTests(context.Background(), []string{"src/a.test.js"}, nil); err != nil {
		t.Fatalf("RunTests failed: %v", err)
	}

	if capturedName != "pnpm" {
		t.Errorf("expected command %q, got %q", "pnpm", capturedName)
	}
	expectedArgs := []string{"jest", "--runInBand", "--runTestsByPath", "src/a.test.js"}
	if !slices.Equal(capturedArgs, expectedArgs) {
		t.Errorf("expected args %v, got %v", expectedArgs, capturedArgs)
	}
}
