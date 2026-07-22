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
	"github.com/DataDog/ddtest/internal/settings"
)

type vitestCommandExecutor struct {
	output         []byte
	err            error
	capturedName   string
	capturedArgs   []string
	capturedEnvMap map[string]string
}

type vitestCommandCall struct {
	name string
	args []string
}

type vitestListOutputEntry struct {
	File        string `json:"file"`
	ProjectName string `json:"projectName,omitempty"`
}

func vitestListOutput(t *testing.T, entries ...vitestListOutputEntry) []byte {
	t.Helper()
	output, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

type vitestSequenceExecutor struct {
	outputs [][]byte
	errors  []error
	calls   []vitestCommandCall
}

func (m *vitestSequenceExecutor) CombinedOutput(_ context.Context, name string, args []string, _ map[string]string) ([]byte, error) {
	callIndex := len(m.calls)
	m.calls = append(m.calls, vitestCommandCall{name: name, args: slices.Clone(args)})
	return m.outputs[callIndex], m.errors[callIndex]
}

func (m *vitestSequenceExecutor) Run(_ context.Context, _ string, _ []string, _ map[string]string) error {
	return nil
}

func (m *vitestCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, envMap map[string]string) ([]byte, error) {
	m.capturedName = name
	m.capturedArgs = slices.Clone(args)
	m.capturedEnvMap = envMap
	return m.output, m.err
}

func (m *vitestCommandExecutor) Run(_ context.Context, name string, args []string, envMap map[string]string) error {
	m.capturedName = name
	m.capturedArgs = slices.Clone(args)
	m.capturedEnvMap = envMap
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

func TestVitest_DiscoverTestFiles_WithCustomCommand(t *testing.T) {
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
			vitestListOutputEntry{File: filepath.Join(tempDir, "packages", "b.spec.ts"), ProjectName: "integration"},
			vitestListOutputEntry{File: "packages/a.test.ts", ProjectName: "unit"},
			vitestListOutputEntry{File: "packages/a.test.ts", ProjectName: "duplicate"},
		),
	}
	vitest := &Vitest{
		executor:        executor,
		commandOverride: []string{"pnpm", "exec", "vitest", "run", "--project", "unit*"},
		platformEnv: map[string]string{
			"NODE_OPTIONS": "--import dd-trace/register.js -r dd-trace/ci/init --max-old-space-size=4096",
		},
	}

	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatalf("DiscoverTestFiles() failed: %v", err)
	}
	if executor.capturedName != "pnpm" {
		t.Fatalf("command = %q, want pnpm", executor.capturedName)
	}
	wantArgs := []string{"exec", "vitest", "list", "--project", "unit*", "--filesOnly", "--json"}
	if !slices.Equal(executor.capturedArgs, wantArgs) {
		t.Fatalf("args = %v, want %v", executor.capturedArgs, wantArgs)
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
				vitestListOutputEntry{File: "generic.test.ts", ProjectName: "unit"},
				vitestListOutputEntry{File: "excluded.test.ts", ProjectName: "unit"},
				vitestListOutputEntry{File: "custom.check.ts", ProjectName: "unit"},
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
		vitestListOutputEntry{File: "excluded.test.ts", ProjectName: "unit"},
		vitestListOutputEntry{File: "custom.check.ts", ProjectName: "unit"},
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

func TestVitest_DiscoverTestFiles_Vitest16UsesConfigAwareFallback(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"packages/a.test.ts", "custom/b.check.ts"} {
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	executor := &vitestSequenceExecutor{
		outputs: [][]byte{
			[]byte("CACError: Unknown option `--filesOnly`"),
			[]byte("config log\n" + vitestV1DiscoveryMarker + `["packages/a.test.ts","custom/b.check.ts"]` + "\nclose log\n"),
		},
		errors: []error{errors.New("exit status 1"), nil},
	}
	vitest := &Vitest{
		executor:        executor,
		commandOverride: []string{"pnpm", "exec", "vitest", "run", "--project", "unit*"},
		platformEnv:     make(map[string]string),
	}

	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"custom/b.check.ts", "packages/a.test.ts"}) {
		t.Fatalf("files = %v", files)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("calls = %v", executor.calls)
	}
	if executor.calls[0].name != "pnpm" || !slices.Equal(executor.calls[0].args, []string{"exec", "vitest", "list", "--project", "unit*", "--filesOnly", "--json"}) {
		t.Fatalf("native discovery call = %#v", executor.calls[0])
	}
	if executor.calls[1].name != "node" || len(executor.calls[1].args) != 4 {
		t.Fatalf("Vitest 1.6 discovery call = %#v", executor.calls[1])
	}
	if got := executor.calls[1].args[3]; got != `["vitest","run","--project","unit*"]` {
		t.Fatalf("Vitest 1.6 CLI args = %s", got)
	}
}

