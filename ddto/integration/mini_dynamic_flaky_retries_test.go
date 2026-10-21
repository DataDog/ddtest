package integration

import (
	"crypto/sha1"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// Dynamic automatic test retries, as in dd-trace-go #5695: the retry count
// comes from the first execution's duration bucket, so retries happen even
// with DD_CIVISIBILITY_FLAKY_RETRY_COUNT=0.
func TestMiniDynamicFlakyRetries(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod": "module example.com/dynamicatr\ngo 1.25\n",
		// The test passes on its fourth execution in the process.
		"flaky_test.go": "package dynamicatr\n\nimport \"testing\"\n\nvar runs int\n\nfunc TestFlaky(t *testing.T) {\n\truns++\n\tif runs < 4 {\n\t\tt.Fatal(\"not yet\")\n\t}\n}\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	for _, tc := range []struct {
		name                string
		env                 []string
		executions, retries int
	}{
		{name: "flat count zero", executions: 1},
		// The first execution is under 5s, so the first bucket's 2 retries apply.
		{name: "dynamic buckets", env: []string{"DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED=true", "DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS=2,1,1,1,1"}, executions: 3, retries: 2},
		// Each retry runs in a new process, where the test fails again.
		{name: "dynamic buckets in process mode", env: []string{"DD_CIVISIBILITY_DYNAMIC_ATR_ENABLED=true", "DD_CIVISIBILITY_DYNAMIC_ATR_BUCKETS=2,1,1,1,1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process"}, executions: 3, retries: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &capture{retry: true}
			server := newServer(http.HandlerFunc(receiver.handler))
			defer server.Close()
			executionDir := t.TempDir()
			env := testEnv(append([]string{"DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL=" + server.URL, "DD_TRACE_AGENT_URL=" + server.URL, "DD_API_KEY=poc-not-a-real-key", "TMPDIR=" + executionDir, "TMP=" + executionDir, "TEMP=" + executionDir, "XDG_CACHE_HOME=" + executionDir, fmt.Sprintf("DD_GIT_COMMIT_SHA=%x", sha1.Sum([]byte(dir+"|"+tc.name))), "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=0"}, tc.env...)...)
			out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.run=^TestFlaky$")
			if code != 1 {
				t.Fatalf("exit %d, want 1: the test must still fail\n%s\n%s", code, out, stderr)
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.failures) > 0 {
				t.Fatalf("wire protocol: %v", receiver.failures)
			}
			executions, retries := 0, 0
			for _, event := range receiver.events {
				content, _ := event["content"].(map[string]any)
				meta, _ := content["meta"].(map[string]any)
				if event["type"] != "test" || meta == nil || meta["test.name"] != "TestFlaky" {
					continue
				}
				executions++
				if meta["test.status"] != "fail" {
					t.Fatalf("execution status %v, want fail", meta["test.status"])
				}
				if meta["test.retry_reason"] == "auto_test_retry" {
					retries++
				}
			}
			if executions != tc.executions || retries != tc.retries {
				t.Fatalf("executions %d, retries %d; want %d and %d\n%s", executions, retries, tc.executions, tc.retries, stderr)
			}
		})
	}
}
