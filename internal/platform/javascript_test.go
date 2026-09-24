package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
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

func TestJavaScript_GetPlatformEnv_DoesNotDependOnFramework(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "vitest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	if got := NewJavaScript().GetPlatformEnv()[nodeOptionsEnvVar]; got != nodeOptionsDDTraceCIArg {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, nodeOptionsDDTraceCIArg)
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
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
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
	tags, err := javascript.CreateTagsMap(context.Background())
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

	probeErr := errors.New("probe failed")
	javascript := &JavaScript{
		executor: &mockCommandExecutor{
			combinedOutput:    []byte(" Invalid NODE_OPTIONS\n"),
			combinedOutputErr: probeErr,
		},
	}

	tags, err := javascript.CreateTagsMap(context.Background())
	if err == nil {
		t.Fatal("expected error when node command fails")
	}
	if tags != nil {
		t.Error("expected nil tags when command fails")
	}
	if !strings.Contains(err.Error(), "failed to execute JavaScript script") {
		t.Errorf("expected JavaScript execution error, got %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid NODE_OPTIONS") {
		t.Errorf("expected error to include probe output, got %q", err.Error())
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("expected error to wrap probe failure, got %v", err)
	}
}

func TestJavaScript_CreateTagsMap_InvalidJSON(t *testing.T) {
	defer func() {
		_ = os.RemoveAll(constants.PlanDirectory)
	}()

	mockExecutor := &mockCommandExecutor{
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if err := os.WriteFile(args[2], []byte("{invalid json}"), 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	tags, err := javascript.CreateTagsMap(context.Background())
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

func TestJavaScript_DetectFramework_Mocha(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "mocha")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "mocha" {
		t.Fatalf("framework name = %q, want mocha", fw.Name())
	}
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != nodeOptionsDDTraceCIArg {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, nodeOptionsDDTraceCIArg)
	}
}

func TestJavaScript_DetectFramework_Cypress(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "cypress")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "cypress" {
		t.Fatalf("framework name = %q, want cypress", fw.Name())
	}
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != nodeOptionsDDTraceCIArg {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, nodeOptionsDDTraceCIArg)
	}
}

func TestJavaScript_DetectFramework_Playwright(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "playwright")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "playwright" {
		t.Fatalf("framework name = %q, want playwright", fw.Name())
	}
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != nodeOptionsDDTraceCIArg {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, nodeOptionsDDTraceCIArg)
	}
}

func TestJavaScript_DetectFramework_Cucumber(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "cucumber")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "cucumber" {
		t.Fatalf("framework name = %q, want cucumber", fw.Name())
	}
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != nodeOptionsDDTraceCIArg {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, nodeOptionsDDTraceCIArg)
	}
}

func TestJavaScript_DetectFramework_Vitest(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, "")
	viper.Reset()
	viper.Set("framework", "vitest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw.Name() != "vitest" {
		t.Fatalf("framework name = %q, want vitest", fw.Name())
	}
	wantNodeOptions := nodeImportArg + " " + ddTraceRegisterModule + " " + nodeOptionsDDTraceCIArg
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != wantNodeOptions {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, wantNodeOptions)
	}
}

func TestJavaScript_DetectFramework_VitestPreservesExistingOptions(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, nodeOptionsDDTraceCIArg+" --max-old-space-size=4096")
	viper.Reset()
	viper.Set("framework", "vitest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}

	want := nodeImportArg + " " + ddTraceRegisterModule + " " + nodeOptionsDDTraceCIArg + " --max-old-space-size=4096"
	if got := fw.GetPlatformEnv()[nodeOptionsEnvVar]; got != want {
		t.Fatalf("NODE_OPTIONS = %q, want %q", got, want)
	}
}

