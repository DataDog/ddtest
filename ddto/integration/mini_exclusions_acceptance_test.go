package integration

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniReviewNestedRows(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"review_row_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
//ddtest:prevent_failure_suppression
//ddtest:no_auto_retry
func TestCriticalRows(t *testing.T){rowPath(t,0)}
//ddtest:prevent_failure_suppression
//ddtest:no_auto_retry
func TestDeepCriticalRows(t *testing.T){rowPath(t,3)}
//ddtest:no_auto_retry
func TestNoRetryRows(t *testing.T){rowPath(t,0)}
//ddtest:prevent_failure_suppression
func TestProtectedRecovery(t *testing.T){recoverRow(t)}
func recoverRow(t *testing.T){f,e:=os.OpenFile(os.Getenv("ROW_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};f.WriteString("x");f.Close();d,e:=os.ReadFile(os.Getenv("ROW_COUNTER"));if e!=nil{t.Fatal(e)};if len(d)==1{t.Error("first recoverable failure")}}
func rowPath(t *testing.T,depth int){if depth>0{t.Run("level",func(t *testing.T){rowPath(t,depth-1)});return};t.Run("row",func(t *testing.T){f,e:=os.OpenFile(os.Getenv("ROW_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("ran");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)};t.Error("protected row")})}
type InnerRowSuite struct{suite.Suite}
func(s *InnerRowSuite) TestMethod(){if e:=os.WriteFile(os.Getenv("ROW_COUNTER"),[]byte("boundary"),0600);e!=nil{s.T().Fatal(e)}}

type RowSuite struct{suite.Suite}
func(s *RowSuite) TestMethod(){s.T().Run("callback",TestCriticalRows)}
func(s *RowSuite) TestSharedPaths(){s.T().Run("protected",TestCriticalRows);s.T().Run("unprotected",TestNoRetryRows)}
func(s *RowSuite) TestDeep(){s.T().Run("callback",TestDeepCriticalRows)}
func(s *RowSuite) TestNoRetry(){s.T().Run("callback",TestNoRetryRows)}
func(s *RowSuite) TestProtectedRecovery(){s.T().Run("callback",TestProtectedRecovery)}
func(s *RowSuite) TestOrdinaryRecovery(){recoverRow(s.T())}
//ddtest:prevent_failure_suppression
//ddtest:no_auto_retry
func(s *RowSuite) TestBoundary(){suite.Run(s.T(),new(InnerRowSuite))}
func TestRowSuite(t *testing.T){suite.Run(t,new(RowSuite))}
`})
	bin := filepath.Join(t.TempDir(), executableName("review-row.test"))
	o, e, c := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if c != 0 {
		t.Fatalf("compile%d %s %s", c, o, e)
	}
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "row")
			r, g := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestRowSuite$/^TestMethod$"}, Policy: policySettings{Disabled: true, AttemptToFix: true, ManagementTarget: "TestRowSuite/TestMethod/callback/row", ManagementSuite: "review_row_test.go/RowSuite"}, Env: []string{"ROW_COUNTER=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false"}})
			data, err := os.ReadFile(counter)
			if g.code != 1 || err != nil || string(data) != "ran" {
				t.Fatalf("protected row exit%d calls%q err%v output%s %s", g.code, data, err, g.out, g.stderr)
			}
			count := 0
			for _, ev := range r.events {
				if ev["type"] != "test" {
					continue
				}
				m := ev["content"].(map[string]any)["meta"].(map[string]any)
				if m["test.name"] != "TestRowSuite/TestMethod/callback/row" {
					continue
				}
				count++
				requireReviewNoRetryOwnership(t, m)
				if m["test.status"] != "fail" || m["test.test_management.prevent_failure_suppression"] != "true" || m["test.test_management.no_auto_retry"] != "true" || m["test.test_management.is_test_disabled"] != "true" || m["test.test_management.is_attempt_to_fix"] != "true" {
					t.Fatalf("row metadata%v", m)
				}
			}
			if count != 1 {
				t.Fatalf("row events%d", count)
			}
		})
	}
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode+"-shared-owner-cache", func(t *testing.T) {
			calls := filepath.Join(t.TempDir(), "calls")
			receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestRowSuite$/^TestSharedPaths$"}, Policy: policySettings{}, Env: []string{"ROW_COUNTER=" + calls, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false"}})
			raw, err := os.ReadFile(calls)
			if got.code != 1 || err != nil || string(raw) != "ranran" {
				t.Fatalf("shared declaration execution exit%d calls%q err%v %s %s", got.code, raw, err, got.out, got.stderr)
			}
			for _, entry := range []struct {
				path, status string
				protected    bool
			}{{"protected", "fail", true}, {"unprotected", "fail", false}} {
				name := "TestRowSuite/TestSharedPaths/" + entry.path + "/row"
				events := reviewTestEvents(receiver, name)
				if len(events) != 1 {
					t.Fatalf("shared path %s events%d: %v", name, len(events), receiver.events)
				}
				meta := events[0]
				requireReviewNoRetryOwnership(t, meta)
				if meta["test.status"] != entry.status || meta["test.test_management.no_auto_retry"] != "true" || (meta["test.test_management.prevent_failure_suppression"] == "true") != entry.protected || meta["test.test_management.is_test_disabled"] != nil || meta["test.test_management.is_attempt_to_fix"] != nil {
					t.Fatalf("shared owner cache leaked policy: %v", meta)
				}
			}
		})
	}

	for _, mode := range []string{"in_process", "process"} {
		for _, tc := range []struct {
			name, method, path               string
			prevent, disabled, atf, recovery bool
		}{
			{"deep", "TestDeep", "/callback/level/level/level/row", true, true, true, false},
			{"shared-no-retry", "TestNoRetry", "/callback/row", false, true, true, false},
			{"pfs-recovery", "TestProtectedRecovery", "/callback", true, false, false, true},
			{"ordinary-recovery", "TestOrdinaryRecovery", "", false, false, false, true},
			{"actual-method-boundary", "TestBoundary", "/TestMethod", false, false, false, false},
		} {
			t.Run(mode+"-"+tc.name, func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "row")
				name := "TestRowSuite/" + tc.method + tc.path
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestRowSuite$/^" + tc.method + "$"}, Policy: policySettings{Retry: tc.recovery, Disabled: tc.disabled, AttemptToFix: tc.atf, ManagementTarget: name, ManagementSuite: "review_row_test.go/RowSuite"}, Env: []string{"ROW_COUNTER=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=" + fmt.Sprint(tc.recovery), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				wantCode := 0
				if tc.disabled && tc.prevent {
					wantCode = 1
				}
				if got.code != wantCode {
					t.Fatalf("exit%d want%d %s %s", got.code, wantCode, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if tc.disabled && !tc.prevent {
					if !os.IsNotExist(err) {
						t.Fatalf("disabled shared callback executed %q %v", data, err)
					}
				} else {
					want := "ran"
					if tc.name == "actual-method-boundary" {
						want = "boundary"
					}
					if tc.recovery {
						want = "xx"
					}
					if err != nil || string(data) != want {
						t.Fatalf("callback count%q want%q err%v", data, want, err)
					}
				}
				events := reviewTestEvents(receiver, name)
				wantCount := 1
				if tc.recovery && mode == "in_process" {
					wantCount = 2
				}
				if len(events) != wantCount {
					t.Fatalf("%s events%d want%d: %v", name, len(events), wantCount, receiver.events)
				}
				for _, meta := range events {
					if (meta["test.test_management.prevent_failure_suppression"] == "true") != tc.prevent || (meta["test.test_management.no_auto_retry"] == "true") != (tc.disabled) {
						t.Fatalf("declaration boundary policy: %v", meta)
					}
					if tc.disabled {
						requireReviewNoRetryOwnership(t, meta)
						if meta["test.test_management.is_test_disabled"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" {
							t.Fatalf("lost assignments: %v", meta)
						}
					}
				}
			})
		}
	}

}

func checkMiniReviewWorkerManagement(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"review_worker_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type WorkerLeaf struct{suite.Suite}
//ddtest:no_auto_retry
func(s *WorkerLeaf) TestLeaf(){workerLeafMark(s.T())}
func workerLeafMark(t *testing.T){f,e:=os.OpenFile(os.Getenv("LEAF_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("ran");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)}}
type ProtectedWorkerLeaf struct{suite.Suite}
//ddtest:no_auto_retry
//ddtest:prevent_failure_suppression
func(s *ProtectedWorkerLeaf) TestLeaf(){workerLeafMark(s.T());s.T().Error("protected leaf failure")}
func workerCarriers(t *testing.T,n int){if n>0{t.Run("wrapper",func(t *testing.T){workerCarriers(t,n-1)});return};if os.Getenv("PROTECTED_LEAF")=="true"{suite.Run(t,new(ProtectedWorkerLeaf))}else{suite.Run(t,new(WorkerLeaf))}}
func TestWorkerRoot(t *testing.T){f,e:=os.OpenFile(os.Getenv("ROOT_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("first attempt before discovery");return};if !os.IsExist(e){t.Fatal(e)};depth:=1;if os.Getenv("DEEP_WORKER")=="true"{depth=3};workerCarriers(t,depth)}
`})
	bin := filepath.Join(t.TempDir(), executableName("review-worker.test"))
	o, e, c := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if c != 0 {
		t.Fatalf("compile%d %s %s", c, o, e)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, deep := range []bool{false, true} {
			for _, protected := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/deep=%t/protected=%t", mode, deep, protected), func(t *testing.T) {
					name := "TestWorkerRoot/wrapper/TestLeaf"
					if deep {
						name = "TestWorkerRoot/wrapper/wrapper/wrapper/TestLeaf"
					}
					counter := filepath.Join(t.TempDir(), "leaf")
					r, g := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestWorkerRoot$"}, Policy: policySettings{Disabled: true, AttemptToFix: true, ManagementTarget: "TestWorkerRoot", ManagementSuite: "review_worker_test.go"}, Env: []string{"DEEP_WORKER=" + fmt.Sprint(deep), "PROTECTED_LEAF=" + fmt.Sprint(protected), "LEAF_COUNTER=" + counter, "ROOT_COUNTER=" + filepath.Join(t.TempDir(), "root"), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false"}})
					data, err := os.ReadFile(counter)
					wantCode := 0
					if protected {
						wantCode = 1
					}
					if g.code != wantCode || (!protected && !os.IsNotExist(err)) || (protected && (err != nil || string(data) != "ran")) {
						t.Fatalf("disabled leaf ran exit%d err%v output%s %s", g.code, err, g.out, g.stderr)
					}
					count := 0
					for _, ev := range r.events {
						if ev["type"] != "test" {
							continue
						}
						m := ev["content"].(map[string]any)["meta"].(map[string]any)
						if m["test.name"] != name {
							continue
						}
						count++
						requireReviewNoRetryOwnership(t, m)
						wantStatus := "skip"
						if protected {
							wantStatus = "fail"
						}
						if (m["test.test_management.prevent_failure_suppression"] == "true") != protected || m["test.status"] != wantStatus || m["test.test_management.no_auto_retry"] != "true" || m["test.test_management.is_test_disabled"] != "true" || m["test.test_management.is_attempt_to_fix"] != "true" {
							t.Fatalf("leaf metadata%v", m)
						}
					}
					if count != 1 {
						t.Fatalf("leaf events%d", count)
					}
				})
			}
		}
	}
}