func TestVitest_DiscoverTestFiles_Vitest16FallsBackToDDTestGlob(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("fallback.test.ts", []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	executor := &vitestSequenceExecutor{
		outputs: [][]byte{[]byte("Unknown option --filesOnly"), []byte("failed to import vitest/node")},
		errors:  []error{errors.New("exit status 1"), errors.New("exit status 1")},
	}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	files, err := vitest.DiscoverTestFiles(context.Background(), discovery.TestFileSet{Pattern: vitest.TestPattern()})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(files, []string{"fallback.test.ts"}) {
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
		vitestListOutputEntry{File: "src/b.test.ts", ProjectName: "unit"},
		vitestListOutputEntry{File: "custom/a.check.ts", ProjectName: "unit"},
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

func TestVitest_RunTests_WithCustomCommand(t *testing.T) {
	executor := &vitestCommandExecutor{}
	vitest := &Vitest{
		executor:        executor,
		commandOverride: []string{"pnpm", "exec", "vitest", "list", "--project", "unit*"},
		platformEnv:     map[string]string{"NODE_OPTIONS": "--import dd-trace/register.js -r dd-trace/ci/init", "SHARED": "platform"},
	}
	err := vitest.RunTests(context.Background(), []string{"src/a.test.ts", "src/b.spec.ts"}, map[string]string{"SHARED": "worker"})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"exec", "vitest", "run", "--project", "unit*", "src/a.test.ts", "src/b.spec.ts"}
	if executor.capturedName != "pnpm" || !slices.Equal(executor.capturedArgs, wantArgs) {
		t.Fatalf("command = %q, args = %v", executor.capturedName, executor.capturedArgs)
	}
	if executor.capturedEnvMap["SHARED"] != "worker" {
		t.Fatal("worker environment should override platform environment")
	}
}

func TestVitest_RunTests_UsesNpxFallback(t *testing.T) {
	tempDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	executor := &vitestCommandExecutor{}
	vitest := &Vitest{executor: executor, platformEnv: make(map[string]string)}
	if err := vitest.RunTests(context.Background(), []string{"src/a.test.ts"}, nil); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"vitest", "run", "src/a.test.ts"}
	if executor.capturedName != "npx" || !slices.Equal(executor.capturedArgs, wantArgs) {
		t.Fatalf("command = %q, args = %v", executor.capturedName, executor.capturedArgs)
	}
}

func TestVitestArgsForSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "replace run", args: []string{"exec", "vitest", "run", "--project", "unit"}, want: []string{"exec", "vitest", "list", "--project", "unit"}},
		{name: "insert after vitest", args: []string{"vitest", "--project", "unit"}, want: []string{"vitest", "list", "--project", "unit"}},
		{name: "direct binary args", args: []string{"--project", "unit"}, want: []string{"list", "--project", "unit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vitestArgsForSubcommand(tt.args, "list"); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVitestCLIArgs(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    []string
		want    []string
	}{
		{name: "package manager", command: "pnpm", args: []string{"exec", "vitest", "run", "--project", "unit"}, want: []string{"vitest", "run", "--project", "unit"}},
		{name: "direct binary", command: "node_modules/.bin/vitest", args: []string{"--config", "vitest.unit.ts"}, want: []string{"vitest", "--config", "vitest.unit.ts"}},
		{name: "npx default", command: "npx", args: []string{"vitest"}, want: []string{"vitest"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vitestCLIArgs(tt.command, tt.args); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripNodeOptionsImport(t *testing.T) {
	input := "--import dd-trace/register.js --import=other/register.js --max-old-space-size=4096"
	want := "--import=other/register.js --max-old-space-size=4096"
	if got := stripNodeOptionsImport(input, ddTraceRegisterPath); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
