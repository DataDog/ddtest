package integration

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestCLIActivation(t *testing.T)     { testCLIActivation(t, false) }
func TestMiniCLIActivation(t *testing.T) { testCLIActivation(t, true) }
func testCLIActivation(t *testing.T, mini bool) {
	var dir, driver string
	if mini {
		dir, driver = prepareMiniFixture(t)
	} else {
		dir, driver = prepareFixture(t, false)
	}
	run := func(env []string, args ...string) (string, string, int) {
		prefix := []string{"test", "--runtime=sdk"}
		if mini {
			prefix[1] = "--runtime=mini"
		}
		return command(t, dir, env, driver, append(prefix, args...)...)
	}
	for _, tc := range []struct {
		name, value, wantEnvironment string
		defined                      bool
		wantTests                    int
	}{
		{name: "unset", wantEnvironment: "false", wantTests: 1},
		{name: "parent", value: "parent", defined: true, wantEnvironment: "false", wantTests: 1},
		// The SDK canonicalizes an explicit true value to 1 at bootstrap.
		{name: "true", value: "true", defined: true, wantEnvironment: "1", wantTests: 2},
		{name: "false", value: "false", defined: true, wantEnvironment: "false"},
		{name: "empty", defined: true},
		{name: "custom", value: "invalid", defined: true, wantEnvironment: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &capture{}
			server := httptest.NewServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "POC_EXPECT_CIVISIBILITY="+tc.wantEnvironment)
			if tc.defined {
				env = append(env, "DD_CIVISIBILITY_ENABLED="+tc.value)
			}
			out, stderr, code := run(env, "-count=1", "-run=^TestCLIEnvironment$", ".", "-args", "-mode=environment")
			if code != 0 {
				t.Fatalf("CLI activation: %d\n%s\n%s", code, out, stderr)
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.failures) > 0 {
				t.Fatalf("wire protocol: %v", receiver.failures)
			}
			count := 0
			for _, event := range receiver.events {
				if event["type"] == "test" {
					count++
				}
			}
			if count != tc.wantTests {
				t.Fatalf("test events including child: got %d, want %d\n%s\n%s", count, tc.wantTests, out, stderr)
			}
		})
	}

	t.Run("managed-retry", func(t *testing.T) {
		receiver := &capture{retry: true}
		server := httptest.NewServer(http.HandlerFunc(receiver.handler))
		defer server.Close()
		env := testEnv("DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "POC_RETRY_COUNTER="+filepath.Join(t.TempDir(), "retry-counter"), "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_TOTAL_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process")
		out, stderr, code := run(env, "-count=1", "-run=^TestRetry$", ".", "-args", "-mode=retry")
		if code != 0 {
			t.Fatalf("parent-mode managed retry: %d\n%s\n%s", code, out, stderr)
		}
		receiver.mu.Lock()
		defer receiver.mu.Unlock()
		if len(receiver.failures) > 0 {
			t.Fatalf("wire protocol: %v", receiver.failures)
		}
		var failed, passed bool
		for _, event := range receiver.events {
			if event["type"] != "test" {
				continue
			}
			content := event["content"].(map[string]any)
			meta := content["meta"].(map[string]any)
			failed = failed || meta["test.status"] == "fail"
			passed = passed || meta["test.status"] == "pass"
		}
		if !failed || !passed {
			t.Fatalf("managed retry did not report both attempts: %v", normalizedEvents(receiver.events))
		}
	})
}
