// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPrepareRubyEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX executable fixture")
	}
	for _, name := range []string{"rspec", "minitest"} {
		for _, available := range []bool{true, false} {
			t.Run(name+"/"+map[bool]string{true: "available", false: "unavailable"}[available], func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				t.Setenv("PATH", root)
				preloads := "-rbundler/setup -rdatadog/ci/auto_instrument"
				t.Setenv("RUBYOPT", preloads)
				t.Setenv("BUNDLE_GEMFILE", "config/Gemfile.test")
				requireWriteFile(t, "Gemfile", "gem '"+name+"'\n")
				if available {
					script := `#!/bin/sh
[ "$RUBYOPT" = '' ] || exit 2
[ "$BUNDLE_GEMFILE" = 'config/Gemfile.test' ] || exit 2
printf '%s\n' 'customer startup output'
for last; do :; done
printf '%s' '{"executable":"/selected ruby/bin/ruby","version":"3.4.7"}' > "$last"
`
					require.NoError(t, os.WriteFile("ruby", []byte(script), 0755))
				}
				drive, err := Prepare("latest")
				require.NoError(t, err)
				require.Equal(t, "Ruby from PATH", drive.environmentInfo.Source)
				require.Equal(t, "Ruby", drive.environmentInfo.Runtime)
				require.NoDirExists(t, drive.session.Directory())
				var output bytes.Buffer
				drive.Preview(&output)
				require.Contains(t, output.String(), "Environment: Ruby from PATH")
				if available {
					require.Contains(t, output.String(), "Ruby 3.4.7: /selected ruby/bin/ruby")
					drive.platform = &fakeTracer{}
					executor := &fakeTestdriveExecutor{}
					drive.executor = executor
					drive.startIntake = func(string) (localIntake, error) {
						return &fakeIntake{findings: intake.Facts{TestEventCount: 1}}, nil
					}
					require.NoError(t, drive.Run(t.Context(), &output))
					require.True(t, strings.HasPrefix(executor.env["RUBYOPT"], preloads+" "))
					require.NotContains(t, executor.env, "BUNDLE_GEMFILE")
					var diagnostics runDiagnostics
					data, err := os.ReadFile(filepath.Join(drive.session.Directory(), diagnosticsFilename))
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(data, &diagnostics))
					require.Equal(t, drive.environmentInfo, diagnostics.Environment)
					report, err := os.ReadFile(filepath.Join(drive.session.Directory(), reportFilename))
					require.NoError(t, err)
					require.Contains(t, string(report), "Ruby 3.4.7")
					require.Contains(t, string(report), "/selected ruby/bin/ruby")
				} else {
					require.Empty(t, drive.environmentInfo.Executable)
					require.Empty(t, drive.environmentInfo.Version)
				}
				require.Equal(t, preloads, os.Getenv("RUBYOPT"))
				require.Equal(t, "config/Gemfile.test", os.Getenv("BUNDLE_GEMFILE"))
			})
		}
	}
}