// The nearest exact ATF=false assignment must survive multiple ordinary
// carriers, including the process retry that discovers them after attempt one.
func checkMiniReviewWorkerOverride(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"worker_override_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type OverrideWorkerLeaf struct{suite.Suite}
//ddtest:no_auto_retry
func(s *OverrideWorkerLeaf) TestLeaf(){f,e:=os.OpenFile(os.Getenv("OVERRIDE_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{s.T().Fatal(e)};f.WriteString("x");f.Close()}
func TestWorkerOverride(t *testing.T){f,e:=os.OpenFile(os.Getenv("OVERRIDE_ROOT"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("first ATF attempt failed");return};if !os.IsExist(e){t.Fatal(e)};t.Run("override",func(t *testing.T){t.Run("ordinary",func(t *testing.T){suite.Run(t,new(OverrideWorkerLeaf))})})}
`})
	bin := filepath.Join(t.TempDir(), executableName("worker-override.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile%d %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		t.Run(mode, func(t *testing.T) {
			receiver := &parityReceiver{policy: policySettings{AttemptToFix: true}, side: map[string][][]byte{}, requests: map[string]int{}}
			server := newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/test-management/tests") {
					tests := map[string]any{}
					for name, atf := range map[string]bool{"TestWorkerOverride": true, "TestWorkerOverride/override": false} {
						tests[name] = map[string]any{"properties": map[string]bool{"attempt_to_fix": atf}}
					}
					writePolicyJSON(w, map[string]any{"data": map[string]any{"id": "override", "type": "ci_app_libraries_tests_request", "attributes": map[string]any{"modules": map[string]any{"example.com/dd-ci-testing-fixture_test": map[string]any{"suites": map[string]any{"worker_override_test.go": map[string]any{"tests": tests}}}}}}})
					return
				}
				receiver.handler(w, r)
			}))
			defer server.Close()
			calls, root := filepath.Join(t.TempDir(), "calls"), filepath.Join(t.TempDir(), "root")
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_ENABLED=true", "DD_CIVISIBILITY_AGENTLESS_URL="+server.URL, "DD_TRACE_AGENT_URL="+server.URL, "DD_API_KEY=fixture", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE="+mode, "DD_TEST_MANAGEMENT_ATTEMPT_TO_FIX_RETRIES=2", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "OVERRIDE_CALLS="+calls, "OVERRIDE_ROOT="+root), bin, "-test.run=^TestWorkerOverride$")
			if code != 1 {
				t.Fatalf("first failed ATF attempt must still block: exit%d %s %s", code, out, stderr)
			}
			data, err := os.ReadFile(calls)
			if err != nil || string(data) != "x" {
				t.Fatalf("leaf calls%q err%v %s %s", data, err, out, stderr)
			}
			events := reviewTestEvents(receiver, "TestWorkerOverride/override/ordinary/TestLeaf")
			if len(events) != 1 {
				t.Fatalf("leaf events%d: %v", len(events), receiver.events)
			}
			meta := events[0]
			requireReviewNoRetryOwnership(t, meta)
			if meta["test.status"] != "pass" || meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.is_attempt_to_fix"] != nil || meta["test.test_management.is_test_disabled"] != nil || meta["test.test_management.is_quarantined"] != nil {
				t.Fatalf("nearest false override lost: %v", meta)
			}
		})
	}
}

// Crash finalization uses effective execution policy while preserving backend
// assignment tags. NoRetry alone must retain its preexisting crash behavior.
func checkMiniExcludedCrashFinalStatus(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("os";"testing";"time")
ANNOTATIONS
func TestCrashPolicy(t *testing.T){
 f,e:=os.OpenFile(os.Getenv("CRASH_POLICY_ENTERED"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)}
 if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)}
 if os.Getenv("CRASH_POLICY_MODE")=="background"{go func(){panic("protected background panic sentinel")}()}
 time.Sleep(time.Minute)
}
`
	for _, flags := range []struct {
		name, comments   string
		prevent, noRetry bool
	}{
		{"pfs", "//ddtest:prevent_failure_suppression", true, false},
		{"both", "//ddtest:prevent_failure_suppression\n//ddtest:no_auto_retry", true, true},
		{"no-retry", "//ddtest:no_auto_retry", false, true},
	} {
		t.Run(flags.name, func(t *testing.T) {
			writeBuildFixture(t, dir, map[string]string{"crash_policy_test.go": strings.Replace(source, "ANNOTATIONS", flags.comments, 1)})
			for _, mode := range []string{"timeout", "background"} {
				t.Run(mode, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), executableName("crash-policy.test"))
					args := exclusionBuildArgs(bin, false)
					if mode == "background" {
						args = append([]string{"test", "--panic-hook"}, args[1:]...)
					}
					out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
					if code != 0 {
						t.Fatalf("compile%d %s %s", code, out, stderr)
					}
					for _, assignment := range []string{"quarantined", "disabled-atf", "unmanaged"} {
						t.Run(assignment, func(t *testing.T) {
							marker := filepath.Join(t.TempDir(), "entered")
							receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestCrashPolicy$", "-test.timeout=250ms"}, Policy: policySettings{Quarantined: assignment == "quarantined", Disabled: assignment == "disabled-atf", AttemptToFix: assignment == "disabled-atf", ManagementTarget: "TestCrashPolicy", ManagementSuite: "crash_policy_test.go"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process", "CRASH_POLICY_MODE=" + mode, "CRASH_POLICY_ENTERED=" + marker}})
							// Exact published-head baseline: NoRetry+quarantine still executes and
							// crashes; NoRetry+disabled excludes ATF and skips the body entirely.
							skipped := !flags.prevent && assignment == "disabled-atf"
							wantCode, wantStatus, wantFinal := 2, "fail", "fail"
							if skipped {
								wantCode, wantStatus, wantFinal = 0, "skip", "skip"
							} else if !flags.prevent && assignment == "quarantined" {
								wantFinal = "skip"
							}
							data, err := os.ReadFile(marker)
							if skipped {
								if !os.IsNotExist(err) {
									t.Fatalf("disabled NoRetry body executed %q err%v", data, err)
								}
							} else if err != nil || string(data) != "x" {
								t.Fatalf("crash body calls%q want x err%v exit%d %s %s", data, err, got.code, got.out, got.stderr)
							}
							if got.code != wantCode {
								t.Fatalf("command exit%d want%d %s %s", got.code, wantCode, got.out, got.stderr)
							}
							events := reviewTestEvents(receiver, "TestCrashPolicy")
							if len(events) != 1 {
								t.Fatalf("crash events%d want1: %v", len(events), receiver.events)
							}
							meta := events[0]
							if meta["test.status"] != wantStatus || meta["test.final_status"] != wantFinal {
								t.Fatalf("crash outcome status=%v final=%v want%s/%s: %v", meta["test.status"], meta["test.final_status"], wantStatus, wantFinal, meta)
							}
							for key, want := range map[string]bool{"prevent_failure_suppression": flags.prevent, "no_auto_retry": flags.noRetry, "is_quarantined": assignment == "quarantined", "is_test_disabled": assignment == "disabled-atf", "is_attempt_to_fix": assignment == "disabled-atf"} {
								tag := "test.test_management." + key
								if want && meta[tag] != "true" || !want && meta[tag] != nil {
									t.Fatalf("crash assignment/annotation %s=%v wanttrue=%t: %v", tag, meta[tag], want, meta)
								}
							}
							requireReviewNoRetryOwnership(t, meta)
							if skipped {
								if meta["error.type"] != nil || meta["error.message"] != nil {
									t.Fatalf("skipped body gained crash diagnostics: %v", meta)
								}
								return
							}
							errorType, sentinel := "timeout", "test timed out after 250ms"
							if mode == "background" {
								errorType, sentinel = "panic", "protected background panic sentinel"
							}
							if meta["error.type"] != errorType || !strings.Contains(fmt.Sprint(meta["error.message"]), sentinel) || !strings.Contains(got.out+got.stderr, sentinel) {
								t.Fatalf("lost crash type/value %s/%s: %v\n%s\n%s", errorType, sentinel, meta, got.out, got.stderr)
							}
						})
					}
				})
			}
		})
	}
}

