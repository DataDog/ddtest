package framework

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
)

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
	if SupportsFullTestDiscovery(jest) {
		t.Error("expected Jest to report unsupported full test discovery")
	}
}

func TestJest_DiscoverTestFiles_DefaultPatterns(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	filesToCreate := []string{
		"src/foo.test.js",
		"src/foo.spec.ts",
		"src/__tests__/bar.jsx",
		"src/not-test.js",
		"node_modules/pkg/bad.test.js",
		"dist/bad.spec.js",
		"coverage/bad.test.ts",
	}
	for _, file := range filesToCreate {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", file, err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create %s: %v", file, err)
		}
	}

	jest := NewJest()
	files, err := discovery.DiscoverTestFiles(jest.TestPattern(), jest.TestExcludePattern())
	if err != nil {
		t.Fatalf("generic discovery failed: %v", err)
	}

	expected := []string{
		"src/__tests__/bar.jsx",
		"src/foo.spec.ts",
		"src/foo.test.js",
	}
	if !slices.Equal(files, expected) {
		t.Errorf("expected files %v, got %v", expected, files)
	}
}

func TestJest_DiscoverTestFiles_WithTestsLocation(t *testing.T) {
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

	jest := NewJest()
	files, err := discovery.DiscoverTestFiles(jest.TestPattern(), jest.TestExcludePattern())
	if err != nil {
		t.Fatalf("generic discovery failed: %v", err)
	}

	expected := []string{"custom/a.check.js", "custom/b.check.js"}
	if !slices.Equal(files, expected) {
		t.Errorf("expected files %v, got %v", expected, files)
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
