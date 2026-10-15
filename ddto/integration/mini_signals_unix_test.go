//go:build unix

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
)

// Signals keep go test's behavior. A test that handles SIGTERM itself keeps
// its process, so the tests after it run and report; Mini's channel does not
// receive that SIGTERM, even though the test stops its own channel at once. A
// SIGTERM that nothing handles still ends the process with that signal, as
// under go test, after Mini has sent its events, also after a test has reset
// SIGTERM to its default action. A signal that the test ignores stays
// ignored, as without ddto, also after a later Notify, Stop or Reset, when the
// test ignored it before Mini started, and when the process started with
// SIGINT ignored. With Mini's own package under coverage, the hook
// leaves no counters without a package.
func TestMiniSignalsKeepGoTestBehavior(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	for _, pkg := range []string{"ignoredbefore", "inherited"} {
		if err := os.MkdirAll(filepath.Join(dir, pkg), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBuildFixture(t, dir, map[string]string{"ignoredbefore/ignored_test.go": `package ignoredbefore

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// SIGINT stays ignored by the operating system after Notify and Stop,
// although signal.Ignored reports false.
func TestMain(m *testing.M) {
	signal.Ignore(os.Interrupt)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
	os.Exit(m.Run())
}

func TestSIGINT(t *testing.T) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}
`, "inherited/inherited_test.go": `package inherited

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// Started with SIGINT ignored, the process keeps it ignored after Notify and
// Stop, although signal.Ignored then reports false.
func TestMain(m *testing.M) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
	os.Exit(m.Run())
}

func TestSIGINT(t *testing.T) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestAfter(t *testing.T) {}
`, "sample_test.go": `package fixture_test

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestHandlesTERM(t *testing.T) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c:
	case <-time.After(time.Minute):
		t.Fatal("SIGTERM never arrived")
	}
	signal.Stop(c)
}

func TestResets(t *testing.T) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-c
	signal.Reset(syscall.SIGTERM)
}

func TestIgnoreThenReset(t *testing.T) {
	signal.Ignore(syscall.SIGTERM)
	signal.Reset(syscall.SIGTERM)
	signal.Reset()
	if !signal.Ignored(syscall.SIGTERM) {
		t.Fatal("Reset undid Ignore")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestIgnoreNotifyStopReset(t *testing.T) {
	signal.Ignore(os.Interrupt)
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Stop(c)
	signal.Reset(os.Interrupt)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
}

func TestAfter(t *testing.T) {}

func TestTERM(t *testing.T) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
	t.Fatal("SIGTERM did not end the process")
}
`})
	coverMini := "-coverpkg=github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	for _, tc := range []struct {
		name, run string
		args      []string
		code      int
		output    string
		tests     []string
		pkg       string
		logs      []string // debug lines that explain what happened
	}{
		{"handled", "^(TestHandlesTERM|TestAfter)$", nil, 0, "--- PASS: TestAfter", []string{"TestAfter", "TestHandlesTERM"}, "", []string{
			`signal handler registered for [interrupt terminated]`,
			`signal "terminated" arrived while a channel of the test is registered for it; the signal does not end the process, and CI Visibility keeps running`,
			`gotesting: test finished name=TestHandlesTERM status=pass`,
		}},
		{"unhandled", "^(TestAfter|TestTERM)$", nil, 1, "signal: terminated", []string{"TestAfter"}, "", []string{
			`received signal "terminated", which no channel of the test handles and which ends the process: closing CI Visibility`,
			`CI Visibility closed; ending the process with signal "terminated"`,
		}},
		{"reset", "^(TestResets|TestAfter|TestTERM)$", nil, 1, "signal: terminated", []string{"TestAfter", "TestResets"}, "", nil},
		{"coverage", "^(TestHandlesTERM|TestAfter)$", []string{coverMini}, 0, "--- PASS: TestAfter", []string{"TestAfter", "TestHandlesTERM"}, "", nil},
		{"ignore-reset", "^(TestIgnoreThenReset|TestAfter)$", nil, 0, "--- PASS: TestAfter", []string{"TestAfter", "TestIgnoreThenReset"}, "", nil},
		{"ignore-notify-stop-reset", "^(TestIgnoreNotifyStopReset|TestAfter)$", nil, 0, "--- PASS: TestAfter", []string{"TestAfter", "TestIgnoreNotifyStopReset"}, "", nil},
		{"ignored-before-mini", "^TestSIGINT$", nil, 0, "--- PASS: TestSIGINT", []string{"TestSIGINT"}, "./ignoredbefore", []string{
			`signal handler registered for [terminated], which reach CI Visibility only when no channel of the test is registered for them; ignored by the process: [interrupt]`,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &capture{}
			server := newServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
			pkg := tc.pkg
			if pkg == "" {
				pkg = "."
			}
			args := append([]string{"test", "--runtime=mini", "-count=1", "-v", "-run=" + tc.run}, tc.args...)
			out, stderr, code := command(t, dir, env, driver, append(args, pkg)...)
			if code != tc.code || !strings.Contains(out, tc.output) || strings.Contains(out+stderr, "bad pkgID") {
				t.Fatalf("exit %d, want %d and %q\n%s\n%s", code, tc.code, tc.output, out, stderr)
			}
			for _, line := range tc.logs {
				if !strings.Contains(out+stderr, line) {
					t.Errorf("the debug log lacks %q", line)
				}
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			var tests []string
			sessions := 0
			for _, event := range receiver.events {
				content, _ := event["content"].(map[string]any)
				meta, _ := content["meta"].(map[string]any)
				switch event["type"] {
				case "test":
					tests = append(tests, fmt.Sprint(meta["test.name"]))
				case "test_session_end":
					sessions++
				}
			}
			sort.Strings(tests)
			if len(receiver.failures) != 0 || !reflect.DeepEqual(tests, tc.tests) || sessions != 1 {
				t.Fatalf("tests %v, sessions %d, failures %v\n%s\n%s", tests, sessions, receiver.failures, out, stderr)
			}
		})
	}

	// A test binary built with -c and started with SIGINT ignored, as a
	// background job of a non-interactive shell is.
	t.Run("inherited-ignore", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "inherited.test")
		if out, stderr, code := command(t, dir, testEnv(), driver, "test", "--runtime=mini", "-c", "-o", bin, "./inherited"); code != 0 {
			t.Fatalf("build: %d\n%s\n%s", code, out, stderr)
		}
		receiver := &capture{}
		server := newServer(http.HandlerFunc(receiver.handler))
		defer server.Close()
		env := testEnv("DD_CIVISIBILITY_ENABLED=parent", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key")
		out, stderr, code := command(t, filepath.Join(dir, "inherited"), env, "sh", "-c", "trap '' INT; exec \"$0\" -test.v", bin)
		if code != 0 || !strings.Contains(out, "--- PASS: TestAfter") {
			t.Fatalf("exit %d\n%s\n%s", code, out, stderr)
		}
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		var tests, sessions []string
		for _, event := range receiver.events {
			content, _ := event["content"].(map[string]any)
			meta, _ := content["meta"].(map[string]any)
			switch event["type"] {
			case "test":
				tests = append(tests, fmt.Sprint(meta["test.name"]))
			case "test_session_end":
				sessions = append(sessions, fmt.Sprint(meta["test.status"]))
			}
		}
		sort.Strings(tests)
		if len(receiver.failures) != 0 || !reflect.DeepEqual(tests, []string{"TestAfter", "TestSIGINT"}) || !reflect.DeepEqual(sessions, []string{"pass"}) {
			t.Fatalf("tests %v, sessions %v, failures %v\n%s\n%s", tests, sessions, receiver.failures, out, stderr)
		}
	})
}