func checkMiniEarlyWorkerTerminalState(t *testing.T) {
	t.Run("completed", checkMiniEarlyCompletedWorkerState)
	t.Run("active", checkMiniEarlyActiveWorkerState)
}

const earlyCompletedWorkerSource = `package fixture_test
import("testing";"os";"runtime";"github.com/stretchr/testify/suite")
func appendEarly(t *testing.T,key string)int{path:=os.Getenv(key);f,e:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)};d,e:=os.ReadFile(path);if e!=nil{t.Fatal(e)};return len(d)}
type EarlyWorkerSuite struct{suite.Suite}
//ddtest:no_auto_retry
EXTRA
func(s *EarlyWorkerSuite) TestOnce(){n:=appendEarly(s.T(),"EARLY_NR_CALLS");if n==1&&os.Getenv("EARLY_PROTECTED_FAIL")=="true"{s.T().Error("first protected declaration failure")}}
func TestEarlyWorkerRoot(t *testing.T){
 appendEarly(t,"EARLY_ROOT_CALLS")
 f,e:=os.OpenFile(os.Getenv("EARLY_ROOT_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("first root failure before discovery");return};if !os.IsExist(e){t.Fatal(e)}
 if os.Getenv("EARLY_ORDINARY_ONLY")!="true"{suite.Run(t,new(EarlyWorkerSuite))}
 t.Run("ordinary",func(t *testing.T){
  appendEarly(t,"EARLY_CHILD_CALLS")
  f,e:=os.OpenFile(os.Getenv("EARLY_CHILD_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();switch os.Getenv("EARLY_TERMINAL"){case "panic":panic("controlled ordinary worker panic sentinel");case "goexit":runtime.Goexit()}}else if !os.IsExist(e){t.Fatal(e)}
 })
}
`

// These markers distinguish native invocations from report-event ownership.
func earlyWorkerEnv(t *testing.T, terminal string, failed bool) ([]string, string, string, string) {
	t.Helper()
	scratch := t.TempDir()
	nr, roots, children := filepath.Join(scratch, "nr"), filepath.Join(scratch, "roots"), filepath.Join(scratch, "children")
	env := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "EARLY_NR_CALLS=" + nr, "EARLY_ROOT_CALLS=" + roots, "EARLY_CHILD_CALLS=" + children, "EARLY_ROOT_FIRST=" + filepath.Join(scratch, "root-first"), "EARLY_CHILD_FIRST=" + filepath.Join(scratch, "child-first"), "EARLY_TERMINAL=" + terminal, "EARLY_PROTECTED_FAIL=" + fmt.Sprint(failed)}
	return env, nr, roots, children
}

func requireEarlyMarker(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if want == "" {
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected callback marker%q data%q err%v", path, data, err)
		}
		return
	}
	if err != nil || string(data) != want {
		t.Fatalf("callback marker%q=%q want%q err%v", path, data, want, err)
	}
}

func checkMiniEarlyCompletedWorkerState(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	// Native Go provides the terminal outcome independently of Mini. Its two
	// invocations perform the first root failure, then one actual declaration.
	writeBuildFixture(t, dir, map[string]string{"early_worker_test.go": strings.Replace(earlyCompletedWorkerSource, "EXTRA\n", "", 1)})
	native := filepath.Join(t.TempDir(), executableName("early-native.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), "go", exclusionBuildArgs(native, true)...)
	if code != 0 {
		t.Fatalf("native compile%d %s %s", code, out, stderr)
	}
	for _, terminal := range []string{"none", "panic", "goexit"} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("native/%s/failed=%t", terminal, failed), func(t *testing.T) {
				env, nr, roots, children := earlyWorkerEnv(t, terminal, failed)
				out, stderr, code := command(t, dir, testEnv(append(env, "DD_CIVISIBILITY_ENABLED=false")...), native, "-test.run=^TestEarlyWorkerRoot$", "-test.count=2")
				wantCode := 2
				if terminal == "none" {
					wantCode = 1
				}
				if code != wantCode {
					t.Fatalf("native exit%d want%d %s %s", code, wantCode, out, stderr)
				}
				requireEarlyMarker(t, nr, "x")
				requireEarlyMarker(t, roots, "xx")
				requireEarlyMarker(t, children, "x")
			})
		}
	}
	for _, protected := range []bool{false, true} {
		extra := ""
		if protected {
			extra = "//ddtest:prevent_failure_suppression\n"
		}
		writeBuildFixture(t, dir, map[string]string{"early_worker_test.go": strings.Replace(earlyCompletedWorkerSource, "EXTRA\n", extra, 1)})
		bin := filepath.Join(t.TempDir(), executableName(fmt.Sprintf("early-completed-%t.test", protected)))
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
		if code != 0 {
			t.Fatalf("compile%d %s %s", code, out, stderr)
		}
		for _, terminal := range []string{"none", "panic", "goexit"} {
			for _, failed := range []bool{false, true} {
				if failed && !protected {
					continue
				}
				t.Run(fmt.Sprintf("protected=%t/failed=%t/%s", protected, failed, terminal), func(t *testing.T) {
					env, nr, roots, children := earlyWorkerEnv(t, terminal, failed)
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestEarlyWorkerRoot$"}, Policy: policySettings{Retry: true}, Env: env})
					wantCode, wantRoots := 0, 3
					if terminal == "none" && !failed {
						wantRoots = 2
					}
					if failed {
						wantCode = 1
						if terminal != "none" {
							wantCode = 2
						}
					}
					if got.code != wantCode {
						t.Fatalf("completed declaration terminal exit%d want%d %s %s", got.code, wantCode, got.out, got.stderr)
					}
					requireEarlyMarker(t, nr, "x")
					requireEarlyMarker(t, roots, strings.Repeat("x", wantRoots))
					requireEarlyMarker(t, children, strings.Repeat("x", wantRoots-1))
					events := reviewTestEvents(receiver, "TestEarlyWorkerRoot/TestOnce")
					if len(events) != 1 {
						t.Fatalf("completed NoRetry events%d want1: %v", len(events), receiver.events)
					}
					meta := events[0]
					wantStatus := "pass"
					if failed {
						wantStatus = "fail"
					}
					if meta["test.status"] != wantStatus || meta["test.final_status"] != wantStatus || meta["test.test_management.no_auto_retry"] != "true" || (meta["test.test_management.prevent_failure_suppression"] == "true") != protected {
						t.Fatalf("first completed declaration event lost/replaced: %v", meta)
					}
					requireReviewNoRetryOwnership(t, meta)
					if failed && !strings.Contains(fmt.Sprint(meta["error.message"]), "first protected declaration failure") {
						t.Fatalf("original completed failure lost: %v", meta)
					}
					rootEvents := reviewTestEvents(receiver, "TestEarlyWorkerRoot")
					if len(rootEvents) != wantRoots {
						t.Fatalf("root event ownership count%d want%d: %v", len(rootEvents), wantRoots, receiver.events)
					}
				})
			}
		}
		if !protected {
			for _, terminal := range []string{"panic", "goexit"} {
				t.Run("ordinary-only/"+terminal, func(t *testing.T) {
					env, nr, roots, children := earlyWorkerEnv(t, terminal, false)
					env = append(env, "EARLY_ORDINARY_ONLY=true")
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestEarlyWorkerRoot$"}, Policy: policySettings{Retry: true}, Env: env})
					if got.code != 0 {
						t.Fatalf("ordinary terminal must retain retry recovery: exit%d %s %s", got.code, got.out, got.stderr)
					}
					requireEarlyMarker(t, nr, "")
					requireEarlyMarker(t, roots, "xxx")
					requireEarlyMarker(t, children, "xx")
					if len(reviewTestEvents(receiver, "TestEarlyWorkerRoot/TestOnce")) != 0 || len(reviewTestEvents(receiver, "TestEarlyWorkerRoot")) != 3 {
						t.Fatalf("ordinary retry event ownership changed: %v", receiver.events)
					}
				})
			}
		}
	}
}