func TestJavaScript_DetectFramework_VitestDoesNotDuplicateRegister(t *testing.T) {
	t.Setenv(nodeOptionsEnvVar, nodeImportArg+" "+ddTraceRegisterModule+" --max-old-space-size=4096")
	viper.Reset()
	viper.Set("framework", "vitest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	fw, err := NewJavaScript().DetectFramework()
	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}

	nodeOptions := fw.GetPlatformEnv()[nodeOptionsEnvVar]
	if strings.Count(nodeOptions, ddTraceRegisterModule) != 1 {
		t.Fatalf("NODE_OPTIONS contains duplicate registration: %q", nodeOptions)
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
		combinedOutput: []byte("/project/node_modules/dd-trace/ci/init.js\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			calls++
			if name != "node" {
				t.Fatalf("expected command 'node', got %q", name)
			}
			if calls == 1 && (len(args) != 1 || args[0] != "--version") {
				t.Fatalf("expected node --version, got %v", args)
			}
			if calls == 2 && (len(args) != 3 || args[0] != "-e" || args[2] != ddTraceCIInitModule) {
				t.Fatalf("expected node require.resolve command, got %v", args)
			}
		},
	}

	javascript := &JavaScript{executor: mockExecutor}
	if err := javascript.SanityCheck(context.Background()); err != nil {
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

	err := javascript.SanityCheck(context.Background())
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
	err := javascript.SanityCheck(context.Background())
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

	err := javascript.SanityCheck(context.Background())
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
	err := javascript.SanityCheck(context.Background())
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

	t.Setenv("PATH", t.TempDir())
	platform, err := DetectPlatform()
	if err != nil {
		t.Fatal(err)
	}
	if platform.Name() != "javascript" {
		t.Fatalf("expected javascript, got %q", platform.Name())
	}

}

func TestJavaScriptDetectionDoesNotOverrideCommand(t *testing.T) {
	for _, script := range []string{"jest --config custom.js", "vitest run", "jest && eslint .", "cross-env NODE_ENV=test jest"} {
		t.Run(script, func(t *testing.T) {
			resetDetectionSettings(t)
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile("package.json", []byte(`{"devDependencies":{"jest":"29"},"scripts":{"test":"`+script+`"}}`), 0644))
			// Capture the actual invocation without installing or running Jest.
			bin := t.TempDir()
			t.Setenv("PATH", bin)
			for _, name := range []string{"npx", "custom-jest"} {
				require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '%s\\n' '"+name+"' \"$@\" > \"$DDTEST_COMMAND_CAPTURE\"\n"), 0755))
			}
			capture := filepath.Join(root, "command.txt")
			t.Setenv("DDTEST_COMMAND_CAPTURE", capture)
			for _, custom := range []string{"", "custom-jest --config explicit.js"} {
				settings.Get().Command = custom
				settings.Get().Framework = "jest"
				fw, err := NewJavaScript().DetectFramework()
				require.NoError(t, err)
				require.NoError(t, fw.RunTests(context.Background(), []string{"example.test.js"}, nil))
				invocation, err := os.ReadFile(capture)
				require.NoError(t, err)
				if custom == "" {
					require.Equal(t, "npx\njest\n--runTestsByPath\nexample.test.js\n", string(invocation))
				} else {
					require.Equal(t, "custom-jest\n--config\nexplicit.js\n--runTestsByPath\nexample.test.js\n", string(invocation))
				}
			}
		})
	}
}

func (m *sequentialMockExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	output, err := m.CombinedOutput(ctx, name, args, env)
	if err != nil {
		return nil, output, err
	}
	return output, nil, nil
}

type command struct {
	name string
	args []string
}

type commandResponse struct {
	output []byte
	stderr []byte
	err    error
}

type fakeCommandExecutor struct {
	commands  []command
	envs      []map[string]string
	responses []commandResponse
}

func (e *fakeCommandExecutor) CombinedOutput(_ context.Context, name string, args []string, env map[string]string) ([]byte, error) {
	e.commands = append(e.commands, command{name: name, args: args})
	e.envs = append(e.envs, env)
	response := e.responses[len(e.commands)-1]
	return append(append([]byte(nil), response.output...), response.stderr...), response.err
}

