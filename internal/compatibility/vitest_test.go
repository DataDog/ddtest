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
	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/platform"
	"github.com/DataDog/ddtest/internal/settings"
)

func configureVitest(config string) {
	configureFramework("", "")
	settings.Get().VitestConfig = config
}

// Teardown completes asynchronously, but the interval deliberately stays active.
// This distinguishes a leaked handle from a teardown promise that never resolves.
func writeVitestShutdownSetup(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, root, "shutdown-setup.mjs", `import { writeFileSync } from 'node:fs'
export default function () {
  if (process.env.DDTEST_LEAK_HANDLE !== 'true') return
  setInterval(() => {}, 1000)
  return async () => {
    await new Promise(resolve => setTimeout(resolve, 50))
    writeFileSync(process.env.DDTEST_VITEST_TEARDOWN, 'completed')
  }
}
`)
}

func TestVitestAdapterIntegration(t *testing.T) {
	nodeModules := requireEnv(t, "DDTEST_VITEST_NODE_MODULES")
	resetSettingsAfterTest(t)

	root := filepath.Join(t.TempDir(), "project with spaces")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
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
    passWithNoTests: process.env.DDTEST_PASS_WITH_NO_TESTS === 'true',
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
  expect(process.env.TEST).toBe('true')
  expect(process.env.VITEST).toBe('true')
  expect(process.env.DDTEST_VITEST_WORKER).toBe('selected')
})
`)
	writeFixture(t, root, "checks/unselected.check.js", `import { test } from 'vitest'

