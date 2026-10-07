package framework

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

type vitestCommandExecutor struct {
	onRun                  func(string, []string, map[string]string) error
	output, stdout, stderr []byte
	err                    error
	capturedName           string
	capturedArgs           []string
	capturedEnvMap         map[string]string
	capturedRequest        vitestRequest
}

func vitestListOutput(t *testing.T, files ...string) []byte {
	t.Helper()
	output, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func (m *vitestCommandExecutor) capture(name string, args []string, env map[string]string) error {
	m.capturedName, m.capturedArgs, m.capturedEnvMap = name, slices.Clone(args), env
	request, err := os.ReadFile(filepath.Join(filepath.Dir(args[0]), "request.json"))
	if err != nil {
		return err
	}
	return json.Unmarshal(request, &m.capturedRequest)
}

func (m *vitestCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	if err := m.capture(name, args, env); err != nil {
		return nil, err
	}
	if m.err != nil {
		return m.output, m.err
	}
	err := os.WriteFile(filepath.Join(filepath.Dir(args[0]), "files.json"), m.output, 0600)
	return append(slices.Clone(m.stdout), m.stderr...), err
}

func (m *vitestCommandExecutor) Run(_ context.Context, name string, args []string, env map[string]string) error {
	if err := m.capture(name, args, env); err != nil {
		return err
	}
	if m.onRun != nil {
		return m.onRun(name, args, env)
	}
	return m.err
}

func TestVitest_FrameworkMetadata(t *testing.T) {
	vitest := NewVitest()
	if vitest.Name() != "vitest" {
		t.Fatalf("Name() = %q, want vitest", vitest.Name())
	}
	if vitest.SupportsFullTestDiscovery() {
		t.Fatal("Vitest should use suite-level discovery")
	}
	if tests, err := vitest.DiscoverTests(context.Background(), discovery.TestFileSet{}); tests != nil || !errors.Is(err, ErrFullTestDiscoveryUnsupported) {
		t.Fatalf("DiscoverTests() = %v, %v; want unsupported", tests, err)
	}
	if sourceFile, ok := vitest.SourceFileForSuite("src/example.test.ts"); !ok || sourceFile != "src/example.test.ts" {
		t.Fatalf("SourceFileForSuite() = %q, %v", sourceFile, ok)
	}
	if _, ok := vitest.SourceFileForSuite(" "); ok {
		t.Fatal("blank suite should not resolve to a source file")
	}
	if pattern := vitest.TestPattern(); pattern != "**/*.{test,spec}.{js,jsx,ts,tsx,mjs,mts,cjs,cts}" {
		t.Fatalf("TestPattern() = %q", pattern)
	}
}

func TestVitest_HasUnskippableMarker(t *testing.T) {
	markedFile := filepath.Join(t.TempDir(), "marked.test.ts")
	if err := os.WriteFile(markedFile, []byte("// @datadog\n// unskippable"), 0644); err != nil {
		t.Fatal(err)
	}

	vitest := NewVitest()
	if !vitest.HasUnskippableMarker(markedFile) {
		t.Fatal("expected unskippable marker")
	}
	if !vitest.HasUnskippableMarker(filepath.Join(t.TempDir(), "missing.test.ts")) {
		t.Fatal("missing files should be treated as guarded")
	}
}

func TestVitest_DiscoverTestFiles_WithConfig(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"packages/a.test.ts", "packages/b.spec.ts"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	executor := &vitestCommandExecutor{
		output: vitestListOutput(t,
			filepath.Join(tempDir, "packages", "b.spec.ts"),
			"packages/a.test.ts",
			"packages/a.test.ts",
		),
	}
	vitest := &Vitest{
		executor:   executor,
		configFile: "vitest.unit.ts",
		platformEnv: map[string]string{
			"NODE_OPTIONS": "--import dd-trace/register.js -r dd-trace/ci/init --max-old-space-size=4096",
		},
	}

	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatalf("DiscoverTestFiles() failed: %v", err)
	}
	if executor.capturedName != "node" || len(executor.capturedArgs) != 1 || filepath.Base(executor.capturedArgs[0]) != "vitest.mjs" {
		t.Fatalf("command = %q %q", executor.capturedName, executor.capturedArgs)
	}
	if executor.capturedRequest.Config != "vitest.unit.ts" || !executor.capturedRequest.Discover {
		t.Fatalf("request = %+v", executor.capturedRequest)
	}
	if _, err := os.Stat(filepath.Dir(executor.capturedArgs[0])); !os.IsNotExist(err) {
		t.Fatalf("discovery adapter directory was not cleaned up: %v", err)
	}
	if got := executor.capturedEnvMap["NODE_OPTIONS"]; got != "--max-old-space-size=4096" {
		t.Fatalf("discovery NODE_OPTIONS = %q", got)
	}
	wantFiles := []string{"packages/a.test.ts", "packages/b.spec.ts"}
	if !slices.Equal(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
}

func TestVitest_DiscoverTestFiles_ExplicitFiles(t *testing.T) {
	executor := &vitestCommandExecutor{err: errors.New("should not execute")}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	want := []string{"src/a.test.ts"}
	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{ExplicitFiles: want})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, want) || executor.capturedName != "" {
		t.Fatalf("files = %v, command = %q", files, executor.capturedName)
	}
}

