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
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = "../.."
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, mode := range []string{"passing", "existing failure", "script selection", "CJS disabled globals", "ESM disabled globals"} {
		t.Run(mode, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			init := exec.CommandContext(ctx, "git", "init", "-q")
			init.Dir = root
			output, err = init.CombinedOutput()
			require.NoError(t, err, string(output))
			require.NoError(t, os.Symlink(modules, filepath.Join(root, "node_modules")))
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
				write("jest.config.js", `module.exports={injectGlobals:false,testEnvironment:'node',testRunner:'jest-circus/runner',watchman:false}`)
				write("one.test.cjs", `const {test,expect}=require('@jest/globals');test('adds',()=>expect(1+1).toBe(2))`)
			}
			if mode == "ESM disabled globals" {
				require.NoError(t, os.Remove(filepath.Join(root, "one.test.js")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "a")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "b")))
				require.NoError(t, os.Remove(filepath.Join(root, "jest.config.js")))
				require.NoError(t, os.RemoveAll(filepath.Join(root, "one.test.cjs")))
				write("package.json", `{"type":"module","scripts":{"test":"node --experimental-vm-modules node_modules/jest/bin/jest.js --runInBand --config jest.config.cjs"},"devDependencies":{"jest":"30.5.1"}}`)
				write("jest.config.cjs", `module.exports={injectGlobals:false,testEnvironment:'node',testRunner:'jest-circus/runner',watchman:false}`)
				write("one.test.mjs", `import {test,expect} from '@jest/globals';test('adds',()=>expect(1+1).toBe(2))`)
			}
			cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--command", "npm test", "--yes")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PWD="+root)
			output, err := cmd.CombinedOutput()
			disabledGlobals := mode == "CJS disabled globals" || mode == "ESM disabled globals"
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
					Name, Command  string
					ExitCode       *int `json:"exit_code"`
					TestEventCount int  `json:"test_event_count"`
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
				require.Equal(t, map[string]int{"probe-pass-control": 0, "probe-fail-control": 1}, controls)
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
