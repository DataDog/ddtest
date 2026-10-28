package integration

import (
	"net/http"
	"path/filepath"
	"testing"
)

// An automatic retry that skips after a failure passes nothing: the test still
// fails, and so does its process, also with retries in their own processes.
func TestMiniRetryThatSkipsAfterFailureFails(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod": "module example.com/retryskip\ngo 1.25\n",
		"retry_test.go": `package retryskip

import (
	"os"
	"testing"
)

func TestFailThenSkip(t *testing.T) {
	marker := os.Getenv("RETRY_SKIP_MARKER")
	if _, err := os.Stat(marker); err == nil {
		t.Skip("retry unavailable")
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Fatal("real failure")
}
`,
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		receiver := &capture{retry: true}
		server := newServer(http.HandlerFunc(receiver.handler))
		marker := filepath.Join(t.TempDir(), "attempted")
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode, "RETRY_SKIP_MARKER="+marker)
		out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.run=^TestFailThenSkip$")
		server.Close()
		if code != 1 {
			t.Errorf("%s retries: exit %d, want 1\n%s\n%s", mode, code, out, stderr)
		}
	}
}
