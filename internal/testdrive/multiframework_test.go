// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"github.com/DataDog/ddtest/internal/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

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
			if name == "cypress" {
				return
			} // Browser wrapper has its own real-run test.
			run.nodeVersion = func() string { return "v20.0.0" }
			installer := &fakeTracer{preloadPath: filepath.Join(root, "isolated")}
			run.platform = installer
			run.projectTracer = ""
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
				require.Equal(t, "false", executor.env["DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED"])
			}
			switch run.language {
			case "javascript":
				require.Contains(t, executor.env["NODE_OPTIONS"], "--import")
			case "python":
				require.NotEmpty(t, executor.env["PYTHONPATH"])
				require.Contains(t, executor.env["PYTEST_ADDOPTS"], "--ddtrace")
				require.NotContains(t, executor.env, "NODE_OPTIONS")
			case "ruby":
				require.NotContains(t, executor.env, "BUNDLE_GEMFILE")
				require.Contains(t, preview.String(), "Bundler updates the project Gemfile and lockfile")
				require.Contains(t, output.String(), "datadog-ci · installed in project")
				require.Contains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
				require.NotContains(t, executor.env, "NODE_OPTIONS")
			}
		})
	}
}

func TestSupportsNodeImport(t *testing.T) {
	for version, want := range map[string]bool{
		"v18.17.1": false,
		"v18.18.0": true,
		"v20.0.0":  true,
		"invalid":  false,
	} {
		if got := supportsNodeImport(version); got != want {
			t.Errorf("supportsNodeImport(%q) = %v, want %v", version, got, want)
		}
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
	t.Setenv("PYTEST_ADDOPTS", "-q")
	t.Setenv("RUBYOPT", "-W0")
	python := (&Testdrive{language: "python"}).environment("/session/python", "http://127.0.0.1:1234", "session")
	require.Equal(t, "/session/python"+string(os.PathListSeparator)+"/customer/modules", python["PYTHONPATH"])
	require.Equal(t, "-q --ddtrace", python["PYTEST_ADDOPTS"])
	ruby := (&Testdrive{language: "ruby"}).environment("/session/Gemfile", "http://127.0.0.1:1234", "session")
	require.True(t, strings.HasPrefix(ruby["RUBYOPT"], "-W0 "))
	require.NotContains(t, ruby, "BUNDLE_PATH") // Inherit project Bundler configuration.
}

func TestCypressWrapperUsesExplicitConfigWithoutEditingIt(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	config := `module.exports={e2e:{supportFile:false}}`
	requireWriteFile(t, filepath.Join(root, "custom.cjs"), config)
	args, err := prepareCypress(root, session, "/tracer/ci/init.js", "cypress", []string{"run", "--config-file=custom.cjs", "--browser", "chrome"})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--browser", "chrome", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
	contents, err := os.ReadFile(filepath.Join(root, "custom.cjs"))
	require.NoError(t, err)
	require.Equal(t, config, string(contents))
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
	require.NoError(t, err)
	require.Contains(t, string(wrapper), filepath.Join(root, "custom.cjs"))
	_, err = prepareCypress(root, session, "/tracer/ci/init.js", "cypress", []string{"--config-file"})
	require.ErrorContains(t, err, "requires a path")
}

func TestCypressWrapperReadsPackageScriptProjectAndConfig(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	project := filepath.Join(root, "apps", "web")
	require.NoError(t, os.MkdirAll(project, 0755))
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run --project apps/web -C custom.ts"}}`)
	requireWriteFile(t, filepath.Join(project, "custom.ts"), `export default {}`)

	args, err := prepareCypress(root, session, "/tracer/ci/init.js", "npm", []string{"test", "--"})
	require.NoError(t, err)
	require.Equal(t, []string{"test", "--", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
	require.NoError(t, err)
	require.Contains(t, string(wrapper), filepath.Join(project, "custom.ts"))
	require.Contains(t, string(wrapper), `"root":"`+project+`"`)
}

func TestCypressWrapperSupportsConfigFileFalseAndDefaultE2E(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	args, err := prepareCypress(root, session, "/tracer/ci/init.js", "cypress", []string{"run", "-C", "false"})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
	require.NoError(t, err)
	require.Contains(t, string(wrapper), `types.add(options.testingType)`)
	require.Contains(t, string(wrapper), `const originalImport = {}`)
}

func TestPythonProjectTracerPreservesImportEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "/project/helpers")
	t.Setenv("PYTEST_ADDOPTS", "-v")
	drive := &Testdrive{language: "python"}
	env := drive.environment("", "http://127.0.0.1:1234", "session")
	if _, changed := env["PYTHONPATH"]; changed {
		t.Fatal("project PYTHONPATH overridden", env)
	}
	if env["PYTEST_ADDOPTS"] != "-v --ddtrace" {
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