const earlyActiveWorkerSource = `package fixture_test
import("testing";"os";"runtime")
func pendingAppend(t *testing.T,key string){f,e:=os.OpenFile(os.Getenv(key),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)}}
//ddtest:no_auto_retry
func TestEarlierCompleted(t *testing.T){pendingAppend(t,"PENDING_EARLIER")}
//ddtest:no_auto_retry
EXTRA
func TestPendingExcluded(t *testing.T){pendingAppend(t,"PENDING_ENTERED");t.Parallel();pendingAppend(t,"PENDING_COMPLETED")}
func TestPendingRoot(t *testing.T){
 pendingAppend(t,"PENDING_ROOT_CALLS")
 f,e:=os.OpenFile(os.Getenv("PENDING_ROOT_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("first root failure");return};if !os.IsExist(e){t.Fatal(e)}
 if os.Getenv("PENDING_WITH_EARLIER")=="true"{t.Run("earlier",TestEarlierCompleted)}
 t.Run("pending",TestPendingExcluded)
 t.Run("ordinary",func(t *testing.T){
  f,e:=os.OpenFile(os.Getenv("PENDING_CHILD_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e!=nil{if !os.IsExist(e){t.Fatal(e)};return};f.Close()
  entered,e:=os.ReadFile(os.Getenv("PENDING_ENTERED"));if e!=nil||string(entered)!="x"{t.Fatalf("writer precondition entered%q err%v",entered,e)}
  if _,e=os.Stat(os.Getenv("PENDING_COMPLETED"));!os.IsNotExist(e){t.Fatalf("writer precondition: pending child already completed %v",e)}
  pendingAppend(t,"PENDING_OBSERVED_INFLIGHT")
  if os.Getenv("PENDING_TERMINAL")=="goexit"{runtime.Goexit()};panic("pending ordinary worker panic sentinel")
 })
}
`

// Native t.Parallel is the barrier: the parent cannot execute the terminal
// child until the annotated callback entered and suspended before completion.
func checkMiniEarlyActiveWorkerState(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	for _, protected := range []bool{false, true} {
		extra := ""
		if protected {
			extra = "//ddtest:prevent_failure_suppression\n"
		}
		writeBuildFixture(t, dir, map[string]string{"pending_worker_test.go": strings.Replace(earlyActiveWorkerSource, "EXTRA\n", extra, 1)})
		bins := map[string]string{}
		for _, kind := range []string{"native", "Mini"} {
			bin := filepath.Join(t.TempDir(), executableName(fmt.Sprintf("pending-%s-%t.test", kind, protected)))
			build := driver
			if kind == "native" {
				build = "go"
			}
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("compile%d %s %s", code, out, stderr)
			}
			bins[kind] = bin
		}
		for _, terminal := range []string{"panic", "goexit"} {
			for _, earlier := range []bool{false, true} {
				if earlier && !protected {
					continue
				}
				for _, kind := range []string{"native", "Mini"} {
					if kind == "native" && earlier {
						continue
					}
					t.Run(fmt.Sprintf("protected=%t/earlier=%t/%s/%s", protected, earlier, terminal, kind), func(t *testing.T) {
						scratch := t.TempDir()
						entered, completed, roots, observed, earlierCalls := filepath.Join(scratch, "entered"), filepath.Join(scratch, "completed"), filepath.Join(scratch, "roots"), filepath.Join(scratch, "observed"), filepath.Join(scratch, "earlier")
						args := []string{"-test.run=^TestPendingRoot$"}
						if kind == "native" {
							args = append(args, "-test.count=2")
						}
						receiver, got := runParityCase(t, dir, bins[kind], parityCase{Args: args, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "PENDING_ENTERED=" + entered, "PENDING_COMPLETED=" + completed, "PENDING_ROOT_CALLS=" + roots, "PENDING_OBSERVED_INFLIGHT=" + observed, "PENDING_EARLIER=" + earlierCalls, "PENDING_WITH_EARLIER=" + fmt.Sprint(earlier), "PENDING_ROOT_FIRST=" + filepath.Join(scratch, "root-first"), "PENDING_CHILD_FIRST=" + filepath.Join(scratch, "child-first"), "PENDING_TERMINAL=" + terminal}})
						if got.code != 2 {
							t.Fatalf("active NoRetry must preserve native terminal exit2; got%d %s %s", got.code, got.out, got.stderr)
						}
						requireEarlyMarker(t, entered, "x")
						requireEarlyMarker(t, completed, "")
						requireEarlyMarker(t, roots, "xx")
						requireEarlyMarker(t, observed, "x")
						sentinel := "pending ordinary worker panic sentinel"
						if terminal == "goexit" {
							sentinel = "runtime.Goexit"
						}
						if !strings.Contains(got.out+got.stderr, sentinel) {
							t.Fatalf("lost native terminal value%s %s %s", sentinel, got.out, got.stderr)
						}
						if kind == "native" {
							return
						}
						if len(reviewTestEvents(receiver, "TestPendingRoot/pending")) != 0 {
							t.Fatalf("unfinished declaration synthesized a completed event: %v", receiver.events)
						}
						rootEvents := reviewTestEvents(receiver, "TestPendingRoot")
						if len(rootEvents) != 2 {
							t.Fatalf("active terminal root event count%d want2: %v", len(rootEvents), receiver.events)
						}
						for _, meta := range rootEvents {
							if meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil {
								t.Fatalf("active child's annotation activated on ordinary owner: %v", meta)
							}
						}
						earlierEvents := reviewTestEvents(receiver, "TestPendingRoot/earlier")
						if !earlier {
							requireEarlyMarker(t, earlierCalls, "")
							if len(earlierEvents) != 0 {
								t.Fatalf("invented earlier event: %v", earlierEvents)
							}
							return
						}
						requireEarlyMarker(t, earlierCalls, "x")
						if len(earlierEvents) != 1 {
							t.Fatalf("completed earlier event lost: %v", receiver.events)
						}
						meta := earlierEvents[0]
						requireReviewNoRetryOwnership(t, meta)
						if meta["test.status"] != "pass" || meta["test.final_status"] != "pass" || meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.prevent_failure_suppression"] != nil {
							t.Fatalf("completed earlier event changed: %v", meta)
						}
					})
				}
			}
		}
	}
}

const pfsWorkerTerminalSource = `package fixture_test
import("os";"runtime";"testing")
func pfsWorkerRecord(t *testing.T,key,value string)int{path:=os.Getenv(key);f,e:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString(value);e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)};d,e:=os.ReadFile(path);if e!=nil{t.Fatal(e)};return len(d)}
ANNOTATIONS
func TestPFSWorkerCallback(t *testing.T){
 if os.Getenv("PFS_WORKER_SELF_PARALLEL")=="true"{t.Parallel()}
 pfsWorkerRecord(t,"PFS_WORKER_CHILD","x")
 if os.Getenv("PFS_WORKER_QUEUED")=="true"{t.Run("queued",func(t *testing.T){t.Parallel();pfsWorkerRecord(t,"PFS_WORKER_ORDER","q")})}
 cleanup:=func(){pfsWorkerRecord(t,"PFS_WORKER_ORDER","c");panic("protected worker cleanup sentinel")}
 switch os.Getenv("PFS_WORKER_TERMINAL"){
 case "cleanup-panic":t.Cleanup(cleanup);return
 case "body-cleanup-panic":t.Cleanup(cleanup)
 case "body-cleanup-skip":t.Cleanup(func(){pfsWorkerRecord(t,"PFS_WORKER_ORDER","c");t.SkipNow()})
 case "body-cleanup-fail":t.Cleanup(func(){pfsWorkerRecord(t,"PFS_WORKER_ORDER","c");t.FailNow()})
 case "body-cleanup-goexit":t.Cleanup(func(){pfsWorkerRecord(t,"PFS_WORKER_ORDER","c");runtime.Goexit()})
 case "goexit":runtime.Goexit()
 }
 panic("protected worker body sentinel")
}
func TestPFSWorkerRoot(t *testing.T){
 pfsWorkerRecord(t,"PFS_WORKER_ROOT","x")
 if os.Getenv("PFS_WORKER_NATIVE")!="true"{f,e:=os.OpenFile(os.Getenv("PFS_WORKER_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("ordinary first failure before protected discovery");return};if !os.IsExist(e){t.Fatal(e)}}
 t.Run("critical",TestPFSWorkerCallback)
}
`

