package testdrive

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/ext"
	"github.com/stretchr/testify/require"
)

// Exercise only the public commands available at this stack position. Native
// browser execution and a local mock intake supply the evidence, not fixtures
// that synthesize a successful validation report.
func TestPublicPlaywrightTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_PLAYWRIGHT_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_PLAYWRIGHT_NODE_MODULES to @playwright/test and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	command := func(root, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		return cmd.CombinedOutput()
	}
	output, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(output))
	for _, mode := range []string{"passing", "existing failure", "sharded repeated alias", "unsupported detached spawn", "dependency projects and bare playwright"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, modules, filepath.Join(root, "node_modules"), false))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			address := listener.Addr().String()
			require.NoError(t, listener.Close())
			write("package.json", `{"scripts":{"test":"playwright test"},"devDependencies":{"@playwright/test":"1.55.1","dd-trace":"6.20.1"}}`)
			if mode == "sharded repeated alias" {
				write("package.json", `{"scripts":{"test":"playwright test --project desktop mobile --repeat-each=2 --shard=1/2"},"devDependencies":{"@playwright/test":"1.55.1","dd-trace":"6.20.1"}}`)
			}
			reporterLog := filepath.Join(t.TempDir(), "customer-reporter.jsonl")
			nodeCheck := ""
			if mode == "existing failure" {
				nodeCheck = `if(!(process.env.NODE_OPTIONS||'').includes('--conditions=ddtest-selected'))throw Error('selected Node condition lost');`
			}
			write("customer,reporter.cjs", `module.exports=class {onTestEnd(){`+nodeCheck+`require('node:fs').appendFileSync(`+fmt.Sprintf("%q", reporterLog)+`,String(process.env.DD_TRACE_ENABLED)+'|'+(process.env.PLAYWRIGHT_JUNIT_OUTPUT_NAME||'')+'\n')}}`)
			write("server.cjs", `require('node:http').createServer((req,res)=>res.end('<h1>hello</h1>')).listen(`+strings.Split(address, ":")[1]+`,'127.0.0.1')`)
			write("playwright.config.cjs", `module.exports={testDir:'test',reporter:[['./customer,reporter.cjs']],workers:1,retries:1,repeatEach:2,projects:[{name:'desktop',use:{browserName:'chromium'}},{name:'mobile',use:{browserName:'chromium'}}],webServer:{command:'node server.cjs',url:`+fmt.Sprintf("%q", "http://"+address)+`,reuseExistingServer:false,timeout:10000},use:{baseURL:`+fmt.Sprintf("%q", "http://"+address)+`}}`)
			body := `await page.goto('/');await expect(page.locator('h1')).toHaveText('hello');`
			if mode == "existing failure" {
				body += `expect(1).toBe(2);`
			}
			if mode == "unsupported detached spawn" {
				body = `require('node:child_process').spawn('/bin/sh',['-c','exit 0'],{detached:true,argv0:'custom',stdio:'ignore'});`
			}
			if mode == "dependency projects and bare playwright" {
				require.NoError(t, os.RemoveAll(filepath.Join(root, "node_modules", "@playwright", "test")))
				require.NoError(t, os.Remove(filepath.Join(root, "node_modules", ".bin", "playwright")))
				require.NoError(t, os.Symlink("../playwright/cli.js", filepath.Join(root, "node_modules", ".bin", "playwright")))
				write("package.json", `{"scripts":{"test":"playwright test --project=desktop --project=mobile --repeat-each=2"},"devDependencies":{"playwright":"1.55.1","dd-trace":"6.20.1"}}`)
				write("playwright.config.cjs", `module.exports={testDir:'test',workers:1,reporter:[['./customer,reporter.cjs']],repeatEach:1,projects:[{name:'setup',testMatch:'*.setup.cjs',teardown:'teardown'},{name:'teardown',testMatch:'*.teardown.cjs'},{name:'desktop',testMatch:'*.spec.cjs',dependencies:['setup'],use:{browserName:'chromium'}},{name:'mobile',testMatch:'*.spec.cjs',dependencies:['setup'],use:{browserName:'chromium'}}]}`)
				write("test/one.setup.cjs", `const {test}=require('playwright/test');test('setup',()=>{});`)
				write("test/one.teardown.cjs", `const {test}=require('playwright/test');test('teardown',()=>{});`)
				body = `await page.setContent('<h1>hello</h1>');await expect(page.locator('h1')).toHaveText('hello');`
			}
			runnerModule := "@playwright/test"
			if mode == "dependency projects and bare playwright" {
				runnerModule = "playwright/test"
			}
			write("test/one.spec.cjs", `const {test,expect}=require('`+runnerModule+`');test('renders',async({page})=>{`+body+`});`)
			write(".github/workflows/ci.yml", `jobs:
  browser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
			if mode == "existing failure" {
				workflowPath := filepath.Join(root, ".github", "workflows", "ci.yml")
				workflow, readErr := os.ReadFile(workflowPath)
				require.NoError(t, readErr)
				requireWriteFile(t, workflowPath, strings.Replace(string(workflow), "NODE_OPTIONS: '-r ", "NODE_OPTIONS: '--conditions=ddtest-selected -r ", 1))
			}
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "playwright")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "already appears in every detected test workflow")
				output, err = command(root, binary, "testdrive", "--framework", "playwright", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed)
				require.Equal(t, "not exercised", check.Compatibility.Status)
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "playwright", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "playwright", "--command", "PLAYWRIGHT_JUNIT_OUTPUT_NAME=results.xml npm test", "--yes"}
			}
			output, err = command(root, binary, args...)
			report := readValidationReport(t, root)
			if mode == "unsupported detached spawn" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.Contains(t, report.Error, "unsupported supervised spawn")
				require.Equal(t, "inconclusive", report.Compatibility.Status)
				require.Len(t, report.Configurations, 1)
				require.Equal(t, "not exercised", report.Configurations[0].Compatibility.Status)
				require.Equal(t, "unsupported_detached_spawn", report.Configurations[0].Compatibility.Code)
			} else {
				require.NoError(t, err, string(output))
				require.True(t, report.Success, string(output))
				require.Equal(t, "compatible", report.Compatibility.Status)
				expectedProjects := 2
				if mode == "sharded repeated alias" {
					expectedProjects = 1
				}
				require.True(t, featureChecksPassed("playwright", report.Features))
				projects := map[string]bool{}
				for _, feature := range report.Features {
					projects[feature.Project] = true
				}
				if mode == "dependency projects and bare playwright" {
					require.Len(t, projects, expectedProjects+2)
				} else {
					require.Len(t, projects, expectedProjects)
				}
				for _, run := range report.Runs {
					if run.ProbeMode != "" {
						require.NotEmpty(t, run.NativeCommand)
						require.NotContains(t, run.NativeCommand, "--shard")
						require.Contains(t, run.NativeCommand, "--repeat-each=1")
						require.Contains(t, run.NativeCommand, "--no-deps")
						require.Equal(t, 1, strings.Count(run.NativeCommand, "--project="))
					}
				}
				for _, feature := range report.Features {
					if feature.Name == "skipping" {
						require.Equal(t, "not supported", feature.Status)
					} else {
						require.Equal(t, "passed", feature.Status, feature)
					}
				}
				require.NotNil(t, report.ReportingCounts)
				expectedExecutions := expectedProjects * 2
				if mode == "dependency projects and bare playwright" {
					expectedExecutions += 2
				}
				require.Equal(t, expectedExecutions, report.ReportingCounts.TestExecutions)
				customerOutput, readErr := os.ReadFile(reporterLog)
				require.NoError(t, readErr)
				require.Contains(t, string(customerOutput), "false|")
				require.Contains(t, string(customerOutput), "true|")
				if mode == "existing failure" {
					require.Contains(t, string(customerOutput), "false|results.xml\n")
					require.Contains(t, string(customerOutput), "true|results.xml\n")
				}
			}
			require.NotNil(t, report.Cleanup)
			require.Equal(t, "passed", report.Cleanup.Status)
			if mode == "unsupported detached spawn" {
				require.False(t, report.HTMLReportCurrent)
			} else {
				require.FileExists(t, htmlReportPath(root))
			}
			if mode == "existing failure" {
				html, err := os.ReadFile(htmlReportPath(root))
				require.NoError(t, err)
				require.Contains(t, string(html), "Expected")
			}
			require.NoDirExists(t, filepath.Join(root, "test-results"))
			require.NoDirExists(t, filepath.Join(root, "playwright-report"))
			entries, err := os.ReadDir(filepath.Join(root, "test"))
			require.NoError(t, err)
			expectedFiles := 1
			if mode == "dependency projects and bare playwright" {
				expectedFiles = 3
			}
			require.Len(t, entries, expectedFiles)
			connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err == nil {
				_ = connection.Close()
			}
			require.Error(t, err, "owned web server must stop after every run")
		})
	}
}

func TestPlaywrightDiscoveryOwnsImportSideEffects(t *testing.T) {
	modules := os.Getenv("DDTEST_PLAYWRIGHT_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_PLAYWRIGHT_NODE_MODULES")
	}
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	portFile := filepath.Join(root, "port")
	child := fmt.Sprintf(`const s=require('node:net').createServer();s.listen(0,'127.0.0.1',()=>require('node:fs').writeFileSync(%q,String(s.address().port)));`, portFile)
	requireWriteFile(t, filepath.Join(root, "playwright.config.cjs"), fmt.Sprintf(`require('node:child_process').spawn(process.execPath,['-e',%q],{detached:true,stdio:'ignore'}).unref();const fs=require('node:fs'),deadline=Date.now()+5000;while(!fs.existsSync(%q)&&Date.now()<deadline)Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,10);module.exports={testDir:'.'};`, child, portFile))
	requireWriteFile(t, filepath.Join(root, "cache-path.cjs"), `require("node:fs").writeFileSync(`+fmt.Sprintf("%q", filepath.Join(root, "cache-path"))+`,require("node:os").tmpdir());`)
	requireWriteFile(t, filepath.Join(root, "one.spec.cjs"), `require('./cache-path.cjs');const {test}=require('@playwright/test');test('discovered',()=>{});`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scratch,with-comma"), 0700))
	drive := &Testdrive{command: filepath.Join(root, "node_modules", ".bin", "playwright"), args: []string{"test"}, executor: &ext.DefaultCommandExecutor{Timeout: 10 * time.Second}, repositoryRoot: root}
	discovered, err := drive.inspectPlaywright(t.Context(), filepath.Join(root, "scratch,with-comma"), "", "")
	require.NoError(t, err)
	require.Len(t, discovered.Tests, 1)
	cachePath, err := os.ReadFile(filepath.Join(root, "cache-path"))
	require.NoError(t, err)
	require.Contains(t, string(cachePath), "scratch,with-comma")
	require.NoDirExists(t, string(cachePath))
	data, err := os.ReadFile(portFile)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		c, e := net.DialTimeout("tcp", "127.0.0.1:"+string(data), 50*time.Millisecond)
		if e == nil {
			_ = c.Close()
		}
		return e != nil
	}, 3*time.Second, 20*time.Millisecond)
}

func TestPlaywrightDiscoveryWithFileTerminatorOnlyLists(t *testing.T) {
	modules := os.Getenv("DDTEST_PLAYWRIGHT_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_PLAYWRIGHT_NODE_MODULES")
	}
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
	marker := filepath.Join(root, "executed")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0700))
	requireWriteFile(t, filepath.Join(root, "playwright.config.cjs"), `module.exports={testDir:'tests'};`)
	requireWriteFile(t, filepath.Join(root, "tests", "foo.spec.cjs"), fmt.Sprintf(`const {test}=require('@playwright/test');test('selected',()=>require('node:fs').writeFileSync(%q,'1'));`, marker))
	requireWriteFile(t, filepath.Join(root, "tests", "other.spec.cjs"), `const {test}=require('@playwright/test');test('other',()=>{});`)
	drive := &Testdrive{command: filepath.Join(root, "node_modules", ".bin", "playwright"), args: []string{"test", "--", "tests/foo.spec.cjs"}, executor: &ext.DefaultCommandExecutor{Timeout: 30 * time.Second}, repositoryRoot: root}
	discovered, err := drive.inspectPlaywright(t.Context(), root, "", "")
	require.NoError(t, err)
	require.Len(t, discovered.Tests, 1, "the file filter after -- still selects one file")
	require.NoFileExists(t, marker, "discovery lists tests; it must not execute them")
}
