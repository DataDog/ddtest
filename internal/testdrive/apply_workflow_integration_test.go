package testdrive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPublicOnboardApply(t *testing.T) {
	modules := os.Getenv("DDTEST_VITEST_NODE_MODULES")
	if modules == "" {
		t.Skip("set DDTEST_VITEST_NODE_MODULES to Vitest and dd-trace dependencies")
	}
	modules, err := filepath.Abs(modules)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
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
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, copyConfiguration(ctx, filepath.Dir(modules), root, false))
	write := func(name, value string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		requireWriteFile(t, filepath.Join(root, name), value)
	}
	write(".gitignore", "node_modules/\n")
	write("package.json", `{"private":true,"type":"module","scripts":{"test":"vitest run"},"devDependencies":{"vitest":"4.1.11","dd-trace":"6.20.1"}}`)
	write("vitest.config.js", `export default { test: { dir: 'src', pool: 'forks', maxWorkers: 1 } }`)
	write("src/add.test.js", `import {test,expect} from 'vitest'; test('adds',()=>expect(1+1).toBe(2))`)
	workflow := `name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-node@v4
        with: {node-version: '24'}
      - run: npm test # keep the customer command
`
	path := filepath.Join(root, ".github/workflows/test.yml")
	write(".github/workflows/test.yml", workflow)
	output, err = command(root, "git", "init", "-q")
	require.NoError(t, err, string(output))
	for _, args := range [][]string{{"onboard", "--framework", "vitest"}, {"onboard", "--framework", "vitest", "--apply"}} {
		output, err = command(root, binary, args...)
		if len(args) == 3 {
			require.NoError(t, err, string(output))
		} else {
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "Preview only")
		}
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Equal(t, workflow, string(data))
	}
	// Unsupported layouts are rejected before changing any source file.
	write(".github/workflows/test.yml", strings.Replace(workflow, "      - run: npm test", "      - if: github.event_name == 'push'\n        run: npm test", 1))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	output, err = command(root, binary, "onboard", "--framework", "vitest", "--apply", "--yes", "--allow-dirty")
	require.Error(t, err, string(output))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	write(".github/workflows/test.yml", workflow)
	output, err = command(root, binary, "onboard", "--framework", "vitest", "--apply", "--yes", "--allow-dirty")
	require.NoError(t, err, string(output))
	assertAppliedWorkflowActionlint(t, path)
	report := readValidationReport(t, root)
	require.True(t, report.Success, string(output))
	require.Equal(t, "compatible", report.Compatibility.Status)
	require.NotEmpty(t, report.Features)
	for _, feature := range report.Features {
		require.Equal(t, "passed", feature.Status, feature)
	}
	require.Equal(t, "passed", report.Cleanup.Status)
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(after), "js-tracer-version: 6.20.1")
	require.Contains(t, string(after), "run: npm test # keep the customer command")
	require.Contains(t, string(output), "before ")
	changes, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	require.NoError(t, err)
	require.Contains(t, string(changes), "/.testoptimization/")
	output, err = command(root, binary, "onboard", "--framework", "vitest", "--apply", "--yes", "--allow-dirty")
	require.NoError(t, err, string(output))
	repeated, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, after, repeated)
	assertAppliedWorkflowActionlint(t, path)
	require.NotContains(t, string(output), "Updated ")
	entries, err := os.ReadDir(filepath.Join(root, ".testoptimization"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.ElementsMatch(t, []string{"report.html", "testdrive.json"}, names)
	require.FileExists(t, htmlReportPath(root))
	require.FileExists(t, filepath.Join(root, ".testoptimization/testdrive.json"))

	// Fresh source edits must be reported as retained when execution is incomplete.
	write(".github/workflows/test.yml", workflow)
	write("src/add.test.js", `import {test} from 'vitest'; test.skip('not locally executable',()=>{})`)
	output, err = command(root, binary, "onboard", "--framework", "vitest", "--apply", "--yes", "--allow-dirty")
	require.Error(t, err, string(output))
	require.Contains(t, string(output), "Source edits remain applied:")
	assertAppliedWorkflowActionlint(t, path)
	require.NotContains(t, string(output), "Usage:")
	marker := "git apply -R --whitespace=nowarn <<'DDTEST_SETUP_REVERT'\n"
	_, patch, found := strings.Cut(string(output), marker)
	require.True(t, found)
	patch, _, found = strings.Cut(patch, "DDTEST_SETUP_REVERT\n")
	require.True(t, found)
	revert := exec.Command("git", "apply", "-R", "--whitespace=nowarn")
	revert.Dir = root
	revert.Stdin = strings.NewReader(patch)
	result, revertErr := revert.CombinedOutput()
	require.NoError(t, revertErr, string(result))
	restored, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, workflow, string(restored))
}

func assertAppliedWorkflowActionlint(t *testing.T, path string) {
	t.Helper()
	if tool := os.Getenv("DDTEST_ACTIONLINT"); tool != "" {
		output, err := exec.Command(tool, "-shellcheck=", "-pyflakes=", path).CombinedOutput()
		require.NoError(t, err, string(output))
	}
}
