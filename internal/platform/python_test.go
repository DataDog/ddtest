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

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestPython_Name(t *testing.T) {
	python := NewPython()
	if python.Name() != "python" {
		t.Errorf("expected %q, got %q", "python", python.Name())
	}
}

func TestPython_TestSkippingLevel(t *testing.T) {
	if got := NewPython().TestSkippingLevel(); got != settings.TestSkippingLevelTest {
		t.Fatalf("TestSkippingLevel() = %q, want %q", got, settings.TestSkippingLevelTest)
	}
}

func TestNormalizePyVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"4.12.0rc1", "4.12.0-rc1"},
		{"4.12.0rc1+g0e3e598", "4.12.0-rc1+g0e3e598"},
		{"4.12.0b2", "4.12.0-b2"},
		{"4.12.0b2+gabc123", "4.12.0-b2+gabc123"},
		{"4.12.0a1", "4.12.0-a1"},
		{"4.12.0a1+local.build", "4.12.0-a1+local.build"},
		{"4.10.3", "4.10.3"},
		{"1.2.3.4", "1.2.3.4"},
	}
	for _, c := range cases {
		if got := normalizePyVersion(c.in); got != c.want {
			t.Errorf("normalizePyVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPython_SanityCheck_SuccessWithPreRelease(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.12.0rc1+g0e3e598\n"),
	}
	python := NewPython()
	python.executor = mockExecutor
	if err := python.SanityCheck(context.Background()); err != nil {
		t.Fatalf("SanityCheck() unexpected error for pre-release version: %v", err)
	}
}

func TestPython_SanityCheck_Success(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.11.0\n"),
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if name != "python" {
				t.Fatalf("expected command 'python', got %q", name)
			}
			if len(args) < 3 || args[0] != "-c" {
				t.Fatalf("unexpected args: %v", args)
			}
			if args[2] != requiredPackageName {
				t.Errorf("expected package name arg %q, got %q", requiredPackageName, args[2])
			}
		},
	}

	python := NewPython()
	python.executor = mockExecutor
	if err := python.SanityCheck(context.Background()); err != nil {
		t.Fatalf("SanityCheck() unexpected error: %v", err)
	}
}

func TestPython_SanityCheck_NotInstalled(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte("No module named importlib.metadata"),
		combinedOutputErr: &exec.ExitError{},
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error when package is not installed")
	}

	if !strings.Contains(err.Error(), requiredPackageName) {
		t.Errorf("expected error to mention %q, got: %v", requiredPackageName, err)
	}
}

func TestPython_SanityCheck_VersionTooOld(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("4.10.3\n"),
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error for outdated ddtrace version")
	}

	if !strings.Contains(err.Error(), "4.10.3") {
		t.Errorf("expected error to mention detected version, got: %v", err)
	}
	if !strings.Contains(err.Error(), requiredPackageVersion) {
		t.Errorf("expected error to mention required version, got: %v", err)
	}
}

func TestPython_SanityCheck_InvalidVersion(t *testing.T) {
	mockExecutor := &mockCommandExecutor{
		combinedOutput: []byte("not-a-version\n"),
	}

	python := NewPython()
	python.executor = mockExecutor
	err := python.SanityCheck(context.Background())
	if err == nil {
		t.Fatal("SanityCheck() expected error for unparseable version")
	}

	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("expected error to mention parse failure, got: %v", err)
	}
}

func TestPython_GetPlatformEnv_SetsWhenNotSet(t *testing.T) {
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	if existed {
		_ = os.Unsetenv(pytestAddOptsEnvVar)
		defer func() { _ = os.Setenv(pytestAddOptsEnvVar, original) }()
	}

	python := NewPython()
	envMap := python.GetPlatformEnv()

	if envMap[pytestAddOptsEnvVar] != pytestDefaultAddOpts {
		t.Errorf("expected %s=%q, got %q", pytestAddOptsEnvVar, pytestDefaultAddOpts, envMap[pytestAddOptsEnvVar])
	}
}

func TestPython_GetPlatformEnv_AppendsWhenAlreadySet(t *testing.T) {
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	existingValue := "-v --tb=short"
	_ = os.Setenv(pytestAddOptsEnvVar, existingValue)
	defer func() {
		if existed {
			_ = os.Setenv(pytestAddOptsEnvVar, original)
		} else {
			_ = os.Unsetenv(pytestAddOptsEnvVar)
		}
	}()

	python := NewPython()
	envMap := python.GetPlatformEnv()

	expected := existingValue + " " + pytestDefaultAddOpts
	if envMap[pytestAddOptsEnvVar] != expected {
		t.Errorf("expected %s=%q, got %q", pytestAddOptsEnvVar, expected, envMap[pytestAddOptsEnvVar])
	}
}