// PFS alone keeps native terminal semantics in ordinary worker callbacks;
// Both retains its established managed-event protocol. No child event is
// invented for a PFS-only ordinary callback whose owner is the retry family.
func checkMiniPFSWorkerTerminalPolicy(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	cases := []struct {
		mode         string
		queued       bool
		nativeExit   int
		order        string
		selfParallel bool
	}{
		{"panic", false, 2, "", false}, {"goexit", false, 2, "", false}, {"cleanup-panic", false, 2, "c", false}, {"body-cleanup-panic", false, 2, "c", false}, {"body-cleanup-skip", false, 0, "c", false}, {"body-cleanup-fail", false, 1, "c", false}, {"body-cleanup-goexit", false, 2, "c", false},
		{"cleanup-panic", true, 2, "qc", false}, {"body-cleanup-panic", true, 2, "c", false},
		{"cleanup-panic", true, 2, "qc", true},
	}
	for _, kind := range []string{"native", "pfs", "both"} {
		t.Run(kind, func(t *testing.T) {
			annotations := ""
			if kind != "native" {
				annotations = "//ddtest:prevent_failure_suppression"
			}
			if kind == "both" {
				annotations += "\n//ddtest:no_auto_retry"
			}
			writeBuildFixture(t, dir, map[string]string{"pfs_worker_terminal_test.go": strings.Replace(pfsWorkerTerminalSource, "ANNOTATIONS", annotations, 1)})
			bin := filepath.Join(t.TempDir(), executableName("pfs-worker-"+kind+".test"))
			build := driver
			if kind == "native" {
				build = "go"
			}
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("compile%d %s %s", code, out, stderr)
			}
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/queued=%t/self-parallel=%t", tc.mode, tc.queued, tc.selfParallel), func(t *testing.T) {
					scratch := t.TempDir()
					roots, child, order := filepath.Join(scratch, "roots"), filepath.Join(scratch, "child"), filepath.Join(scratch, "order")
					env := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "PFS_WORKER_ROOT=" + roots, "PFS_WORKER_CHILD=" + child, "PFS_WORKER_ORDER=" + order, "PFS_WORKER_FIRST=" + filepath.Join(scratch, "first"), "PFS_WORKER_TERMINAL=" + tc.mode, "PFS_WORKER_QUEUED=" + fmt.Sprint(tc.queued), "PFS_WORKER_SELF_PARALLEL=" + fmt.Sprint(tc.selfParallel), "PFS_WORKER_NATIVE=" + fmt.Sprint(kind == "native")}
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestPFSWorkerRoot$", "-test.parallel=2", "-test.timeout=20s"}, Policy: policySettings{Disabled: kind != "native", AttemptToFix: kind != "native", ManagementTarget: "TestPFSWorkerRoot", ManagementSuite: "pfs_worker_terminal_test.go"}, Env: env})
					wantCode := tc.nativeExit
					if kind == "both" && wantCode != 0 {
						wantCode = 1
					}
					if got.code != wantCode {
						t.Fatalf("%s terminal exit%d want%d %s %s", kind, got.code, wantCode, got.out, got.stderr)
					}
					wantRoots := "xx"
					if kind == "native" {
						wantRoots = "x"
					}
					requireEarlyMarker(t, roots, wantRoots)
					requireEarlyMarker(t, child, "x")
					requireEarlyMarker(t, order, tc.order)
					if kind == "native" {
						return
					}
					rootEvents := reviewTestEvents(receiver, "TestPFSWorkerRoot")
					if len(rootEvents) != 2 {
						t.Fatalf("family event count%d want2: %v", len(rootEvents), receiver.events)
					}
					var retry map[string]any
					for _, meta := range rootEvents {
						if meta["test.test_management.is_test_disabled"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" || meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil {
							t.Fatalf("root assignment/declaration identity changed: %v", meta)
						}
						if meta["test.is_retry"] == "true" {
							if retry != nil {
								t.Fatal("multiple retry-family events")
							}
							retry = meta
						} else if meta["test.status"] != "fail" || !strings.Contains(fmt.Sprint(meta["error.message"]), "ordinary first failure before protected discovery") {
							t.Fatalf("lost initial ordinary failure: %v", meta)
						}
					}
					if retry == nil || retry["test.retry_reason"] != "attempt_to_fix" || retry["test.test_management.attempt_to_fix_passed"] != "false" {
						t.Fatalf("missing original ATF attempt metadata: %v", retry)
					}
					wantStatus, wantFinal := "fail", "fail"
					if tc.nativeExit == 0 {
						wantStatus, wantFinal = "pass", "skip"
					}
					if retry["test.status"] != wantStatus || retry["test.final_status"] != wantFinal {
						t.Fatalf("protected/cancelled family outcome %v want%s/%s", retry, wantStatus, wantFinal)
					}
					if tc.mode == "body-cleanup-skip" {
						for _, key := range []string{"error.type", "error.message", "error.stack"} {
							if retry[key] != nil {
								t.Fatalf("native cleanup SkipNow cancellation revived %s: %v", key, retry)
							}
						}
					}
					if kind == "pfs" {
						if len(reviewTestEvents(receiver, "TestPFSWorkerRoot/critical")) != 0 {
							t.Fatalf("ordinary PFS callback gained an unsupported child event: %v", receiver.events)
						}
						testCount := 0
						for _, event := range receiver.events {
							if event["type"] == "test" {
								testCount++
							}
						}
						if testCount != 2 {
							t.Fatalf("PFS family-only event count%d want2: %v", testCount, receiver.events)
						}
						if tc.mode == "panic" || tc.mode == "goexit" || tc.mode == "cleanup-panic" || tc.mode == "body-cleanup-panic" || tc.mode == "body-cleanup-goexit" {
							if retry["error.type"] != "panic" {
								t.Fatalf("surviving native terminal lost panic type: %v", retry)
							}
						}
					} else {
						events := reviewTestEvents(receiver, "TestPFSWorkerRoot/critical")
						if len(events) != 1 {
							t.Fatalf("managed callback events%d want1: %v", len(events), receiver.events)
						}
						meta := events[0]
						requireReviewNoRetryOwnership(t, meta)
						status := "fail"
						if tc.nativeExit == 0 {
							status = "skip"
						}
						if meta["test.status"] != status || meta["test.final_status"] != status || meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.prevent_failure_suppression"] != "true" || meta["test.test_management.is_test_disabled"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" {
							t.Fatalf("managed callback outcome/assignment changed: %v", meta)
						}
					}
					sessions := 0
					for _, event := range receiver.events {
						if event["type"] != "test_session_end" {
							continue
						}
						sessions++
						meta := event["content"].(map[string]any)["meta"].(map[string]any)
						status := "fail"
						if wantCode == 0 {
							status = "pass"
						}
						if meta["test.status"] != status {
							t.Fatalf("session failed to preserve command outcome: %v", meta)
						}
					}
					if sessions != 1 {
						t.Fatalf("session count%d want1", sessions)
					}
				})
			}
		})
	}
	t.Run("ordinary-retry-recovery", checkMiniPFSWorkerRetryRecovery)
	t.Run("unmanaged-queued-terminal", checkMiniPFSUnmanagedQueuedTerminal)
	t.Run("concurrent-irreversible-failure", checkMiniPFSConcurrentIrreversibleWorkerFailure)
}

// Native callbacks, Testify rows, and actual methods retain ordinary ATR
// recovery when PFS is present without NoRetry. Declaration and reporting
// ownership can differ; positive family events and body counters prove recovery.
func checkMiniPFSWorkerRetryRecovery(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"pfs_recovery_test.go": `package fixture_test
import("os";"testing";"github.com/stretchr/testify/suite")
func pfsRecoveryCall(t *testing.T)int{path:=os.Getenv("PFS_RECOVERY_CHILD");f,e:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};f.Close();d,e:=os.ReadFile(path);if e!=nil{t.Fatal(e)};return len(d)}
//ddtest:prevent_failure_suppression
func TestPFSRecoverCallback(t *testing.T){if pfsRecoveryCall(t)==1{panic("recoverable protected callback sentinel")}}
type PFSRecoverRow struct{suite.Suite}
func(s *PFSRecoverRow) TestMethod(){s.T().Run("critical",TestPFSRecoverCallback)}
type PFSRecoverMethod struct{suite.Suite}
//ddtest:prevent_failure_suppression
func(s *PFSRecoverMethod) TestMethod(){if pfsRecoveryCall(s.T())==1{panic("recoverable protected method sentinel")}}
func TestPFSRecoveryRoot(t *testing.T){
 f,e:=os.OpenFile(os.Getenv("PFS_RECOVERY_ROOT"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};f.Close()
 f,e=os.OpenFile(os.Getenv("PFS_RECOVERY_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("first ordinary failure");return};if !os.IsExist(e){t.Fatal(e)}
 switch os.Getenv("PFS_RECOVERY_KIND"){case "callback":t.Run("critical",TestPFSRecoverCallback);case "row":suite.Run(t,new(PFSRecoverRow));case "method":suite.Run(t,new(PFSRecoverMethod));default:t.Fatal("invalid recovery kind")}
}
`})
	bin := filepath.Join(t.TempDir(), executableName("pfs-recovery.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile%d %s %s", code, out, stderr)
	}
	for _, kind := range []string{"callback", "row", "method"} {
		t.Run(kind, func(t *testing.T) {
			scratch := t.TempDir()
			roots, child := filepath.Join(scratch, "roots"), filepath.Join(scratch, "child")
			receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestPFSRecoveryRoot$"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "PFS_RECOVERY_KIND=" + kind, "PFS_RECOVERY_ROOT=" + roots, "PFS_RECOVERY_CHILD=" + child, "PFS_RECOVERY_FIRST=" + filepath.Join(scratch, "first")}})
			if got.code != 0 {
				t.Fatalf("PFS-only ordinary retry recovery failed: exit%d %s %s", got.code, got.out, got.stderr)
			}
			requireEarlyMarker(t, roots, "xxx")
			requireEarlyMarker(t, child, "xx")
			rootEvents := reviewTestEvents(receiver, "TestPFSRecoveryRoot")
			if len(rootEvents) != 3 {
				t.Fatalf("recovered family events%d want3: %v", len(rootEvents), receiver.events)
			}
			for _, meta := range rootEvents {
				if meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil {
					t.Fatalf("child policy leaked into ordinary retry owner: %v", meta)
				}
			}
		})
	}
}

