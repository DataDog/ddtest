// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testdrive/intake"
	"github.com/stretchr/testify/require"
)

func TestPrepareAllSupportedFrameworks(t *testing.T) {
	for _, name := range []string{"jest", "mocha", "vitest", "playwright", "cucumber", "cypress"} {
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
				require.NotContains(t, executor.env, "BUNDLE_GEMFILE")
				require.Contains(t, preview.String(), "Bundler updates the project Gemfile and lockfile")
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
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run --project apps/web -C custom.ts","test:cypress":"cypress run --project apps/web -C custom.ts"}}`)
	requireWriteFile(t, filepath.Join(project, "custom.ts"), `export default {}`)
	for _, command := range []string{"npm", "yarn", "pnpm", "bun"} {
		for _, args := range [][]string{{"test", "--"}, {"run", "test:cypress", "--"}} {
			t.Run(command+"/"+args[0], func(t *testing.T) {
				got, err := prepareCypress(root, session, "/tracer/ci/init.js", command, args)
				require.NoError(t, err)
				require.Equal(t, append(args, "--config-file", filepath.Join(session, "cypress.config.ts")), got)
				wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
				require.NoError(t, err)
				require.Contains(t, string(wrapper), filepath.Join(project, "custom.ts"))
				require.Contains(t, string(wrapper), `"root":"`+project+`"`)
			})
		}
	}
	for _, command := range []string{"yarn", "pnpm", "bun"} {
		args := cypressInspectionArgs(root, command, []string{"test:cypress", "--project", "override", "-C", "other.ts"})
		require.Equal(t, "override", optionValue(args, "--project", "-P"))
		require.Equal(t, "other.ts", optionValue(args, "--config-file", "-C"))
	}
}

func TestCypressWrapperSupportsConfigFileFalseAndDefaultE2E(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "cypress.config.js"), `throw new Error('disabled config must not load')`)
	args, err := prepareCypress(root, session, "/tracer/ci/init.js", "cypress", []string{"run", "-C", "false"})
	require.NoError(t, err)
	require.Equal(t, []string{"run", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
	require.NoError(t, err)
	require.Contains(t, string(wrapper), `types.add(options.testingType)`)
	require.Contains(t, string(wrapper), `const originalImport = {}`)
}

func TestCypressWrapperPreservesProjectInitializationAndHooks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute the Cypress wrapper")
	}
	root := t.TempDir()
	session := t.TempDir()
	tracer := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci", "cypress"), 0755))
	requireWriteFile(t, filepath.Join(root, "marker.txt"), "project")
	requireWriteFile(t, filepath.Join(root, "custom.cjs"), `
const assert = require('node:assert/strict');
assert.equal(require('node:fs').readFileSync('marker.txt', 'utf8'), 'project');
module.exports = {e2e: {async setupNodeEvents(on, config) {
  on('task', {project: () => 'project'});
  on('after:run', async () => { require('node:fs').writeFileSync('after-run.txt', 'project'); return 'project result'; });
  return {env: {...config.env, project: true}};
}}};`)
	requireWriteFile(t, filepath.Join(tracer, "ci", "cypress", "plugin.js"), `
const assert = require('node:assert/strict');
module.exports = async (on, config) => {
  assert.equal(config.projectRoot, process.cwd());
  assert.equal(config.env.project, true);
  assert.equal(config.env.original, true);
  on('task', {datadog: () => 'datadog'});
  on('after:run', async () => { assert.equal(require('node:fs').readFileSync('after-run.txt', 'utf8'), 'project'); });
  return config;
};`)
	for _, support := range []string{"support.cjs", ""} {
		t.Run("support="+support, func(t *testing.T) {
			_, err := prepareCypress(root, session, filepath.Join(tracer, "ci", "init.js"), "cypress", []string{"run", "-C", "custom.cjs"})
			require.NoError(t, err)
			wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
			require.NoError(t, err)
			// The generated wrapper is also valid JavaScript. Execute its imports
			// and lifecycle with Node; real Cypress QA covers TypeScript loading.
			requireWriteFile(t, filepath.Join(session, "wrapper.mjs"), string(wrapper))
			requireWriteFile(t, filepath.Join(session, "check.mjs"), `
import assert from 'node:assert/strict';
import fs from 'node:fs';
import wrapped from './wrapper.mjs';
const handlers = new Map();
const config = await wrapped;
const result = await config.e2e.setupNodeEvents((name, handler) => {
  assert.equal(handlers.has(name), false, 'each event must be registered once');
  handlers.set(name, handler);
}, {projectRoot: process.cwd(), supportFile: process.argv[2] || false, env: {original: true}});
assert.equal(handlers.get('task').project(), 'project');
assert.equal(handlers.get('task').datadog(), 'datadog');
assert.equal(await handlers.get('after:run')(), 'project result');
const support = fs.readFileSync(result.supportFile, 'utf8');
assert.ok(support.includes('ci/cypress/support'));
assert.equal(support.includes('support.cjs'), Boolean(process.argv[2]));
`)
			command := exec.CommandContext(t.Context(), "node", filepath.Join(session, "check.mjs"), support)
			command.Dir = session
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}
