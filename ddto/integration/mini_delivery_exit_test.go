package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMiniDeliveryFailurePreservesGoExit(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"sample_test.go": `package fixture_test
import "testing"
func TestPass(t *testing.T){}
func TestFail(t *testing.T){t.Error("fixture failure")}
`})
	for _, deferred := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			code int
		}{{"Pass", 0}, {"Fail", 1}} {
			t.Run(fmt.Sprintf("%s/deferred=%t", tc.name, deferred), func(t *testing.T) {
				var rejected atomic.Int32
				receiver := &capture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/citestcycle") {
						rejected.Add(1)
						http.Error(w, "fixture rejected payload", http.StatusUnauthorized)
						return
					}
					receiver.handler(w, r)
				}))
				defer server.Close()
				env := testEnv("DD_TRACE_DEBUG=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", fmt.Sprintf("DD_CIVISIBILITY_DEFERRED_DELIVERY=%t", deferred))
				out, stderr, code := command(t, dir, env, driver, "test", "--runtime=mini", "-count=1", "-v", "-run=^Test"+tc.name+"$", ".")
				if code != tc.code {
					t.Fatalf("go exit=%d want=%d\n%s\n%s", code, tc.code, out, stderr)
				}
				runtimeOutput := out + stderr
				if !strings.Contains(runtimeOutput, "status_code=401 network_error=false retry=false") {
					t.Fatalf("missing failed delivery diagnostics: %s", runtimeOutput)
				}
				for _, operation := range []string{"flush", "close"} {
					found := false
					for _, line := range strings.Split(runtimeOutput, "\n") {
						if strings.Contains(line, "ci mini tracer: "+operation+" finished duration=") && strings.HasSuffix(line, "status=error") {
							found = true
						}
					}
					if !found {
						t.Errorf("missing %s error timing: %s", operation, runtimeOutput)
					}
				}
				if rejected.Load() == 0 || !strings.Contains(out+stderr, "CI event close failed") {
					t.Fatalf("missing delivery/error log: rejected=%d\n%s\n%s", rejected.Load(), out, stderr)
				}
			})
		}
	}
}
