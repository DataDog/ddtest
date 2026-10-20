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
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
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
	for _, mode := range []string{"passing", "existing failure", "unsupported detached spawn"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			address := listener.Addr().String()
			require.NoError(t, listener.Close())
			write("package.json", `{"scripts":{"test":"playwright test"},"devDependencies":{"@playwright/test":"1.55.1","dd-trace":"6.20.1"}}`)
			write("server.cjs", `require('node:http').createServer((req,res)=>res.end('<h1>hello</h1>')).listen(`+strings.Split(address, ":")[1]+`,'127.0.0.1')`)
			write("playwright.config.cjs", `module.exports={testDir:'test',workers:1,retries:1,projects:[{name:'desktop',use:{browserName:'chromium'}},{name:'mobile',use:{browserName:'chromium'}}],webServer:{command:'node server.cjs',url:`+fmt.Sprintf("%q", "http://"+address)+`,reuseExistingServer:false,timeout:10000},use:{baseURL:`+fmt.Sprintf("%q", "http://"+address)+`}}`)
			body := `await page.goto('/');await expect(page.locator('h1')).toHaveText('hello');`
			if mode == "existing failure" {
				body += `expect(1).toBe(2);`
			}
			if mode == "unsupported detached spawn" {
				body = `require('node:child_process').spawn('/bin/sh',['-c','exit 0'],{detached:true,argv0:'custom',stdio:'ignore'});`
			}
			write("test/one.spec.cjs", `const {test,expect}=require('@playwright/test');test('renders',async({page})=>{`+body+`});`)
			write(".github/workflows/ci.yml", `jobs:
  browser:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "playwright")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "not support Test Impact Analysis")
				output, err = command(root, binary, "testdrive", "--framework", "playwright", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed)
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "playwright", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "playwright", "--command", "npm test", "--yes"}
			}
			output, err = command(root, binary, args...)
			report := readValidationReport(t, root)
			if mode == "unsupported detached spawn" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.Contains(t, report.Error, "unsupported detached spawn")
				require.Equal(t, "inconclusive", report.Compatibility.Status)
				require.Len(t, report.Configurations, 1)
				require.Equal(t, "not exercised", report.Configurations[0].Compatibility.Status)
			} else {
				require.NoError(t, err, string(output))
				require.True(t, report.Success, string(output))
				require.Equal(t, "compatible", report.Compatibility.Status)
				require.Len(t, report.Features, 12)
				for _, feature := range report.Features {
					if feature.Name == "skipping" {
						require.Equal(t, "not supported", feature.Status)
					} else {
						require.Equal(t, "passed", feature.Status, feature)
					}
				}
				require.NotNil(t, report.ReportingCounts)
				require.Equal(t, 2, report.ReportingCounts.TestExecutions)
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
			require.Len(t, entries, 1)
			connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
			if err == nil {
				_ = connection.Close()
			}
			require.Error(t, err, "owned web server must stop after every run")
		})
	}
}
