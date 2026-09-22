// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
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
			run, err := Prepare(root)
			require.NoError(t, err)
			var preview bytes.Buffer
			run.Preview(&preview)
			require.Contains(t, preview.String(), displayName(name))
			_, err = os.Stat(filepath.Join(root, ".testoptimization"))
			require.True(t, os.IsNotExist(err), "preview must be read-only")
			if name == "cypress" {
				return
			} // Browser wrapper has its own real-run test.
			run.tracer = &fakeTracer{preloadPath: filepath.Join(root, "isolated")}
			executor := &fakeTestdriveExecutor{}
			run.executor = executor
			run.startIntake = func(string) (localIntake, error) {
				return &fakeIntake{url: "http://127.0.0.1:1234", findings: intake.Findings{TestEventCount: 1, TestCount: 1}}, nil
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
				require.NotEmpty(t, executor.env["BUNDLE_GEMFILE"])
				require.Contains(t, executor.env["RUBYOPT"], "datadog/ci/auto_instrument")
				require.NotContains(t, executor.env, "NODE_OPTIONS")
			}
		})
	}
}

func TestPrepareRequiresSelectionForMultipleFrameworks(t *testing.T) {
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"vitest run","e2e":"playwright test"}}`)
	_, err := Prepare(root)
	require.ErrorContains(t, err, "--framework")
	run, err := PrepareWithFramework(root, "vitest")
	require.NoError(t, err)
	require.Equal(t, "vitest", run.framework.Name())
	_, err = PrepareWithFramework(root, "pytest")
	require.ErrorContains(t, err, "could not detect")
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
	require.Equal(t, "/session/gems", ruby["BUNDLE_PATH"])
}

func TestCypressWrapperUsesExplicitConfigWithoutEditingIt(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	config := `module.exports={e2e:{supportFile:false}}`
	requireWriteFile(t, filepath.Join(root, "custom.cjs"), config)
	args, err := prepareCypress(root, session, "/tracer/ci/init.js", []string{"run", "--config-file=custom.cjs", "--browser", "chrome"})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--browser", "chrome", "--config-file", filepath.Join(session, "cypress.config.cjs")}, args)
	contents, err := os.ReadFile(filepath.Join(root, "custom.cjs"))
	require.NoError(t, err)
	require.Equal(t, config, string(contents))
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.cjs"))
	require.NoError(t, err)
	require.Contains(t, string(wrapper), filepath.Join(root, "custom.cjs"))
	_, err = prepareCypress(root, session, "/tracer/ci/init.js", []string{"--config-file"})
	require.ErrorContains(t, err, "requires a path")
}
