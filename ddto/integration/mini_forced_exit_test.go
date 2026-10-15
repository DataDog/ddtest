package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// os.Exit and log.Fatal while tests run end the process with go test's
// result, and Mini still sends the events of the tests that finished and the
// failed session: from the test body, from a cleanup, from another
// goroutine, and with deferred delivery. Mini closes a test when its body
// returns, before testing runs its cleanups, so a test whose cleanup exits
// reports its body's result. An os.Exit in a package's init, before Mini
// starts, exits with its own status and reports nothing.
func TestMiniForcedExitKeepsEvents(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "exiter"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, dir, map[string]string{"exiter/exiter.go": `package exiter

import (
	"os"
	"testing"
)

// Importing testing initializes this package after testing, which registers
// the exit hook, and its import path before Mini's.
var _ = testing.Short

func init() {
	if os.Getenv("FIXTURE_EXIT") == "init" {
		os.Exit(3)
	}
}
`, "sample_test.go": `package fixture_test

import (
	"log"
	"os"
	"testing"
	"time"

	_ "example.com/dd-ci-testing-fixture/exiter"
)

func TestFirst(t *testing.T) {}

func TestExit(t *testing.T) {
	switch os.Getenv("FIXTURE_EXIT") {
	case "body":
		os.Exit(3)
	case "fatal":
		log.Fatal("fixture fatal")
	case "cleanup":
		t.Cleanup(func() { os.Exit(4) })
	case "goroutine":
		go os.Exit(5)
		time.Sleep(time.Minute)
		t.Fatal("os.Exit did not end the process")
	}
}
`})
	first := []string{"TestFirst pass"}
	for _, tc := range []struct {
		name, exit string
		deferred   bool
		tests      []string
	}{
		{"body", "body", false, first},
		{"fatal", "fatal", false, first},
		{"cleanup", "cleanup", false, []string{"TestFirst pass", "TestExit pass"}},
		{"goroutine", "goroutine", false, first},
		{"deferred", "body", true, first},
		{"init", "init", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &capture{}
			server := newServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key",
				"FIXTURE_EXIT="+tc.exit, fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", tc.deferred))
			out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-v", ".")
			ran := tc.tests != nil
			// The debug log names the call that ended the process.
			if ran && tc.exit != "goroutine" {
				for _, line := range []string{"os.Exit before CI Visibility closed; closing it. os.Exit was called from:", "sample_test.go:", "closed CI Visibility for os.Exit duration=", "gotesting: test finished name=TestFirst status=pass", "civisibility: test started name=TestExit"} {
					if !strings.Contains(out+stderr, line) {
						t.Errorf("the debug log lacks %q", line)
					}
				}
			}
			if code != 1 || strings.Contains(out, "--- PASS: TestFirst") != ran || strings.Contains(out+stderr, "exit hook invoked") || strings.Contains(out+stderr, "fatal error") {
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
					tests = append(tests, fmt.Sprint(meta["test.name"])+" "+fmt.Sprint(meta["test.status"]))
				case "test_session_end":
					sessions = append(sessions, fmt.Sprint(meta["test.status"]))
				}
			}
			wantSessions := []string{"fail"}
			if !ran {
				wantSessions = nil
			}
			if len(receiver.failures) != 0 || !reflect.DeepEqual(tests, tc.tests) || !reflect.DeepEqual(sessions, wantSessions) {
				t.Fatalf("tests %v, sessions %v, failures %v\n%s\n%s", tests, sessions, receiver.failures, out, stderr)
			}
		})
	}
}
