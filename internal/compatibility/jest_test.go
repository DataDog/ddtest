package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/testdrive"
	"github.com/stretchr/testify/require"
)

func TestJestAdapterIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_JEST_NODE_MODULES")

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "jest.config.js", `module.exports = {
  testMatch: ['<rootDir>/tests/**/*.test.js'],
  setupFilesAfterEnv: ['<rootDir>/setup.js'],
}
`)
	writeFixture(t, root, "setup.js", "globalThis.ddtestJestSetup = true\n")
	writeFixture(t, root, "tests/selected.test.js", `test('preserves config while running an assigned file', () => {
  expect(globalThis.ddtestJestSetup).toBe(true)
  expect(process.env.DDTEST_JEST_WORKER).toBe('selected')
})
`)
	writeFixture(t, root, "tests/unselected.test.js", `test('must not run', () => {
  throw new Error('unselected file ran')
})
`)
	writeFixture(t, root, "noisy.cjs", `process.stdout.write('startup without newline');
process.stderr.write('startup stderr');
process.on('exit', () => {
  process.stdout.write('shutdown without newline');
  process.stderr.write('shutdown stderr');
});
`)
	t.Setenv("NODE_OPTIONS", "--require "+strconv.Quote(filepath.Join(root, "noisy.cjs")))
	t.Chdir(root)

	jest := framework.NewJest()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: jest.TestPattern()}
	files, err := jest.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"tests/selected.test.js", "tests/unselected.test.js"}
	requireFiles(t, files, wantFiles)

	if err := jest.RunTests(ctx, []string{"tests/selected.test.js"}, map[string]string{"DDTEST_JEST_WORKER": "selected"}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}

// A real tracer must emit events, not merely exit successfully. In particular,
// dd-trace 5.86 waits forever for disabled git upload if local settings advertise
// test skipping, allowing Node to exit before Jest starts. Reporting-only must
// work, while the separate skipping probe must leave validation incomplete.
func TestJestTestdriveIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_JEST_NODE_MODULES")
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.Symlink(nodeModules, filepath.Join(root, "node_modules")))
	writeFixture(t, root, "package.json", `{"name":"testdrive-regression","private":true,"scripts":{"test":"jest"}}`)
	writeFixture(t, root, "example.test.js", `test('runs the test body', () => { expect(1 + 1).toBe(2); });`)
	writeFixture(t, root, "noisy.cjs", `process.stdout.write('startup'); process.on('exit', () => process.stdout.write('shutdown'));`)
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "--require "+strconv.Quote(filepath.Join(root, "noisy.cjs")))
	resetSettingsAfterTest(t)
	configureFramework(shellCommand("node", filepath.Join(root, "node_modules", "jest", "bin", "jest.js"), "--runInBand"), "")
	drive, err := testdrive.Prepare("git:must-not-install")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var output bytes.Buffer
	runErr := drive.Run(ctx, &output)
	data, err := os.ReadFile(filepath.Join(root, ".testoptimization", "testdrive.json"))
	require.NoError(t, err, output.String())
	var report struct {
		Success       bool
		Summary       struct{ Status string }
		Compatibility struct{ Status string }
		Runs          []struct {
			Name           string
			Instrumented   bool
			ExitCode       *int                                  `json:"exit_code"`
			TestEventCount int                                   `json:"test_event_count"`
			TestCounts     struct{ Passed, Failed, Skipped int } `json:"test_counts"`
			ResultError    string                                `json:"result_error"`
		}
		Features []struct{ Name, Status string }
	}
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, "compatible", report.Compatibility.Status, "%s\n%s", output.String(), data)
	// The comparison never retries the customer's suite. Features run later
	// against separate probes, so their events must not inflate these counts.
	require.GreaterOrEqual(t, len(report.Runs), 2)
	for i, name := range []string{"baseline", "reporting-only"} {
		run := report.Runs[i]
		require.Equal(t, name, run.Name)
		require.Equal(t, i == 1, run.Instrumented)
		require.NotNil(t, run.ExitCode)
		require.Zero(t, *run.ExitCode)
		require.Empty(t, run.ResultError)
		require.Equal(t, 1, run.TestCounts.Passed)
		require.Zero(t, run.TestCounts.Failed)
		require.Zero(t, run.TestCounts.Skipped)
		require.Equal(t, i, run.TestEventCount)
	}
	require.Len(t, report.Features, 6)
	features := map[string]string{}
	for _, feature := range report.Features {
		features[feature.Name] = feature.Status
	}
	require.Equal(t, map[string]string{
		"auto-retries": "passed", "early-flake-detection": "passed",
		"skipping": "inconclusive", "quarantine": "passed",
		"disabled": "passed", "attempt-to-fix": "passed",
	}, features, "%s\n%s", output.String(), data)
	// Keep the pinned old tracer's skipping defect visible even though its
	// process exits zero: no native results means skipping was not validated.
	var skippingFound bool
	for _, run := range report.Runs {
		if run.Name == "skipping" {
			skippingFound = true
			require.NotNil(t, run.ExitCode)
			require.Zero(t, *run.ExitCode)
			require.Equal(t, "Jest did not produce its JSON results", run.ResultError)
			require.Zero(t, run.TestEventCount)
		}
	}
	require.True(t, skippingFound)
	require.ErrorContains(t, runErr, "validation is incomplete")
	require.False(t, report.Success)
	require.Equal(t, "INCOMPLETE", report.Summary.Status)
	require.Contains(t, output.String(), "reused")
}
