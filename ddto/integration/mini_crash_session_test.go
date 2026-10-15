package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// sessionEvents summarizes a capture: each test as "name status", each
// session's status, and the IDs that link the hierarchy.
type sessionEvents struct {
	tests, sessions []string
	// testErrors maps a test's name to its error type and message.
	testErrors map[string]string
	// sessionError is the last session's error message.
	sessionError string
	// modules and suites count the end events.
	modules, suites int
	// sessionIDs are the distinct test_session_id values of every event.
	sessionIDs map[string]bool
	commands   map[string]bool
	events     []map[string]any
}

func summarizeSessionEvents(receiver *capture) sessionEvents {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	s := sessionEvents{testErrors: map[string]string{}, sessionIDs: map[string]bool{}, commands: map[string]bool{}, events: receiver.events}
	for _, event := range receiver.events {
		content, _ := event["content"].(map[string]any)
		meta, _ := content["meta"].(map[string]any)
		s.sessionIDs[fmt.Sprint(content["test_session_id"])] = true
		s.commands[fmt.Sprint(meta["test.command"])] = true
		switch event["type"] {
		case "test":
			s.tests = append(s.tests, fmt.Sprint(meta["test.name"])+" "+fmt.Sprint(meta["test.status"]))
			if meta["error.type"] != nil {
				s.testErrors[fmt.Sprint(meta["test.name"])] = fmt.Sprint(meta["error.type"]) + ": " + fmt.Sprint(meta["error.message"])
			}
		case "test_session_end":
			s.sessions = append(s.sessions, fmt.Sprint(meta["test.status"]))
			s.sessionError = fmt.Sprint(meta["error.message"])
		case "test_module_end":
			s.modules++
		case "test_suite_end":
			s.suites++
		}
	}
	sort.Strings(s.tests)
	return s
}

// A crash that ends a test process keeps its events, or recreates what it
// left open, and the session that ddto sends reports it. The test binary
// keeps its own exit status, and ddto go test's.
func TestMiniCrashKeepsEvents(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test

import (
	"os"
	"sync"
	"testing"
	"time"
)

func TestFirst(t *testing.T) {}

func TestCrash(t *testing.T) {
	switch os.Getenv("FIXTURE_CRASH") {
	case "timeout":
		time.Sleep(time.Minute)
	case "goroutine":
		go func() { panic("background goroutine failed") }()
		time.Sleep(time.Minute)
	case "map":
		m := map[int]int{}
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10000000; j++ {
					m[j%1000] = j
				}
			}()
		}
		wg.Wait()
		t.Fatal("no concurrent map write detected")
	case "kill":
		process, _ := os.FindProcess(os.Getpid())
		_ = process.Kill()
		time.Sleep(time.Minute)
	}
}

