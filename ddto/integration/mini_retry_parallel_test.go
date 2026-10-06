package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In-process attempts of parallel tests must end them in testing's own
// accounting, as tRunner does, for a test that passes at once and for one that
// is retried. With -count=2, the second iteration's AllocsPerRun runs after
// the first iteration's parallel tests; it panics while testing still counts a
// parallel test as running.
func TestMiniInProcessRetryKeepsAllocsPerRun(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import (
 "sync/atomic"
 "testing"
)
var attempts atomic.Int32
func TestAllocsPerRunAfterRetry(t *testing.T) {
 if testing.AllocsPerRun(10, func() {}) != 0 { t.Fatal("unexpected allocations") }
}
func TestPassingParallel(t *testing.T) {
 t.Parallel()
}
func TestRetriedParallel(t *testing.T) {
 t.Parallel()
 if attempts.Add(1) == 1 { t.Fatal("first attempt fails") }
}
`
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), executableName("retry.test"))
	if out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "--runtime=mini", "-c", "-o", bin, "."); code != 0 {
		t.Fatalf("compile: %s %s", out, stderr)
	}
	receiver := &parityReceiver{policy: policySettings{Retry: true}, side: map[string][][]byte{}, requests: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(receiver.handler))
	defer server.Close()
	env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=false", "DD_TRACE_AGENT_URL="+server.URL,
		"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process")
	out, stderr, code := command(t, dir, env, bin, "-test.v", "-test.count=2", "-test.timeout=60s")
	output := out + stderr
	if !strings.Contains(output, "first attempt fails") {
		t.Fatalf("the parallel test was not retried: exit %d\n%s", code, output)
	}
	if strings.Contains(output, "AllocsPerRun called during parallel test") || strings.Count(output, "--- PASS: TestAllocsPerRunAfterRetry") != 2 {
		t.Fatalf("AllocsPerRun broke after the retry: exit %d\n%s", code, output)
	}
}