func TestPython_CreateTagsMap_Success(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	expectedPythonTags := map[string]string{
		"runtime.name":    "python",
		"runtime.version": "3.11.0",
		"os.platform":     "linux",
		"os.architecture": "x86_64",
		"os.version":      "5.15.0",
	}

	expectedOutput, err := json.Marshal(expectedPythonTags)
	if err != nil {
		t.Fatalf("failed to marshal expected tags: %v", err)
	}

	mockExecutor := &mockCommandExecutor{
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if name != "python" {
				t.Errorf("expected command 'python', got %q", name)
			}
			if len(args) < 3 {
				t.Errorf("expected at least 3 args, got %d", len(args))
				return
			}
			if args[0] != "-c" {
				t.Errorf("expected args[0]='-c', got %q", args[0])
			}
			if args[1] == "" {
				t.Error("python script should not be empty")
			}
			tempFile := args[2]
			if tempFile == "" {
				t.Error("temp file path should not be empty")
			}
			if err := os.WriteFile(tempFile, expectedOutput, 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap(context.Background())
	if err != nil {
		t.Fatalf("CreateTagsMap failed: %v", err)
	}

	if tags["language"] != "python" {
		t.Errorf("expected language tag to be 'python', got %q", tags["language"])
	}

	for key, expectedValue := range expectedPythonTags {
		if actualValue, exists := tags[key]; !exists {
			t.Errorf("expected tag %q to exist", key)
		} else if actualValue != expectedValue {
			t.Errorf("expected tag %q=%q, got %q", key, expectedValue, actualValue)
		}
	}
}

func TestPython_CreateTagsMap_CommandFailure(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	probeErr := errors.New("probe failed")
	mockExecutor := &mockCommandExecutor{
		combinedOutput:    []byte(" Python startup hook failed\n"),
		combinedOutputErr: probeErr,
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap(context.Background())

	if err == nil {
		t.Error("expected error when python command fails")
	}
	if tags != nil {
		t.Error("expected nil tags when command fails")
	}

	expectedPrefix := "failed to execute Python script"
	if !strings.HasPrefix(err.Error(), expectedPrefix) {
		t.Errorf("expected error to start with %q, got %q", expectedPrefix, err.Error())
	}
	if !strings.Contains(err.Error(), "Python startup hook failed") {
		t.Errorf("expected error to include probe output, got %q", err.Error())
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("expected error to wrap probe failure, got %v", err)
	}
}

func TestPython_CreateTagsMap_InvalidJSON(t *testing.T) {
	defer func() { _ = os.RemoveAll(constants.PlanDirectory) }()

	invalidJSON := `{invalid json}`
	mockExecutor := &mockCommandExecutor{
		onCombinedOutput: func(name string, args []string, envMap map[string]string) {
			if len(args) < 3 {
				t.Errorf("expected at least 3 args, got %d", len(args))
				return
			}
			tempFile := args[2]
			if err := os.WriteFile(tempFile, []byte(invalidJSON), 0644); err != nil {
				t.Errorf("failed to write temp file: %v", err)
			}
		},
	}

	python := &Python{executor: mockExecutor}
	tags, err := python.CreateTagsMap(context.Background())

	if err == nil {
		t.Error("expected error when JSON is invalid")
	}
	if tags != nil {
		t.Error("expected nil tags when JSON parsing fails")
	}

	if !strings.Contains(err.Error(), "failed to parse runtime tags JSON") {
		t.Errorf("expected error to contain 'failed to parse runtime tags JSON', got %q", err.Error())
	}
}

func TestPython_DetectFramework_Pytest(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "pytest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	// Ensure PYTEST_ADDOPTS is unset so GetPlatformEnv produces a deterministic value
	original, existed := os.LookupEnv(pytestAddOptsEnvVar)
	if existed {
		_ = os.Unsetenv(pytestAddOptsEnvVar)
		defer func() { _ = os.Setenv(pytestAddOptsEnvVar, original) }()
	}

	python := NewPython()
	fw, err := python.DetectFramework()

	if err != nil {
		t.Fatalf("DetectFramework failed: %v", err)
	}
	if fw == nil {
		t.Fatal("expected framework to be non-nil")
	}
	if fw.Name() != "pytest" {
		t.Errorf("expected framework name 'pytest', got %q", fw.Name())
	}

	frameworkEnv := fw.GetPlatformEnv()
	if frameworkEnv[pytestAddOptsEnvVar] != pytestDefaultAddOpts {
		t.Errorf("expected framework platformEnv %s=%q, got %q",
			pytestAddOptsEnvVar, pytestDefaultAddOpts, frameworkEnv[pytestAddOptsEnvVar])
	}
}

func TestPython_DetectFramework_Unsupported(t *testing.T) {
	viper.Reset()
	viper.Set("framework", "unittest")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	python := NewPython()
	fw, err := python.DetectFramework()

	if err == nil {
		t.Errorf("expected error for unsupported framework, but got framework: %v", fw)
		return
	}
	if fw != nil {
		t.Error("expected nil framework for unsupported framework")
	}

	expectedError := "framework 'unittest' is not supported by platform 'python'"
	if err.Error() != expectedError {
		t.Errorf("expected error %q, got %q", expectedError, err.Error())
	}
}

func TestPython_EmbeddedScript(t *testing.T) {
	if pythonEnvScript == "" {
		t.Error("embedded Python script should not be empty")
	}

	expectedContent := []string{
		"import json",
		"import sys",
		"import platform",
		"sys.argv[1]",
		"json.dump",
	}

	for _, expected := range expectedContent {
		if !strings.Contains(pythonEnvScript, expected) {
			t.Errorf("expected Python script to contain %q", expected)
		}
	}
}

func TestDetectPlatform_Python(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	viper.Reset()
	viper.Set("platform", "python")
	settings.Init()
	defer func() {
		viper.Reset()
		settings.Init()
	}()

	platform, err := DetectPlatform()
	if err != nil {
		t.Fatalf("DetectPlatform() unexpected error: %v", err)
	}
	if platform == nil {
		t.Fatal("expected non-nil platform")
	}
	if platform.Name() != "python" {
		t.Errorf("expected platform name 'python', got %q", platform.Name())
	}
}

func TestPythonRealProjectConfigurations(t *testing.T) {
	for _, name := range []string{"requests-legacy", "django", "flask-legacy", "requests", "virtualenv"} {
		t.Run(name, func(t *testing.T) {
			files := detectionFiles(t, filepath.Join("testdata", "detection", name))
			root := t.TempDir()
			t.Chdir(root)
			t.Setenv("PATH", t.TempDir())
			for name, data := range files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0644))
			}
			found, err := NewPython().Detect(root)
			require.NoError(t, err)
			require.True(t, found)
			fw, err := NewPython().DetectFramework()
			require.NoError(t, err)
			require.Equal(t, "pytest", fw.Name())
			for name, data := range files {
				t.Run(name, func(t *testing.T) {
					isolated := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(isolated, name), []byte(data), 0644))
					found, err := NewPython().Detect(isolated)
					require.NoError(t, err)
					require.True(t, found)
				})
			}
			require.Equal(t, files, detectionFiles(t, root))
		})
	}
}

func TestPythonConfigurationEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, file, contents string
		python               bool
	}{
		{"unrelated cfg", "setup.cfg", "[server]\nrunner = pytest\n", false},
		{"generic metadata", "setup.cfg", "[metadata]\nname = pytest\n", false},
		{"commented pytest section", "setup.cfg", "# [tool:pytest]\n[server]\nmessage=pytest\n", false},
		{"packaging description", "setup.cfg", "[options]\npackages=find:\n[metadata]\ndescription=pytest integration\n", true},
		{"extras dependency", "setup.cfg", "[options.extras_require]\ntest =\n    pytest>=8\n    pytest-cov\n", true},
		{"tests_require", "setup.cfg", "[options]\ntests_require =\n    pytest; python_version > '3.9'\n", true},
		{"plugin is not runner", "requirements.txt", "pytest-cov\npytest-mock\n", true},
		{"requirements comment", "requirements.txt", "# pytest>=8\nrequests # install pytest later\n", true},
		{"requirement extras", "requirements-dev.txt", "pytest[testing]>=8\n", false},
		{"direct dependency URL", "requirements.txt", "pytest @ https://example.com/pytest.whl\n", true},
		{"description is not dependency", "pyproject.toml", "[project]\nname='pytest-helper'\ndescription='pytest'\ndependencies=['pytest-cov']\n", true},
		{"comment is not section", "pyproject.toml", "# [tool.pytest.ini_options]\n[tool.ruff]\nline-length=88\n", true},
		{"unrelated TOML", "pyproject.toml", "[tool.custom]\nrunner='pytest'\n", true},
		{"optional dependencies", "pyproject.toml", "[project.optional-dependencies]\ntest=['pytest>=8']\n", true},
		{"dependency groups", "pyproject.toml", "[dependency-groups]\ntest=[{include-group='base'},'pytest>=8']\n", true},
		{"group name not dependency", "pyproject.toml", "[dependency-groups]\npytest=['requests']\ntest=[{include-group='pytest'}]\n", true},
		{"poetry group", "pyproject.toml", "[tool.poetry.group.test.dependencies]\npytest={version='^8',optional=true}\n", true},
		{"pdm dev group", "pyproject.toml", "[tool.pdm.dev-dependencies]\ntest=['pytest>=8']\n", true},
		{"native pytest TOML", "pyproject.toml", "[tool.pytest]\nminversion='9.0'\n", true},
		{"tox unrelated value", "tox.ini", "[testenv]\ncommands=echo pytest\nsetenv=RUNNER=pytest\n", true},
		{"tox module command", "tox.ini", "[testenv:unit]\ncommands=\n    {envpython} -m pytest {posargs}\n", true},
		{"tox dependency", "tox.ini", "[testenv]\ndeps=\n    pytest>=8\ncommands=python tests.py\n", true},
		{"setup source not executed", "setup.py", "raise RuntimeError('pytest')\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			require.NoError(t, os.WriteFile(filepath.Join(root, tc.file), []byte(tc.contents), 0644))
			found, err := NewPython().Detect(root)
			require.NoError(t, err)
			require.Equal(t, tc.python, found)
			_, err = NewPython().DetectFramework()
			require.NoError(t, err)
		})
	}
}

