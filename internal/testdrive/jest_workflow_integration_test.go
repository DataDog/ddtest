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

// Exercise the public CLI with the runner/tracer dependencies selected by CI.
// Do not require onboard --apply or the agent coordinator before they ship.
func TestPublicJestTestdrive(t *testing.T) {
	modules := os.Getenv("DDTEST_JEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_JEST_NODE_MODULES to installed Jest/dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = "../.."
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	modes := []string{"passing", "existing failure", "script selection", "multiple selected projects", "sharded threshold script", "native retries", "temporary tracer", "missing Jest", "script startup override", "controlled mismatch", "CJS disabled globals", "ESM disabled globals"}
	if _, err := os.Stat(filepath.Join(modules, "ts-jest")); err == nil {
		modes = append(modes, "TypeScript", "TypeScript isolated modules", "TypeScript disabled globals")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			init := exec.CommandContext(ctx, "git", "init", "-q")
			init.Dir = root
			output, err = init.CombinedOutput()
			require.NoError(t, err, string(output))
			if mode == "temporary tracer" {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "node_modules"), 0700))
				entries, err := os.ReadDir(modules)
				require.NoError(t, err)
				for _, entry := range entries {
					if entry.Name() == "dd-trace" {
						continue
					}
					require.NoError(t, os.Symlink(filepath.Join(modules, entry.Name()), filepath.Join(root, "node_modules", entry.Name())))
				}
			} else if mode != "missing Jest" {
				require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
			}
			write := func(name, value string) {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(value), 0600))
			}
			write("package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`)
			write("add.js", `module.exports=(a,b)=>a+b;`)
			write("one.test.js", `test('adds',()=>expect(require('./add')(1,1)).toBe(2));`)
			write("jest.config.js", `module.exports={testEnvironment:'node',testRunner:'jest-circus/runner',watchman:false};`)
			if mode == "existing failure" {
				write("one.test.js", `test('adds',()=>expect(require('./add')(1,1)).toBe(3));`)
			}
			if mode == "script selection" {
				require.NoError(t, os.Remove(filepath.Join(root, "one.test.js")))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "a"), 0700))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "b"), 0700))
				write("a/one.test.cjs", `test('adds',()=>expect(require('../add')(1,1)).toBe(2))`)
				write("b/other.test.cjs", `throw new Error('excluded project must not load')`)
				write("package.json", `{"scripts":{"test":"jest --runInBand --selectProjects A --outputFile=customer.json a/one.test.cjs"},"devDependencies":{"jest":"30.5.1"}}`)
				write("jest.config.js", `module.exports={projects:[{displayName:'A',testMatch:['<rootDir>/a/*.test.cjs'],testEnvironment:'node',watchman:false},{displayName:'B',testMatch:['<rootDir>/b/*.test.cjs'],testEnvironment:'node',watchman:false}]}`)
			}
			if mode == "CJS disabled globals" {
				require.NoError(t, os.Remove(filepath.Join(root, "one.test.js")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "a")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "b")))
				write("package.json", `{"scripts":{"test":"jest --runInBand"},"devDependencies":{"jest":"30.5.1"}}`)
				write("jest.config.js", `module.exports={injectGlobals:false,testMatch:['**/*.test.cjs'],testEnvironment:'node',testRunner:'jest-circus/runner',watchman:false}`)
				write("one.test.cjs", `const {test,expect}=require('@jest/globals');test('adds',()=>expect(1+1).toBe(2))`)
			}
			if mode == "ESM disabled globals" {
				require.NoError(t, os.Remove(filepath.Join(root, "one.test.js")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "a")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "b")))
				require.NoError(t, os.Remove(filepath.Join(root, "jest.config.js")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "one.test.cjs")))
				write("package.json", `{"type":"module","scripts":{"test":"node --experimental-vm-modules node_modules/jest/bin/jest.js --runInBand --config jest.config.cjs"},"devDependencies":{"jest":"30.5.1"}}`)
				write("jest.config.cjs", `module.exports={injectGlobals:false,testMatch:['**/*.test.mjs'],testEnvironment:'node',testRunner:'jest-circus/runner',watchman:false}`)
				write("one.test.mjs", `import {test,expect} from '@jest/globals';test('adds',()=>expect(1+1).toBe(2))`)
			}

			if mode == "multiple selected projects" {
				write("package.json", `{"scripts":{"test":"jest --runInBand --selectProjects A B"},"devDependencies":{"jest":"*"}}`)
				write("jest.config.js", `module.exports={projects:[{displayName:'A',testMatch:['<rootDir>/*.test.js'],testEnvironment:'node'},{displayName:'B',testMatch:['<rootDir>/*.test.js'],testEnvironment:'node'}]}`)
			}
			if mode == "sharded threshold script" {
				write("two.test.js", `test('also adds',()=>expect(require('./add')(2,2)).toBe(4));`)
				write("package.json", `{"scripts":{"test":"jest --runInBand --shard=2/2 --coverage --coverageThreshold=\"{\\\"global\\\":{\\\"lines\\\":100}}\""},"devDependencies":{"jest":"*"}}`)
			}
			if mode == "native retries" {
				write("retry-setup.js", `jest.retryTimes(2);`)
				write("jest.config.js", `module.exports={testEnvironment:'node',setupFilesAfterEnv:['<rootDir>/retry-setup.js']}`)
				write("one.test.js", `let attempts=0;test('adds',()=>{if(++attempts===1)throw new Error('native retry');expect(require('./add')(1,1)).toBe(2)});`)
			}
			if strings.HasPrefix(mode, "TypeScript") {
				require.NoError(t, os.Remove(filepath.Join(root, "one.test.js")))
				isolated := "true"
				if mode == "TypeScript" {
					isolated = "false"
				}
				write("tsconfig.json", `{"compilerOptions":{"strict":true,"types":["jest"],"isolatedModules":`+isolated+`,"target":"ES2020","module":"commonjs"}}`)
				write("jest.config.js", `module.exports={preset:'ts-jest',testEnvironment:'node'}`)
				write("add.ts", `export const add=(a:number,b:number)=>a+b;`)
				write("one.test.ts", `import {add} from './add';test('adds',()=>expect(add(1,1)).toBe(2));`)
				if mode == "TypeScript disabled globals" {
					write("jest.config.js", `module.exports={preset:'ts-jest',injectGlobals:false,testEnvironment:'node'}`)
					write("one.test.ts", `import {test,expect} from '@jest/globals';import {add} from './add';test('adds',()=>expect(add(1,1)).toBe(2));`)
				}
			}
			if mode == "controlled mismatch" {
				write("one.test.js", `test('changes only with instrumentation',()=>expect(process.env.DD_CIVISIBILITY_ENABLED).toBe('false'));`)
			}
			if mode == "script startup override" {
				write("package.json", `{"scripts":{"test":"NODE_OPTIONS=--experimental-vm-modules jest --runInBand"},"devDependencies":{"jest":"*"}}`)
			}
			versionData, err := os.ReadFile(filepath.Join(modules, "dd-trace", "package.json"))
			require.NoError(t, err)
			var tracer struct{ Version string }
			require.NoError(t, json.Unmarshal(versionData, &tracer))
			cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--command", "npm test", "--tracer-version", tracer.Version, "--yes")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PWD="+root)
			output, err := cmd.CombinedOutput()
			if mode == "missing Jest" || mode == "script startup override" {
				require.Error(t, err, string(output))
				data, readErr := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
				require.NoError(t, readErr)
				if mode == "missing Jest" {
					require.Contains(t, string(data), "jest_not_installed")
					require.NoDirExists(t, filepath.Join(root, "node_modules"))
				} else {
					require.Contains(t, string(data), "NODE_OPTIONS")
				}
				return
			}
			if mode == "controlled mismatch" {
				require.Error(t, err, string(output))
				reportPath := filepath.Join(root, ".testoptimization/testdrive.json")
				data, readErr := os.ReadFile(reportPath)
				require.NoError(t, readErr)
				var report struct {
					Compatibility   struct{ Status string }
					Summary         struct{ Status string }
					ReportingCounts struct {
						Executions int `json:"test_executions"`
					} `json:"reporting_counts"`
					Runs []struct{ Role string }
				}
				require.NoError(t, json.Unmarshal(data, &report))
				require.Equal(t, "suspected regression", report.Compatibility.Status)
				require.Equal(t, "FAILED", report.Summary.Status)
				require.Equal(t, 1, report.ReportingCounts.Executions)
				repeats := 0
				for _, run := range report.Runs {
					if run.Role == "repeat" {
						repeats++
					}
				}
				require.Equal(t, 2, repeats)
				htmlPath := filepath.Join(root, ".testoptimization/report.html")
				before, readErr := os.Stat(htmlPath)
				require.NoError(t, readErr)
				check := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--command", "npm test", "--check-only", "--yes")
				check.Dir = root
				check.Env = append(os.Environ(), "PWD="+root)
				checkOutput, checkErr := check.CombinedOutput()
				require.NoError(t, checkErr, string(checkOutput))
				after, readErr := os.Stat(htmlPath)
				require.NoError(t, readErr)
				require.Equal(t, before.ModTime(), after.ModTime())
				data, readErr = os.ReadFile(reportPath)
				require.NoError(t, readErr)
				var historical struct {
					Last struct {
						Status  string
						Current bool
					} `json:"last_execution"`
				}
				require.NoError(t, json.Unmarshal(data, &historical))
				require.Equal(t, "failed", historical.Last.Status)
				require.False(t, historical.Last.Current)
				return
			}
			disabledGlobals := strings.Contains(mode, "disabled globals")
			if !disabledGlobals {
				require.NoError(t, err, string(output))
			}
			commandSucceeded := err == nil
			data, err := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
			require.NoError(t, err)
			var report struct {
				Success       bool `json:"success"`
				LocalSuccess  bool `json:"local_success"`
				Compatibility struct {
					Status string `json:"status"`
				} `json:"compatibility"`
				Cleanup struct {
					Status string `json:"status"`
				} `json:"cleanup"`
				Features      []struct{ Name, Status, Project string } `json:"features"`
				ProjectChecks []struct {
					Project  string
					Excluded bool
				} `json:"project_checks"`
				Runs []struct {
					Name, Command      string
					ExitCode           *int `json:"exit_code"`
					TestEventCount     int  `json:"test_event_count"`
					Role               string
					ThresholdsDisabled bool `json:"probe_coverage_thresholds_disabled"`
				} `json:"runs"`
			}
			require.NoError(t, json.Unmarshal(data, &report))
			if !disabledGlobals {
				require.True(t, report.Success, string(data))
				require.True(t, report.LocalSuccess)
			} else {
				// This pinned tracer registers EFD/attempt-to-fix retries through global.test.
				// With injectGlobals:false these features can truthfully fail despite valid imports.
				// The API-import fix must prove both controls ran and retain actual failures.
				require.Equal(t, commandSucceeded, report.Success)
				controls := map[string]int{}
				for _, run := range report.Runs {
					if strings.Contains(run.Name, "probe-pass-control") || strings.Contains(run.Name, "probe-fail-control") {
						require.NotNil(t, run.ExitCode)
						controls[filepath.Base(run.Name)] = *run.ExitCode
					}
				}
				require.Equal(t, map[string]int{"probe-pass-control": 0, "probe-fail-control": 1}, controls, string(output))
			}
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Equal(t, "passed", report.Cleanup.Status)
			features := map[string]string{}
			for _, f := range report.Features {
				features[f.Name] = f.Status
			}
			if !disabledGlobals {
				require.Equal(t, map[string]string{"auto-retries": "passed", "early-flake-detection": "passed", "skipping": "passed", "quarantine": "passed", "disabled": "passed", "attempt-to-fix": "passed"}, features)
			} else {
				require.Len(t, features, 6)
				for _, name := range []string{"auto-retries", "skipping", "quarantine", "disabled"} {
					require.Equal(t, "passed", features[name])
				}
				allPassed := true
				for _, name := range []string{"auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix"} {
					status := features[name]
					require.Contains(t, []string{"passed", "failed"}, status, "valid imported-API probe controls must not become inconclusive")
					allPassed = allPassed && status == "passed"
				}
				require.Equal(t, allPassed, report.Success)
				require.Equal(t, allPassed, report.LocalSuccess)
			}
			require.GreaterOrEqual(t, len(report.Runs), 2)
			require.Equal(t, "baseline", report.Runs[0].Name)
			require.Equal(t, 0, report.Runs[0].TestEventCount)
			require.Equal(t, "reporting-only", report.Runs[1].Name)
			require.Positive(t, report.Runs[1].TestEventCount)
			require.NotEmpty(t, report.Runs[0].Command)
			require.NotEmpty(t, report.Runs[1].Command)
			if mode == "existing failure" {
				require.Equal(t, 1, *report.Runs[0].ExitCode)
				require.Equal(t, 1, *report.Runs[1].ExitCode)
			}
			if mode == "script selection" {
				require.Len(t, report.ProjectChecks, 2)
				require.Equal(t, "B", report.ProjectChecks[1].Project)
				require.True(t, report.ProjectChecks[1].Excluded)
				for _, feature := range report.Features {
					require.Equal(t, "A", feature.Project)
				}
			}
			require.NoFileExists(t, filepath.Join(root, "customer.json"), "runner JSON output must stay in owned temporary storage")
			entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
			require.NoError(t, err)
			var files []string
			for _, entry := range entries {
				files = append(files, entry.Name())
			}
			require.ElementsMatch(t, []string{"testdrive.json", "report.html"}, files)
			entries, err = os.ReadDir(root)
			require.NoError(t, err)
			for _, entry := range entries {
				require.False(t, strings.HasPrefix(entry.Name(), "ddtest"), "probe must be removed")
			}
		})
	}
}