func (e *fakeCommandExecutor) Output(ctx context.Context, name string, args []string, env map[string]string) ([]byte, []byte, error) {
	_, err := e.CombinedOutput(ctx, name, args, env)
	response := e.responses[len(e.commands)-1]
	return response.output, response.stderr, err
}

func TestJavaScriptInstall(t *testing.T) {
	sessionDirectory := t.TempDir()
	resolvedPath := filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init.js")
	executor := &fakeCommandExecutor{
		responses: []commandResponse{{err: errors.New("project tracer unavailable")},
			{},
			{output: []byte(resolvedPath), stderr: []byte("MODULE 123: looking for dd-trace\n")},
		},
	}
	javascript := &JavaScript{executor: executor}

	ciInitPath, err := javascript.InstallTestdriveTracer(context.Background(), TracerOptions{Directory: sessionDirectory})
	require.NoError(t, err)
	require.Equal(t, resolvedPath, ciInitPath.Path)
	require.False(t, ciInitPath.Project)
	require.True(t, filepath.IsAbs(ciInitPath.Path))
	require.Equal(t, "node", executor.commands[0].name)
	require.Equal(t, []string{"-e", resolveJavaScriptModule, ddTraceCIInitModule}, executor.commands[0].args)
	require.Equal(t, []command{
		executor.commands[0],
		{
			name: "npm",
			args: []string{
				"install",
				"--prefix", sessionDirectory,
				"--global=false",
				"--no-save",
				"--package-lock=false",
				"--no-audit",
				"--no-fund",
				"dd-trace@latest",
			},
		},
		{
			name: "node",
			args: []string{
				"-e",
				resolveJavaScriptModule,
				filepath.Join(sessionDirectory, "node_modules", "dd-trace", "ci", "init"),
			},
		},
	}, executor.commands)
	require.Equal(t, []map[string]string{{"NODE_OPTIONS": ""}, {"NODE_OPTIONS": "", "NPM_CONFIG_GLOBAL": "false", "npm_config_global": "false"}, {"NODE_OPTIONS": "", "NPM_CONFIG_GLOBAL": "false", "npm_config_global": "false"}}, executor.envs)
}

func TestJavaScriptInstallReportsNPMError(t *testing.T) {
	executor := &fakeCommandExecutor{
		responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {
			output: []byte("registry unavailable"),
			err:    errors.New("exit status 1"),
		}},
	}
	javascript := &JavaScript{executor: executor}

	_, err := javascript.InstallTestdriveTracer(context.Background(), TracerOptions{Directory: t.TempDir()})
	require.ErrorContains(t, err, "install dd-trace@latest")
	require.ErrorContains(t, err, "registry unavailable")
}

func TestJavaScriptInstallReportsResolveErrorWithoutOutput(t *testing.T) {
	executor := &fakeCommandExecutor{
		responses: []commandResponse{{err: errors.New("project tracer unavailable")},
			{},
			{err: errors.New("exit status 1")},
		},
	}
	javascript := &JavaScript{executor: executor}

	_, err := javascript.InstallTestdriveTracer(context.Background(), TracerOptions{Directory: t.TempDir()})
	require.ErrorContains(t, err, "resolve dd-trace/ci/init: exit status 1")
}

func TestJavaScriptInstallReportsResolveStderr(t *testing.T) {
	exitErr := errors.New("exit status 1")
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")},
		{},
		{stderr: []byte("Cannot find module dd-trace/ci/init"), err: exitErr},
	}}
	javascript := &JavaScript{executor: executor}

	path, err := javascript.InstallTestdriveTracer(context.Background(), TracerOptions{Directory: t.TempDir()})
	require.Empty(t, path)
	require.ErrorContains(t, err, "resolve dd-trace/ci/init: Cannot find module dd-trace/ci/init")
	require.ErrorIs(t, err, exitErr)
}

