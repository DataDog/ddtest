package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func checkMiniTestifyNativeCallback(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"callback_test.go": `package fixture_test
import("os";"testing";"github.com/stretchr/testify/suite")
//ddtest:no_auto_retry
//ddtest:prevent_failure_suppression
func TestCriticalCallback(t *testing.T){
 f,e:=os.OpenFile(os.Getenv("CALLBACK_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};f.WriteString("x");f.Close();t.Error("protected callback")
}
//ddtest:prevent_failure_suppression
func TestProtectedCallback(t *testing.T){
 f,e:=os.OpenFile(os.Getenv("CALLBACK_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};f.WriteString("x");f.Close();t.Error("protected callback without NoRetry")
}
type CallbackSuite struct{suite.Suite}
func(s *CallbackSuite) TestMethod(){if os.Getenv("PROTECTED_ONLY")=="true"{s.T().Run("critical",TestProtectedCallback)}else{s.T().Run("critical",TestCriticalCallback)}}
func TestCallbackSuite(t *testing.T){suite.Run(t,new(CallbackSuite))}
type CallbackFirst struct{suite.Suite}
//ddtest:no_auto_retry
func(s *CallbackFirst) TestMethod(){s.T().Run("critical",TestCriticalCallback)}
type CallbackSecond struct{suite.Suite}
//ddtest:no_auto_retry
func(s *CallbackSecond) TestMethod(){s.T().Run("critical",TestCriticalCallback)}
func TestSwitchCallback(t *testing.T){
 f,e:=os.OpenFile(os.Getenv("PARENT_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if e==nil{f.Close();suite.Run(t,new(CallbackFirst))}else if os.IsExist(e){suite.Run(t,new(CallbackSecond))}else{t.Fatal(e)}
}
`})
	bin := filepath.Join(t.TempDir(), executableName("callback.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, tc := range []struct {
			name, root string
			managed    bool
		}{
			{"ordinary", "TestCallbackSuite", false},
			{"quarantined", "TestCallbackSuite", true},
			{"switch-declarations", "TestSwitchCallback", false},
		} {
			// Only race-enabled process isolation executes quarantined selected
			// methods; the existing capability fallback does not discover callbacks.
			if tc.managed && (mode != "process" || os.Getenv("PARITY_TEST_MODE") != "race") {
				continue
			}
			t.Run(mode+"-"+tc.name, func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "counter")
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^" + tc.root + "$"}, Policy: policySettings{Retry: !tc.managed, Quarantined: tc.managed, ManagementTarget: tc.root + "/TestMethod", ManagementSuite: "callback_test.go/CallbackSuite"}, Env: []string{"PROTECTED_ONLY=" + fmt.Sprint(tc.managed), "CALLBACK_COUNTER=" + counter, "PARENT_COUNTER=" + filepath.Join(t.TempDir(), "parent"), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=" + fmt.Sprint(!tc.managed), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				if got.code != 1 {
					t.Fatalf("exit%d %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if err != nil || string(data) != "x" {
					t.Fatalf("callback count=%q err=%v", data, err)
				}
				count := 0
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] != tc.root+"/TestMethod/critical" {
						continue
					}
					count++
					if (meta["test.test_management.no_auto_retry"] == "true") != !tc.managed || meta["test.test_management.prevent_failure_suppression"] != "true" {
						t.Fatalf("lost callback policy: %v", meta)
					}
				}
				if count != 1 {
					t.Fatalf("callback events=%d", count)
				}
			})
		}
	}
}