func TestPythonDefaultFrameworkDoesNotInspectConfiguration(t *testing.T) {
	resetDetectionSettings(t)
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project"), 0644))
	found, err := NewPython().Detect(root)
	require.NoError(t, err)
	require.True(t, found)
	fw, err := NewPython().DetectFramework()
	require.NoError(t, err)
	require.Equal(t, "pytest", fw.Name())
}

func TestPythonTracerDetectionUsesSelectedEnvironment(t *testing.T) {
	executor := &mockCommandExecutor{combinedOutput: []byte("3.0.0\n"), onCombinedOutput: func(name string, args []string, env map[string]string) {
		require.Equal(t, "uv", name)
		require.Equal(t, []string{"run", "python", "-c"}, args[:3])
		require.Equal(t, "import importlib.metadata, sys; print(importlib.metadata.version(sys.argv[1]))", args[3])
	}}
	version, err := (&Python{executor: executor}).DetectTracer(context.Background(), TracerOptions{Command: "uv", Args: []string{"run", "python"}})
	require.NoError(t, err)
	require.Equal(t, "3.0.0", version) // Presence is independent of minimum supported version.
}

func TestPythonInstallUsesSelectedInterpreterAndIsolatedTarget(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {}}}
	installer := &Python{executor: executor}
	path, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Command: "/customer/venv/bin/python"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(directory, "python"), path.Path)
	require.Equal(t, "/customer/venv/bin/python", executor.commands[1].name)
	require.Equal(t, []string{"-m", "pip", "install", "--disable-pip-version-check", "--target", filepath.Join(directory, "python-packages"), "ddtrace"}, executor.commands[1].args)
	contents, err := os.ReadFile(filepath.Join(path.Path, "sitecustomize.py"))
	require.NoError(t, err)
	require.Contains(t, string(contents), filepath.Join(directory, "python-packages"))
	require.Contains(t, string(contents), "sys.path.append(")
	executor = &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {output: []byte("pip unavailable"), err: errors.New("exit 1")}}}
	installer.executor = executor
	_, err = installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Command: "/customer/venv/bin/python"})
	require.ErrorContains(t, err, "pip unavailable")
}
func TestPythonInterpreterUsesRunnerCommand(t *testing.T) {
	for _, test := range []struct {
		command     string
		args        []string
		wantCommand string
		wantPrefix  []string
	}{
		{command: "python3.12", args: []string{"-m", "pytest"}, wantCommand: "python3.12"},
		{command: ".venv/bin/pytest", wantCommand: filepath.Join(".venv", "bin", "python")},
		{command: "uv", args: []string{"run", "pytest"}, wantCommand: "uv", wantPrefix: []string{"run", "python"}},
		{command: "poetry", args: []string{"run", "pytest"}, wantCommand: "poetry", wantPrefix: []string{"run", "python"}},
	} {
		command, prefix := pythonInterpreter(test.command, test.args)
		require.Equal(t, test.wantCommand, command)
		require.Equal(t, test.wantPrefix, prefix)
	}
}