func TestJavaScriptInstallRejectsRelativePreloadPath(t *testing.T) {
	executor := &fakeCommandExecutor{
		responses: []commandResponse{{err: errors.New("project tracer unavailable")},
			{},
			{output: []byte("node_modules/dd-trace/ci/init.js\n")},
		},
	}
	javascript := &JavaScript{executor: executor}

	_, err := javascript.InstallTestdriveTracer(context.Background(), TracerOptions{Directory: t.TempDir()})
	require.ErrorContains(t, err, `node returned non-absolute path "node_modules/dd-trace/ci/init.js"`)
}

func TestJavaScriptInstallEndToEnd(t *testing.T) {
	if os.Getenv("DDTEST_RUN_NPM_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_NPM_INTEGRATION_TEST=1 to install the selected tracer from npm")
	}

	t.Setenv("NODE_DEBUG", "module")
	t.Setenv("NODE_OPTIONS", "-r dd-trace/ci/init")
	t.Setenv("NPM_CONFIG_GLOBAL", "true")
	sessionDirectory := filepath.Join(t.TempDir(), "session with spaces")
	require.NoError(t, os.MkdirAll(sessionDirectory, 0o755))
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	ciInitPath, err := NewJavaScript().InstallTestdriveTracer(ctx, TracerOptions{Directory: sessionDirectory, Version: "latest"})
	require.NoError(t, err)
	require.FileExists(t, ciInitPath.Path)
	resolvedSessionDirectory, err := filepath.EvalSymlinks(sessionDirectory)
	require.NoError(t, err)
	require.Equal(t, resolvedSessionDirectory, filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(ciInitPath.Path)))))
}

func TestJavaScriptVersions(t *testing.T) {
	for _, tt := range []struct{ version, spec string }{
		{"", "dd-trace@latest"},
		{"latest", "dd-trace@latest"},
		{"6.15.0", "dd-trace@6.15.0"},
		{"6.16.0-pre.1", "dd-trace@6.16.0-pre.1"},
		{"git:abc1234", "dd-trace@git+https://github.com/DataDog/dd-trace-js.git#abc1234"},
		{"git:master", "dd-trace@git+https://github.com/DataDog/dd-trace-js.git#master"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			directory := t.TempDir()
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {}, {output: []byte(filepath.Join(directory, "init.js"))}}}
			installer := NewJavaScript()
			installer.executor = executor
			_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Version: tt.version})
			require.NoError(t, err)
			require.Equal(t, tt.spec, executor.commands[1].args[len(executor.commands[1].args)-1])
		})
	}
	executor := &fakeCommandExecutor{}
	installer := NewJavaScript()
	installer.executor = executor
	_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: "git:"})
	require.ErrorContains(t, err, "git ref must not be empty")
	require.Empty(t, executor.commands)
}

func TestJavaScriptReusesProjectRegardlessOfRequestedVersion(t *testing.T) {
	for _, version := range []string{"latest", "6.15.0", "git:abc1234"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node_modules", "dd-trace", "ci", "init.js")
			executor := &fakeCommandExecutor{responses: []commandResponse{{output: []byte(path)}}}
			installer := NewJavaScript()
			installer.executor = executor
			result, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Version: version})
			require.NoError(t, err)
			require.Equal(t, TracerInstallation{Path: path, Project: true}, result)
			require.Len(t, executor.commands, 1)
			require.Equal(t, "node", executor.commands[0].name)
		})
	}
}

func TestJavaScriptProbeFailureAttemptsInstall(t *testing.T) {
	executor := &fakeCommandExecutor{responses: []commandResponse{{stderr: []byte("broken project tracer"), err: errors.New("exit 1")}, {output: []byte("npm unavailable"), err: errors.New("exit 1")}}}
	installer := NewJavaScript()
	installer.executor = executor
	_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir()})
	require.ErrorContains(t, err, "npm unavailable")
	require.Len(t, executor.commands, 2)
	require.Equal(t, "npm", executor.commands[1].name)
}

func (e *fakeCommandExecutor) Run(ctx context.Context, name string, args []string, env map[string]string) error {
	_, err := e.CombinedOutput(ctx, name, args, env)
	return err
}
