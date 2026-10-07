package compatibility

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/discovery"
	"github.com/DataDog/ddtest/internal/ext"
	"github.com/DataDog/ddtest/internal/framework"
)

func TestVitestAdapterIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_VITEST_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "vitest.unit.mjs", `import { appendFileSync } from 'node:fs'
if (process.env.DDTEST_VITEST_CONFIG_EVENTS)
  appendFileSync(process.env.DDTEST_VITEST_CONFIG_EVENTS, 'config\n')
export default {
  test: {
    include: ['checks/**/*.check.js'],
    setupFiles: ['./setup.js'],
    globalSetup: ['./global-setup.js'],
  },
}

`)
	writeFixture(t, root, "setup.js", "globalThis.ddtestVitestSetup = true\n")
	writeFixture(t, root, "global-setup.js", `import { appendFileSync } from 'node:fs'
export default function () {
  if (process.env.DDTEST_VITEST_CONFIG_EVENTS)
    appendFileSync(process.env.DDTEST_VITEST_CONFIG_EVENTS, 'setup\n')
  return () => {
    if (process.env.DDTEST_VITEST_CONFIG_EVENTS)
      appendFileSync(process.env.DDTEST_VITEST_CONFIG_EVENTS, 'teardown\n')
  }
}
`)
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

	lifecycle := filepath.Join(root, "lifecycle.txt")
	if err := vitest.RunTests(ctx, []string{"checks/selected.check.js"}, map[string]string{
		"DDTEST_VITEST_WORKER": "selected", "DDTEST_VITEST_CONFIG_EVENTS": lifecycle,
	}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
	if contents, err := os.ReadFile(lifecycle); err != nil || string(contents) != "config\nsetup\nteardown\n" {
		t.Fatalf("Vitest lifecycle = %q, error = %v", contents, err)
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

	t.Run("project specifications and configured reporters", func(t *testing.T) {
		writeFixture(t, root, "multi-project.config.mjs", `import { createRequire } from 'node:module'
const require = createRequire(import.meta.url)
const major = Number(require('vitest/package.json').version.split('.')[0])
export default {
  test: {
    ...(major < 4 ? { workspace: './workspace.mjs' } : { projects: ['./one/vitest.config.mjs', './two/vitest.config.mjs'] }),
    reporters: ['json'],
    outputFile: './results.json',
  },
}
`)
		writeFixture(t, root, "workspace.mjs", `export default ['./one/vitest.config.mjs', './two/vitest.config.mjs']`)
		for _, name := range []string{"one", "two"} {
			writeFixture(t, root, name+"/vitest.config.mjs", `export default { test: {
  name: '`+name+`', root: new URL('..', import.meta.url).pathname, include: ['project-checks/*.test.js'],
  env: { DDTEST_PROJECT: '`+name+`' },
} }
`)
		}
		writeFixture(t, root, "project-checks/selected.test.js", `import { test } from 'vitest'
import { appendFileSync } from 'node:fs'
test('selected in each project', () => {
  appendFileSync(process.env.DDTEST_VITEST_EVENTS, process.env.DDTEST_PROJECT + '\n')
})
test('filtered by test name', () => { throw new Error('test name filter lost') })
`)
		writeFixture(t, root, "project-checks/unselected.test.js", `import { test } from 'vitest'
test('must not run', () => { throw new Error('file assignment lost') })
`)
		for _, project := range []string{"one", ""} {
			events := filepath.Join(root, "projects.txt")
			if err := os.WriteFile(events, nil, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", "multi-project.config.mjs", "--testNamePattern", "selected in each project"}
			want := []string{"one"}
			if project != "" {
				args = append(args, "--project", project)
			} else {
				want = append(want, "two")
			}
			configureFramework(shellCommand(append([]string{filepath.Join(root, "node_modules/.bin/vitest")}, args...)...), "")
			if err := framework.NewVitest().RunTests(ctx, []string{"project-checks/selected.test.js"}, map[string]string{"DDTEST_VITEST_EVENTS": events}); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(events)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Fields(string(contents))
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("executed projects %v, want %v", got, want)
			}
			report, err := os.ReadFile(filepath.Join(root, "results.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				TestResults []struct{ AssertionResults []struct{ Status string } }
			}
			if err := json.Unmarshal(report, &result); err != nil {
				t.Fatalf("reporter result = %s, error = %v", report, err)
			}
			passed := 0
			for _, file := range result.TestResults {
				for _, assertion := range file.AssertionResults {
					if assertion.Status == "passed" {
						passed++
					}
				}
			}
			if passed != len(want) {
				t.Fatalf("reporter recorded %d passing tests, want %d", passed, len(want))
			}
		}
	})

	t.Run("no matching specification", func(t *testing.T) {
		configureFramework(shellCommand(filepath.Join(root, "node_modules/.bin/vitest"), "--config", "vitest.unit.mjs"), "")
		err := framework.NewVitest().RunTests(ctx, []string{"src/endOfYear/test.js"}, nil)
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("expected no-test failure, got %v", err)
		}
		configureFramework(shellCommand(filepath.Join(root, "node_modules/.bin/vitest"), "--config", "vitest.unit.mjs", "--passWithNoTests"), "")
		if err := framework.NewVitest().RunTests(ctx, []string{"src/endOfYear/test.js"}, nil); err != nil {
			t.Fatalf("passWithNoTests failed: %v", err)
		}
	})
}

func TestVitestTracingIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_VITEST_NODE_MODULES")
	tracerModules := requireEnv(t, "DDTEST_DD_TRACE_NODE_MODULES")
	resetSettingsAfterTest(t)
	root := t.TempDir()
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "vitest.config.mjs", `export default { test: { dir: 'src', include: ['**/test.js'] } }`)
	writeFixture(t, root, "src/endOfYear/test.js", `import { test } from 'vitest'
test('ddtest exact traced assignment', () => {})
`)
	writeFixture(t, root, "src/eachWeekendOfYear/test.js", `import { test } from 'vitest'
test('ddtest unassigned traced file', () => { throw new Error('unassigned file ran') })
`)

	// Capture actual CI events locally, including session completion and worker
	// spans. A passing test run alone would not catch lost Node API tracing.
	var mu sync.Mutex
	var payloads []byte
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/info" {
			_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v2"]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/citestcycle") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			payloads = append(payloads, body...)
			mu.Unlock()
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	defer agent.Close()
	t.Chdir(root)
	t.Setenv("NODE_OPTIONS", "")
	configureFramework(shellCommand(filepath.Join(root, "node_modules/.bin/vitest")), "")
	vitest := framework.NewVitest()
	tracerRoot := filepath.Join(tracerModules, "dd-trace")
	vitest.SetPlatformEnv(map[string]string{
		"NODE_OPTIONS": "--require " + strconv.Quote(filepath.Join(tracerRoot, "ci/init.js")) +
			" --import " + strconv.Quote(filepath.Join(tracerRoot, "register.js")),
		"DD_TRACE_AGENT_URL":                    agent.URL,
		"DD_TRACE_ENABLED":                      "true",
		"DD_CIVISIBILITY_ENABLED":               "true",
		"DD_CIVISIBILITY_AGENTLESS_ENABLED":     "false",
		"DD_CIVISIBILITY_ITR_ENABLED":           "false",
		"DD_CIVISIBILITY_CODE_COVERAGE_ENABLED": "false",
		"DD_GIT_METADATA_ENABLED":               "false",
		"DD_INSTRUMENTATION_TELEMETRY_ENABLED":  "false",
		"DD_TRACE_STARTUP_LOGS":                 "false",
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// Compare against the CLI baseline: the minimum tracer has no Vitest 5
	// sequencer hook, so that combination cannot emit CI events even without
	// ddtest. Exercise its file-selection adapter in the other integration test.
	if err := (&ext.DefaultCommandExecutor{}).Run(ctx, filepath.Join(root, "node_modules/.bin/vitest"),
		[]string{"run", "src/endOfYear/test.js", "--exclude", "**/eachWeekendOfYear/**"}, vitest.GetPlatformEnv()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	baselineTraced := bytes.Contains(payloads, []byte("test_session_end"))
	payloads = nil
	mu.Unlock()
	if !baselineTraced {
		manifest, err := os.ReadFile(filepath.Join(nodeModules, "vitest/package.json"))
		if err != nil {
			t.Fatal(err)
		}
		var pkg struct{ Version string }
		if err := json.Unmarshal(manifest, &pkg); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(pkg.Version, "5.") {
			t.Fatal("unmodified Vitest CLI did not emit a CI session")
		}
		t.Skip("installed dd-trace does not emit a CI session for the unmodified Vitest CLI")
	}
	if err := vitest.RunTests(ctx, []string{"src/endOfYear/test.js"}, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, marker := range []string{"test_session_end", "test_suite_end", "ddtest exact traced assignment"} {
		if !bytes.Contains(payloads, []byte(marker)) {
			t.Errorf("CI payloads missing %q", marker)
		}
	}
	if bytes.Contains(payloads, []byte("ddtest unassigned traced file")) {
		t.Error("CI payload contains an unassigned test")
	}
}
