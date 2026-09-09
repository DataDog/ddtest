package compatibility

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/framework"
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
