package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMiniDeclarationExclusions checks command outcome and actual CI events,
// including process retry children, with no dependency on runtime source reads.
func TestMiniDeclarationExclusions(t *testing.T) {
	cases := []struct {
		name, comments   string
		prevent, noRetry bool
	}{
		{"both", "//ddtest:prevent_failure_suppression\n//ddtest:no_auto_retry\n", true, true},
		{"no-retry", "//ddtest:no_auto_retry\n", false, true},
		{"prevent-suppression", "//ddtest:prevent_failure_suppression\n", true, false},
		{"ordinary", "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, driver := prepareMiniFixture(t)
			path := filepath.Join(dir, "sample_test.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "func TestManaged(", tc.comments+"func TestManaged(", 1))
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(t.TempDir(), executableName("exclusions.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("compile: %d %s %s", code, out, stderr)
			}
			// Compiled annotation metadata survives source removal and process retries.
			if err = os.Rename(path, path+".saved"); err != nil {
				t.Fatal(err)
			}
			for _, profile := range []string{"disabled", "quarantined", "attempt_to_fix", "efd", ""} {
				t.Run("profile-"+profile, func(t *testing.T) {
					got := executeProfile(t, dir, bin, []string{"-test.run=^TestManaged$", "-mode=managed"}, true, profile == "", profile)
					wantCode := 1
					if !tc.prevent && (profile == "disabled" || profile == "quarantined") {
						wantCode = 0
					}
					if got.code != wantCode {
						t.Fatalf("exit=%d want=%d\n%s\n%v", got.code, wantCode, got.stderr, got.events)
					}
					count := 0
					for _, event := range got.events {
						if !strings.Contains(event, `"type":"test"`) {
							continue
						}
						count++
						for key, want := range map[string]bool{"prevent_failure_suppression": tc.prevent, "no_auto_retry": tc.noRetry} {
							tag := `"test.test_management.` + key + `":"true"`
							if strings.Contains(event, tag) != want {
								t.Fatalf("annotation %s expected=%t: %s", key, want, event)
							}
						}
						assigned := map[string]string{"disabled": "is_test_disabled", "quarantined": "is_quarantined", "attempt_to_fix": "is_attempt_to_fix"}[profile]
						if assigned != "" && !strings.Contains(event, `"test.test_management.`+assigned+`":"true"`) {
							t.Fatalf("lost assigned property: %s", event)
						}
						if tc.noRetry && strings.Contains(event, `"test.is_retry":"true"`) {
							t.Fatalf("excluded test retried: %s", event)
						}
					}
					if count == 0 || tc.noRetry && count != 1 {
						t.Fatalf("test events=%d: %v", count, got.events)
					}
					if !tc.noRetry && (profile == "efd" || profile == "") && count < 2 {
						t.Fatalf("unexcluded test did not retry: %v", got.events)
					}
				})
			}
		})
	}
	t.Run("table-declarations", checkMiniTableExclusions)
	t.Run("top-level-tia", checkMiniTopLevelExcludedTIA)
	t.Run("cleanup-terminal", checkMiniExcludedCleanupTerminal)
	t.Run("terminal-provenance", checkMiniTerminalProvenance)
	t.Run("crash-final-status", checkMiniExcludedCrashFinalStatus)
	t.Run("descendant-crash-policy", checkMiniProtectedDescendantCrashPolicy)
	t.Run("terminal-worker-provenance", checkMiniTerminalWorkerProvenance)
	t.Run("parallel-cleanup", checkMiniExcludedParallelCleanup)
	t.Run("cleanup-goexit", checkMiniExcludedCleanupGoexit)
}

// An excluded Testify method must not rerun with its parent suite, and an
// ordinary sibling must still receive the parent's automatic retries.
func TestMiniTestifyExclusionsKeepSiblingRetries(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"exclusion_suite_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type ExclusionSuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *ExclusionSuite) TestOnce(){
 f,err:=os.OpenFile(os.Getenv("EXCLUSION_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
 if os.Getenv("EXCLUSION_FAIL")=="true" {tt:=s.T();tt.Cleanup(func(){tt.Error("excluded cleanup failure")})}
}
//ddtest:no_auto_retry
func(s ExclusionSuite) TestValue(){
 f,err:=os.OpenFile(os.Getenv("VALUE_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
}
func(s *ExclusionSuite) TestSibling(){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err==nil{f.Close();s.T().Error("recoverable sibling failure")}else if !os.IsExist(err){s.T().Fatal(err)}
}
func TestExclusionSuite(t *testing.T){suite.Run(t,new(ExclusionSuite))}
`})
	bin := filepath.Join(t.TempDir(), executableName("testify-exclusions.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile: %d %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, failed := range []bool{false, true} {
			for _, efd := range []bool{false, true} {
				for _, managed := range []bool{false, true} {
					t.Run(fmt.Sprint("managed=", managed, "-efd=", efd, "-")+mode+"-failed="+fmt.Sprint(failed), func(t *testing.T) {
						counter := filepath.Join(t.TempDir(), "once")
						valueCounter := filepath.Join(t.TempDir(), "value")
						receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestExclusionSuite$", "-test.count=2"}, Policy: policySettings{Retry: true, EFD: efd, Quarantined: managed, AttemptToFix: managed, ManagementTarget: "TestExclusionSuite", ManagementSuite: "exclusion_suite_test.go"}, Env: []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "EXCLUSION_COUNTER=" + counter, "VALUE_COUNTER=" + valueCounter, fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), fmt.Sprintf("EXCLUSION_FAIL=%t", failed)}})
						want := 0
						if failed && !managed {
							want = 1
						}
						if got.code != want {
							t.Fatalf("exit %d want %d: %s %s", got.code, want, got.out, got.stderr)
						}
						data, err := os.ReadFile(counter)
						if err != nil || string(data) != "xx" {
							t.Fatalf("excluded method executed %q: %v", data, err)
						}
						valueData, err := os.ReadFile(valueCounter)
						if err != nil || string(valueData) != "xx" {
							t.Fatalf("value receiver executed %q: %v", valueData, err)
						}
						tests := 0
						valueTests := 0
						for _, event := range receiver.events {
							if event["type"] != "test" {
								continue
							}
							meta := event["content"].(map[string]any)["meta"].(map[string]any)
							if meta["test.test_management.no_auto_retry"] == "true" {
								for _, key := range []string{"test.is_retry", "test.retry_reason", "test.retry.execution_mode", "test.test_management.attempt_to_fix_passed"} {
									if meta[key] != nil {
										t.Fatalf("excluded method inherited %s: %v", key, meta)
									}
								}
								if managed && meta["test.test_management.is_attempt_to_fix"] != "true" {
									t.Fatalf("lost ATF assignment: %v", meta)
								}
							}
							if meta["test.name"] == "TestExclusionSuite/TestValue" {
								valueTests++
								if meta["test.test_management.no_auto_retry"] != "true" {
									t.Fatalf("missing value receiver tag: %v", meta)
								}
							}
							if meta["test.name"] == "TestExclusionSuite/TestOnce" {
								tests++
								if meta["test.test_management.no_auto_retry"] != "true" {
									t.Fatalf("missing method declaration tag: %v", meta)
								}
							}
						}
						if tests != 2 || valueTests != 2 {
							t.Fatalf("excluded method emitted %d events", tests)
						}
					})
				}
			}
		}
	}
	t.Run("subtest-gate", func(t *testing.T) { checkMiniSubtestGate(t, dir, bin) })
	t.Run("imported-promoted-declarations", checkMiniImportedPromotedExclusions)
	t.Run("generic-declarations", checkMiniGenericDeclarations)
	t.Run("cgo-declarations", checkMiniCgoImportedExclusions)
	t.Run("runtime-dependency-declarations", checkMiniRuntimeDependencyExclusions)
	t.Run("declaration-cache", checkMiniDeclarationCacheIdentity)
	t.Run("promoted-declaration-cache", checkMiniPromotedDeclarationCache)
	t.Run("public-method-exclusions", checkMiniPublicMethodExclusions)
	t.Run("testify-native-callback", checkMiniTestifyNativeCallback)
	t.Run("nested-callback-policy", checkMiniReviewNestedRows)
	t.Run("early-worker-terminal-state", checkMiniEarlyWorkerTerminalState)
	t.Run("pfs-worker-terminal-policy", checkMiniPFSWorkerTerminalPolicy)
	t.Run("ordinary-worker-management", checkMiniReviewWorkerManagement)
	t.Run("ordinary-worker-atf-override", checkMiniReviewWorkerOverride)
	t.Run("inherited-owner-cache", checkMiniInheritedOwnerCache)
	t.Run("protected-aggregate-failure", checkMiniProtectedAggregateFailure)
	t.Run("omitted-excluded-failure", checkMiniOmittedExcludedFailure)
	t.Run("ancestor-declaration-cache", checkMiniAncestorDeclarationCache)
	t.Run("nested-worker-declarations", checkMiniNestedWorkerDeclarations)
	t.Run("late-worker-tia", checkMiniLateExcludedTIA)
	t.Run("process-continuations", checkMiniExclusionProcessContinuations)
	t.Run("isolated-declaration-root", checkMiniExcludedIsolationRoot)
	t.Run("nested-management-cache", checkMiniNestedManagementCache)
	t.Run("large-exclusion-payload", checkMiniLargeExclusionPayload)
	t.Run("protected-suppression", checkMiniTestifyProtection)
	t.Run("late-declaration", checkMiniLateExclusionDeclaration)
}

func checkMiniTestifyProtection(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"protected_suite_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type ProtectedSuite struct{suite.Suite}
//ddtest:prevent_failure_suppression
func(s *ProtectedSuite) TestCritical(){
 if os.Getenv("PROTECTED_RECOVER")=="true" || os.Getenv("PROTECTED_LATE")=="true" {
  f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
  if err!=nil && os.IsExist(err){if os.Getenv("PROTECTED_RECOVER")=="true" {return}}else{if err!=nil{s.T().Fatal(err)};f.Close();if os.Getenv("PROTECTED_LATE")=="true"{return}}
 }
 s.T().Error("protected failure")
}
type ProtectedCleanupSuite struct{suite.Suite}
//ddtest:prevent_failure_suppression
//ddtest:no_auto_retry
func(s *ProtectedCleanupSuite) TestCritical(){tt:=s.T();tt.Cleanup(func(){tt.Error("protected cleanup failure")})}
func TestProtectedSuite(t *testing.T){
 if os.Getenv("PROTECTED_SKIP")=="true"{t.Skip("native parent skip")}
 if os.Getenv("PROTECTED_CLEANUP")=="true"{suite.Run(t,new(ProtectedCleanupSuite))}else{suite.Run(t,new(ProtectedSuite))}
}
`})
	bin := filepath.Join(t.TempDir(), executableName("protected.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, tc := range []struct {
			name, target, suite                                               string
			disabled, quarantined, recover, skip, attempt, late, cleanup, efd bool
			want                                                              int
		}{
			{name: "parent-disabled", target: "TestProtectedSuite", suite: "protected_suite_test.go", disabled: true, want: 0},
			{name: "native-parent-skip", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, skip: true, want: 0},
			{name: "parent-quarantine-atf-earlier-failure", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, attempt: true, recover: true, want: 1},
			{name: "efd-retry-recovers", target: "TestProtectedSuite/TestCritical", suite: "protected_suite_test.go/ProtectedSuite", quarantined: true, recover: true, efd: true, want: 0},
			{name: "parent-quarantine-atf-late-failure", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, attempt: true, late: true, want: 1},
			{name: "parent-quarantined-cleanup", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, cleanup: true, want: 1},
			{name: "parent-quarantine-atf-cleanup", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, attempt: true, cleanup: true, want: 1},
			{name: "parent-quarantined", target: "TestProtectedSuite", suite: "protected_suite_test.go", quarantined: true, want: 1},
			{name: "selected-method-disabled", target: "TestProtectedSuite/TestCritical", suite: "protected_suite_test.go/ProtectedSuite", disabled: true, want: 1},
			{name: "retry-recovers", target: "TestProtectedSuite/TestCritical", suite: "protected_suite_test.go/ProtectedSuite", quarantined: true, recover: true, want: 0},
		} {
			t.Run(mode+"-"+tc.name, func(t *testing.T) {
				method := "TestCritical"
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestProtectedSuite$/^" + method + "$"}, Policy: policySettings{Retry: true, EFD: tc.efd, Disabled: tc.disabled, Quarantined: tc.quarantined, AttemptToFix: tc.attempt, ManagementTarget: tc.target, ManagementSuite: tc.suite}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", fmt.Sprintf("PROTECTED_RECOVER=%t", tc.recover), fmt.Sprintf("PROTECTED_SKIP=%t", tc.skip), fmt.Sprintf("PROTECTED_LATE=%t", tc.late), fmt.Sprintf("PROTECTED_CLEANUP=%t", tc.cleanup), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", tc.efd)}})
				if got.code != tc.want {
					t.Fatalf("exit %d want %d: %s %s", got.code, tc.want, got.out, got.stderr)
				}
				seen := false
				lastRootStatus := ""
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] == "TestProtectedSuite/"+method {
						seen = true
						if meta["test.test_management.prevent_failure_suppression"] != "true" {
							t.Fatalf("missing declaration: %v", meta)
						}
						if tc.cleanup && (meta["test.status"] != "fail" || meta["test.test_management.no_auto_retry"] != "true" || meta["test.is_retry"] != nil) {
							t.Fatalf("protected cleanup outcome lost: %v", meta)
						}
						assignment := "test.test_management.is_quarantined"
						if tc.disabled {
							assignment = "test.test_management.is_test_disabled"
						}
						if meta[assignment] != "true" {
							t.Fatalf("lost assignment: %v", meta)
						}
					} else if meta["test.test_management.prevent_failure_suppression"] != nil {
						t.Fatalf("annotation leaked into ancestor: %v", meta)
					} else if meta["test.name"] == "TestProtectedSuite" {
						lastRootStatus, _ = meta["test.final_status"].(string)
					}
				}
				if tc.want == 1 && lastRootStatus != "fail" {
					t.Fatalf("protected failure root final_status=%q", lastRootStatus)
				}
				if tc.recover && tc.want == 0 && lastRootStatus != "pass" {
					t.Fatalf("recovered root final_status=%q", lastRootStatus)
				}
				if !seen && tc.want != 0 {
					t.Fatal("protected method not reported")
				}
			})
		}
	}
}

func checkMiniTableExclusions(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	writeBuildFixture(t, dir, map[string]string{"rows_test.go": `package fixture_test
import "testing"
func sharedRow(t *testing.T){t.Parallel();t.Run("nested",func(t *testing.T){t.Error("row failed")})}
//ddtest:prevent_failure_suppression
//ddtest:no_auto_retry
func TestCriticalRows(t *testing.T){for _,name:=range []string{"a","b"}{t.Run(name,sharedRow)}}
func TestOrdinaryRows(t *testing.T){for _,name:=range []string{"a","b"}{t.Run(name,sharedRow)}}
`})
	bin := filepath.Join(t.TempDir(), executableName("table.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, false)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, profile := range []string{"disabled", "quarantined", "attempt_to_fix"} {
			t.Run(mode+"-"+profile, func(t *testing.T) {
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^Test(CriticalRows|OrdinaryRows)$", "-test.parallel=4"}, Policy: policySettings{Retry: true, Disabled: profile == "disabled", Quarantined: profile == "quarantined", AttemptToFix: profile == "attempt_to_fix", ManagementTarget: "TestCriticalRows", ManagementSuite: "rows_test.go"}, Env: []string{"DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				if got.code != 1 {
					t.Fatalf("exit %d: %s %s", got.code, got.out, got.stderr)
				}
				critical, ordinary := 0, 0
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					name, _ := meta["test.name"].(string)
					if strings.HasPrefix(name, "TestCriticalRows") {
						critical++
						for _, key := range []string{"prevent_failure_suppression", "no_auto_retry"} {
							if meta["test.test_management."+key] != "true" {
								t.Fatalf("missing generated case annotation: %v", meta)
							}
						}
						if meta["test.is_retry"] != nil {
							t.Fatalf("annotated generated case retried: %v", meta)
						}
					} else if strings.HasPrefix(name, "TestOrdinaryRows") {
						ordinary++
						for _, key := range []string{"prevent_failure_suppression", "no_auto_retry"} {
							if meta["test.test_management."+key] != nil {
								t.Fatalf("shared callback leaked policy: %v", meta)
							}
						}
					}
				}
				if critical != 5 || ordinary < 7 {
					t.Fatalf("events critical=%d ordinary=%d", critical, ordinary)
				}
			})
		}
	}
}

func exclusionBuildArgs(bin string, modules bool) []string {
	args := []string{"test", "-c", "-o", bin}
	if modules {
		args = append(args, "-mod=mod")
	}
	if os.Getenv("PARITY_TEST_MODE") == "race" {
		args = append(args, "-race")
	}
	return append(args, ".")
}

func checkMiniLateExclusionDeclaration(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"late_suite_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
type LateSuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *LateSuite) TestOnce(){
 f,err:=os.OpenFile(os.Getenv("EXCLUSION_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
 if os.Getenv("LATE_FAIL")=="true"{s.T().Error("excluded failure")}
}
func(s *LateSuite) TestSibling(){if os.Getenv("LATE_SIBLING_PASS")!="true" {s.T().Error("parent must retry")}}
func TestLateSuite(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err==nil{f.Close();if os.Getenv("LATE_INITIAL_PASS")!="true"{t.Error("fail before discovering the suite")};return};if !os.IsExist(err){t.Fatal(err)}
 suite.Run(t,new(LateSuite))
}
`})
	bin := filepath.Join(t.TempDir(), executableName("late.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, family := range []string{"atr", "efd", "atf-parent", "atr-method-atf"} {
			efd := family == "efd"
			atf := family == "atf-parent" || family == "atr-method-atf"
			target, suiteName := "TestLateSuite", "late_suite_test.go"
			if family == "atr-method-atf" {
				target = "TestLateSuite/TestOnce"
				suiteName += "/LateSuite"
			}
			t.Run(mode+"-"+family, func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "once")
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestLateSuite$"}, Policy: policySettings{Retry: !efd, EFD: efd, AttemptToFix: atf, ManagementTarget: target, ManagementSuite: suiteName}, Env: []string{"EXCLUSION_COUNTER=" + counter, fmt.Sprintf("LATE_INITIAL_PASS=%t", family == "atf-parent"), fmt.Sprintf("LATE_FAIL=%t", atf), fmt.Sprintf("LATE_SIBLING_PASS=%t", family == "atr-method-atf"), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", !efd), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "DD_CIVISIBILITY_INTERNAL_PARALLEL_EARLY_FLAKE_DETECTION_ENABLED=true", "DD_CIVISIBILITY_RETRY_PROCESS_MAX_CONCURRENCY=2"}})
				if got.code != 1 {
					t.Fatalf("exit %d: %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if err != nil || string(data) != "x" {
					t.Fatalf("late-discovered excluded callback executed %q: %v", data, err)
				}
				events := 0
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] != "TestLateSuite/TestOnce" {
						continue
					}
					events++
					if meta["test.test_management.no_auto_retry"] != "true" {
						t.Fatalf("missing late declaration: %v", meta)
					}
					if atf && (meta["test.test_management.is_attempt_to_fix"] != "true" || meta["test.status"] != "fail") {
						t.Fatalf("excluded ATF assignment/result lost: %v", meta)
					}
					for _, key := range []string{"test.is_retry", "test.retry_reason", "test.retry.execution_mode", "test.test_management.attempt_to_fix_passed"} {
						if meta[key] != nil {
							t.Fatalf("first excluded execution inherited retry marker %s: %v", key, meta)
						}
					}
				}
				if events != 1 {
					t.Fatalf("late-discovered excluded execution events=%d", events)
				}
			})
		}
	}
}

func checkMiniSubtestGate(t *testing.T, dir, bin string) {
	for _, mode := range []string{"in_process", "process"} {
		for _, profile := range []string{"disabled", "quarantined", "attempt_to_fix"} {
			t.Run(mode+"-"+profile, func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "once")
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestExclusionSuite$/^TestOnce$"}, Policy: policySettings{Retry: true, Disabled: profile == "disabled", Quarantined: profile == "quarantined", AttemptToFix: profile == "attempt_to_fix", ManagementTarget: "TestExclusionSuite/TestOnce", ManagementSuite: "exclusion_suite_test.go/ExclusionSuite"}, Env: []string{"DD_CIVISIBILITY_SUBTEST_FEATURES_ENABLED=false", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", "EXCLUSION_COUNTER=" + counter, "EXCLUSION_FAIL=true"}})
				if got.code != 1 {
					t.Fatalf("gated backend action controlled command: %d %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if err != nil || string(data) != "x" {
					t.Fatalf("excluded callback count %q: %v", data, err)
				}
				events := 0
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] != "TestExclusionSuite/TestOnce" {
						continue
					}
					events++
					assigned := map[string]string{"disabled": "is_test_disabled", "quarantined": "is_quarantined", "attempt_to_fix": "is_attempt_to_fix"}[profile]
					if meta["test.status"] != "fail" || meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management."+assigned] != "true" {
						t.Fatalf("gated execution/assignment mismatch: %v", meta)
					}
					if meta["test.is_retry"] != nil || meta["test.test_management.attempt_to_fix_passed"] != nil {
						t.Fatalf("excluded method claimed retry: %v", meta)
					}
				}
				if events != 1 {
					t.Fatalf("gated excluded event count=%d", events)
				}
			})
		}
	}
}
