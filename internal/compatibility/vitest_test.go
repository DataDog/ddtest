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

func TestVitestAdapterIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_VITEST_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "vitest.unit.mjs", `export default {
  test: {
    include: ['checks/**/*.check.js'],
    setupFiles: ['./setup.js'],
  },
}
`)
	writeFixture(t, root, "setup.js", "globalThis.ddtestVitestSetup = true\n")
	writeFixture(t, root, "checks/selected.check.js", `import { expect, test } from 'vitest'

test('preserves config while running an assigned file', () => {
  expect(globalThis.ddtestVitestSetup).toBe(true)
  expect(process.env.DDTEST_VITEST_WORKER).toBe('selected')
})
`)
	writeFixture(t, root, "checks/unselected.check.js", `import { test } from 'vitest'

test('must not run', () => {
  throw new Error('unselected file ran')
})
`)
	t.Chdir(root)
	configureFramework(shellCommand(filepath.Join(root, "node_modules", ".bin", "vitest"), "--config", "vitest.unit.mjs"), "")

	vitest := framework.NewVitest()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	testFiles := discovery.TestFileSet{Pattern: vitest.TestPattern()}
	files, err := vitest.DiscoverTestFiles(ctx, testFiles)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"checks/selected.check.js", "checks/unselected.check.js"}
	requireFiles(t, files, wantFiles)

	if err := vitest.RunTests(ctx, []string{"checks/selected.check.js"}, map[string]string{"DDTEST_VITEST_WORKER": "selected"}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
}