func FuzzSeeds(f *testing.F) {
	f.Add(1)
	f.Add(2)
	f.Fuzz(func(t *testing.T, n int) {
		if n == 2 && os.Getenv("FIXTURE_CRASH") == "fuzz" {
			time.Sleep(time.Minute)
		}
	})
}
`})
	both := []string{"TestCrash fail", "TestFirst pass"}
	// The seeds that completed keep their outcome.
	fuzz := []string{"FuzzSeeds fail", "FuzzSeeds/seed#0 pass", "FuzzSeeds/seed#1 fail", "TestCrash pass", "TestFirst pass"}
	for _, tc := range []struct {
		name, crash string
		flags, env  []string
		tests       []string
		// testError is TestCrash's error type and message prefix.
		testError, sessionError string
		modules                 int
	}{
		{"timeout", "timeout", []string{"-timeout=5s"}, nil, both, "timeout: test timed out after 5s", "panic: test timed out after 5s", 1},
		{"fuzz seed timeout", "fuzz", []string{"-timeout=5s"}, nil, fuzz, "", "panic: test timed out after 5s", 1},
		{"fuzz seed timeout deferred", "fuzz", []string{"-timeout=5s"}, []string{"DD_CIVISIBILITY_DEFERRED_DELIVERY=true"}, fuzz, "", "panic: test timed out after 5s", 1},
		{"panic hook", "goroutine", []string{"--panic-hook"}, nil, both, "panic: panic: background goroutine failed", "panic: background goroutine failed", 1},
		// Without the hook, the process's events are lost; ddto recreates the
		// containers and the test that the crash output names.
		{"panic", "goroutine", nil, nil, []string{"TestCrash fail"}, "crash: panic: background goroutine failed", "panic: background goroutine failed", 1},
		{"fatal error", "map", nil, nil, []string{"TestCrash fail"}, "crash: fatal error in example.com/dd-ci-testing-fixture_test.TestCrash.func", "fatal error in ", 1},
		// The panic hook's runtime also copies the fatal error's message to
		// the crash output.
		{"fatal error with panic hook", "map", []string{"--panic-hook"}, nil, []string{"TestCrash fail"}, "crash: fatal error: concurrent map writes", "fatal error: concurrent map writes", 1},
		{"killed", "kill", nil, nil, nil, "", "the test process ended before it closed CI Visibility", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &capture{}
			server := newServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv(append([]string{"DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL=" + server.URL, "DD_TRACE_AGENT_URL=" + server.URL, "DD_API_KEY=poc-not-a-real-key", "FIXTURE_CRASH=" + tc.crash}, tc.env...)...)
			args := append([]string{"test", "--runtime=mini"}, tc.flags...)
			out, stderr, code := command(t, dir, env, driver, append(args, "-count=1", "-v", ".")...)
			// go test reports the crashed package, as without ddto.
			if code != 1 || !strings.Contains(out, "FAIL\texample.com/dd-ci-testing-fixture\t") || strings.Contains(out, "\nok ") {
				t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
			}
			got := summarizeSessionEvents(receiver)
			if len(receiver.failures) != 0 || !reflect.DeepEqual(got.tests, tc.tests) || !reflect.DeepEqual(got.sessions, []string{"fail"}) || got.modules != tc.modules || len(got.sessionIDs) != 1 {
				t.Fatalf("tests %v, sessions %v, modules %d, session IDs %v, failures %v\n%s\n%s", got.tests, got.sessions, got.modules, got.sessionIDs, receiver.failures, out, stderr)
			}
			if !strings.HasPrefix(got.testErrors["TestCrash"], tc.testError) || !strings.Contains(got.sessionError, tc.sessionError) {
				t.Fatalf("TestCrash error %q, want %q; session error %q, want %q\n%s", got.testErrors["TestCrash"], tc.testError, got.sessionError, tc.sessionError, stderr)
			}
			// The debug log explains how the process ended.
			for _, line := range []string{"test session owned=true", "test session sent=true"} {
				if !strings.Contains(out+stderr, line) {
					t.Errorf("the debug log lacks %q", line)
				}
			}
		})
	}
}

// While Mini sends before an unrecovered panic, the tests can finish and
// call os.Exit(0); the process still ends with the panic's status.
func TestMiniPanicHookKeepsExitStatus(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test

import (
	"testing"
	"time"
)

func TestStartsLatePanic(t *testing.T) {
	go func() {
		time.Sleep(200 * time.Millisecond)
		panic("late panic")
	}()
}

func TestRunsMeanwhile(t *testing.T) { time.Sleep(500 * time.Millisecond) }

func TestLast(t *testing.T) {}
`})
	receiver := &capture{}
	// A slow intake keeps the panic's send going while M.Run finishes.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/citestcycle") {
			time.Sleep(2 * time.Second)
		}
		receiver.handler(w, r)
	})
	server := newServer(slow)
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
	out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "--panic-hook", "-count=1", "-v", ".")
	if code != 1 || !strings.Contains(out, "panic: late panic") || !strings.Contains(out, "FAIL\texample.com/dd-ci-testing-fixture\t") || strings.Contains(out, "\nok ") {
		t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
	}
	got := summarizeSessionEvents(receiver)
	if !reflect.DeepEqual(got.sessions, []string{"fail"}) || !reflect.DeepEqual(got.tests, []string{"TestRunsMeanwhile fail", "TestStartsLatePanic pass"}) {
		t.Fatalf("tests %v, sessions %v\n%s", got.tests, got.sessions, stderr)
	}
}

