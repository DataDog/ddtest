// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These small real-runner tests exercise the public coordinator, not adapter
// mocks. CI runs each JavaScript framework on one Node version with local intake.
func TestPublicFrameworkTestdrives(t *testing.T) {
	if os.Getenv("DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST") == "" {
		t.Skip("set DDTEST_RUN_FRAMEWORK_INTEGRATION_TEST=1")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 18*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	integrationCommand(t, ctx, "../..", nil, "go", "build", "-o", binary, ".")
	fixtures := []struct {
		name, manifest string
		files          map[string]string
	}{
		{"jest", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`, map[string]string{"one.test.js": `test('adds',()=>expect(require('./src/add.cjs')(1,1)).toBe(2));`}},
		{"vitest", `{"type":"module","scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.6","@vitest/coverage-v8":"4.1.6"}}`, map[string]string{"one.test.js": `import {test,expect} from 'vitest'; import add from './src/add.cjs'; test('adds',()=>expect(add(1,1)).toBe(2));`}},
		{"mocha", `{"scripts":{"test":"nyc -- mocha"},"devDependencies":{"mocha":"11.7.5","nyc":"17.1.0"}}`, map[string]string{"test/one.js": `const assert=require('node:assert'); describe('addition',()=>{it('adds',()=>assert.equal(require('../src/add.cjs')(1,1),2));});`}},
		{"playwright", `{"scripts":{"test":"playwright test"},"devDependencies":{"@playwright/test":"1.55.1"}}`, map[string]string{"one.spec.js": `const {test,expect}=require('@playwright/test'); test('renders',async({page})=>{await page.setContent('<h1>hello</h1>');await expect(page.locator('h1')).toHaveText('hello');});`}},
		{"cucumber", `{"scripts":{"test":"cucumber-js"},"devDependencies":{"@cucumber/cucumber":"12.2.0","nyc":"17.1.0"}}`, map[string]string{"features/one.feature": "Feature: Arithmetic\n  Scenario: Add\n    Given addition works\n", "features/step_definitions/one.js": `const {Given}=require('@cucumber/cucumber'); Given('addition works',()=>require('node:assert').equal(require('../../src/add.cjs')(1,1),2));`}},
		{"cypress", `{"scripts":{"test":"cypress run"},"devDependencies":{"cypress":"15.1.0"}}`, map[string]string{
			"src/browser.js":        `window.add=(a,b)=>a+b;`,
			"cypress.config.js":     `const fs=require('node:fs'); const path=require('node:path'); module.exports=require('cypress').defineConfig({e2e:{supportFile:false,async setupNodeEvents(on,config){const source=path.resolve('src/browser.js');const code=fs.readFileSync(source,'utf8');const server=require('node:http').createServer((req,res)=>{res.setHeader('content-type','text/html');res.end('<script>'+code+'</script>');});on('after:run',()=>new Promise(resolve=>server.close(resolve)));await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));config.baseUrl='http://127.0.0.1:'+server.address().port;return config;}}});`,
			"cypress/e2e/one.cy.js": `it('adds in the application',()=>{cy.visit('/');cy.window().then(win=>expect(win.add(1,1)).to.equal(2));});`,
		}},
		{"webdriverio", `{"scripts":{"test":"wdio run wdio.conf.cjs"},"devDependencies":{"@wdio/cli":"9.20.0","@wdio/local-runner":"9.20.0","@wdio/mocha-framework":"9.20.0","@wdio/spec-reporter":"9.20.0"}}`, map[string]string{
			"wdio.conf.cjs": `exports.config={
 runner:'local',
 specs:['./test/*.js'],maxInstances:1,framework:'mocha',reporters:['spec'],capabilities:[{browserName:'chrome','goog:chromeOptions':{binary:process.env.DDTEST_CHROME_BINARY,args:['--headless=new','--no-sandbox','--disable-dev-shm-usage']}}]};`,
			"test/one.js": `describe('browser',()=>{it('renders',async()=>{await browser.url('data:text/html,<h1>hello</h1>');if(await $('h1').getText()!=='hello')throw new Error('missing heading');});});`,
		}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project space")
			require.NoError(t, os.MkdirAll(root, 0755))
			root, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)
			retainIntegrationReports(t, root, fixture.name)
			integrationCommand(t, ctx, root, nil, "git", "init", "-q")
			integrationFile(t, root, ".gitignore", "node_modules/\n/.testoptimization/\n/coverage/\n/.nyc_output/\n")
			integrationFile(t, root, "src/add.cjs", "module.exports=(a,b)=>a+b;\n")
			for name, contents := range fixture.files {
				integrationFile(t, root, name, contents)
			}
			var pkg map[string]any
			require.NoError(t, json.Unmarshal([]byte(fixture.manifest), &pkg))
			pkg["name"], pkg["version"], pkg["files"] = "ddtest-integration-fixture", "1.0.0", []string{"src"}
			manifest, err := json.Marshal(pkg)
			require.NoError(t, err)
			integrationFile(t, root, "package.json", string(manifest))
			// Simulate this fixture's CI checkout even locally. Copied configurations
			// must rebase it without disabling provider detection to pass.
			env := []string{"CYPRESS_CACHE_FOLDER=" + filepath.Join(t.TempDir(), "cypress"), "PLAYWRIGHT_BROWSERS_PATH=" + filepath.Join(t.TempDir(), "playwright"), "GITHUB_ACTIONS=true", "GITHUB_WORKSPACE=" + root}
			integrationCommand(t, ctx, root, env, "npm", "install", "--no-audit", "--no-fund")
			if fixture.name == "playwright" {
				integrationCommand(t, ctx, root, env, "npx", "--no-install", "playwright", "install", "chromium")
			}
			node := strings.TrimSpace(integrationCommand(t, ctx, root, env, "node", "--version"))
			integrationFile(t, root, ".github/workflows/test.yml", "name: tests\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: '"+strings.TrimPrefix(node, "v")+"'\n      - run: npm ci\n      - run: npm test\n")
			args := []string{"onboard", "--framework", fixture.name, "--apply", "--yes", "--format", "json"}
			if fixture.name == "cypress" {
				// Cypress needs application/config hooks; the automatic editor deliberately
				// returns a source-integration gap. Exercise the public execution entry
				// point with an explicitly configured CI fixture, not a guessed editor.
				version := strings.TrimSpace(integrationCommand(t, ctx, root, env, "npm", "view", "dd-trace", "version"))
				integrationFile(t, root, ".github/workflows/test.yml", "name: tests\non: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/setup-node@v4\n        with:\n          node-version: '"+strings.TrimPrefix(node, "v")+"'\n      - uses: datadog/test-visibility-github-action@v3\n        with:\n          languages: js\n          js-tracer-version: '"+version+"'\n      - run: npm test\n        env:\n          NODE_OPTIONS: -r ${{ env.DD_TRACE_PACKAGE }}\n")
				args = []string{"testdrive", "--framework", "cypress", "--all", "--agent", "--yes", "--tracer-version", version, "--format", "json"}
			}
			output := integrationCommand(t, ctx, root, env, binary, args...)
			var result struct {
				Status string `json:"status"`
			}
			lines := strings.Split(strings.TrimSpace(output), "\n")
			require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &result), output)
			require.Equal(t, "complete_locally", result.Status, output)
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
			require.NoError(t, err)
			var report struct {
				Summary       struct{ Status string }
				Compatibility struct{ Status string }
				Cleanup       struct{ Status string }
				LastExecution struct{ Current bool } `json:"last_execution"`
				Features      []struct{ Name, Status string }
				Runs          []struct {
					Name             string
					Instrumented     bool
					ExecutionContext string                       `json:"execution_context"`
					TestCounts       struct{ Passed, Failed int } `json:"test_counts"`
					TestEventCount   int                          `json:"test_event_count"`
					Coverage         *struct{ Status string }
				}
			}
			require.NoError(t, json.Unmarshal(data, &report))
			require.Equal(t, "COMPLETE LOCALLY", report.Summary.Status)
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Equal(t, "passed", report.Cleanup.Status)
			require.True(t, report.LastExecution.Current)
			expected := map[string]string{"auto-retries": "passed", "early-flake-detection": "passed", "skipping": "passed", "quarantine": "passed", "disabled": "passed", "attempt-to-fix": "passed"}
			if fixture.name == "playwright" || fixture.name == "webdriverio" {
				expected["skipping"] = "not supported"
			}
			if fixture.name == "cypress" {
				expected["skipping"] = "deferred"
			}
			actual := map[string]string{}
			for _, feature := range report.Features {
				actual[feature.Name] = feature.Status
			}
			require.Equal(t, expected, actual)
			if fixture.name == "cypress" {
				for _, run := range report.Runs {
					require.NotEqual(t, "skipping", run.Name, "deferred TIA must not execute a skipping probe")
					if run.Name == "reporting-only" {
						require.NotNil(t, run.Coverage)
						require.Equal(t, "not observed", run.Coverage.Status, "the uninstrumented application must pass without claiming coverage")
					}
				}
			}
			paired := 0
			for _, run := range report.Runs {
				if run.Name != "baseline" && run.Name != "reporting-only" {
					continue
				}
				paired++
				require.Equal(t, 1, run.TestCounts.Passed)
				require.Zero(t, run.TestCounts.Failed)
				if run.Instrumented {
					require.Positive(t, run.TestEventCount)
				}
				if fixture.name == "playwright" || fixture.name == "cypress" || fixture.name == "webdriverio" {
					require.NotEmpty(t, run.ExecutionContext)
				}
			}
			require.Equal(t, 2, paired)
			for _, name := range []string{"package-lock.json", "node_modules/dd-trace"} {
				if name == "package-lock.json" {
					require.FileExists(t, filepath.Join(root, name))
				} else {
					require.NoDirExists(t, filepath.Join(root, name))
				}
			}
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			require.Len(t, entries, 2, "only HTML and JSON reports remain")
			require.FileExists(t, filepath.Join(root, ".testoptimization/report.html"))
			final := integrationCommand(t, ctx, root, env, binary, "testdrive", "--report", "--format", "json")
			require.True(t, json.Valid([]byte(final)), final)

			if fixture.name == "playwright" {
				integrationFile(t, root, "one.spec.js", `const {test}=require('@playwright/test');test('unsupported child',()=>{require('node:child_process').spawn('/bin/sh',['-c','exit 0'],{detached:true,argv0:'custom',stdio:'ignore'});});`)
				command := exec.CommandContext(ctx, binary, "testdrive", "--framework", "playwright", "--all", "--agent", "--yes", "--format", "json")
				command.Dir = root
				command.Env = integrationEnvironment(env)
				blocked, err := command.CombinedOutput()
				require.Error(t, err, string(blocked))
				lines := strings.Split(strings.TrimSpace(string(blocked)), "\n")
				var result struct{ Status, Code string }
				require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &result), string(blocked))
				require.Equal(t, "unsupported", result.Status, string(blocked))
				require.Equal(t, "unsupported_detached_spawn", result.Code)
				data, err := os.ReadFile(filepath.Join(root, ".testoptimization/testdrive.json"))
				require.NoError(t, err)
				require.NoError(t, json.Unmarshal(data, &report))
				require.NotEqual(t, "compatible", report.Compatibility.Status)
			}
		})
	}
}

func retainIntegrationReports(t *testing.T, root, framework string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, name := range []string{"testdrive.json", "report.html"} {
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization", name))
			if os.IsNotExist(err) {
				continue
			}
			require.NoError(t, err)
			if name == "testdrive.json" {
				t.Logf("Failed validation report:\n%s", data)
			}
			if directory := os.Getenv("DDTEST_FRAMEWORK_REPORTS"); directory != "" {
				require.NoError(t, os.MkdirAll(directory, 0755))
				require.NoError(t, os.WriteFile(filepath.Join(directory, framework+"-"+name), data, 0600))
			}
		}
	})
}

func integrationFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
}

func integrationCommand(t *testing.T, ctx context.Context, directory string, env []string, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = directory
	command.Env = integrationEnvironment(env)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %v:\n%s", name, args, output)
	return string(output)
}

func integrationEnvironment(env []string) []string {
	var result []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DD_") && !strings.HasPrefix(entry, "DATADOG_") {
			result = append(result, entry)
		}
	}
	return append(result, env...)
}
