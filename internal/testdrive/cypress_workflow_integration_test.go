package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
	ctx, cancel := context.WithTimeout(t.Context(), 28*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	newCommand := func(root, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		return cmd
	}
	command := func(root, name string, args ...string) ([]byte, error) {
		return newCommand(root, name, args...).CombinedOutput()
	}
	output, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(output))
	for _, mode := range []string{"passing", "existing failure", "action API", "action custom CLI", "native retries", "type retries", "CLI retries", "action retries", "TypeScript", "ESM", "subpackage", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			write("package.json", `{"scripts":{"test":"cypress run --browser electron"},"devDependencies":{"cypress":"15.1.0","dd-trace":"6.20.1"}}`)
			config := `module.exports=require('cypress').defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){on('after:run',results=>console.log('customer after:run',results.totalTests))}}})`
			write("cypress.config.cjs", config)
			if mode == "recovery" {
				t.Setenv("TMPDIR", t.TempDir())
				t.Setenv("DDTEST_CYPRESS_PID_LOG", filepath.Join(t.TempDir(), "pids.jsonl"))
				write("cypress.config.cjs", `module.exports=require('cypress').defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){require('node:fs').appendFileSync(process.env.DDTEST_CYPRESS_PID_LOG,JSON.stringify([process.pid,process.ppid])+'\n');on('after:run',()=>console.log('customer after:run'))}}})`)
			}
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
			if mode == "native retries" {
				write("cypress.config.cjs", `module.exports=require('cypress').defineConfig({video:false,retries:{runMode:2},e2e:{supportFile:false,setupNodeEvents(on){let count=0;on('task',{attempt:()=>++count});on('after:run',()=>console.log('customer after:run'))}}})`)
				write("cypress/e2e/one.cy.js", `describe('browser',()=>{it('retries once',()=>cy.task('attempt').should('satisfy',n=>n%2===0));it('stable',()=>cy.wrap(1).should('eq',1))})`)
			}
			if mode == "type retries" {
				write("cypress.config.cjs", strings.Replace(config, "e2e:{", "e2e:{retries:{runMode:2},", 1))
			}
			if mode == "CLI retries" {
				write("package.json", `{"scripts":{"test":"cypress run --browser electron --config retries=2 --record --key fake-secret --auto-cancel-after-failures 1 -t CI"},"devDependencies":{"cypress":"15.1.0","dd-trace":"6.20.1"}}`)
			}
			if mode == "TypeScript" {
				require.NoError(t, os.Remove(filepath.Join(root, "cypress.config.cjs")))
				write("cypress.config.ts", `import {defineConfig} from 'cypress'; export default defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){on('after:run',()=>console.log('customer after:run'))}}})`)
			}
			if mode == "ESM" {
				require.NoError(t, os.Remove(filepath.Join(root, "cypress.config.cjs")))
				write("package.json", `{"type":"module","scripts":{"test":"cypress run --browser electron"},"devDependencies":{"cypress":"15.1.0","dd-trace":"6.20.1"}}`)
				write("cypress.config.js", `import {defineConfig} from 'cypress'; export default defineConfig({video:false,e2e:{supportFile:false,setupNodeEvents(on){on('after:run',()=>console.log('customer after:run'))}}})`)
			}
			var address string
			if mode == "action API" || mode == "action custom CLI" || mode == "action retries" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				address = listener.Addr().String()
				require.NoError(t, listener.Close())
				write("zero.cjs", `console.log("customer one-shot startup")`)
				write("server.cjs", `console.log("customer server started");require('node:http').createServer((req,res)=>res.end('ready')).listen(`+strings.Split(address, ":")[1]+`,'127.0.0.1')`)
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
          start: node zero.cjs,node server.cjs
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

			projectRoot := root
			if mode == "subpackage" {
				projectRoot = filepath.Join(root, "frontend")
				require.NoError(t, os.MkdirAll(projectRoot, 0700))
				for _, name := range []string{"node_modules", "package.json", "cypress.config.cjs", "cypress"} {
					require.NoError(t, os.Rename(filepath.Join(root, name), filepath.Join(projectRoot, name)))
				}
				write("package.json", `{"scripts":{"test":"npm --prefix frontend test"}}`)
			}
			workflowPath := filepath.Join(root, ".github/workflows/ci.yml")
			workflow, err := os.ReadFile(workflowPath)
			require.NoError(t, err)
			label := map[string]string{"darwin": "macos-latest", "linux": "ubuntu-latest"}[runtime.GOOS]
			architecture := map[string]string{"arm64": "arm64", "amd64": "x64"}[runtime.GOARCH]
			text := strings.Replace(string(workflow), "  browser:\n", "  browser:\n    runs-on: "+label+"\n", 1)
			text = strings.ReplaceAll(text, "{node-version: '24'}", "{node-version: '24', architecture: '"+architecture+"'}")
			write(".github/workflows/ci.yml", text)
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "action retries" {
				workflow, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
				require.NoError(t, err)
				write(".github/workflows/ci.yml", strings.Replace(string(workflow), "config: viewportWidth=800", "config: retries=2,viewportWidth=800", 1))
			}
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "cypress")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "deferred")
				output, err = command(root, binary, "testdrive", "--framework", "cypress", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed)
				require.False(t, check.Success)
				require.Equal(t, "not exercised", check.Compatibility.Status)
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "cypress", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "cypress", "--command", "npm test", "--yes"}
			}
			if mode == "recovery" {
				logPath := filepath.Join(t.TempDir(), "killed-run.log")
				log, err := os.Create(logPath)
				require.NoError(t, err)
				killed := newCommand(root, binary, args...)
				killed.Stdout = log
				killed.Stderr = log
				require.NoError(t, killed.Start())
				t.Cleanup(func() { _ = killed.Process.Kill() })
				require.Eventually(t, func() bool {
					data, _ := os.ReadFile(logPath)
					if !strings.Contains(string(data), "Running quarantine") {
						return false
					}
					audit, _ := os.ReadFile(os.Getenv("DDTEST_CYPRESS_PID_LOG"))
					// Baseline, reporting, two controls, ATR and EFD precede quarantine.
					// Wait for its actual config process, not just the ddtest log message.
					return len(strings.Split(strings.TrimSpace(string(audit)), "\n")) >= 7
				}, 5*time.Minute, 100*time.Millisecond)
				require.NoError(t, killed.Process.Kill())
				require.Error(t, killed.Wait())
				require.NoError(t, log.Close())
				data, err := os.ReadFile(os.Getenv("DDTEST_CYPRESS_PID_LOG"))
				require.NoError(t, err)
				var pids []int
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var pair []int
					require.NoError(t, json.Unmarshal([]byte(line), &pair))
					pids = append(pids, pair...)
				}
				require.NotEmpty(t, pids)
				require.Eventually(t, func() bool {
					for _, pid := range pids {
						state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
						if err == nil && strings.TrimSpace(string(state)) != "" && !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
							return false
						}
					}
					return true
				}, 15*time.Second, 100*time.Millisecond, "Cypress config and native parent processes must stop after owner loss")
			}
			output, err = command(root, binary, args...)
			require.NoError(t, err, string(output))
			report := readValidationReport(t, root)
			require.True(t, report.Success, string(output))
			if mode == "recovery" {
				entries, err := os.ReadDir(os.Getenv("TMPDIR"))
				require.NoError(t, err)
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "ddtest-") && !strings.Contains(entry.Name(), "locks") {
						t.Errorf("owned temporary storage remained: %s", entry.Name())
					}
				}
			}
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Len(t, report.Features, 6)
			for _, feature := range report.Features {
				if feature.Name == "skipping" {
					require.Equal(t, "deferred", feature.Status)
				} else {
					require.Equal(t, "passed", feature.Status, feature)
				}
			}
			if mode == "action API" || mode == "action custom CLI" || mode == "action retries" {
				require.Len(t, report.Configurations, 1)
				if mode == "action API" || mode == "action retries" {
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
			require.NotContains(t, string(html), "fake-secret")
			if mode == "action API" || mode == "action custom CLI" || mode == "action retries" {
				require.Contains(t, string(html), "customer one-shot startup")
				require.Contains(t, string(html), "customer server started")
			}
			require.NotNil(t, report.ReportingCounts)
			if mode == "native retries" {
				require.Equal(t, 2, report.ReportingCounts.TestExecutions)
			} else {
				require.Equal(t, 1, report.ReportingCounts.TestExecutions)
			}
			raw, err := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
			require.NoError(t, err)
			require.NotContains(t, string(raw), "fake-secret")
			for _, run := range report.Runs {
				require.Nil(t, run.Coverage, "deferred application coverage must not be presented as invalid")
			}
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			require.Len(t, entries, 2)
			for _, name := range []string{"screenshots", "videos", "downloads", "fixtures"} {
				require.NoDirExists(t, filepath.Join(projectRoot, "cypress", name))
			}
			files, err := os.ReadDir(filepath.Join(projectRoot, "cypress/e2e"))
			require.NoError(t, err)
			require.Len(t, files, 1)
		})
	}
}

func TestCypressTypeScriptWrapperTranspilesToCommonJS(t *testing.T) {
	modules := os.Getenv("DDTEST_CYPRESS_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_CYPRESS_NODE_MODULES to include TypeScript")
	}
	root := t.TempDir()
	requireWriteFile(t, filepath.Join(root, "observer.cjs"), cypressObserver)
	requireWriteFile(t, filepath.Join(root, "cypress.config.ts"), `export default {e2e:{}}`)
	script := `const assert=require('node:assert/strict'),fs=require('node:fs');
 const ts=require(require.resolve('typescript',{paths:[process.argv[2]]}));
 require.extensions['.ts']=(m,file)=>m._compile(ts.transpileModule(fs.readFileSync(file,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,esModuleInterop:true}}).outputText,file);
 const wrapper=require(process.argv[1]+'/observer.cjs')(process.argv[1],null,process.argv[1],false,false);
 assert.equal(typeof require(wrapper).default.e2e.setupNodeEvents,'function');`
	output, err := exec.CommandContext(t.Context(), "node", "-e", script, root, modules).CombinedOutput()
	require.NoError(t, err, string(output))
}