// ddto owns one session for the command: every module, suite and test of
// its packages reports in it with ddto's command, and the session lasts the
// command and fails with it. --session-per-binary keeps one session per test
// binary, and a command whose results all come from Go's cache sends none.
func TestMiniTestSessionSpansTheCommand(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	for _, pkg := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(dir, pkg), 0o700); err != nil {
			t.Fatal(err)
		}
		writeBuildFixture(t, filepath.Join(dir, pkg), map[string]string{pkg + "_test.go": "package " + pkg + "\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc Test" + strings.ToUpper(pkg[:1]) + pkg[1:] + "(t *testing.T) {\n\tif os.Getenv(\"FIXTURE_FAIL\") == \"" + pkg + "\" {\n\t\tt.Fatal(\"fixture failure\")\n\t}\n}\n"})
		// go test caches no result whose source changed in the last seconds.
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(dir, pkg, pkg+"_test.go"), old, old); err != nil {
			t.Fatal(err)
		}
	}
	receiver := &capture{}
	// One intake for every run: test processes read its URL, which go test's
	// cache then keys on.
	server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { receiver.handler(w, r) }))
	defer server.Close()
	run := func(t *testing.T, extra []string, args ...string) (sessionEvents, string, int) {
		t.Helper()
		receiver.mu.Lock()
		receiver.events, receiver.failures = nil, nil
		receiver.mu.Unlock()
		env := testEnv(append([]string{"DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL=" + server.URL, "DD_TRACE_AGENT_URL=" + server.URL, "DD_API_KEY=poc-not-a-real-key"}, extra...)...)
		out, stderr, code := command(t, dir, env, driver, append([]string{"test", "--runtime=mini"}, args...)...)
		return summarizeSessionEvents(receiver), out + stderr, code
	}
	t.Run("one session", func(t *testing.T) {
		started := time.Now()
		got, output, code := run(t, []string{"FIXTURE_FAIL=beta"}, "-count=1", "-ldflags=-X=main.private=private-flag-value", "./alpha", "./beta")
		elapsed := time.Since(started)
		// ddto's build diagnostics never show argument values, also with
		// its session.
		if logs := cliDebugLines(output); !strings.Contains(logs, "test session owned=true") || strings.Contains(logs, "private-flag-value") || strings.Contains(logs, "ldflags") {
			t.Errorf("session debug lines expose arguments:\n%s", logs)
		}
		if code != 1 || !reflect.DeepEqual(got.tests, []string{"TestAlpha pass", "TestBeta fail"}) || !reflect.DeepEqual(got.sessions, []string{"fail"}) || got.modules != 2 || len(got.sessionIDs) != 1 {
			t.Fatalf("exit %d, tests %v, sessions %v, modules %d, session IDs %v\n%s", code, got.tests, got.sessions, got.modules, got.sessionIDs, output)
		}
		want := filepath.Base(driver) + " test --runtime=mini -count=1 -ldflags=-X=main.private=private-flag-value ./alpha ./beta"
		if len(got.commands) != 1 || !got.commands[want] {
			t.Fatalf("commands %v, want %q", got.commands, want)
		}
		var start, end int64
		var moduleStart, moduleEnd int64
		for _, event := range got.events {
			content := event["content"].(map[string]any)
			s, d := toInt64(content["start"]), toInt64(content["duration"])
			switch event["type"] {
			case "test_session_end":
				start, end = s, s+d
			case "test_module_end":
				if moduleStart == 0 || s < moduleStart {
					moduleStart = s
				}
				moduleEnd = max(moduleEnd, s+d)
			}
		}
		if start > moduleStart || end < moduleEnd || time.Duration(end-start) > elapsed {
			t.Fatalf("session [%d, %d] does not span the modules [%d, %d] within the command's %s", start, end, moduleStart, moduleEnd, elapsed)
		}
	})
	t.Run("session per binary", func(t *testing.T) {
		got, output, code := run(t, nil, "--session-per-binary", "-count=1", "./alpha", "./beta")
		if code != 0 || !reflect.DeepEqual(got.sessions, []string{"pass", "pass"}) || len(got.sessionIDs) != 2 || !strings.Contains(output, `test session owned=false reason="--session-per-binary"`) {
			t.Fatalf("exit %d, sessions %v, session IDs %v\n%s", code, got.sessions, got.sessionIDs, output)
		}
	})
	t.Run("chdir", func(t *testing.T) {
		// ddto runs in another repository and moves go test with -C: the
		// session's Git metadata is the test processes'.
		other := t.TempDir()
		for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://example.com/other.git"}, {"-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "commit", "-q", "--allow-empty", "-m", "other"}} {
			if out, stderr, code := command(t, other, testEnv(), "git", args...); code != 0 {
				t.Fatalf("git %v: %s %s", args, out, stderr)
			}
		}
		receiver.mu.Lock()
		receiver.events, receiver.failures = nil, nil
		receiver.mu.Unlock()
		// Git, not the fixtures' variables, gives the metadata and the service.
		env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_GIT_REPOSITORY_URL=", "DD_GIT_COMMIT_SHA=", "DD_SERVICE=")
		out, stderr, code := command(t, other, env, driver, "go", "-C", dir, "test", "--runtime=mini", "-count=1", "./alpha")
		got := summarizeSessionEvents(receiver)
		if code != 0 || len(got.sessions) != 1 {
			t.Fatalf("exit %d, sessions %v\n%s\n%s", code, got.sessions, out, stderr)
		}
		urls, services := map[string]bool{}, map[string]bool{}
		for _, event := range got.events {
			content := event["content"].(map[string]any)
			meta, _ := content["meta"].(map[string]any)
			urls[fmt.Sprint(meta["git.repository_url"])] = true
			services[fmt.Sprint(content["service"])] = true
		}
		if len(urls) != 1 || urls["https://example.com/other.git"] || len(services) != 1 {
			t.Fatalf("repository URLs %v, services %v", urls, services)
		}
	})
	t.Run("cached", func(t *testing.T) {
		if _, output, code := run(t, nil, "./alpha", "./beta"); code != 0 {
			t.Fatalf("exit %d\n%s", code, output)
		}
		got, output, code := run(t, nil, "./alpha", "./beta")
		if code != 0 || len(got.events) != 0 || !strings.Contains(output, "(cached)") {
			t.Fatalf("exit %d, %d events\n%s", code, len(got.events), output)
		}
	})
}

func toInt64(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}
