package testdrive

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/testdrive/workspace"
	"github.com/stretchr/testify/require"
)

// Only public flags available at this stack position; all telemetry goes to
// the local mock. Native Cucumber/NYC runs supply the evidence.
func TestPublicCucumberTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_CUCUMBER_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_CUCUMBER_NODE_MODULES to Cucumber, NYC and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	manifest, err := os.ReadFile(filepath.Join(modules, "@cucumber/cucumber/package.json"))
	require.NoError(t, err)
	var installed struct{ Version string }
	require.NoError(t, json.Unmarshal(manifest, &installed))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	var hookFile string
	newCommand := func(root, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "DD_") && !strings.HasPrefix(value, "DATADOG_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "DDTEST_CUCUMBER_HOOK_FILE="+hookFile)
		return cmd
	}
	command := func(root, name string, args ...string) ([]byte, error) {
		return newCommand(root, name, args...).CombinedOutput()
	}
	output, err := command("../..", "go", "build", "-o", binary, ".")
	require.NoError(t, err, string(output))
	for _, mode := range cucumberWorkflowScenarios.selected(t) {
		t.Run(mode, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(modules, "tsx")); mode == "source loader" && err != nil {
				t.Skip("tsx is not installed in DDTEST_CUCUMBER_NODE_MODULES")
			}
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			require.NoError(t, workspace.Copy(ctx, filepath.Dir(modules), root, skipIgnoredOutput))
			write := func(name, value string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
				requireWriteFile(t, filepath.Join(root, name), value)
			}
			hookFile = filepath.Join(t.TempDir(), "hooks.txt")
			appDirectory := root
			appWrite := write
			if mode == "package directory" {
				write("package.json", `{"private":true}`)
				appDirectory = filepath.Join(root, "packages/api")
				appWrite = func(name, value string) { write(filepath.Join("packages/api", name), value) }
			}
			appWrite("src/add.cjs", "module.exports=(a,b)=>a+b;\n")
			appWrite("features/one.feature", "Feature: Arithmetic\n  Scenario: Add\n    Given addition works\n")
			assertion := "require('node:assert').equal(require('../../src/add.cjs')(1,1),2)"
			if mode == "existing failure" {
				assertion = "require('node:assert').equal(require('../../src/add.cjs')(1,1),3)"
			}
			if mode == "no application coverage" || mode == "custom support coverage" {
				assertion = "require('node:assert').equal(1+1,2)"
			}
			appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>{`+assertion+`});`)
			if mode == "custom support coverage" {
				appWrite("support/steps.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>require('node:assert').equal(1+1,2));`)
			}
			if mode == "support library" {
				// The default support glob loads features/**/*.js, this library included.
				appWrite("features/support/lib/app.js", "module.exports=(a,b)=>a+b")
				appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>require('node:assert').equal(require('../support/lib/app.js')(1,1),2));`)
			}
			if mode == "support helper" {
				appWrite("support/steps.cjs", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>require('./assertions.cjs')());`)
				appWrite("support/assertions.cjs", "module.exports=()=>require('node:assert').equal(1+1,2)")
			}
			if mode == "feature application" {
				appWrite("src/features/payments/service.cjs", "module.exports=(a,b)=>a+b")
				appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>require('node:assert').equal(require('../../src/features/payments/service.cjs')(1,1),2));`)
			}
			appWrite("features/support/hooks.js", `const {Before,setWorldConstructor}=require('@cucumber/cucumber');class World{};setWorldConstructor(World);Before(function(){if(!(this instanceof World))throw new Error('World missing');require('node:fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE,'hook\n');});`)

			script := "nyc -- cucumber-js"
			if mode == "custom support coverage" {
				script += " --require support/steps.js"
			}
			if mode == "support helper" {
				script += " --require support/steps.cjs"
			}
			switch mode {
			case "recovery":
				t.Setenv("TMPDIR", t.TempDir())
				t.Setenv("DDTEST_CUCUMBER_PID_LOG", filepath.Join(t.TempDir(), "pids"))
				appWrite("features/support/owner.cjs", `const {BeforeAll}=require('@cucumber/cucumber');BeforeAll(async function(){require('node:fs').appendFileSync(process.env.DDTEST_CUCUMBER_PID_LOG,JSON.stringify([process.pid,process.ppid])+'\n');if(process.env.DDTEST_PROBE_MODE==='fail')await new Promise(resolve=>setTimeout(resolve,1500))});`)
			case "timeout":
				appWrite("features/support/hang.cjs", `const {BeforeAll}=require('@cucumber/cucumber');BeforeAll(()=>{setInterval(()=>{},1000);return new Promise(()=>{})})`)
			case "scoped options":
				script = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-}" nyc -- cucumber-js`
			case "c8":
				script = "c8 -- cucumber-js"
			case "thresholds":
				appWrite(".nycrc", `{"all":true,"check-coverage":true,"functions":95}`)
				appWrite("src/unused.cjs", "module.exports=()=>42")
			case "native retries", "profile retries":
				script += " --retry 2"
				if mode == "profile retries" {
					script = "nyc -- cucumber-js"
					appWrite("cucumber.cjs", `module.exports={default:{retry:1}}`)
				}
				appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');let attempts=0;Given('addition works',()=>{require('../../src/add.cjs')(1,1);if(++attempts===1)throw Error('native first attempt')})`)
			case "CLI selectors":
				script += " --tags @smoke --name '^Add$' features/one.feature"
				appWrite("features/one.feature", "@smoke\nFeature: Arithmetic\n  Scenario: Add\n    Given addition works\n")
			case "profile selectors":
				appWrite("cucumber.cjs", `module.exports={default:{tags:'@smoke',paths:['features/**/*.feature'],name:['^Add$']}}`)
				appWrite("features/one.feature", "@smoke\nFeature: Arithmetic\n  Scenario: Add\n    Given addition works\n")
			case "catch-all":
				script += " --require support/steps.cjs"
				appWrite("support/steps.cjs", `const {Given,Before,setWorldConstructor}=require('@cucumber/cucumber');setWorldConstructor(class{constructor(){this.value=42}});Before(function(){if(this.value!==42)throw Error('World lost');this.hook=true;require('node:fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE,'hook\n')});Given(/^(.*)$/,function(text){if(!this.hook)throw Error('hook lost');require('../src/add.cjs')(1,1)})`)
			case "parallel":
				script += " --parallel 2"
			case "outlines":
				appWrite("features/one.feature", "Feature: Arithmetic\n Scenario Outline: Add\n  Given addition works\n Examples:\n  | input |\n  | 1 |\n  | 2 |\n")
			case "pending", "pending non-strict", "undefined", "undefined non-strict", "ambiguous":
				if strings.Contains(mode, "non-strict") {
					script += " --no-strict"
				}
				status := strings.Fields(mode)[0]
				appWrite("features/one.feature", "Feature: Arithmetic\n Scenario: Add\n  Given addition works\n Scenario: Other\n  Given special status\n")
				extra := ""
				if status == "pending" {
					extra = `Given('special status',()=> 'pending');`
				}
				if status == "ambiguous" {
					extra = `Given('special status',()=>{});Given(/^special status$/,()=>{});`
				}
				appWrite("features/step_definitions/one.js", `const {Given}=require('@cucumber/cucumber');Given('addition works',()=>require('../../src/add.cjs')(1,1));`+extra)
			case "child process":
				// A hook's helper process in another folder cannot resolve Cucumber.
				appWrite("features/support/child.js", `const {BeforeAll}=require('@cucumber/cucumber');BeforeAll(()=>{require('node:child_process').execFileSync(process.execPath,['-e','process.exit(0)'],{cwd:require('node:os').tmpdir()})})`)
			case "profile run options", "CLI run options":
				// Without forceExit the interval keeps Cucumber alive; the World
				// requires its parameter. Probes need both, like the suite.
				baseURL := map[string]string{"profile run options": "http://profile.test", "CLI run options": "http://cli.test"}[mode]
				appWrite("features/support/hooks.js", `const {Before,BeforeAll,setWorldConstructor}=require('@cucumber/cucumber');class World{constructor({parameters}){if(parameters.baseUrl!==`+strconv.Quote(baseURL)+`)throw new Error('baseUrl world parameter missing')}};setWorldConstructor(World);BeforeAll(()=>{setInterval(()=>{},1000)});Before(function(){if(!(this instanceof World))throw new Error('World missing');require('node:fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE,'hook\n');});`)
				if mode == "profile run options" {
					appWrite("cucumber.cjs", `module.exports={default:{forceExit:true,worldParameters:{baseUrl:'http://profile.test'}}}`)
				} else {
					script += ` --world-parameters '{"baseUrl":"http://cli.test"}' --exit`
				}
			case "probe lifecycle":
				// The suite's own posttest removes it; keep it out of coverage.
				appWrite(".nycrc", `{"exclude":["features/support/generated.js","fixtures/**"]}`)
				appWrite("fixtures/generated.js", `const {Before}=require('@cucumber/cucumber');Before(function(){require('node:fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE,'generated\n')})`)
			case "source loader":
				// Modeled on astrologyjs: TypeScript support imports `.js` paths that
				// only exist as `.ts`, resolved by the script's own tsx loader.
				script = `NODE_OPTIONS="${NODE_OPTIONS:-} ${DD_CUCUMBER_NODE_OPTIONS:-} --import tsx" nyc -- cucumber-js --import features/steps.ts --import features/support/hooks.ts`
				appWrite("features/support/hooks.ts", `import {Before,setWorldConstructor} from "@cucumber/cucumber";class World{};setWorldConstructor(World);Before(function(){if(!(this instanceof World))throw Error("World lost");process.getBuiltinModule('fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE!,'hook\n')})`)
				appWrite("src/add.ts", "export default (a: number, b: number): number => a + b")
				appWrite("features/steps.ts", `import {Given} from '@cucumber/cucumber';import add from '../src/add.js';Given('addition works',()=>{if(add(1,1)!==2)throw Error('bad add')})`)
			case "ESM", "CJS ESM import":
				script += " --import features/steps.mjs --import features/support/hooks.mjs"
				appWrite("features/support/hooks.mjs", `import {Before,setWorldConstructor} from "@cucumber/cucumber";class World{};setWorldConstructor(World);Before(function(){if(!(this instanceof World))throw Error("World lost");process.getBuiltinModule('fs').appendFileSync(process.env.DDTEST_CUCUMBER_HOOK_FILE,'hook\n')})`)
				appWrite("src/add.mjs", "export default (a,b)=>a+b")
				appWrite("features/steps.mjs", `import {Given} from '@cucumber/cucumber';import add from '../src/add.mjs';Given('addition works',()=>{if(add(1,1)!==2)throw Error('bad add')})`)
			}
			pkg := map[string]any{"scripts": map[string]string{"test": script}, "devDependencies": map[string]string{"@cucumber/cucumber": installed.Version, "nyc": "17.1.0", "dd-trace": "6.20.1"}}
			if mode == "lifecycle" {
				pkg["scripts"] = map[string]string{"pretest": script, "test": "echo done"}
			}
			if mode == "probe lifecycle" {
				// npm creates support code before the runner and removes it after.
				pkg["scripts"] = map[string]string{"pretest": "cp fixtures/generated.js features/support/generated.js", "test": script, "posttest": "rm features/support/generated.js"}
			}
			if mode == "nested alias" {
				pkg["scripts"] = map[string]string{"test": "npm run scenarios", "scenarios": script}
			}
			if mode == "ESM" {
				pkg["type"] = "module"
			}
			manifest, err := json.Marshal(pkg)
			require.NoError(t, err)
			appWrite("package.json", string(manifest))
			write(".github/workflows/ci.yml", `jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24', architecture: x64}
      - uses: datadog/test-visibility-github-action@v3
        with: {languages: js, js-tracer-version: '6.20.1'}
      - run: npm test
        env: {NODE_OPTIONS: '-r ${{ env.DD_TRACE_PACKAGE }} --import ${{ env.DD_TRACE_ESM_IMPORT }}'}
`)
			if mode == "scoped options" || mode == "source loader" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "env: {NODE_OPTIONS:", "env: {DD_CUCUMBER_NODE_OPTIONS:")), 0600))
			}
			if mode == "ESM" || mode == "CJS ESM import" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "--import ${{ env.DD_TRACE_ESM_IMPORT }}", "--import ${{ env.DD_TRACE_ESM_IMPORT }} --experimental-loader @istanbuljs/esm-loader-hook")), 0600))
			}
			if runtime.GOOS == "darwin" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(strings.ReplaceAll(string(body), "ubuntu-latest", "macos-latest"), "architecture: x64", "architecture: arm64")), 0600))
			}
			if mode == "package directory" {
				path := filepath.Join(root, ".github/workflows/ci.yml")
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, []byte(strings.Replace(string(data), "      - run: npm test", "      - run: npm test\n        working-directory: packages/api", 1)), 0600))
			}
			output, err = command(root, "git", "init", "-q")
			require.NoError(t, err, string(output))
			if mode == "passing" {
				output, err = command(root, binary, "onboard", "--instructions", "--framework", "cucumber")
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "NYC")
				output, err = command(root, binary, "testdrive", "--framework", "cucumber", "--all", "--check-only", "--yes")
				require.NoError(t, err, string(output))
				check := readValidationReport(t, root)
				require.True(t, check.ChecksPassed, string(output))
				require.Empty(t, check.Runs)
			}
			args := []string{"testdrive", "--framework", "cucumber", "--all", "--yes"}
			if mode == "existing failure" {
				args = []string{"testdrive", "--framework", "cucumber", "--command", "npm test", "--yes"}
			}
			if mode == "timeout" {
				args = append(args, "--timeout", "2s")
			}
			if mode == "profile run options" || mode == "CLI run options" {
				args = append(args, "--timeout", "3m")
			}
			if mode == "recovery" {
				logPath := filepath.Join(t.TempDir(), "killed.log")
				log, err := os.Create(logPath)
				require.NoError(t, err)
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
						t.Logf("killed trial: %s", data)
					}
				})
				reached := false
				require.Eventually(t, func() bool {
					select {
					case <-finished:
						return true
					default:
					}
					data, _ := os.ReadFile(logPath)
					audit, _ := os.ReadFile(os.Getenv("DDTEST_CUCUMBER_PID_LOG"))
					reached = strings.Contains(string(data), "Running quarantine") && len(strings.Split(strings.TrimSpace(string(audit)), "\n")) >= 7
					return reached
				}, 2*time.Minute, 25*time.Millisecond)
				require.True(t, reached, "validation exited before an active quarantine process")
				require.NoError(t, killed.Process.Kill())
				require.Error(t, <-finished)
				require.NoError(t, log.Close())
				data, err := os.ReadFile(os.Getenv("DDTEST_CUCUMBER_PID_LOG"))
				require.NoError(t, err)
				var pids []int
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var pair []int
					require.NoError(t, json.Unmarshal([]byte(line), &pair))
					pids = append(pids, pair...)
				}
				require.Eventually(t, func() bool {
					for _, pid := range pids {
						state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
						if err == nil && strings.TrimSpace(string(state)) != "" && !strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
							return false
						}
					}
					return true
				}, 10*time.Second, 100*time.Millisecond)
			}
			output, err = command(root, binary, args...)
			report := readValidationReport(t, root)
			if mode == "passing" {
				require.NotNil(t, report.Preflight)
				require.Equal(t, installed.Version, report.Preflight.Version)
			}
			if !report.Success && mode != "timeout" && mode != "no application coverage" && mode != "custom support coverage" && mode != "support library" && mode != "support helper" && mode != "parallel" && mode != "c8" && mode != "probe lifecycle" {
				details, _ := json.MarshalIndent(report, "", "  ")
				t.Logf("Report: %s", details)
				html, _ := os.ReadFile(htmlReportPath(root))
				for _, match := range regexp.MustCompile(`.{0,50}(?:Error:|Cannot find|MODULE_NOT_FOUND).{0,500}`).FindAllString(string(html), -1) {
					t.Logf("HTML diagnostic: %s", match)
				}
			}
			if mode == "timeout" {
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.NotEmpty(t, report.Runs)
				require.NotZero(t, *report.Runs[0].ExitCode)
				require.Equal(t, "inconclusive", report.Compatibility.Status)
				require.Len(t, report.Configurations, 1)
				require.Equal(t, "baseline exceeded --timeout 2s; raise --timeout or fix the hanging step.", report.Configurations[0].Compatibility.Reason)
				require.Equal(t, "baseline exceeded --timeout 2s; raise --timeout or fix the hanging step", report.Runs[0].Timeout)
				blockers := strings.Join(summarizeValidation(report).BlockingChecks, "\n")
				require.Contains(t, blockers, "baseline exceeded --timeout 2s")
				require.NotContains(t, blockers, "real-suite coverage was not observed")
				require.NotContains(t, blockers, "/quarantine")
				require.NotNil(t, report.Cleanup)
				require.Equal(t, "passed", report.Cleanup.Status)
				require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
				require.NoDirExists(t, filepath.Join(root, ".testoptimization/.probes"))
				return
			}
			switch mode {
			case "parallel":
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.True(t, missingRealCoverage(report))
				require.Contains(t, report.Runs[1].Coverage.Reason, "the selected dd-trace sends no suite coverage from parallel workers")
			case "c8":
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.True(t, missingRealCoverage(report))
				for _, run := range report.Runs {
					require.NotContains(t, run.Command, "nyc/bin/")
				}
			case "probe lifecycle":
				// Discovery leaves file-changing hooks for review; probes still run.
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				for _, check := range report.Summary.BlockingChecks {
					require.Contains(t, check, "CI file or dependency changes require separate validation")
				}
			case "no application coverage", "custom support coverage", "support library", "support helper":
				require.Error(t, err, string(output))
				require.False(t, report.Success)
				require.True(t, missingRealCoverage(report))
				if file := map[string]string{"support library": "features/support/lib/app.js", "support helper": "support/assertions.cjs"}[mode]; file != "" {
					reason := missingRealCoverageReason(report)
					require.Contains(t, reason, "Coverage was observed only in Cucumber support code", string(output))
					require.Contains(t, reason, file)
				}
			default:
				require.NoError(t, err, string(output))
				require.True(t, report.Success, string(output))
			}
			require.Equal(t, "compatible", report.Compatibility.Status, string(output))
			require.Len(t, report.Features, 6)
			// Parallel outcomes depend on the worker transport of each Cucumber
			// major; ones that could not be checked are inconclusive, not failed.
			parallelOutcomes := map[string]map[string]string{
				"10": {"auto-retries": "passed", "early-flake-detection": "inconclusive", "skipping": "passed", "quarantine": "inconclusive", "disabled": "inconclusive", "attempt-to-fix": "inconclusive"},
				"13": {"auto-retries": "passed", "early-flake-detection": "passed", "skipping": "passed", "quarantine": "passed", "disabled": "passed", "attempt-to-fix": "passed"},
			}[strings.Split(installed.Version, ".")[0]]
			for _, feature := range report.Features {
				switch {
				case mode == "parallel" && parallelOutcomes != nil:
					require.Equal(t, parallelOutcomes[feature.Name], feature.Status, feature)
					if feature.Status != "passed" {
						require.Contains(t, feature.Reason, "parallel mode")
					}
				case mode == "parallel" && feature.Status != "passed":
					require.Equal(t, "inconclusive", feature.Status, feature)
					require.Contains(t, feature.Reason, "parallel mode")
				default:
					require.Equal(t, "passed", feature.Status, feature)
				}
			}
			require.FileExists(t, htmlReportPath(root))
			require.NotNil(t, report.Cleanup)
			require.Equal(t, "passed", report.Cleanup.Status)
			if mode == "recovery" {
				entries, err := os.ReadDir(os.Getenv("TMPDIR"))
				require.NoError(t, err)
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), "ddtest-") && !strings.Contains(entry.Name(), "locks") {
						t.Errorf("owned storage remains: %s", entry.Name())
					}
				}
			}
			require.NoDirExists(t, filepath.Join(root, ".nyc_output"))
			require.NoDirExists(t, filepath.Join(root, "coverage"))
			entries, err := os.ReadDir(filepath.Join(appDirectory, "features"))
			require.NoError(t, err)
			for _, entry := range entries {
				require.NotContains(t, entry.Name(), "ddtest")
				require.NotContains(t, entry.Name(), ".steps.cjs")
				require.NotContains(t, entry.Name(), ".config.cjs")
			}
			if mode == "probe lifecycle" {
				require.NoFileExists(t, filepath.Join(appDirectory, "features/support/generated.js"), "posttest runs after the probes")
				hooks, err := os.ReadFile(hookFile)
				require.NoError(t, err)
				require.Greater(t, strings.Count(string(hooks), "generated\n"), 2, "feature controls must load support code that pretest creates")
			}
			if mode != "custom support coverage" && mode != "support helper" {
				hooks, err := os.ReadFile(hookFile)
				require.NoError(t, err)
				require.Greater(t, strings.Count(string(hooks), "hook\n"), 2, "feature controls must load the same default hooks as the paired suite")
			}
		})
	}
}

// Relative durations from CI; see integrationScenarios.
var cucumberWorkflowScenarios = integrationScenarios{
	names:   []string{"passing", "lifecycle", "nested alias", "existing failure", "no application coverage", "package directory", "custom support coverage", "support library", "support helper", "feature application", "thresholds", "native retries", "profile retries", "CLI selectors", "profile selectors", "catch-all", "parallel", "outlines", "pending", "pending non-strict", "undefined", "undefined non-strict", "ambiguous", "ESM", "CJS ESM import", "scoped options", "source loader", "c8", "timeout", "recovery", "child process", "profile run options", "CLI run options", "probe lifecycle"},
	weights: map[string]int{"recovery": 26, "ESM": 20, "CJS ESM import": 18, "parallel": 15, "c8": 14, "child process": 13, "timeout": 6},
}