// The same queued-child barrier is respected when the ordinary process owner
// has ATR without management metadata: surviving PFS terminals still block,
// while the otherwise permitted retry invokes the callback again.
func checkMiniPFSUnmanagedQueuedTerminal(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"pfs_worker_terminal_test.go": strings.Replace(pfsWorkerTerminalSource, "ANNOTATIONS", "//ddtest:prevent_failure_suppression", 1)})
	bin := filepath.Join(t.TempDir(), executableName("pfs-unmanaged-queued.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, false)...)
	if code != 0 {
		t.Fatalf("compile%d %s %s", code, out, stderr)
	}
	for _, tc := range []struct{ mode, order string }{{"body-cleanup-panic", "cc"}, {"cleanup-panic", "qcqc"}} {
		t.Run(tc.mode, func(t *testing.T) {
			scratch := t.TempDir()
			roots, child, order := filepath.Join(scratch, "roots"), filepath.Join(scratch, "child"), filepath.Join(scratch, "order")
			receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestPFSWorkerRoot$", "-test.parallel=2", "-test.timeout=20s"}, Policy: policySettings{Retry: true}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "PFS_WORKER_ROOT=" + roots, "PFS_WORKER_CHILD=" + child, "PFS_WORKER_ORDER=" + order, "PFS_WORKER_FIRST=" + filepath.Join(scratch, "first"), "PFS_WORKER_TERMINAL=" + tc.mode, "PFS_WORKER_QUEUED=true"}})
			if got.code != 2 {
				t.Fatalf("unmanaged PFS terminal exit%d want2 %s %s", got.code, got.out, got.stderr)
			}
			requireEarlyMarker(t, roots, "xxx")
			requireEarlyMarker(t, child, "xx")
			requireEarlyMarker(t, order, tc.order)
			events := reviewTestEvents(receiver, "TestPFSWorkerRoot")
			if len(events) != 3 {
				t.Fatalf("unmanaged ATR root event count%d want3: %v", len(events), receiver.events)
			}
			for _, meta := range events {
				for _, key := range []string{"test.test_management.no_auto_retry", "test.test_management.prevent_failure_suppression", "test.test_management.is_test_disabled", "test.test_management.is_attempt_to_fix", "test.test_management.is_quarantined"} {
					if meta[key] != nil {
						t.Fatalf("unmanaged owner gained %s: %v", key, meta)
					}
				}
			}
		})
	}
}

// An ordinary sibling can terminate a worker while a protected callback is
// still blocked. Its already-observed native failure must survive even though
// the protected callback has not returned or run cleanup.
func checkMiniPFSConcurrentIrreversibleWorkerFailure(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("os";"testing")
var irreversibleReady chan struct{}
func irreversibleRecord(t *testing.T,key string){f,e:=os.OpenFile(os.Getenv(key),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)}}
ANNOTATIONS
func TestBlockedProtected(t *testing.T){t.Parallel();irreversibleRecord(t,"IRREVERSIBLE_CHILD");t.Error("irreversible protected parallel failure");close(irreversibleReady);select{}}
func TestConcurrentPFSRoot(t *testing.T){
 irreversibleRecord(t,"IRREVERSIBLE_ROOT")
 if os.Getenv("IRREVERSIBLE_NATIVE")!="true"{f,e:=os.OpenFile(os.Getenv("IRREVERSIBLE_FIRST"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if e==nil{f.Close();t.Error("ordinary first failure");return};if !os.IsExist(e){t.Fatal(e)}}
 irreversibleReady=make(chan struct{})
 t.Run("protected",TestBlockedProtected)
 t.Run("ordinary",func(t *testing.T){t.Parallel();<-irreversibleReady;irreversibleRecord(t,"IRREVERSIBLE_TERMINAL");panic("ordinary sibling panic after irreversible PFS failure")})
}
`
	for _, kind := range []string{"native", "pfs", "ordinary"} {
		t.Run(kind, func(t *testing.T) {
			annotations := ""
			if kind == "pfs" {
				annotations = "//ddtest:prevent_failure_suppression"
			}
			writeBuildFixture(t, dir, map[string]string{"irreversible_pfs_test.go": strings.Replace(source, "ANNOTATIONS", annotations, 1)})
			bin := filepath.Join(t.TempDir(), executableName("irreversible-"+kind+".test"))
			build := driver

			if kind == "native" {
				build = "go"
			}
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("compile%d %s %s", code, out, stderr)
			}
			scratch := t.TempDir()
			roots, child, terminal := filepath.Join(scratch, "roots"), filepath.Join(scratch, "child"), filepath.Join(scratch, "terminal")
			receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestConcurrentPFSRoot$", "-test.parallel=2", "-test.timeout=10s"}, Policy: policySettings{Disabled: kind != "native", AttemptToFix: kind != "native", ManagementTarget: "TestConcurrentPFSRoot", ManagementSuite: "irreversible_pfs_test.go"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "IRREVERSIBLE_ROOT=" + roots, "IRREVERSIBLE_CHILD=" + child, "IRREVERSIBLE_TERMINAL=" + terminal, "IRREVERSIBLE_FIRST=" + filepath.Join(scratch, "first"), "IRREVERSIBLE_NATIVE=" + fmt.Sprint(kind == "native")}})
			r, re := os.ReadFile(roots)
			c, ce := os.ReadFile(child)
			p, pe := os.ReadFile(terminal)
			rows := []map[string]any{}
			for _, event := range receiver.events {
				if event["type"] == "test" {
					rows = append(rows, event["content"].(map[string]any)["meta"].(map[string]any))
				}
			}
			t.Logf("IRREVERSIBLE kind=%s cmd%d roots%q child%q terminal%q rows%d", kind, got.code, r, c, p, len(rows))
			for _, meta := range rows {
				t.Logf("ROW name=%v status=%v final=%v PFS=%v disabled=%v ATF=%v error=%v", meta["test.name"], meta["test.status"], meta["test.final_status"], meta["test.test_management.prevent_failure_suppression"], meta["test.test_management.is_test_disabled"], meta["test.test_management.is_attempt_to_fix"], meta["error.message"])
			}
			if re != nil || ce != nil || pe != nil || len(r) == 0 || len(c) == 0 || string(c) != string(p) {
				t.Fatalf("causal markersmissing root%q/%v child%q/%v terminal%q/%v %s %s", r, re, c, ce, p, pe, got.out, got.stderr)
			}

			wantCode, wantRoots := 2, "xx"
			if kind == "ordinary" {
				wantCode = 0
			}
			if kind == "native" {
				wantRoots = "x"
			}
			if got.code != wantCode {
				t.Fatalf("irreversible protected failure terminal exit%d want%d %s %s", got.code, wantCode, got.out, got.stderr)
			}
			requireEarlyMarker(t, roots, wantRoots)
			requireEarlyMarker(t, child, "x")
			requireEarlyMarker(t, terminal, "x")
			if kind == "native" {
				return
			}
			if len(rows) != 2 || len(reviewTestEvents(receiver, "TestConcurrentPFSRoot")) != 2 {
				t.Fatalf("concurrent family-only event count%d want2: %v", len(rows), receiver.events)
			}
			for _, meta := range rows {
				if meta["test.test_management.is_test_disabled"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" || meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil {
					t.Fatalf("concurrent family assignment/annotation ownership changed: %v", meta)
				}
				if meta["test.is_retry"] != "true" {
					continue
				}
				final := "fail"
				if kind == "ordinary" {
					final = "skip"
				}
				if meta["test.status"] != "fail" || meta["test.final_status"] != final || meta["error.type"] != "panic" || meta["error.message"] != "ordinary sibling panic after irreversible PFS failure" {
					t.Fatalf("irreversible failure lost from concurrent terminal outcome: %v", meta)
				}
			}

		})
	}
}

// A completed child's trusted protected failure survives a later crash of its
// still-open assigned parent. A passing child or a separate fresh event does
// not create that fact, and public annotation tags remain declaration-local.
func checkMiniProtectedDescendantCrashPolicy(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("os";"testing";"time")
func descendantRecord(t *testing.T,key string){f,e:=os.OpenFile(os.Getenv(key),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)}}
CHILD_ANNOTATION
func TestDescendantCrashChild(t *testing.T){descendantRecord(t,"DESCENDANT_CHILD");if os.Getenv("DESCENDANT_PASS")!="true"{t.Error("protected descendant assertion sentinel")}}
ROOT_ANNOTATION
func TestDescendantCrashParent(t *testing.T){
 descendantRecord(t,"DESCENDANT_PARENT")
 if os.Getenv("DESCENDANT_NO_CHILD")!="true"{t.Run("critical",TestDescendantCrashChild)}
 descendantRecord(t,"DESCENDANT_RETURNED")
 if os.Getenv("DESCENDANT_MODE")=="background"{go func(){panic("parent background crash after protected child")}()}
 time.Sleep(time.Minute)
}
`
	for _, kind := range []string{"protected-child", "ordinary-child", "direct-pfs-root", "no-child", "passing-child", "native"} {
		t.Run(kind, func(t *testing.T) {
			childAnnotation, rootAnnotation := "", ""
			if kind == "protected-child" || kind == "passing-child" || kind == "native" {
				childAnnotation = "//ddtest:prevent_failure_suppression"
			}
			if kind == "direct-pfs-root" {
				rootAnnotation = "//ddtest:prevent_failure_suppression"
			}
			fixture := strings.ReplaceAll(strings.ReplaceAll(source, "CHILD_ANNOTATION", childAnnotation), "ROOT_ANNOTATION", rootAnnotation)
			writeBuildFixture(t, dir, map[string]string{"descendant_crash_test.go": fixture})
			for _, mode := range []string{"timeout", "background"} {
				t.Run(mode, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), executableName("descendant-crash.test"))
					args := exclusionBuildArgs(bin, false)
					build := driver
					if kind == "native" {
						build = "go"
					} else if mode == "background" {
						args = append([]string{"test", "--panic-hook"}, args[1:]...)
					}
					out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, args...)
					if code != 0 {
						t.Fatalf("compile%d %s %s", code, out, stderr)
					}
					scratch := t.TempDir()
					parent, child, returned := filepath.Join(scratch, "parent"), filepath.Join(scratch, "child"), filepath.Join(scratch, "returned")
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestDescendantCrashParent$", "-test.timeout=250ms"}, Policy: policySettings{Quarantined: kind != "native", ManagementTarget: "TestDescendantCrashParent", ManagementSuite: "descendant_crash_test.go"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "DESCENDANT_PARENT=" + parent, "DESCENDANT_CHILD=" + child, "DESCENDANT_RETURNED=" + returned, "DESCENDANT_MODE=" + mode, "DESCENDANT_NO_CHILD=" + fmt.Sprint(kind == "no-child"), "DESCENDANT_PASS=" + fmt.Sprint(kind == "passing-child")}})
					if got.code != 2 {
						t.Fatalf("native process crash exit%d want2 %s %s", got.code, got.out, got.stderr)
					}
					requireEarlyMarker(t, parent, "x")
					requireEarlyMarker(t, returned, "x")
					wantChild := "x"
					if kind == "no-child" {
						wantChild = ""
					}
					requireEarlyMarker(t, child, wantChild)
					if kind == "native" {
						if len(receiver.events) != 0 {
							t.Fatal("native control emitted telemetry")
						}
						return
					}
					parents := reviewTestEvents(receiver, "TestDescendantCrashParent")
					children := reviewTestEvents(receiver, "TestDescendantCrashParent/critical")
					wantChildren := 1
					if kind == "no-child" {
						wantChildren = 0
					}
					if len(parents) != 1 || len(children) != wantChildren {
						t.Fatalf("actual descendant crash event counts parent%d child%d want1/%d: %v", len(parents), len(children), wantChildren, receiver.events)
					}
					testCount := 0
					for _, event := range receiver.events {
						if event["type"] == "test" {
							testCount++
						}
					}
					if testCount != 1+wantChildren {
						t.Fatalf("unexpected extra test events%d want%d", testCount, 1+wantChildren)
					}
					meta := parents[0]
					wantFinal := "skip"
					if kind == "protected-child" || kind == "direct-pfs-root" {
						wantFinal = "fail"
					}
					if meta["test.status"] != "fail" || meta["test.final_status"] != wantFinal || meta["test.test_management.is_quarantined"] != "true" || (meta["test.test_management.prevent_failure_suppression"] == "true") != (kind == "direct-pfs-root") || meta["test.test_management.no_auto_retry"] != nil {
						t.Fatalf("descendant crash trusted fact/assignment/declaration changed: %v", meta)
					}
					requireReviewNoRetryOwnership(t, meta)
					errorType, sentinel := "timeout", "test timed out after 250ms"
					if mode == "background" {
						errorType, sentinel = "panic", "parent background crash after protected child"
					}
					if meta["error.type"] != errorType || !strings.Contains(fmt.Sprint(meta["error.message"]), sentinel) {
						t.Fatalf("parent crash cause lost: %v", meta)
					}
					if wantChildren == 0 {
						return
					}
					cm := children[0]
					childFinal := "skip"
					if kind == "protected-child" || kind == "direct-pfs-root" || kind == "passing-child" {
						childFinal = "fail"
					}
					childStatus := "fail"
					if kind == "passing-child" {
						childStatus, childFinal = "pass", "pass"
					}
					protected := kind == "protected-child" || kind == "direct-pfs-root" || kind == "passing-child"
					if cm["test.status"] != childStatus || cm["test.final_status"] != childFinal || cm["test.test_management.is_quarantined"] != "true" || (cm["test.test_management.prevent_failure_suppression"] == "true") != protected || cm["test.test_management.no_auto_retry"] != nil {
						t.Fatalf("completed child outcome/assignment changed: %v", cm)
					}
					requireReviewNoRetryOwnership(t, cm)
					if kind == "passing-child" {
						if cm["error.type"] != nil || cm["error.message"] != nil {
							t.Fatalf("fresh passing child acquired a protected failure: %v", cm)
						}
					} else if cm["error.message"] != "protected descendant assertion sentinel" {
						t.Fatalf("original completed child assertion lost: %v", cm)
					}
				})
			}
		})
	}
	t.Run("active-child", checkMiniActiveProtectedDescendantCrashPolicy)
	t.Run("retained-atf", checkMiniRetainedDescendantCrashPolicy)
}