func TestVitest_DiscoverTestFiles_ExcludeStillUsesVitestDiscovery(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"generic.test.ts", "excluded.test.ts", "custom.check.ts"} {
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	setTestsExcludePattern(t, "excluded.test.ts")
	vitest := &Vitest{
		executor: &vitestCommandExecutor{
			output: vitestListOutput(t,
				"generic.test.ts",
				"excluded.test.ts",
				"custom.check.ts",
			),
		},
		platformEnv: make(map[string]string),
	}
	resolvedTestFiles, err := discovery.ResolveTestFiles(vitest.TestPattern(), settings.GetTestsExcludePattern())
	if err != nil {
		t.Fatal(err)
	}
	if !resolvedTestFiles.UseExplicitFiles() || slices.Contains(resolvedTestFiles.ExplicitFiles, "custom.check.ts") {
		t.Fatalf("expected generic glob candidates without custom Vitest file, got %v", resolvedTestFiles.ExplicitFiles)
	}

	files, err := vitest.DiscoverTestFiles(context.Background(), resolvedTestFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom.check.ts", "generic.test.ts"}) {
		t.Fatalf("files = %v", files)
	}
	if executor := vitest.executor.(*vitestCommandExecutor); executor.capturedName == "" {
		t.Fatal("expected Vitest discovery command to run")
	}
}

func TestVitest_DiscoverTestFiles_ExcludeWithEmptyCandidatesStillUsesVitestDiscovery(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"excluded.test.ts", "custom.check.ts"} {
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	setTestsExcludePattern(t, "excluded.test.ts")
	executor := &vitestCommandExecutor{output: vitestListOutput(t,
		"excluded.test.ts",
		"custom.check.ts",
	)}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	resolvedTestFiles, err := discovery.ResolveTestFiles(vitest.TestPattern(), settings.GetTestsExcludePattern())
	if err != nil {
		t.Fatal(err)
	}
	if !resolvedTestFiles.Empty() {
		t.Fatalf("expected empty generic glob candidates, got %v", resolvedTestFiles.ExplicitFiles)
	}

	files, err := vitest.DiscoverTestFiles(context.Background(), resolvedTestFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom.check.ts"}) {
		t.Fatalf("files = %v", files)
	}
	if executor.capturedName == "" {
		t.Fatal("expected Vitest discovery command to run")
	}
}

