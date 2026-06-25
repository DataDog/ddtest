package platform

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
)

// sequentialMockExecutor returns pre-configured responses in order for CombinedOutput calls.
// Used when successive calls to SanityCheck need different return values.
type sequentialMockExecutor struct {
	responses []struct {
		output []byte
		err    error
	}
	index int
}

func (m *sequentialMockExecutor) CombinedOutput(_ context.Context, _ string, _ []string, _ map[string]string) ([]byte, error) {
	if m.index >= len(m.responses) {
		return nil, nil
	}
	r := m.responses[m.index]
	m.index++
	return r.output, r.err
}

func (m *sequentialMockExecutor) Run(_ context.Context, _ string, _ []string, _ map[string]string) error {
	return nil
}

func TestJavaScript_Name(t *testing.T) {
	javascript := NewJavaScript()
	if javascript.Name() != "javascript" {
		t.Errorf("expected %q, got %q", "javascript", javascript.Name())
	}
}

func TestJavaScript_TestSkippingLevel(t *testing.T) {
	if got := NewJavaScript().TestSkippingLevel(); got != settings.TestSkippingLevelSuite {
		t.Fatalf("TestSkippingLevel() = %q, want %q", got, settings.TestSkippingLevelSuite)
	}
}

func TestJavaScript_GetPlatformEnv_SetsNODEOPTIONS(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")

	javascript := NewJavaScript()
	envMap := javascript.GetPlatformEnv()

	if envMap[nodeOptionsEnvVar] != nodeOptionsDDTraceCIArg {
		t.Errorf("expected NODE_OPTIONS to be %q, got %q", nodeOptionsDDTraceCIArg, envMap[nodeOptionsEnvVar])
	}
}

func TestJavaScript_GetPlatformEnv_PreservesExistingNODEOPTIONS(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "--max-old-space-size=4096")

	javascript := NewJavaScript()
	envMap := javascript.GetPlatformEnv()

	expected := nodeOptionsDDTraceCIArg + " --max-old-space-size=4096"
	if envMap[nodeOptionsEnvVar] != expected {
		t.Errorf("expected NODE_OPTIONS to be %q, got %q", expected, envMap[nodeOptionsEnvVar])
	}
}

func TestJavaScript_GetPlatformEnv_DoesNotDuplicateDDTraceInit(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "-r dd-trace/ci/init --max-old-space-size=4096")

	javascript := NewJavaScript()
	envMap := javascript.GetPlatformEnv()

	if len(envMap) != 0 {
		t.Errorf("expected empty env map when dd-trace init is already present, got %v", envMap)
	}
}

