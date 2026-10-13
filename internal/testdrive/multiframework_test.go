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
	"strings"
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
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run --project apps/web -C custom.ts","test:cypress":"cypress run -Papps/web -Ccustom.ts"}}`)
	requireWriteFile(t, filepath.Join(project, "custom.ts"), `export default {}`)
	for _, command := range []string{"npm", "yarn", "pnpm", "bun"} {
		for _, args := range [][]string{{"test", "--"}, {"run", "test:cypress", "--"}} {
			if command == "pnpm" {
				args = args[:len(args)-1] // pnpm forwards -- to the script.
			}
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
		args, err := cypressInspectionArgs(root, command, []string{"test:cypress", "--project", "override", "-C", "other.ts"})
		require.NoError(t, err)
		require.Equal(t, "override", optionValue(args, "--project", "-P"))
		require.Equal(t, "other.ts", optionValue(args, "--config-file", "-C"))
	}
}

func TestCypressWrapperReadsAttachedShortOptions(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	for _, config := range []string{"custom.ts", "false"} {
		t.Run(config, func(t *testing.T) {
			args, err := prepareCypress(root, session, "/tracer/ci/init.js", "cypress", []string{"run", "-Papps/web", "-C" + config})
			require.NoError(t, err)
			require.Equal(t, []string{"run", "-Papps/web", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
			wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
			require.NoError(t, err)
			require.Contains(t, string(wrapper), `"root":"`+filepath.Join(root, "apps", "web")+`"`)
			if config == "false" {
				require.Contains(t, string(wrapper), `const originalImport = {}`)
			} else {
				require.Contains(t, string(wrapper), filepath.Join(root, "apps", "web", config))
			}
		})
	}
	require.Equal(t, "last", optionValue([]string{"-Pfirst", "--project=second", "-P", "third", "-Plast"}, "--project", "-P"))
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

func TestCypressWrapperForwardsOptionsThroughAbsoluteNPM(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run"}}`)
	for _, command := range []string{"npm", "/opt/node/bin/npm", "/opt/node/bin/npm.cmd", "/opt/node/bin/npm.ps1"} {
		t.Run(command, func(t *testing.T) {
			args, err := prepareCypress(root, session, "/tracer/ci/init.js", command, []string{"test"})
			require.NoError(t, err)
			require.Equal(t, []string{"test", "--", "--config-file", filepath.Join(session, "cypress.config.ts")}, args)
		})
	}
}

func TestCypressWrapperInsertsOptionsBeforeFrameworkSeparator(t *testing.T) {
	root := t.TempDir()
	session := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run"}}`)
	for _, tc := range []struct {
		command string
		args    []string
		prefix  []string
	}{
		{"cypress", []string{"run", "--"}, []string{"run"}},
		{"node", []string{"node_modules/cypress/bin/cypress", "run", "--"}, []string{"node_modules/cypress/bin/cypress", "run"}},
		{"npx", []string{"--", "cypress", "run", "--"}, []string{"--", "cypress", "run"}},
		{"npx", []string{"--package", "cypress", "--", "cypress", "run", "--"}, []string{"--package", "cypress", "--", "cypress", "run"}},
		{"npm", []string{"exec", "--", "cypress", "run", "--"}, []string{"exec", "--", "cypress", "run"}},
		{"npm", []string{"test", "--", "--"}, []string{"test", "--"}},
		{"yarn", []string{"test", "--", "--"}, []string{"test", "--"}},
		{"pnpm", []string{"test", "--"}, []string{"test"}},
		{"bun", []string{"run", "test", "--", "--"}, []string{"run", "test", "--"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			// These tokens are positional after Cypress's separator, not options.
			positional := []string{"--config-file", "ignored.cjs", "--project", "ignored", "--component"}
			args, err := prepareCypress(root, session, "/tracer/ci/init.js", tc.command, append(tc.args, positional...))
			require.NoError(t, err)
			want := append(tc.prefix, "--config-file", filepath.Join(session, "cypress.config.ts"), "--")
			require.Equal(t, append(want, positional...), args)
			wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
			require.NoError(t, err)
			require.NotContains(t, string(wrapper), "ignored")
			require.Contains(t, string(wrapper), `"testingType":"e2e"`)
		})
	}
	requireWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"test":"cypress run --"}}`)
	_, err := prepareCypress(root, session, "/tracer/ci/init.js", "npm", []string{"test"})
	require.ErrorContains(t, err, "use --command with a direct Cypress invocation")
}

func TestCypressWrapperPreservesProjectInitializationAndHooks(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute the Cypress wrapper")
	}
	root := t.TempDir()
	session := t.TempDir()
	tracer := filepath.Join(session, "node_modules", "dd-trace")
	require.NoError(t, os.MkdirAll(filepath.Join(tracer, "ci", "cypress"), 0755))
	requireWriteFile(t, filepath.Join(root, "marker.txt"), "project")
	requireWriteFile(t, filepath.Join(root, "custom.cjs"), `
const assert = require('node:assert/strict');
assert.equal(require('node:fs').readFileSync('marker.txt', 'utf8'), 'project');
module.exports = {e2e: {async setupNodeEvents(on, config) {
  on('task', {project: () => 'replaced', retained: () => 'retained'});
  on('task', {project: () => 'project'});
  on('after:run', () => { throw new Error('replaced hook must not run'); });
  on('after:run', async () => { require('node:fs').writeFileSync('after-run.txt', 'project'); return 'project result'; });
  on('before:run', () => { throw new Error('replaced project-only hook must not run'); });
  on('before:run', () => 'last project-only hook');
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
	requireWriteFile(t, filepath.Join(tracer, "ci", "cypress", "support.js"), `globalThis.supportOrder = ['instrumentation'];`)
	requireWriteFile(t, filepath.Join(root, "support.cjs"), `globalThis.supportOrder.push('project');`)
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
import {pathToFileURL} from 'node:url';
import wrapped from './wrapper.mjs';
const handlers = new Map();
const config = wrapped;
assert.equal(typeof config.then, 'undefined', 'Cypress needs a synchronous config export');
const result = await config.e2e.setupNodeEvents((name, handler) => {
  assert.equal(handlers.has(name), false, 'each event must be registered once');
  handlers.set(name, handler);
}, {projectRoot: process.cwd(), supportFile: process.argv[2] || false, env: {original: true}});
assert.equal(handlers.get('task').project(), 'project');
assert.equal(handlers.get('task').retained(), 'retained');
assert.equal(handlers.get('task').datadog(), 'datadog');
assert.equal(await handlers.get('after:run')(), 'project result');
assert.equal(await handlers.get('before:run')(), 'last project-only hook');
const support = fs.readFileSync(result.supportFile, 'utf8');
assert.ok(support.includes('ci/cypress/support'));
assert.equal(support.includes('support.cjs'), Boolean(process.argv[2]));
fs.writeFileSync(result.supportFile + '.mjs', support);
await import(pathToFileURL(result.supportFile + '.mjs').href);
assert.deepEqual(globalThis.supportOrder, process.argv[2] ? ['instrumentation', 'project'] : ['instrumentation']);
`)
			command := exec.CommandContext(t.Context(), "node", filepath.Join(session, "check.mjs"), support)
			command.Dir = session
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
		})
	}
}

func TestCypressWrapperImportsEscapedPaths(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to execute the Cypress wrapper")
	}
	suffix := " # space%"
	if filepath.Separator != '\\' {
		suffix += "?" // Question marks are not valid Windows filename characters.
	}
	root := filepath.Join(t.TempDir(), "project"+suffix)
	session := filepath.Join(t.TempDir(), "session"+suffix)
	tracer := filepath.Join(t.TempDir(), "tracer"+suffix)
	for _, directory := range []string{root, session, filepath.Join(tracer, "ci", "cypress")} {
		require.NoError(t, os.MkdirAll(directory, 0755))
	}
	requireWriteFile(t, filepath.Join(filepath.Dir(session), "package.json"), `{"type":"module"}`)
	requireWriteFile(t, filepath.Join(root, "custom.cjs"), `module.exports = {e2e: {setupNodeEvents(on, config) {return {env: {project: true}};}}};`)
	requireWriteFile(t, filepath.Join(tracer, "ci", "cypress", "plugin.js"), `module.exports = (on, config) => ({...config, env: {...config.env, instrumented: true}});`)
	_, err := prepareCypress(root, session, filepath.Join(tracer, "ci", "init.js"), "cypress", []string{"run", "-C", "custom.cjs"})
	require.NoError(t, err)
	wrapper, err := os.ReadFile(filepath.Join(session, "cypress.config.ts"))
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(session, "wrapper.mjs"), string(wrapper))
	requireWriteFile(t, filepath.Join(session, "check.mjs"), `
import assert from 'node:assert/strict';
import config from './wrapper.mjs';
const resolved = await config.e2e.setupNodeEvents(() => {}, {supportFile: false});
assert.deepEqual(resolved.env, {project: true, instrumented: true});
`)
	command := exec.CommandContext(t.Context(), "node", filepath.Join(session, "check.mjs"))
	command.Dir = session
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)

	// Older Cypress versions compile TypeScript imports to require(). Those
	// imports must remain filesystem paths in a CommonJS package scope.
	requireWriteFile(t, filepath.Join(session, "package.json"), `{"type":"commonjs"}`)
	_, err = prepareCypress(root, session, filepath.Join(tracer, "ci", "init.js"), "cypress", []string{"run", "-C", "custom.cjs"})
	require.NoError(t, err)
	requireWriteFile(t, filepath.Join(session, "check.cjs"), `
const assert = require('node:assert/strict');
const fs = require('node:fs');
const source = fs.readFileSync('cypress.config.ts', 'utf8');
const imports = [...source.matchAll(/^import (?:.+ from )?(".+");$/gm)];
assert.equal(imports.length, 3);
for (const [, specifier] of imports) require(JSON.parse(specifier));
`)
	command = exec.CommandContext(t.Context(), "node", filepath.Join(session, "check.cjs"))
	command.Dir = session
	output, err = command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
