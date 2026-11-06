package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniProtectedAggregateFailure(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"protected_aggregate_test.go": `package fixture_test
import("os";"testing";"github.com/stretchr/testify/suite")
type AggregateInner struct{suite.Suite}
//ddtest:prevent_failure_suppression
func(s *AggregateInner) TestCritical(){
 path:=os.Getenv("AGGREGATE_COUNTER")
 f,err:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
 data,err:=os.ReadFile(path);if err!=nil{s.T().Fatal(err)}
 if len(data)==1{s.T().Error("early protected failure followed by successful attempts")}
}
func TestProtectedAggregate(t *testing.T){suite.Run(t,new(AggregateInner))}
`})
	bin := filepath.Join(t.TempDir(), executableName("protected-aggregate.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, profile := range []struct{ mode, family string }{
		{"in_process", "atf"}, {"process", "atf"}, {"in_process", "atr"}, {"in_process", "efd"},
	} {
		mode, family := profile.mode, profile.family
		t.Run(mode+"-"+family, func(t *testing.T) {
			atf, efd := family == "atf", family == "efd"
			receiver := &parityReceiver{policy: policySettings{AttemptToFix: atf, Quarantined: true, Retry: !efd, EFD: efd}, side: map[string][][]byte{}, requests: map[string]int{}}
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/test-management/tests") {
					suites := map[string]any{
						"protected_aggregate_test.go":                map[string]any{"tests": map[string]any{"TestProtectedAggregate": map[string]any{"properties": map[string]bool{"quarantined": atf, "attempt_to_fix": false}}}},
						"protected_aggregate_test.go/AggregateInner": map[string]any{"tests": map[string]any{"TestProtectedAggregate/TestCritical": map[string]any{"properties": map[string]bool{"attempt_to_fix": atf}}}},
					}
					writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "aggregate", "type": "ci_app_libraries_tests_request", "attributes": map[string]any{"modules": map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"suites": suites}}}}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/ci/libraries/tests") {
					rootKnown := "TestProtectedAggregate"
					if efd {
						rootKnown = "OtherKnownRoot"
					}
					tests := map[string]any{"protected_aggregate_test.go": []string{rootKnown}, "protected_aggregate_test.go/AggregateInner": []string{"OtherKnownMethod"}}
					writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "aggregate", "type": "ci_app_libraries_tests", "attributes": map[string]any{"tests": map[string]any{"example.com/dd-ci-testing-fixture_test": tests}}}})
					return
				}
				receiver.handler(w, r)
			}))
			defer server.Close()
			counter := filepath.Join(t.TempDir(), "calls")
			env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode, "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES=2", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", !efd), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=2", "AGGREGATE_COUNTER="+counter)
			out, stderr, code := command(t, dir, env, bin, "-test.run=^TestProtectedAggregate$")
			want := 0
			if atf {
				want = 1
			}
			if code != want {
				t.Errorf("aggregate protection exit=%d want=%d: %s %s", code, want, out, stderr)
			}
			data, err := os.ReadFile(counter)
			if err != nil {
				t.Fatal(err)
			}
			// Selected Testify methods deliberately fall back to one native
			// execution when fresh subtest retry layouts are unavailable. The
			// configured ATF assignment must not hide that terminal failure.
			atfCalls := "x"
			// Only race binaries enable quarantine subtree workers. ATF's
			// configured count is total executions, so that path runs twice.
			if mode == "process" && os.Getenv("PARITY_TEST_MODE") == "race" {
				atfCalls = "xx"
			}
			if atf && string(data) != atfCalls || family == "atr" && string(data) != "xx" || efd && len(data) < 2 {
				t.Fatalf("%s callbacks=%q", family, data)
			}
			childEvents, failedEvents, passedEvents := 0, 0, 0
			fallbackSeen := false
			parentFinal, childFinal := "", ""
			for _, event := range receiver.events {
				if event["type"] != "test" {
					continue
				}
				meta := event["content"].(map[string]any)["meta"].(map[string]any)
				if meta["test.name"] == "TestProtectedAggregate/TestCritical" {
					childEvents++
					stack, _ := meta["error.stack"].(string)
					fallbackSeen = fallbackSeen || strings.Contains(stack, "runRetryAttemptCapabilityFallback")
					if meta["test.status"] == "fail" {
						failedEvents++
					}
					if meta["test.status"] == "pass" {
						passedEvents++
					}
					if value, ok := meta["test.final_status"].(string); ok {
						childFinal = value
					}
					if meta["test.test_management.prevent_failure_suppression"] != "true" || atf && meta["test.test_management.is_quarantined"] != "true" {
						t.Fatalf("protected child assignment/annotation lost: %v", meta)
					}
					if atf && meta["test.test_management.is_attempt_to_fix"] != "true" {
						t.Fatalf("ATF owner lost: %v", meta)
					}
				}
				if meta["test.name"] == "TestProtectedAggregate" {
					parentFinal, _ = meta["test.final_status"].(string)
				}
			}
			if childEvents != len(data) || failedEvents != 1 || !atf && passedEvents < 1 || atf && passedEvents != len(atfCalls)-1 {
				t.Fatalf("callbacks=%q events=%d failed=%d passed=%d", data, childEvents, failedEvents, passedEvents)
			}
			if atf {
				if len(atfCalls) == 1 && !fallbackSeen {
					t.Fatal("single ATF execution lacked actual native fallback stack evidence")
				}
				if len(atfCalls) > 1 && fallbackSeen {
					t.Fatal("worker ATF continuations unexpectedly took native fallback")
				}
				if childFinal != "fail" || parentFinal != "fail" {
					t.Fatalf("aggregate failure masked: childFinal=%q parentFinal=%q", childFinal, parentFinal)
				}
			} else if childFinal != "pass" || parentFinal == "fail" {
				t.Fatalf("legitimate %s recovery stayed protected: child=%q parent=%q", family, childFinal, parentFinal)
			}
		})
	}
}
