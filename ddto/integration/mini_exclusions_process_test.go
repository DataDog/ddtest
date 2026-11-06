package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniExclusionProcessContinuations(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"continuation_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
func appendContinuation(t *testing.T,path string)int{f,err:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString("x");f.Close();if err!=nil{t.Fatal(err)};data,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)};return len(data)}
type ContinuationOuter struct{suite.Suite}
func(s *ContinuationOuter) TestQuarantine(){suite.Run(s.T(),new(ContinuationFix))}
type ContinuationFix struct{suite.Suite}
func(s *ContinuationFix) TestFix(){n:=appendContinuation(s.T(),os.Getenv("FIX_CALLS"));if os.Getenv("LATE_PROTECTED")!="true"||n>1{suite.Run(s.T(),new(ContinuationLeaf))}}
type ContinuationLeaf struct{suite.Suite}
//ddtest:no_auto_retry
//ddtest:prevent_failure_suppression
func(s *ContinuationLeaf) TestCritical(){appendContinuation(s.T(),os.Getenv("EXCLUDED_CALLS"));if os.Getenv("LATE_PROTECTED")=="true"{s.T().Error("protected continuation-only failure")}}
func TestContinuationRoot(t *testing.T){f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.Close();return};if !os.IsExist(err){t.Fatal(err)};suite.Run(t,new(ContinuationOuter))}
`})
	bin := filepath.Join(t.TempDir(), executableName("continuation.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "-c", "-race", "-mod=mod", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late-protected=%t", late), func(t *testing.T) {
			receiver := &parityReceiver{policy: policySettings{AttemptToFix: true, Quarantined: true}, side: map[string][][]byte{}, requests: map[string]int{}}
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/test-management/tests") {
					suites := map[string]any{}
					for _, entry := range []struct {
						suite, name string
						properties  map[string]bool
					}{
						{"continuation_test.go", "TestContinuationRoot", map[string]bool{"attempt_to_fix": true}},
						{"continuation_test.go/ContinuationOuter", "TestContinuationRoot/TestQuarantine", map[string]bool{"quarantined": true, "attempt_to_fix": false}},
						{"continuation_test.go/ContinuationFix", "TestContinuationRoot/TestQuarantine/TestFix", map[string]bool{"attempt_to_fix": true}},
					} {
						suites[entry.suite] = map[string]any{"tests": map[string]any{entry.name: map[string]any{"properties": entry.properties}}}
					}
					writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "continuation", "type": "ci_app_libraries_tests_request", "attributes": map[string]any{"modules": map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"suites": suites}}}}})
					return
				}
				receiver.handler(w, r)
			}))
			defer server.Close()
			calls, fix, root := filepath.Join(t.TempDir(), "excluded"), filepath.Join(t.TempDir(), "fix"), filepath.Join(t.TempDir(), "root")
			env := testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES=2", "POC_RETRY_COUNTER="+root, "EXCLUDED_CALLS="+calls, "FIX_CALLS="+fix, fmt.Sprintf("LATE_PROTECTED=%t", late))
			out, stderr, code := command(t, dir, env, bin, "-test.run=^TestContinuationRoot$")
			data, err := os.ReadFile(calls)
			want := 0
			if late {
				want = 1
			}
			if code != want || err != nil || string(data) != "x" {
				t.Fatalf("continuation cache/outcome: exit=%d want%d calls=%q err=%v %s %s", code, want, data, err, out, stderr)
			}
			leaf, rootStatus := 0, ""
			for _, event := range receiver.events {
				if event["type"] != "test" {
					continue
				}
				meta := event["content"].(map[string]any)["meta"].(map[string]any)
				name, _ := meta["test.name"].(string)
				if strings.HasSuffix(name, "/TestCritical") {
					leaf++
					if meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.prevent_failure_suppression"] != "true" || meta["test.is_retry"] != nil {
						t.Fatalf("excluded continuation event=%v", meta)
					}
				}
				if name == "TestContinuationRoot" {
					rootStatus, _ = meta["test.final_status"].(string)
				}
			}
			if leaf != 1 || late && rootStatus != "fail" {
				t.Fatalf("continuation events=%d rootfinal=%q", leaf, rootStatus)
			}
		})
	}
}

func checkMiniExcludedIsolationRoot(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"isolated_exclusion_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type IsolatedExcludedSuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *IsolatedExcludedSuite) TestOnly(){if err:=os.WriteFile(os.Getenv("ISOLATED_CALL"),[]byte("x"),0600);err!=nil{s.T().Fatal(err)}}
func TestIsolatedExcluded(t *testing.T){suite.Run(t,new(IsolatedExcludedSuite))}
`})
	bin := filepath.Join(t.TempDir(), executableName("isolated-exclusion.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, "test", "-c", "-race", "-mod=mod", "-o", bin, ".")
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	calls := filepath.Join(t.TempDir(), "calls")
	receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestIsolatedExcluded$/^TestOnly$"}, Policy: policySettings{Quarantined: true, ManagementTarget: "TestIsolatedExcluded/TestOnly", ManagementSuite: "isolated_exclusion_test.go/IsolatedExcludedSuite"}, Env: []string{"ISOLATED_CALL=" + calls, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process"}})
	data, err := os.ReadFile(calls)
	if got.code != 0 || err != nil || string(data) != "x" {
		t.Fatalf("excluded isolation root rejected: exit%d calls=%q err=%v %s %s", got.code, data, err, got.out, got.stderr)
	}
	count := 0
	for _, event := range receiver.events {
		if event["type"] != "test" {
			continue
		}
		meta := event["content"].(map[string]any)["meta"].(map[string]any)
		if meta["test.name"] == "TestIsolatedExcluded/TestOnly" {
			count++
			if meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.is_quarantined"] != "true" || meta["test.is_retry"] != nil {
				t.Fatalf("excluded isolation root event=%v", meta)
			}
		}
	}
	if count != 1 {
		t.Fatalf("excluded isolation root events=%d", count)
	}
}