func TestVitest_DiscoverTestFiles_ErrorIncludesOutput(t *testing.T) {
	executor := &vitestCommandExecutor{output: []byte("invalid Vitest config"), err: errors.New("exit status 1")}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	_, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err == nil || !strings.Contains(err.Error(), "invalid Vitest config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVitest_DiscoverTestFiles_InvalidJSON(t *testing.T) {
	executor := &vitestCommandExecutor{output: []byte("not JSON")}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	_, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err == nil || !strings.Contains(err.Error(), "failed to parse Vitest test file list") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVitest_DiscoverTestFiles_IgnoresStdoutAndStderrNoise(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(tempDir, "named.test.ts")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	executor := &vitestCommandExecutor{
		output: vitestListOutput(t, testFile),
		stdout: []byte("Vitest config log\n"),
		stderr: []byte("Vite deprecation warning\n"),
	}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "named.test.ts" {
		t.Fatalf("files = %v", files)
	}
}

func TestVitest_DiscoverTestFiles_FiltersCustomLocation(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"custom/a.check.ts", "src/b.test.ts"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	setTestsLocation(t, "custom/**/*.check.ts")

	executor := &vitestCommandExecutor{output: vitestListOutput(t,
		"src/b.test.ts",
		"custom/a.check.ts",
	)}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom/a.check.ts"}) {
		t.Fatalf("files = %v", files)
	}
}

func TestVitest_RejectsCustomCommand(t *testing.T) {
	executor := &vitestCommandExecutor{}
	vitest := &Vitest{executor: executor, customCommand: "pnpm exec vitest run"}
	_, discoveryErr := vitest.DiscoverTestFiles(t.Context(), discovery.TestFileSet{ExplicitFiles: []string{"a.test.js"}})
	runErr := vitest.RunTests(t.Context(), []string{"a.test.js"}, nil)
	for _, err := range []error{discoveryErr, runErr} {
		if err == nil || !strings.Contains(err.Error(), "does not support --command") || !strings.Contains(err.Error(), "--vitest-config") {
			t.Fatalf("expected migration instructions, got %v", err)
		}
	}
	if executor.capturedName != "" {
		t.Fatal("custom command must not be executed")
	}
}

func TestVitest_ConfigFromEnvironment(t *testing.T) {
	t.Cleanup(func() { viper.Reset(); settings.Init() })
	t.Setenv("DD_TEST_OPTIMIZATION_RUNNER_VITEST_CONFIG", "config with spaces/vitest.ts")
	viper.Reset()
	settings.Init()
	if got := NewVitest().configFile; got != "config with spaces/vitest.ts" {
		t.Fatalf("config = %q", got)
	}
}

func TestVitest_RunTests_ExactSelectionEnvironmentAndCleanup(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("test process failed")} {
		t.Run(fmt.Sprintf("run error %v", runErr), func(t *testing.T) {
			var adapterDir string
			workerEnv := map[string]string{"NODE_OPTIONS": "--max-old-space-size=2048", "SHARED": "worker"}
			executor := &vitestCommandExecutor{onRun: func(name string, args []string, env map[string]string) error {
				if name != "node" || len(args) != 1 {
					t.Fatalf("command = %q %q", name, args)
				}
				adapterDir = filepath.Dir(args[0])
				contents, err := os.ReadFile(filepath.Join(adapterDir, "request.json"))
				if err != nil {
					t.Fatal(err)
				}
				var request vitestRequest
				if err := json.Unmarshal(contents, &request); err != nil {
					t.Fatal(err)
				}
				if request.Discover || request.Config != "vitest.unit.ts" || !slices.Equal(request.Files, []string{"src/endOfYear/test.ts"}) {
					t.Fatalf("request = %+v", request)
				}
				if env["NODE_OPTIONS"] != workerEnv["NODE_OPTIONS"] || env["SHARED"] != "worker" {
					t.Fatalf("worker environment was changed: %v", env)
				}
				for _, name := range []string{"vitest.mjs", "vitest_modern.mjs", "vitest_legacy.mjs"} {
					if _, err := os.Stat(filepath.Join(adapterDir, name)); err != nil {
						t.Fatal(err)
					}
				}
				return runErr
			}}
			vitest := &Vitest{executor: executor, configFile: "vitest.unit.ts", platformEnv: map[string]string{"NODE_OPTIONS": "platform-options", "SHARED": "platform"}}
			if err := vitest.RunTests(t.Context(), []string{"src/endOfYear/test.ts"}, workerEnv); !errors.Is(err, runErr) {
				t.Fatalf("got %v, want %v", err, runErr)
			}
			if _, err := os.Stat(adapterDir); !os.IsNotExist(err) {
				t.Fatalf("adapter directory was not cleaned up: %v", err)
			}
			if len(workerEnv) != 2 || workerEnv["NODE_OPTIONS"] != "--max-old-space-size=2048" {
				t.Fatalf("mutated worker environment: %v", workerEnv)
			}
		})
	}
}

func TestVitest_RunTests_EmptyBatch(t *testing.T) {
	executor := &vitestCommandExecutor{onRun: func(string, []string, map[string]string) error {
		t.Fatal("empty batch must not invoke Vitest")
		return nil
	}}
	if err := (&Vitest{executor: executor}).RunTests(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestStripNodeOptionsImport(t *testing.T) {
	input := "--import dd-trace/register.js --import=other/register.js --max-old-space-size=4096"
	want := "--import=other/register.js --max-old-space-size=4096"
	if got := stripNodeOptionsImport(input, ddTraceRegisterPath); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
