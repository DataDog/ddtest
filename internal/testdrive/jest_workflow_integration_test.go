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
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "ddtest")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = "../.."
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
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
	for _, mode := range []string{"passing", "existing failure"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "existing failure" {
				write("one.test.js", `test('adds',()=>expect(require('./add')(1,1)).toBe(3));`)
			}
			cmd := exec.CommandContext(ctx, binary, "testdrive", "--framework", "jest", "--command", "npm test", "--yes")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PWD="+root)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, string(output))
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
				Features []struct{ Name, Status string } `json:"features"`
				Runs     []struct {
					Name, Command  string
					ExitCode       *int `json:"exit_code"`
					TestEventCount int  `json:"test_event_count"`
				} `json:"runs"`
			}
			require.NoError(t, json.Unmarshal(data, &report))
			require.True(t, report.Success, string(data))
			require.True(t, report.LocalSuccess)
			require.Equal(t, "compatible", report.Compatibility.Status)
			require.Equal(t, "passed", report.Cleanup.Status)
			features := map[string]string{}
			for _, f := range report.Features {
				features[f.Name] = f.Status
			}
			require.Equal(t, map[string]string{"auto-retries": "passed", "early-flake-detection": "passed", "skipping": "passed", "quarantine": "passed", "disabled": "passed", "attempt-to-fix": "passed"}, features)
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