test('must not run', () => {
  throw new Error('unselected file ran')
})
`)
	t.Chdir(root)
	configureVitest("vitest.unit.mjs")

	vitest := framework.NewVitest(platform.NewJavaScript())
	runVitest := func(ctx context.Context, files []string, env map[string]string) error {
		if env == nil {
			env = make(map[string]string)
		}
		env["NODE_OPTIONS"] = ""
		return framework.NewVitest(platform.NewJavaScript()).RunTests(ctx, files, env)
	}
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
	if err := runVitest(ctx, []string{"checks/selected.check.js"}, map[string]string{
		"DDTEST_VITEST_WORKER": "selected", "DDTEST_VITEST_CONFIG_EVENTS": lifecycle,
	}); err != nil {
		t.Fatalf("selected-file run failed: %v", err)
	}
	if contents, err := os.ReadFile(lifecycle); err != nil || string(contents) != "config\nsetup\nteardown\n" {
		t.Fatalf("Vitest lifecycle = %q, error = %v", contents, err)
	}

	t.Run("completed teardown with a leaked handle", func(t *testing.T) {
		writeVitestShutdownSetup(t, root)
		writeFixture(t, root, "shutdown.config.mjs", `export default { test: {
  include: ['checks/selected.check.js'], setupFiles: ['./setup.js'],
  globalSetup: ['./shutdown-setup.mjs'], teardownTimeout: 1000,
} }`)
		configureVitest("shutdown.config.mjs")
		teardown := filepath.Join(t.TempDir(), "teardown.txt")
		// Leave ample startup time, but fail if shutdown waits on the leaked timer.
		shutdownCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		err := runVitest(shutdownCtx, []string{"checks/selected.check.js"}, map[string]string{
			"DDTEST_VITEST_WORKER": "selected", "DDTEST_LEAK_HANDLE": "true", "DDTEST_VITEST_TEARDOWN": teardown,
		})
		if contents, readErr := os.ReadFile(teardown); readErr != nil || string(contents) != "completed" {
			t.Fatalf("global teardown did not complete: contents = %q, error = %v", contents, readErr)
		}
		if shutdownCtx.Err() != nil {
			t.Fatalf("Vitest stayed alive after teardown completed: %v", shutdownCtx.Err())
		}
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exact file membership with overlapping names", func(t *testing.T) {
		writeFixture(t, root, "vitest.overlap.mjs", `export default {
  test: { dir: 'src', include: ['**/test.js'], setupFiles: ['./setup.js'],
    shard: process.env.DDTEST_SHARD || undefined,
  },
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
			name, selected, shard string
		}{
			{"shard in run mode", "src/endOfYear/test.js", "1/1"},
			{"relative path", "src/endOfYear/test.js", ""},
			{"absolute path", filepath.Join(root, "src/endOfYear/test.js"), ""},
			{"second assignment", "src/eachWeekendOfYear/test.js", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				events := filepath.Join(root, "executed.txt")
				if err := os.WriteFile(events, nil, 0600); err != nil {
					t.Fatal(err)
				}
				configureVitest("vitest.overlap.mjs")
				name := filepath.Base(filepath.Dir(tc.selected))
				if err := runVitest(ctx, []string{tc.selected}, map[string]string{
					"DDTEST_VITEST_WORKER": name, "DDTEST_VITEST_EVENTS": events, "DDTEST_SHARD": tc.shard,
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
		configureVitest("vitest.overlap.mjs")
		if err := runVitest(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		// Preserve the framework's nonzero exit status on an assigned test failure.
		err := runVitest(ctx, []string{"src/endOfYear/test.js"}, map[string]string{"DDTEST_VITEST_WORKER": "wrong"})
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
    project: process.env.DDTEST_PROJECT_FILTER ? [process.env.DDTEST_PROJECT_FILTER] : [],
    testNamePattern: 'selected in each project',
    reporters: ['json'],
    outputFile: './results.json',
  },
}
`)
		writeFixture(t, root, "workspace.mjs", `export default ['./one/vitest.config.mjs', './two/vitest.config.mjs']`)
		for _, name := range []string{"one", "two"} {
			writeFixture(t, root, name+"/vitest.config.mjs", `import { fileURLToPath } from 'node:url'
export default { test: {
  name: '`+name+`', root: fileURLToPath(new URL('..', import.meta.url)), include: ['project-checks/*.test.js'],
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
			want := []string{"one"}
			if project == "" {
				want = append(want, "two")
			}
			configureVitest("multi-project.config.mjs")
			if err := runVitest(ctx, []string{"project-checks/selected.test.js"}, map[string]string{"DDTEST_VITEST_EVENTS": events, "DDTEST_PROJECT_FILTER": project}); err != nil {
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

	t.Run("CI snapshots require an explicit update", func(t *testing.T) {
		writeFixture(t, root, "snapshot.config.mjs", `export default { test: { include: ['snapshot.test.js'], update: process.env.DDTEST_UPDATE_SNAPSHOTS === 'true' } }`)
		writeFixture(t, root, "snapshot.test.js", `import { expect, test } from 'vitest'
test('snapshot policy', () => { expect({ assigned: true }).toMatchSnapshot() })
`)
		configureVitest("snapshot.config.mjs")
		err := runVitest(ctx, []string{"snapshot.test.js"}, map[string]string{"CI": "true"})
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("expected missing-snapshot failure in CI, got %v", err)
		}
		snapshot := filepath.Join(root, "__snapshots__/snapshot.test.js.snap")
		if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
			t.Fatalf("snapshot should not be written without explicit update, got %v", err)
		}
		if err := runVitest(ctx, []string{"snapshot.test.js"}, map[string]string{"CI": "true", "DDTEST_UPDATE_SNAPSHOTS": "true"}); err != nil {
			t.Fatalf("explicit snapshot update failed: %v", err)
		}
		if _, err := os.Stat(snapshot); err != nil {
			t.Fatalf("explicit update did not write a snapshot: %v", err)
		}
	})

	t.Run("coverage provider and reporting", func(t *testing.T) {
		writeFixture(t, root, "coverage.config.mjs", `export default { test: {
  include: ['coverage.test.js'],
  coverage: { enabled: true, provider: 'v8', include: ['math.js'], reporter: ['json'], reportsDirectory: './coverage-report' },
} }`)
		writeFixture(t, root, "math.js", `export function add(a, b) { return a + b }`)
		writeFixture(t, root, "coverage.test.js", `import { expect, test } from 'vitest'
import { add } from './math.js'
test('records coverage', () => { expect(add(1, 2)).toBe(3) })
`)
		configureVitest("coverage.config.mjs")
		if err := runVitest(ctx, []string{"coverage.test.js"}, nil); err != nil {
			t.Fatal(err)
		}
		report, err := os.ReadFile(filepath.Join(root, "coverage-report/coverage-final.json"))
		if err != nil {
			t.Fatal(err)
		}
		var coverage map[string]struct {
			Statements map[string]int `json:"s"`
		}
		if err := json.Unmarshal(report, &coverage); err != nil {
			t.Fatal(err)
		}
		for file, counts := range coverage {
			if filepath.Base(file) == "math.js" {
				for _, count := range counts.Statements {
					if count > 0 {
						return
					}
				}
			}
		}
		t.Fatalf("coverage did not record executed statements in math.js: %s", report)
	})

	t.Run("configuration errors fail discovery and execution", func(t *testing.T) {
		writeFixture(t, root, "broken.config.mjs", `throw new Error('invalid fixture config')`)
		configureVitest("broken.config.mjs")
		_, err := framework.NewVitest(platform.NewJavaScript()).DiscoverTestFiles(ctx, discovery.TestFileSet{Pattern: "**/*.test.js"})
		if err == nil || !strings.Contains(err.Error(), "broken.config.mjs") {
			t.Fatalf("expected configuration error, got %v", err)
		}
		err = runVitest(ctx, []string{"src/endOfYear/test.js"}, nil)
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("expected configuration failure, got %v", err)
		}
	})

	t.Run("no matching specification", func(t *testing.T) {
		configureVitest("vitest.unit.mjs")
		err := runVitest(ctx, []string{"src/endOfYear/test.js"}, nil)
		if err == nil || !strings.Contains(err.Error(), "exit status 1") {
			t.Fatalf("expected no-test failure, got %v", err)
		}
		if err := runVitest(ctx, []string{"src/endOfYear/test.js"}, map[string]string{"DDTEST_PASS_WITH_NO_TESTS": "true"}); err != nil {
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
	writeVitestShutdownSetup(t, root)
	writeFixture(t, root, "vitest.config.mjs", `export default { test: {
  dir: 'src', include: ['**/test.js'], globalSetup: ['./shutdown-setup.mjs'], teardownTimeout: 1000,
} }`)
	writeFixture(t, root, "src/endOfYear/test.js", `import { test } from 'vitest'
test('ddtest exact traced assignment', () => {
  if (process.env.DDTEST_FAIL_ASSIGNED === 'true') throw new Error('assigned failure')
})
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
	configureVitest("")
	settings.Get().Framework = "vitest"
	tracerRoot := filepath.Join(tracerModules, "dd-trace")
	for key, value := range map[string]string{
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
	} {
		t.Setenv(key, value)
	}
	p := platform.NewJavaScript()
	vitest, err := p.DetectFramework()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		failure      bool
		leakedHandle bool
	}{
		{name: "passing"},
		{name: "failing", failure: true},
		{name: "passing with leaked handle", leakedHandle: true},
		{name: "failing with leaked handle", failure: true, leakedHandle: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			teardown := filepath.Join(t.TempDir(), "teardown.txt")
			mu.Lock()
			payloads = nil
			mu.Unlock()
			err := vitest.RunTests(ctx, []string{"src/endOfYear/test.js"}, map[string]string{
				"DDTEST_FAIL_ASSIGNED":   strconv.FormatBool(tc.failure),
				"DDTEST_LEAK_HANDLE":     strconv.FormatBool(tc.leakedHandle),
				"DDTEST_VITEST_TEARDOWN": teardown,
			})
			if tc.leakedHandle {
				if contents, readErr := os.ReadFile(teardown); readErr != nil || string(contents) != "completed" {
					t.Fatalf("global teardown did not complete: contents = %q, error = %v", contents, readErr)
				}
			}
			if ctx.Err() != nil {
				t.Fatalf("Vitest did not exit before the watchdog: %v", ctx.Err())
			}
			if tc.failure {
				if err == nil || !strings.Contains(err.Error(), "exit status 1") {
					t.Fatalf("expected assigned-test failure, got %v", err)
				}
			} else if err != nil {
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
		})
	}
}
