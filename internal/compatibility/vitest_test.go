package compatibility

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	t.Run("exact file membership with overlapping names", func(t *testing.T) {
		writeFixture(t, root, "vitest.overlap.mjs", `export default {
  test: { dir: 'src', include: ['**/test.js'], setupFiles: ['./setup.js'] },
}
`)
		for _, name := range []string{"endOfYear", "eachWeekendOfYear", "otherEndOfYear"} {
			writeFixture(t, root, "src/"+name+"/test.js", `import { expect, test } from 'vitest'
import { appendFileSync } from 'node:fs'
test('runs only in its assigned batch', () => {
  expect(globalThis.ddtestVitestSetup).toBe(true)
  expect(process.env.DDTEST_VITEST_WORKER).toBe('`+name+`')
  appendFileSync(process.env.DDTEST_VITEST_EVENTS, '`+name+`\n')
})
`)
		}
		for _, tc := range []struct {
			name, command, selected string
		}{
			{"direct", shellCommand(filepath.Join(root, "node_modules/.bin/vitest"), "--config", "vitest.overlap.mjs"), "src/endOfYear/test.js"},
			{"node with separator", shellCommand("node", filepath.Join(root, "node_modules/vitest/vitest.mjs"), "run", "--config", "vitest.overlap.mjs", "--", "old.test.js"), filepath.Join(root, "src/endOfYear/test.js")},
			{"package manager", shellCommand("npx", "--no-install", "vitest", "run", "--config", "vitest.overlap.mjs"), "src/eachWeekendOfYear/test.js"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				events := filepath.Join(root, "executed.txt")
				if err := os.WriteFile(events, nil, 0600); err != nil {
					t.Fatal(err)
				}
				configureFramework(tc.command, "")
				name := filepath.Base(filepath.Dir(tc.selected))
				if err := framework.NewVitest().RunTests(ctx, []string{tc.selected}, map[string]string{
					"DDTEST_VITEST_WORKER": name, "DDTEST_VITEST_EVENTS": events,
				}); err != nil {
					t.Fatalf("exact-file run failed: %v", err)
				}
				contents, err := os.ReadFile(events)
				if err != nil {
					t.Fatal(err)
				}
				if string(contents) != name+"\n" {
					t.Fatalf("executed %q, want exactly %q", contents, name+"\n")
				}
			})
		}

		// An empty batch must not turn into an unfiltered full-suite run.
		configureFramework(shellCommand(filepath.Join(root, "node_modules/.bin/vitest"), "--config", "vitest.overlap.mjs"), "")
		if err := framework.NewVitest().RunTests(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		// Preserve the framework's nonzero exit status on an assigned test failure.
		err := framework.NewVitest().RunTests(ctx, []string{"src/endOfYear/test.js"}, map[string]string{"DDTEST_VITEST_WORKER": "wrong"})
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("expected assigned-test failure, got %v", err)
		}
	})
}
