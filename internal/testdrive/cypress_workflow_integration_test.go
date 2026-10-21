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

func TestPublicCypressTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_CYPRESS_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_CYPRESS_NODE_MODULES to Cypress and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
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
	for _, mode := range []string{"passing", "existing failure", "action API", "action custom CLI"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			write("package.json", `{"scripts":{"test":"cypress run --browser electron"},"devDependencies":{"cypress":"15.1.0","dd-trace":"6.20.1"}}`)
			write("cypress.config.cjs", `module.exports=require('cypress').defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){on('after:run',results=>console.log('customer after:run',results.totalTests))}}})`)
			want := "1"
			if mode == "existing failure" {
				want = "2"
			}
			write("cypress/e2e/one.cy.js", `describe('browser',()=>it('works',()=>cy.wrap(1).should('eq',`+want+`)))`)
			write(".github/workflows/ci.yml", `jobs:
  browser:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`)
			var address string
			if mode == "action API" || mode == "action custom CLI" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				address = listener.Addr().String()
				require.NoError(t, listener.Close())
				write("server.cjs", `require('node:http').createServer((req,res)=>res.end('ready')).listen(`+strings.Split(address, ":")[1]+`,'127.0.0.1')`)
				write(".github/workflows/ci.yml", fmt.Sprintf(`jobs:
  browser:
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - uses: cypress-io/github-action@c541a95ea92d9239347a2355f2e895cd4b3f8060
        with:
          browser: electron
          config: viewportWidth=800
          env: feature=true,number=7
          start: node server.cjs
          wait-on: http://%s
          wait-on-timeout: '15'
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }}'}
`, address))
			}
			if mode == "action API" {
				write("cypress/e2e/one.cy.js", `it('action inputs',()=>{expect(Cypress.config('viewportWidth')).to.equal(800);expect(Cypress.env('feature')).to.equal(true);expect(Cypress.env('number')).to.equal(7);cy.wrap(1).should('eq',1)})`)
			}
			if mode == "action custom CLI" {
				write("build.cjs", `require('node:fs').writeFileSync('generated.json',JSON.stringify({ready:true}))`)
				write("cypress.config.cjs", `if(!require('./generated.json').ready)throw new Error('build missing');module.exports=require('cypress').defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){on('after:run',r=>console.log('customer after:run',r.totalTests))}}})`)
				file := filepath.Join(root, ".github/workflows/ci.yml")
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				text := strings.Replace(string(data), "          browser: electron", "          command: npm test\n          build: node build.cjs", 1)
				require.NoError(t, os.WriteFile(file, []byte(text), 0600))
			}

			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "cypress")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "deferred")
				output, err = command(root, binary, "testdrive", "--framework", "cypress", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed)
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "cypress", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "cypress", "--command", "npm test", "--yes"}
			}
			output, err = command(root, binary, args...)
			require.NoError(t, err, string(output))
			report := readValidationReport(t, root)
			require.True(t, report.Success, string(output))
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				if feature.Name == "skipping" {
					require.Equal(t, "deferred", feature.Status)
				} else {
					require.Equal(t, "passed", feature.Status, feature)
				}
			}
			if mode == "action API" || mode == "action custom CLI" {
				require.Len(t, report.Configurations, 1)
				if mode == "action API" {
					require.Contains(t, report.Configurations[0].Command, "Cypress.run")
				} else {
					require.Equal(t, "npm test", report.Configurations[0].Command)
				}
				connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
				if err == nil {
					_ = connection.Close()
				}
				require.Error(t, err, "owned action startup must stop")
			}
			require.Equal(t, "passed", report.Cleanup.Status)
			require.True(t, report.HTMLReportCurrent)
			html, err := os.ReadFile(htmlReportPath(root))
			require.NoError(t, err)
			require.Contains(t, string(html), cypressTIADeferredReason)
			require.Contains(t, string(html), "customer after:run")
			require.NotNil(t, report.ReportingCounts)
			require.Equal(t, 1, report.ReportingCounts.TestExecutions)
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			require.Len(t, entries, 2)
			for _, name := range []string{"screenshots", "videos", "downloads", "fixtures"} {
				require.NoDirExists(t, filepath.Join(root, "cypress", name))
			}
			files, err := os.ReadDir(filepath.Join(root, "cypress/e2e"))
			require.NoError(t, err)
			require.Len(t, files, 1)
		})
	}
}