func TestPrepareAllSupportedFrameworks(t *testing.T) {
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cucumber", "cypress", "pytest", "rspec", "minitest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			switch name {
			case "pytest":
				requireWriteFile(t, filepath.Join(root, "pyproject.toml"), "[tool.pytest.ini_options]\n")
			case "rspec", "minitest":
				requireWriteFile(t, filepath.Join(root, "Gemfile"), "gem '"+name+"'\n")
			default:
				requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"`+name+`"}}`)
			}
			t.Chdir(root)
			run, err := Prepare("latest")
			require.NoError(t, err)
			var preview bytes.Buffer
			run.Preview(&preview)
			require.Contains(t, preview.String(), displayName(name))
			_, err = os.Stat(filepath.Join(root, ".testoptimization"))
			require.True(t, os.IsNotExist(err), "preview must be read-only")
			// Execution below is simulated; environment prerequisite checks have their own tests.
			run.preflight = nil
			run.nodeVersion = func() string { return "v20.0.0" }
			installer := &fakeTracer{preloadPath: filepath.Join(root, "isolated")}
			run.platform = installer
			run.projectTracer = ""
			run.projectTracer = ""
			executor := &fakeTestdriveExecutor{}
			run.executor = executor
			run.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Facts{TestEventCount: 1, TestCount: 1}}, nil
			}
			var output bytes.Buffer
			require.NoError(t, run.Run(t.Context(), &output))
			require.Contains(t, output.String(), displayName(name)+": Passed")
			require.Contains(t, output.String(), "Tests with coverage: 0 / 1")
			require.Equal(t, "ddtest-testdrive", executor.env["DD_API_KEY"])
			if name == "cucumber" {
				require.Equal(t, "true", executor.env["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"])
			}
			switch run.language {
			case "javascript":
				require.NotContains(t, executor.env["NODE_OPTIONS"], "--import")
			case "python":
				require.NotEmpty(t, executor.env["PYTHONPATH"])
				require.Contains(t, executor.env["PYTEST_ADDOPTS"], "--ddtrace")
				require.NotContains(t, executor.env, "NODE_OPTIONS")
			case "ruby":
				var diagnostics runDiagnostics
				data, err := os.ReadFile(filepath.Join(run.session.Directory(), diagnosticsFilename))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &diagnostics))
				require.Equal(t, run.environmentInfo, diagnostics.Environment)
				require.Equal(t, "Ruby from PATH", diagnostics.Environment.Source)
				require.NotContains(t, executor.env, "BUNDLE_GEMFILE")
				require.Contains(t, preview.String(), "Bundler updates Gemfile and Gemfile.lock.")
				require.NotContains(t, preview.String(), "It will not change")
				require.Contains(t, output.String(), "datadog-ci · installed in project")
				require.Contains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
				require.NotContains(t, executor.env, "NODE_OPTIONS")
			}
		})
	}
}

func TestJavaScriptEnvironmentImportsExistingRegister(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "")
	packageDir := filepath.Join(t.TempDir(), "dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(packageDir, "ci"), 0755))
	register := filepath.Join(packageDir, "register.js")
	require.NoError(t, os.WriteFile(register, nil, 0644))
	run := &Testdrive{nodeVersion: func() string { return "v22.0.0" }}
	preload := filepath.Join(packageDir, "ci", "init.js")
	require.Contains(t, run.javascriptEnvironment(preload)["NODE_OPTIONS"], " --import ")
	require.NoError(t, os.Remove(register))
	require.NotContains(t, run.javascriptEnvironment(preload)["NODE_OPTIONS"], "--import")
}

func TestSupportsNodeRegister(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "")
	for version, want := range map[string]bool{
		"v16.20.0": false,
		"v18.17.1": false,
		"v18.18.0": false,
		"v18.19.0": true,
		"v19.9.0":  false,
		"v20.0.0":  false,
		"v20.5.0":  false,
		"v20.6.0":  true,
		"v22.0.0":  true,
		"v24.0.0":  true,
		"invalid":  false,
	} {
		t.Run(version, func(t *testing.T) {
			require.Equal(t, want, supportsNodeRegister(version))
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "register.js"), "")
			run := &Testdrive{nodeVersion: func() string { return version }}
			require.Equal(t, want, strings.Contains(run.javascriptEnvironment(filepath.Join(root, "ci", "init.js"))["NODE_OPTIONS"], " --import "))
		})
	}
}

func TestPrepareRequiresSelectionForMultipleFrameworks(t *testing.T) {
	previous := settings.Get().Framework
	t.Cleanup(func() { settings.Get().Framework = previous })
	settings.Get().Framework = ""
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run","e2e":"playwright test"}}`)
	t.Chdir(root)
	_, err := Prepare("latest")
	require.ErrorContains(t, err, "--framework")
	settings.Get().Framework = "vitest"
	run, err := Prepare("latest")
	require.NoError(t, err)
	require.Equal(t, "vitest", run.framework.Name())
	settings.Get().Framework = "unsupported"
	_, err = Prepare("latest")
	require.ErrorContains(t, err, "unsupported framework")
}

