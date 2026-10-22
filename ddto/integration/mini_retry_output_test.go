package integration

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Without -v, go test prints a failing test's output. A test that still
// fails after its automatic retries keeps that diagnostic. Process retries run
// after testing reported the package, which the README describes.
func TestMiniRetriedFailureKeepsItsOutput(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":        "module example.com/retryoutput\ngo 1.25\n",
		"retry_test.go": "package retryoutput\n\nimport \"testing\"\n\nfunc TestDiagnostic(t *testing.T) { t.Fatal(\"DIAGNOSTIC_SENTINEL\") }\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	for _, mode := range []string{"in_process"} {
		receiver := &capture{retry: true}
		server := newServer(http.HandlerFunc(receiver.handler))
		env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode)
		out, stderr, code := command(t, dir, env, bin, "-test.run=^TestDiagnostic$")
		server.Close()
		if code != 1 || !strings.Contains(out, "DIAGNOSTIC_SENTINEL") {
			t.Errorf("%s retries: exit %d; want 1 with the failure's output\n%s\n%s", mode, code, out, stderr)
		}
	}
}

// An attempt that panics keeps its messages too, and its duration: the time
// that its body ran, which dynamic retry budgets read.
func TestMiniRetriedPanicKeepsItsOutputAndDuration(t *testing.T) {
	driver := sharedDriver(t, "..")
	dir := t.TempDir()
	writeBuildFixture(t, dir, map[string]string{
		"go.mod":        "module example.com/retrypanic\ngo 1.25\n",
		"retry_test.go": "package retrypanic\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestPanics(t *testing.T) {\n\tt.Log(\"PANIC_SENTINEL\")\n\ttime.Sleep(50 * time.Millisecond)\n\tpanic(\"boom\")\n}\n",
	})
	bin := filepath.Join(t.TempDir(), executableName("fixture.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false", "GOWORK=off"), driver, "test", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s\n%s", out, stderr)
	}
	receiver := &capture{retry: true}
	server := newServer(http.HandlerFunc(receiver.handler))
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=poc-not-a-real-key", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=1", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process")
	out, stderr, code := command(t, dir, env, bin, "-test.run=^TestPanics$")
	server.Close()
	if code == 0 || !strings.Contains(out+stderr, "PANIC_SENTINEL") {
		t.Fatalf("exit %d; want a failure with the test's message\n%s\n%s", code, out, stderr)
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	tests := 0
	for _, event := range receiver.events {
		content, _ := event["content"].(map[string]any)
		if event["type"] != "test" {
			continue
		}
		tests++
		if duration := fmt.Sprint(content["duration"]); duration == "0" || duration == "<nil>" || len(duration) < 8 {
			t.Errorf("test event duration %s ns, want at least the 50ms that the body ran", duration)
		}
	}
	if tests == 0 {
		t.Fatalf("no test events\n%s", stderr)
	}
}