// ATF deliberately retains earlier protected failures across all permitted
// attempts. NoRetry cache omission and ordinary child's later pass both leave
// that trusted family truth intact when the second root crashes before return.
func checkMiniRetainedDescendantCrashPolicy(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("os";"testing";"time")
func retainedRecord(t *testing.T,key string)int{path:=os.Getenv(key);f,e:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if e!=nil{t.Fatal(e)};if _,e=f.WriteString("x");e!=nil{t.Fatal(e)};if e=f.Close();e!=nil{t.Fatal(e)};d,e:=os.ReadFile(path);if e!=nil{t.Fatal(e)};return len(d)}
//ddtest:prevent_failure_suppression
EXTRA
func TestRetainedProtectedChild(t *testing.T){if retainedRecord(t,"RETAINED_CHILD")==1{t.Error("first protected child failure before parent retry")}}
func TestRetainedProtectedParent(t *testing.T){
 n:=retainedRecord(t,"RETAINED_PARENT")
 if n==1{t.Run("critical",TestRetainedProtectedChild);retainedRecord(t,"RETAINED_RETURNED");return}
 if os.Getenv("RETAINED_NO_RETRY")=="true"{retainedRecord(t,"RETAINED_OMITTED")}else{t.Run("critical",TestRetainedProtectedChild);retainedRecord(t,"RETAINED_HEALTHY")}
 if os.Getenv("RETAINED_MODE")=="background"{go func(){panic("parent crash after retained or recovered protected child")}()}
 time.Sleep(time.Minute)
}
`
	for _, noRetry := range []bool{true, false} {
		extra := ""
		if noRetry {
			extra = "//ddtest:no_auto_retry\n"
		}
		writeBuildFixture(t, dir, map[string]string{"retained_descendant_crash_test.go": strings.Replace(source, "EXTRA\n", extra, 1)})
		for _, mode := range []string{"timeout", "background"} {
			t.Run(fmt.Sprintf("no-retry=%t/%s", noRetry, mode), func(t *testing.T) {
				bin := filepath.Join(t.TempDir(), executableName("retained-descendant.test"))
				args := exclusionBuildArgs(bin, false)
				if mode == "background" {
					args = append([]string{"test", "--panic-hook"}, args[1:]...)
				}
				out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, args...)
				if code != 0 {
					t.Fatalf("compile%d %s %s", code, out, stderr)
				}
				scratch := t.TempDir()
				paths := map[string]string{}
				for _, key := range []string{"PARENT", "CHILD", "RETURNED", "OMITTED", "HEALTHY"} {
					paths[key] = filepath.Join(scratch, strings.ToLower(key))
				}
				env := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "RETAINED_MODE=" + mode, "RETAINED_NO_RETRY=" + fmt.Sprint(noRetry)}
				for key, path := range paths {
					env = append(env, "RETAINED_"+key+"="+path)
				}
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestRetainedProtectedParent$", "-test.timeout=250ms"}, Policy: policySettings{Retry: true, Quarantined: true, AttemptToFix: true, ManagementTarget: "TestRetainedProtectedParent", ManagementSuite: "retained_descendant_crash_test.go"}, Env: env})
				if got.code != 2 {
					t.Fatalf("retained protected descendant parent crash exit%d want2 %s %s", got.code, got.out, got.stderr)
				}
				requireEarlyMarker(t, paths["PARENT"], "xx")
				requireEarlyMarker(t, paths["RETURNED"], "x")
				childCalls, wantChildren := "xx", 2
				if noRetry {
					childCalls, wantChildren = "x", 1
				}
				requireEarlyMarker(t, paths["CHILD"], childCalls)
				if noRetry {
					requireEarlyMarker(t, paths["OMITTED"], "x")
					requireEarlyMarker(t, paths["HEALTHY"], "")
				} else {
					requireEarlyMarker(t, paths["OMITTED"], "")
					requireEarlyMarker(t, paths["HEALTHY"], "x")
				}
				parents := reviewTestEvents(receiver, "TestRetainedProtectedParent")
				children := reviewTestEvents(receiver, "TestRetainedProtectedParent/critical")
				if len(parents) != 2 || len(children) != wantChildren {
					t.Fatalf("retained attempt event counts parent%d child%d want2/%d: %v", len(parents), len(children), wantChildren, receiver.events)
				}
				var retry map[string]any
				for _, meta := range parents {
					if meta["test.test_management.is_quarantined"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" || meta["test.test_management.prevent_failure_suppression"] != nil || meta["test.test_management.no_auto_retry"] != nil {
						t.Fatalf("retained parent assignment/declaration changed: %v", meta)
					}
					if meta["test.is_retry"] == "true" {
						if retry != nil {
							t.Fatal("duplicate retry parent event")
						}
						retry = meta
					}
				}
				if retry == nil || retry["test.status"] != "fail" || retry["test.final_status"] != "fail" || retry["test.retry_reason"] != "attempt_to_fix" {
					t.Fatalf("known retained ATF protection lost before crash: %v", retry)
				}
				errorType := "timeout"
				if mode == "background" {
					errorType = "panic"
				}
				if retry["error.type"] != errorType {
					t.Fatalf("retained parent crash cause lost: %v", retry)
				}
				failed, passed := 0, 0
				for _, meta := range children {
					if meta["test.test_management.prevent_failure_suppression"] != "true" || (meta["test.test_management.no_auto_retry"] == "true") != noRetry || meta["test.test_management.is_quarantined"] != "true" || meta["test.test_management.is_attempt_to_fix"] != "true" {
						t.Fatalf("retained child annotation/assignment lost: %v", meta)
					}
					switch meta["test.status"] {
					case "fail":
						failed++
						if meta["test.final_status"] != "fail" || meta["error.message"] != "first protected child failure before parent retry" {
							t.Fatalf("original child failure lost: %v", meta)
						}
					case "pass":
						passed++
						if meta["test.final_status"] != "pass" || meta["error.type"] != nil {
							t.Fatalf("later child pass corrupted: %v", meta)
						}
					default:
						t.Fatalf("unexpected child outcome: %v", meta)
					}
					if noRetry {
						requireReviewNoRetryOwnership(t, meta)
					}
				}
				wantPassed := 1
				if noRetry {
					wantPassed = 0
				}
				if failed != 1 || passed != wantPassed {
					t.Fatalf("original/later child outcomes failed%d passed%d want1/%d", failed, passed, wantPassed)
				}
			})
		}
	}
}

// A failure that returned to an active child is already irreversible even when
// the child body and its cleanup never finish before the process crashes.
func checkMiniActiveProtectedDescendantCrashPolicy(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	source := `package fixture_test
import("os";"testing")
var activeCrashReady=make(chan struct{})
func activeCrashMark(t *testing.T,key string){if e:=os.WriteFile(os.Getenv(key),[]byte("x"),0600);e!=nil{t.Fatal(e)}}
CHILD_ANNOTATION
func TestActiveCrashChild(t *testing.T){
 activeCrashMark(t,"ACTIVE_CHILD")
 switch os.Getenv("ACTIVE_FAILURE"){case "Fail":t.Fail();case "Error":t.Error("active child assertion before crash");case "Errorf":t.Errorf("active child %s before crash","assertion");default:t.Fatal("unknown failure operation")}
 activeCrashMark(t,"ACTIVE_AFTER_ERROR");close(activeCrashReady);select{}
}
ROOT_ANNOTATION
func TestActiveCrashParent(t *testing.T){activeCrashMark(t,"ACTIVE_PARENT");if os.Getenv("ACTIVE_MODE")=="background"{go func(){<-activeCrashReady;panic("parent crash while protected child is active")}()};t.Run("critical",TestActiveCrashChild);activeCrashMark(t,"ACTIVE_RETURNED")}
`
	for _, kind := range []string{"protected-child", "ordinary-child", "direct-pfs-root", "native-protected", "native-ordinary"} {
		t.Run(kind, func(t *testing.T) {
			childAnnotation, rootAnnotation := "", ""
			native := strings.HasPrefix(kind, "native-")
			if kind == "protected-child" || kind == "native-protected" {
				childAnnotation = "//ddtest:prevent_failure_suppression"
			}
			if kind == "direct-pfs-root" {
				rootAnnotation = "//ddtest:prevent_failure_suppression"
			}
			fixture := strings.ReplaceAll(strings.ReplaceAll(source, "CHILD_ANNOTATION", childAnnotation), "ROOT_ANNOTATION", rootAnnotation)
			writeBuildFixture(t, dir, map[string]string{"active_crash_test.go": fixture})
			failures := []string{"Error"}
			if kind == "protected-child" || kind == "ordinary-child" {
				failures = []string{"Fail", "Error", "Errorf"}
			}
			for _, mode := range []string{"timeout", "background"} {
				t.Run(mode, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), executableName("active-crash.test"))
					args := exclusionBuildArgs(bin, false)
					build := driver
					if native {
						build = "go"
					} else if mode == "background" {
						args = append([]string{"test", "--panic-hook"}, args[1:]...)
					}
					out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), build, args...)
					if code != 0 {
						t.Fatalf("compile%d %s %s", code, out, stderr)
					}
					for _, failure := range failures {
						t.Run(failure, func(t *testing.T) {
							scratch := t.TempDir()
							paths := map[string]string{}
							env := []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=in_process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=false", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=false", "ACTIVE_MODE=" + mode, "ACTIVE_FAILURE=" + failure}
							for _, key := range []string{"PARENT", "CHILD", "AFTER_ERROR", "RETURNED"} {
								paths[key] = filepath.Join(scratch, key)
								env = append(env, "ACTIVE_"+key+"="+paths[key])
							}
							receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestActiveCrashParent$", "-test.timeout=250ms"}, Policy: policySettings{Quarantined: !native, ManagementTarget: "TestActiveCrashParent", ManagementSuite: "active_crash_test.go"}, Env: env})
							if got.code != 2 {
								t.Fatalf("active child native terminal exit%d want2 %s %s", got.code, got.out, got.stderr)
							}
							for _, key := range []string{"PARENT", "CHILD", "AFTER_ERROR"} {
								requireEarlyMarker(t, paths[key], "x")
							}
							requireEarlyMarker(t, paths["RETURNED"], "")
							if native {
								if len(receiver.events) != 0 {
									t.Fatal("native active-child control emitted telemetry")
								}
								return
							}
							parents := reviewTestEvents(receiver, "TestActiveCrashParent")
							children := reviewTestEvents(receiver, "TestActiveCrashParent/critical")
							count := 0
							for _, event := range receiver.events {
								if event["type"] == "test" {
									count++
								}
							}
							if len(parents) != 1 || len(children) != 1 || count != 2 {
								t.Fatalf("active crash exact counts parent%d child%d total%d want1/1/2: %v", len(parents), len(children), count, receiver.events)
							}
							final := "fail"
							if kind == "ordinary-child" {
								final = "skip"
							}
							errorType, sentinel := "timeout", "test timed out after 250ms"
							if mode == "background" {
								errorType, sentinel = "panic", "parent crash while protected child is active"
							}
							for _, row := range []struct {
								meta      map[string]any
								protected bool
							}{{parents[0], kind == "direct-pfs-root"}, {children[0], kind != "ordinary-child"}} {
								m := row.meta
								if m["test.status"] != "fail" || m["test.final_status"] != final || m["test.test_management.is_quarantined"] != "true" || (m["test.test_management.prevent_failure_suppression"] == "true") != row.protected || m["test.test_management.no_auto_retry"] != nil {
									t.Fatalf("active irreversible failure or assignment/declaration lost: %v", m)
								}
								requireReviewNoRetryOwnership(t, m)
								if m["error.type"] != errorType || !strings.Contains(fmt.Sprint(m["error.message"]), sentinel) {
									t.Fatalf("active event crash cause lost: %v", m)
								}
							}
						})
					}
				})
			}
		})
	}
}