func TestLanguageEnvironmentsPreserveCustomerOptions(t *testing.T) {
	t.Setenv("PYTHONPATH", "/customer/modules")
	t.Setenv("PYTEST_ADDOPTS", "-q --disable-plugin-autoload")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	t.Setenv("RUBYOPT", "-W0")
	python := (&Testdrive{language: "python"}).environment("/session/python", "http://127.0.0.1:1234", "session")
	require.Equal(t, "/session/python"+string(os.PathListSeparator)+"/customer/modules", python["PYTHONPATH"])
	require.Equal(t, "--ddtrace -q --disable-plugin-autoload", python["PYTEST_ADDOPTS"])
	require.NotContains(t, python, "PYTEST_DISABLE_PLUGIN_AUTOLOAD")
	ruby := (&Testdrive{language: "ruby"}).environment("/session/Gemfile", "http://127.0.0.1:1234", "session")
	require.True(t, strings.HasPrefix(ruby["RUBYOPT"], "-W0 "))
	require.NotContains(t, ruby, "BUNDLE_PATH") // Inherit project Bundler configuration.
}

func TestCypressTestdrivePreservesCommand(t *testing.T) {
	t.Setenv("NODE_OPTIONS", "--conditions=development")
	for _, command := range [][]string{
		{"npx", "cypress", "run", "-Papps/web", "-Ccustom.ts", "--"},
		{"npm", "run", "test:cypress", "--", "--component"},
		{"node", "-C", "development", "node_modules/cypress/bin/cypress", "run"},
	} {
		t.Run(command[0], func(t *testing.T) {
			root := t.TempDir()
			requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run"}}`)
			t.Chdir(root)
			run, err := Prepare("latest")
			require.NoError(t, err)
			run.command, run.args = command[0], command[1:]
			// The selected tracer can live outside the project's module tree.
			run.projectTracer = filepath.Join(t.TempDir(), "dd-trace", "ci", "init.js")
			run.nodeVersion = func() string { return "v20.20.0" }
			executor := &fakeTestdriveExecutor{}
			run.executor = executor
			run.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{findings: intake.Facts{TestEventCount: 1, TestCount: 1}}, nil
			}
			var output bytes.Buffer
			require.NoError(t, run.Run(t.Context(), &output))
			require.Equal(t, command[0], executor.command)
			require.Equal(t, command[1:], executor.args)
			require.Contains(t, executor.env["NODE_OPTIONS"], run.projectTracer)
			require.Contains(t, executor.env["NODE_OPTIONS"], "--conditions=development")
			require.NoFileExists(t, filepath.Join(run.session.Directory(), "cypress.config.ts"))
		})
	}
}

func TestPythonProjectTracerPreservesImportEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "/project/helpers")
	t.Setenv("PYTEST_ADDOPTS", "-v -p ddtrace.testing.internal.pytest.entry_point")
	t.Setenv("PYTEST_DISABLE_PLUGIN_AUTOLOAD", "1")
	drive := &Testdrive{language: "python"}
	env := drive.environment("", "http://127.0.0.1:1234", "session")
	if _, changed := env["PYTHONPATH"]; changed {
		t.Fatal("project PYTHONPATH overridden", env)
	}
	if env["PYTEST_ADDOPTS"] != "--ddtrace -v -p ddtrace.testing.internal.pytest.entry_point" {
		t.Fatal(env)
	}
}

func TestRubyProjectTracerPreservesBundleEnvironment(t *testing.T) {
	drive := &Testdrive{language: "ruby"}
	env := drive.environment("", "http://127.0.0.1:1234", "session")
	for _, key := range []string{"BUNDLE_GEMFILE", "BUNDLE_PATH", "BUNDLE_APP_CONFIG", "BUNDLE_FROZEN", "BUNDLE_WITHOUT"} {
		if _, changed := env[key]; changed {
			t.Fatal("project Bundler setting overridden", key)
		}
	}
	if !strings.Contains(env["RUBYOPT"], "-rdatadog/ci/auto_instrument") {
		t.Fatal(env)
	}
}
