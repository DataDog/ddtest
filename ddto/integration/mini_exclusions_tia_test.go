package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A method first discovered by a retry worker emits its only event through
// excluded-event replay; that event must retain the worker's TIA decision.
func checkMiniLateExcludedTIA(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"late_tia_test.go": `package fixture_test
import("os";"testing";"github.com/stretchr/testify/suite")
type LateTIA struct{suite.Suite}
func(s *LateTIA) record(){
 f,err:=os.OpenFile(os.Getenv("EXCLUSION_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
}
//ddtest:no_auto_retry
func(s *LateTIA) TestSkipped(){s.record()}
//dd:test.unskippable
//ddtest:no_auto_retry
func(s *LateTIA) TestForced(){s.record()}
func(s *LateTIA) TestSibling(){s.T().Error("parent must retry again after discovery")}
func TestLateTIA(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err==nil{f.Close();t.Error("first invocation does not discover the suite");return};if !os.IsExist(err){t.Fatal(err)}
 suite.Run(t,new(LateTIA))
}
`})
	bin := filepath.Join(t.TempDir(), executableName("late-tia.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	// Native Testify method instrumentation retains the existing top-level-only
	// TIA scope (see the itr-method-sdk-limit parity case). Compare that native
	// baseline with TIA off/on; worker subtree policy supports method decisions.
	for _, profile := range []struct {
		name, mode string
		tia        bool
	}{
		{"native-baseline", "in_process", false},
		{"native-tia", "in_process", true},
		{"worker-tia", "process", true},
	} {
		mode := profile.mode
		for _, forced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-forced-%t", profile.name, forced), func(t *testing.T) {
				method := "TestSkipped"
				if forced {
					method = "TestForced"
				}
				name := "TestLateTIA/" + method
				counter := filepath.Join(t.TempDir(), "calls")
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestLateTIA$/^(" + method + "|TestSibling)$"}, Policy: policySettings{ITR: profile.tia, Retry: true, SkippableTarget: name, SkippableSuite: "late_tia_test.go/LateTIA"}, Env: []string{"EXCLUSION_COUNTER=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				if got.code != 1 {
					t.Fatalf("exit %d: %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if forced || mode == "in_process" {
					if err != nil || string(data) != "x" {
						t.Fatalf("forced callback=%q err=%v", data, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("skipped callback ran: %q err=%v", data, err)
				}
				events, sessions := 0, 0
				for _, event := range receiver.events {
					content := event["content"].(map[string]any)
					meta, _ := content["meta"].(map[string]any)
					if event["type"] == "test" && meta["test.name"] == name {
						events++
						if meta["test.test_management.no_auto_retry"] != "true" {
							t.Fatalf("declaration lost: %v", meta)
						}
						if mode == "in_process" {
							if meta["test.status"] != "pass" || meta["test.itr.forced_run"] != nil || meta["test.skipped_by_itr"] != nil {
								t.Fatalf("native method TIA baseline changed: %v", meta)
							}
						} else if forced {
							if meta["test.itr.forced_run"] != "true" || meta["test.status"] != "pass" || meta["test.skipped_by_itr"] != nil {
								t.Fatalf("forced TIA result lost: %v", meta)
							}
						} else if meta["test.skipped_by_itr"] != "true" || meta["test.status"] != "skip" {
							t.Fatalf("skipped TIA result lost: %v", meta)
						}
						for _, key := range []string{"test.is_retry", "test.retry_reason", "test.retry.execution_mode"} {
							if meta[key] != nil {
								t.Fatalf("first execution inherited %s: %v", key, meta)
							}
						}
					}
					if event["type"] == "test_session_end" {
						sessions++
						metrics, _ := content["metrics"].(map[string]any)
						count := "0"
						if value, ok := metrics["test.itr.tests_skipping.count"]; ok {
							count = fmt.Sprint(value)
						}
						want := "1"
						if forced || mode == "in_process" {
							want = "0"
						}
						if count != want {
							t.Fatalf("actual TIA skipped count=%v want=%v session=%v", count, want, content)
						}
						if !forced && mode == "process" && meta["_dd.ci.itr.tests_skipped"] != "true" {
							t.Fatalf("actual TIA skip not reflected in session: %v", content)
						}
					}
				}
				if events != 1 || sessions != 1 {
					t.Fatalf("events=%d sessions=%d want one each", events, sessions)
				}
			})
		}
	}
}

// Both declaration policies preserve native-supported top-level TIA decisions.
func checkMiniTopLevelExcludedTIA(t *testing.T) {
	for _, annotation := range []string{"//ddtest:no_auto_retry", "//ddtest:prevent_failure_suppression", "//ddtest:no_auto_retry\n//ddtest:prevent_failure_suppression"} {
		t.Run(annotation, func(t *testing.T) {
			dir, driver := prepareMiniFixture(t)
			writeBuildFixture(t, dir, map[string]string{"top_tia_test.go": `package fixture_test
import("testing";"os")
func writeTopTIA(t *testing.T){if err:=os.WriteFile(os.Getenv("TOP_TIA_CALL"),[]byte("x"),0600);err!=nil{t.Fatal(err)}}
` + annotation + `
func TestTopSkipped(t *testing.T){writeTopTIA(t)}
` + annotation + `
//dd:test.unskippable
func TestTopForced(t *testing.T){writeTopTIA(t)}
`})
			bin := filepath.Join(t.TempDir(), executableName("top-tia.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("compile%d: %s %s", code, out, stderr)
			}
			for _, forced := range []bool{false, true} {
				t.Run(fmt.Sprint("forced=", forced), func(t *testing.T) {
					name := "TestTopSkipped"
					if forced {
						name = "TestTopForced"
					}
					counter := filepath.Join(t.TempDir(), "calls")
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^" + name + "$"}, Policy: policySettings{ITR: true, Retry: true, SkippableTarget: name, SkippableSuite: "top_tia_test.go"}, Env: []string{"TOP_TIA_CALL=" + counter, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true"}})
					if got.code != 0 {
						t.Fatalf("TIA policy changed command exit%d: %s %s", got.code, got.out, got.stderr)
					}
					data, err := os.ReadFile(counter)
					if forced {
						if err != nil || string(data) != "x" {
							t.Fatalf("forced callback=%q %v", data, err)
						}
					} else if !os.IsNotExist(err) {
						t.Fatalf("skipped callback ran: %q %v", data, err)
					}
					events := 0
					for _, event := range receiver.events {
						content := event["content"].(map[string]any)
						meta, _ := content["meta"].(map[string]any)
						if event["type"] == "test" && meta["test.name"] == name {
							events++
							for _, flag := range []string{"no_auto_retry", "prevent_failure_suppression"} {
								if (meta["test.test_management."+flag] == "true") != strings.Contains(annotation, flag) {
									t.Fatalf("TIA event lost declaration %s: %v", flag, meta)
								}
							}
							if forced {
								if meta["test.itr.forced_run"] != "true" || meta["test.status"] != "pass" {
									t.Fatalf("forced event=%v", meta)
								}
							} else if meta["test.skipped_by_itr"] != "true" || meta["test.status"] != "skip" {
								t.Fatalf("skipped event=%v", meta)
							}
						}
						if event["type"] == "test_session_end" {
							metrics, _ := content["metrics"].(map[string]any)
							count := int64(0)
							if value, ok := metrics["test.itr.tests_skipping.count"]; ok {
								count = toInt64(value)
							}
							want := int64(1)
							if forced {
								want = 0
							}
							if count != want {
								t.Fatalf("native skipped count=%d want%d", count, want)
							}
						}
					}
					if events != 1 {
						t.Fatalf("native TIA execution events=%d", events)
					}
				})
			}
		})
	}
}
