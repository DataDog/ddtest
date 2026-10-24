package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"

	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Only public flags available at this stack position; all telemetry goes to
// the local mock. Native WebdriverIO/browser runs supply the evidence.
func TestPublicWebdriverIOTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_WEBDRIVERIO_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_WEBDRIVERIO_NODE_MODULES to WebdriverIO and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 24*time.Minute)
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
	// Process sockets live under /tmp, not TMPDIR; collect the folders that exist
	// before a run so later checks only judge the ones the run created.
	processFolders := func() map[string]bool {
		matches, err := filepath.Glob(filepath.Join(processSocketBase(), "ddtest-process-*"))
		require.NoError(t, err)
		folders := map[string]bool{}
		for _, match := range matches {
			folders[match] = true
		}
		return folders
	}
	for _, mode := range webdriverIOWorkflowScenarios.selected(t) {
		t.Run(mode, func(t *testing.T) {
			var existingProcessFolders map[string]bool
			// Leave room for Chrome's bounded Linux SingletonSocket beneath the
			// session and run directories, while isolating every case's storage.
			temporaryBase := os.TempDir()
			if runtime.GOOS != "windows" {
				temporaryBase = "/tmp"
			}
			temporary, err := os.MkdirTemp(temporaryBase, "wd-")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(temporary)) })
			for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(key, temporary)
			}
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root, false))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}

			write("package.json", `{"scripts":{"test":"wdio run wdio.conf.cjs"},"devDependencies":{"@wdio/cli":"9.20.0","@wdio/local-runner":"9.20.0","@wdio/mocha-framework":"9.20.0","@wdio/spec-reporter":"9.20.0","dd-trace":"6.20.1"}}`)
			config := `exports.config={runner:'local',specs:['./test/*.js'],maxInstances:1,framework:'mocha',reporters:['spec'],capabilities:[{browserName:'chrome','goog:chromeOptions':{binary:process.env.DDTEST_CHROME_BINARY,args:['--headless=new','--no-sandbox','--disable-dev-shm-usage']},...(process.env.DDTEST_CHROMEDRIVER_BINARY?{'wdio:chromedriverOptions':{binary:process.env.DDTEST_CHROMEDRIVER_BINARY}}:{})}]};`
			if mode == "capability specs" {
				config = strings.Replace(config, "specs:['./test/*.js']", "specs:[]", 1)
				config = strings.Replace(config, "browserName:'chrome'", "browserName:'chrome',specs:['./test/*.js'],exclude:['**/ddtest*']", 1)
			}
			if mode == "existing failure" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,specFileRetries:1", 1)
			}
			if mode == "passing" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,onPrepare(){require('node:fs').writeFileSync('customer-output.txt','changed')}", 1)
			}
			if mode == "browser runner" {
				config = strings.Replace(config, "runner:'local'", "runner:'browser'", 1)
			}
			if mode == "Mocha retries" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,mochaOpts:{retries:1}", 1)
			}
			if mode == "spec retries" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,specFileRetries:1", 1)
			}
			if mode == "shard" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,shard:{current:2,total:2}", 1)
			}
			if mode == "repeat shard" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,repeat:2,spec:['./test/one.js'],shard:{current:1,total:1}", 1)
			}
			if mode == "TDD" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,mochaOpts:{ui:'tdd'}", 1)
			}
			if mode == "Jasmine expectations" {
				config = strings.Replace(config, "framework:'mocha'", "framework:'jasmine',jasmineOpts:{failSpecWithNoExpectations:true}", 1)
			}
			if mode == "cloud grid" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,hostname:'ondemand.saucelabs.com',user:'fake-user',key:'fake-key',onPrepare(){throw Error('cloud hook must not run')}", 1)
			}
			if mode == "same browser capabilities" {
				config = strings.Replace(config, "exports.config=", "const original=", 1) + "exports.config={...original,capabilities:[original.capabilities[0],{...original.capabilities[0],browserVersion:undefined}]}"
			}
			if mode == "grouped root tests" {
				config = strings.Replace(config, "specs:['./test/*.js']", "specs:[['./test/one.js','./test/two.js']]", 1)
			}
			if mode == "timeout" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,before(){return new Promise(()=>{})}", 1)
			}
			if mode == "missing native" {
				config = strings.Replace(config, "maxInstances:1", "maxInstances:1,onComplete(){require('node:fs').rmSync(process.env.DDTEST_WDIO_RESULTS,{recursive:true,force:true})}", 1)
			}
			if mode == "occupied port" {
				listener, err := net.Listen("tcp", ":0")
				require.NoError(t, err)
				t.Cleanup(func() { _ = listener.Close() })
				config = strings.Replace(config, "maxInstances:1", fmt.Sprintf("maxInstances:1,services:[['static-server',{port:%d}]]", listener.Addr().(*net.TCPAddr).Port), 1)
			}
			write("wdio.conf.cjs", config)
			body := `await browser.url('data:text/html,<h1>hello</h1>'); if(await $('h1').getText()!=='hello')throw new Error('missing heading');`
			if mode == "existing failure" {
				body += `throw new Error('existing failure');`
			}
			if mode == "Mocha retries" {
				body = `globalThis.count=(globalThis.count||0)+1;if(globalThis.count===1)throw Error('first attempt');` + body
			}
			if mode == "spec retries" {
				body = `const fs=require('node:fs'),path=require('node:path'),marker=path.join(process.env.TMPDIR,'native-retry');if(!fs.existsSync(marker)){fs.writeFileSync(marker,'retried');throw Error('first spec attempt')};` + body
			}
			if mode == "Jasmine expectations" {
				body += `expect(true).toBe(true);`
			}
			write("test/one.js", `describe('browser',()=>{it('renders',async()=>{`+body+`});`+"it('renders',()=>{});"+`});`)
			if mode == "TDD" {
				write("test/one.js", `suite('browser',()=>test('renders',async()=>{`+body+`}))`)
			}
			if mode == "Jasmine expectations" {
				write("test/one.js", `describe('browser',()=>it('renders',async()=>{`+body+`}))`)
			}
			if mode == "grouped root tests" {
				write("test/one.js", `it('root one',async()=>{`+body+`})`)
				write("test/two.js", `it('root two',async()=>{`+body+`})`)
			}
			if mode == "shard" {
				write("test/two.js", `describe('browser',()=>{it('renders',async()=>{`+body+`});it('other',()=>{})})`)
			}
			write("customer-output.txt", "keep")

			runnerOS := "ubuntu-latest"
			if runtime.GOOS == "darwin" {
				runnerOS = "macos-latest"
			}
			architecture := "x64"
			if runtime.GOARCH == "arm64" {
				architecture = "arm64"
			}
			write(".github/workflows/ci.yml", fmt.Sprintf(`jobs:
  test:
    runs-on: %s
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24.14.1', architecture: %s}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`, runnerOS, architecture))
			if mode == "passing" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				data, e := os.ReadFile(path)
				require.NoError(t, e)
				requireWriteFile(t, path, strings.Replace(string(data), "run: npm test", "run: npx wdio run wdio.conf.cjs", 1))
			}
			if mode == "unresolved suffix" {
				write("unknown-wrapper.js", `require('node:fs').writeFileSync('must-not-run','bad')`)
				write("package.json", `{"scripts":{"test":"wdio run wdio.conf.cjs && node unknown-wrapper.js"},"devDependencies":{"@wdio/cli":"9.20.0","dd-trace":"6.20.1"}}`)
			}
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--framework", "webdriverio")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "WebdriverIO")
				output, err = command(root, binary, "testdrive", "--framework", "webdriverio", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed, string(output))
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "webdriverio", "--command", "npm test", "--yes"}
			if mode == "passing" {
				args = []string{"testdrive", "--framework", "webdriverio", "--yes"}
			}
			if mode == "browser runner" || mode == "unresolved suffix" {
				args = append(args, "--check-only")
			}
			if mode == "timeout" {
				args = append(args, "--timeout", "2s")
			}
			if mode == "recovery" {
				signalFile := filepath.Join(t.TempDir(), "browser.json")
				t.Setenv("DDTEST_WDIO_RECOVERY_SIGNAL", signalFile)
				hanging := strings.Replace(config, "maxInstances:1", `maxInstances:1,before(){require('node:fs').writeFileSync(process.env.DDTEST_WDIO_RECOVERY_SIGNAL,JSON.stringify({worker:process.pid,address:browser.capabilities['goog:chromeOptions'].debuggerAddress}));return new Promise(()=>{})}`, 1)
				write("wdio.conf.cjs", hanging)
				logPath := filepath.Join(t.TempDir(), "killed-run.log")
				log, err := os.Create(logPath)
				require.NoError(t, err)
				existingProcessFolders = processFolders()
				killed := newCommand(root, binary, args...)
				killed.Stdout = log
				killed.Stderr = log
				require.NoError(t, killed.Start())
				finished := make(chan error, 1)
				go func() { finished <- killed.Wait() }()
				t.Cleanup(func() {
					_ = killed.Process.Kill()
					_ = log.Close()
					if t.Failed() {
						data, _ := os.ReadFile(logPath)
						t.Logf("hard-kill output: %s", data)
					}
				})
				var signal struct {
					Worker  int
					Address string
				}
				reached := false
				require.Eventually(t, func() bool {
					select {
					case <-finished:
						return true
					default:
					}
					data, _ := os.ReadFile(signalFile)
					reached = json.Unmarshal(data, &signal) == nil && signal.Address != ""
					return reached
				}, 2*time.Minute, 100*time.Millisecond)
				if !reached {
					data, readErr := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
					if readErr == nil {
						var failed validationResult
						if json.Unmarshal(data, &failed) == nil {
							for _, run := range failed.Runs {
								t.Logf("recovery startup %s: %s; native results: %s", run.Name, run.Diagnostic, run.ResultError)
							}
						}
					}
				}
				require.True(t, reached, "exited before browser hook")
				require.NoError(t, killed.Process.Kill())
				require.Error(t, <-finished)
				require.NoError(t, log.Close())
				require.Eventually(t, func() bool {
					connection, err := net.DialTimeout("tcp", signal.Address, 100*time.Millisecond)
					if err == nil {
						_ = connection.Close()
					}
					return err != nil
				}, 15*time.Second, 100*time.Millisecond, "owned Chrome must stop after owner loss")
				write("wdio.conf.cjs", config)
			}
			output, err = command(root, binary, args...)
			report := readValidationReport(t, root)
			if !report.Success && mode != "browser runner" && mode != "cloud grid" && mode != "occupied port" && mode != "unresolved suffix" && mode != "timeout" && mode != "missing native" {
				for _, run := range report.Runs {
					t.Logf("%s exit=%v result=%s diagnostic=%s", run.Name, run.ExitCode, run.ResultError, run.Diagnostic)
				}
			}
			if mode == "timeout" || mode == "missing native" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.Equal(t, "inconclusive", report.Compatibility.Status)
				require.NotNil(t, report.Cleanup)
				require.Equal(t, "passed", report.Cleanup.Status)
				return
			}
			if mode == "occupied port" || mode == "unresolved suffix" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.Empty(t, report.Runs)
				require.NoFileExists(t, filepath.Join(root, "must-not-run"))
				return
			}
			if mode == "browser runner" || mode == "cloud grid" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.Empty(t, report.Runs)
				if mode == "cloud grid" {
					require.Contains(t, report.Error, "Cloud grids")
				} else {
					require.Contains(t, report.Error, "local runner")
				}
				return
			}
			require.NoError(t, err, string(output))
			require.True(t, report.Success, string(output))
			if mode == "recovery" {
				entries, err := os.ReadDir(os.Getenv("TMPDIR"))
				require.NoError(t, err)
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "ddtest-") && !strings.Contains(entry.Name(), "locks") {
						t.Errorf("owned temporary storage remained: %s", entry.Name())
					}
				}
				// An unmarked folder is never recovered, so any new one is a leak;
				// marked folders of other repositories belong to concurrent runs.
				for folder := range processFolders() {
					if existingProcessFolders[folder] {
						continue
					}
					var owner sessionOwner
					data, err := os.ReadFile(filepath.Join(folder, sessionOwnerMarker))
					if err != nil || json.Unmarshal(data, &owner) != nil || owner.Root == canonicalWorkflowRoot(root) {
						t.Errorf("process ownership storage remained: %s", folder)
					}
				}
			}
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Len(t, report.Features, len(featurePolicies("webdriverio"))*len(report.Preflight.WebdriverIO.Capabilities))
			for _, feature := range report.Features {
				status := "passed"
				if feature.Name == "skipping" {
					status = "not supported"
				}
				require.Equal(t, status, feature.Status, feature)
			}
			for _, run := range report.Runs {
				if run.Name == "baseline" || run.Name == "reporting-only" {
					if mode == "existing failure" {
						require.Equal(t, 1, run.TestCounts.Failed)
					} else {
						expected := 2
						if mode == "TDD" || mode == "Jasmine expectations" {
							expected = 1
						}
						if mode == "repeat shard" || mode == "same browser capabilities" {
							expected = 4
						}
						require.Equal(t, expected, run.TestCounts.Passed)
					}
					require.NotEmpty(t, run.ExecutionContext)
				}
			}

			marker, e := os.ReadFile(filepath.Join(root, "customer-output.txt"))
			require.NoError(t, e)
			require.Equal(t, "keep", string(marker), "validation runs must not overwrite customer output")
			require.FileExists(t, htmlReportPath(root))
			require.NotNil(t, report.Cleanup)
			require.Equal(t, "passed", report.Cleanup.Status)
			require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
			require.NoDirExists(t, filepath.Join(root, "coverage"))
			entries, err := os.ReadDir(filepath.Join(root, "test"))
			require.NoError(t, err)
			expectedFiles := []string{"one.js"}
			if mode == "grouped root tests" || mode == "shard" {
				expectedFiles = append(expectedFiles, "two.js")
			}
			names := []string{}
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			require.ElementsMatch(t, expectedFiles, names, "no owned probe left")
			entries, err = os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			names = nil
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			require.ElementsMatch(t, []string{"report.html", "testdrive.json"}, names, "report pair only")

		})
	}
}

// Relative durations from CI; see integrationScenarios.
var webdriverIOWorkflowScenarios = integrationScenarios{
	names:   []string{"recovery", "passing", "existing failure", "capability specs", "Mocha retries", "spec retries", "repeat shard", "shard", "same browser capabilities", "grouped root tests", "TDD", "Jasmine expectations", "cloud grid", "browser runner", "timeout", "missing native", "occupied port", "unresolved suffix"},
	weights: map[string]int{"same browser capabilities": 18, "recovery": 13, "missing native": 3, "timeout": 2, "occupied port": 2, "cloud grid": 2, "browser runner": 1, "unresolved suffix": 1},
}