func TestPythonTracerVersions(t *testing.T) {
	for _, tt := range []struct{ version, spec string }{
		{"", "ddtrace"}, {"latest", "ddtrace"}, {"4.15.1", "ddtrace==4.15.1"},
		{"4.16.0rc1", "ddtrace==4.16.0rc1"},
		{"git:abc1234", "ddtrace @ git+https://github.com/DataDog/dd-trace-py.git@abc1234"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("project tracer unavailable")}, {}}}
			installer := NewPython()
			installer.executor = executor
			_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Command: "uv", Args: []string{"run", "pytest"}, Version: tt.version})
			require.NoError(t, err)
			require.Equal(t, "uv", executor.commands[1].name)
			require.Equal(t, []string{"run", "--with", "pip", "python", "-m", "pip"}, executor.commands[1].args[:6])
			require.Equal(t, tt.spec, executor.commands[1].args[len(executor.commands[1].args)-1])
		})
	}
	_, err := NewPython().InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Command: "python", Version: "git:"})
	require.ErrorContains(t, err, "git ref must not be empty")
}

func TestPythonReusesProjectTracer(t *testing.T) {
	for _, version := range []string{"latest", "4.15.1", "git:abc1234"} {
		t.Run(version, func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{output: []byte("3.0.0\n")}}}
			installer := NewPython()
			installer.executor = executor
			directory := t.TempDir()
			result, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: directory, Command: "uv", Args: []string{"run", "pytest"}, Version: version})
			require.NoError(t, err)
			require.Equal(t, TracerInstallation{Project: true}, result)
			require.Len(t, executor.commands, 1)
			require.Equal(t, "uv", executor.commands[0].name)
			require.Equal(t, []string{"run", "python", "-c"}, executor.commands[0].args[:3])
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestPythonProbeFailureAttemptsInstall(t *testing.T) {
	executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("interpreter unavailable")}, {err: errors.New("pip unavailable")}}}
	installer := NewPython()
	installer.executor = executor
	_, err := installer.InstallTestdriveTracer(t.Context(), TracerOptions{Directory: t.TempDir(), Command: "python"})
	require.ErrorContains(t, err, "pip unavailable")
	require.Len(t, executor.commands, 2)
	require.Equal(t, []string{"-m", "pip", "install"}, executor.commands[1].args[:3])
}

func TestPythonUVOptionsApplyToProbeAndInstall(t *testing.T) {
	for _, tc := range []struct {
		args, prefix []string
	}{
		{[]string{"run", "--isolated", "--no-dev", "--python", "3.12", "pytest", "-q"}, []string{"run", "--isolated", "--no-dev", "--python", "3.12", "python"}},
		{[]string{"run", "--package", "pytest", "--group", "tests", "pytest"}, []string{"run", "--package", "pytest", "--group", "tests", "python"}},
		{[]string{"run", "--python=3.12", "--", "pytest"}, []string{"run", "--python=3.12", "--", "python"}},
		{[]string{"run", "--isolated", "-m", "pytest"}, []string{"run", "--isolated", "python"}},
		{[]string{"run", "--no-dev", "python3.12", "-m", "pytest"}, []string{"run", "--no-dev", "python3.12"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			executor := &fakeCommandExecutor{responses: []commandResponse{{err: errors.New("no ddtrace")}, {}}}
			p := &Python{executor: executor}
			directory := t.TempDir()
			_, err := p.InstallTestdriveTracer(t.Context(), TracerOptions{Command: "/tools/uv", Args: tc.args, Directory: directory})
			require.NoError(t, err)
			require.Equal(t, tc.prefix, executor.commands[0].args[:len(tc.prefix)])
			installPrefix := append([]string{"run", "--with", "pip"}, tc.prefix[1:]...)
			require.Equal(t, installPrefix, executor.commands[1].args[:len(installPrefix)])
			require.Contains(t, executor.commands[1].args, filepath.Join(directory, "python-packages"))
		})
	}
}