func TestJavaScript_CreateTagsMap_Success(t *testing.T) {
	defer func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	}()

	expectedJavaScriptTags := map[string]string{
		"os.platform":     "darwin",
		"os.architecture": "arm64",
		"os.version":      "24.5.0",
		"runtime.name":    "node",
		"runtime.version": "v22.16.0",
	}

	expectedOutput, err := json.Marshal(expectedJavaScriptTags)
	if err != nil {
		t.Fatalf("failed to marshal expected tags: %v", err)
	}

	mockExecutor := &mockCommandExecutor{
		onRun: func(name string, args []string, envMap map[string]string) {
			if name != "node" {
				t.Errorf("expected command to be 'node', got %q", name)
			}
			if len(args) != 3 {
				t.Errorf("expected 3 args, got %d: %v", len(args), args)
				return
			}
			if args[0] != "-e" {
				t.Errorf("expected first arg to be '-e', got %q", args[0])
			}
			if args[1] == "" {
				t.Error("javascript script should not be empty")
			}
			if err := os.WriteFile(args[2], expectedOutput, 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	tags, err := javascript.CreateTagsMap()
	if err != nil {
		t.Fatalf("CreateTagsMap failed: %v", err)
	}

	if tags["language"] != "javascript" {
		t.Errorf("expected language tag to be 'javascript', got %q", tags["language"])
	}
	for key, expectedValue := range expectedJavaScriptTags {
		if actualValue := tags[key]; actualValue != expectedValue {
			t.Errorf("expected tag %q to be %q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestJavaScript_CreateTagsMap_CommandFailure(t *testing.T) {
	defer func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	}()

	javascript := &JavaScript{
		executor: &mockCommandExecutor{runErr: &exec.ExitError{}},
	}

	tags, err := javascript.CreateTagsMap()
	if err == nil {
		t.Fatal("expected error when node command fails")
	}
	if tags != nil {
		t.Error("expected nil tags when command fails")
	}
	if !strings.Contains(err.Error(), "failed to execute JavaScript script") {
		t.Errorf("expected JavaScript execution error, got %v", err)
	}
}

func TestJavaScript_CreateTagsMap_InvalidJSON(t *testing.T) {
	defer func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	}()

	mockExecutor := &mockCommandExecutor{
		onRun: func(name string, args []string, envMap map[string]string) {
			if err := os.WriteFile(args[2], []byte("{invalid json}"), 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	tags, err := javascript.CreateTagsMap()
	if err == nil {
		t.Fatal("expected error when JSON is invalid")
	}
	if tags != nil {
		t.Error("expected nil tags when JSON parsing fails")
	}
	if !strings.Contains(err.Error(), "failed to parse runtime tags JSON") {
		t.Errorf("expected JSON parsing error, got %v", err)
	}
}

func TestJavaScript_DetectFramework_Jest(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "jest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	javascript := NewJavaScript()
	fw, err := javascript.DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "jest" {
		t.Errorf("expected framework name to be 'jest', got %q", fw.Name())
	}
}

func TestJavaScript_DetectFramework_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "rspec")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	javascript := NewJavaScript()
	fw, err := javascript.DetectFramework()
	if err == nil {
		t.Fatalf("expected unsupported framework error, got framework %v", fw)
	}
	if fw != nil {
		t.Error("expected nil framework for unsupported framework")
	}
	expectedError := "framework 'rspec' is not supported by platform 'javascript'"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestJavaScript_SanityCheck_Passes(t *testing.T) {
	calls := 0
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("v22.16.0\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			calls++
			if name != "node" {
				t.Fatalf("expected command 'node', got %q", name)
			}
			if calls == 1 && (len(args) != 1 || args[0] != "--version") {
				t.Fatalf("expected node --version, got %v", args)
			}
			if calls == 2 && (len(args) != 2 || args[0] != "-e" || !strings.Contains(args[1], ddTraceCIInitModule)) {
				t.Fatalf("expected node require.resolve command, got %v", args)
			}
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	if err := javascript.SanityCheck(); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 sanity check commands, got %d", calls)
	}
}

func TestJavaScript_SanityCheck_FailsWhenNodeMissing(t *testing.T) {
	javascript := &JavaScript{
		executor: &mockCommandExecutor{
			combinedOutput:    []byte("node: command not found"),
			combinedOutputErr: &exec.ExitError{},
		},
	}

	err := javascript.SanityCheck()
	if err == nil {
		t.Fatal("expected sanity check error")
	}
	if !strings.Contains(err.Error(), "node --version command failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJavaScript_EmbeddedScript(t *testing.T) {
	if javascriptEnvScript == "" {
		t.Error("embedded JavaScript script should not be empty")
	}

	for _, expected := range []string{
		"require(\"os\")",
		"process.version",
		"process.arch",
		"process.platform",
		"fs.writeFileSync",
	} {
		if !strings.Contains(javascriptEnvScript, expected) {
			t.Errorf("expected JavaScript script to contain %q", expected)
		}
	}
}

func TestJavaScript_SanityCheck_FailsWhenDDTraceMissing(t *testing.T) {
	mockExecutor := &sequentialMockExecutor{
		responses: []struct {
			output []byte
			err    error
		}{
			{output: []byte("v22.16.0\n"), err: nil},
			{output: []byte("Cannot find module 'dd-trace/ci/init'"), err: &exec.ExitError{}},
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	err := javascript.SanityCheck()
	if err == nil {
		t.Fatal("expected sanity check error when dd-trace is missing")
	}
	if !strings.Contains(err.Error(), "failed to resolve "+ddTraceCIInitModule) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJavaScript_DetectFramework_SetsPlatformEnv(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")

	viper.Reset()
	viper.Set("framework", "jest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	javascript := NewJavaScript()
	fw, err := javascript.DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw == nil {
		t.Fatal("expected framework to be non-nil")
	}

	frameworkPlatformEnv := fw.GetPlatformEnv()
	if frameworkPlatformEnv[nodeOptionsEnvVar] != nodeOptionsDDTraceCIArg {
		t.Errorf("expected framework platformEnv %s=%q, got %q", nodeOptionsEnvVar, nodeOptionsDDTraceCIArg, frameworkPlatformEnv[nodeOptionsEnvVar])
	}
}

func TestJavaScript_GetPlatformEnv_UnsetNODEOPTIONS(t *testing.T) {
	// When NODE_OPTIONS is completely unset (not just empty), we should still
	// set it to the dd-trace init argument.
	if err := os.Unsetenv(nodeOptionsEnvVar); err != nil {
		t.Fatal(err)
	}

	javascript := NewJavaScript()
	envMap := javascript.GetPlatformEnv()

	if envMap[nodeOptionsEnvVar] != nodeOptionsDDTraceCIArg {
		t.Errorf("expected NODE_OPTIONS to be %q, got %q", nodeOptionsDDTraceCIArg, envMap[nodeOptionsEnvVar])
	}
}

func TestJavaScript_SanityCheck_NodeFailsEmptyOutput(t *testing.T) {
	javascript := &JavaScript{
		executor: &mockCommandExecutor{
			combinedOutput:    []byte(""),
			combinedOutputErr: &exec.ExitError{},
		},
	}

	err := javascript.SanityCheck()
	if err == nil {
		t.Fatal("expected sanity check error")
	}
	if !strings.Contains(err.Error(), "node --version command failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestJavaScript_SanityCheck_DDTraceFailsEmptyOutput(t *testing.T) {
	mockExecutor := &sequentialMockExecutor{
		responses: []struct {
			output []byte
			err    error
		}{
			{output: []byte("v22.16.0\n"), err: nil},
			{output: []byte(""), err: &exec.ExitError{}},
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	err := javascript.SanityCheck()
	if err == nil {
		t.Fatal("expected sanity check error when dd-trace resolve fails with empty output")
	}
	if !strings.Contains(err.Error(), "failed to resolve "+ddTraceCIInitModule) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDetectPlatform_JavaScript(t *testing.T) {
	viper.Reset()
	viper.Set("platform", "javascript")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	platform, err := DetectPlatform()
	if err != nil {
		// SanityCheck failed — verify the error names the javascript platform
		if !strings.Contains(err.Error(), "javascript") {
			t.Errorf("expected error to mention javascript platform, got: %v", err)
		}
		if platform != nil {
			t.Error("expected nil platform when sanity check fails")
		}
	} else {
		// SanityCheck passed (node + dd-trace available in this environment)
		if platform.Name() != "javascript" {
			t.Errorf("expected platform name 'javascript', got %q", platform.Name())
		}
	}
}
