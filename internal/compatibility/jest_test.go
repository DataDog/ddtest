package compatibility

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
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

	jest := framework.NewJest(platform.NewJavaScript())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: jest.TestPattern()}
	files, err := jest.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"tests/selected.test.js", "tests/unselected.test.js"}
	requireFiles(t, files, wantFiles)

	if err := jest.RunTests(ctx, []string{"tests/selected.test.js"}, map[string]string{"DDTEST_JEST_WORKER": "selected", "NODE_OPTIONS": os.Getenv("NODE_OPTIONS")}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}

// A real tracer must emit events, not merely exit successfully. In particular,
// dd-trace 5.86 waits forever for disabled git upload if local settings advertise
// test skipping, allowing Node to exit before Jest starts.
func TestJestTestdriveIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_JEST_NODE_MODULES")
	root := t.TempDir()
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
	require.NoError(t, drive.Run(ctx, &output), output.String())
	// Local EFD settings execute this one new test twice.
	require.Contains(t, output.String(), "Test events: 2")
	require.Contains(t, output.String(), "Jest: Passed")
	require.Contains(t, output.String(), "reused")
}
