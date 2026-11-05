package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniDeclarationCacheIdentity(t *testing.T) {
	for _, excludedSecond := range []bool{false, true} {
		t.Run(fmt.Sprint("second-excluded=", excludedSecond), func(t *testing.T) {
			dir, driver := prepareTestifyFixture(t, false)
			annotation := ""
			if excludedSecond {
				annotation = "//ddtest:no_auto_retry"
			}
			source := strings.ReplaceAll(`package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
func recordCacheCall(t *testing.T,letter string){f,err:=os.OpenFile(os.Getenv("CACHE_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString(letter);f.Close();if err!=nil{t.Fatal(err)}}
type FirstCacheSuite struct{suite.Suite}
type SecondCacheSuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *FirstCacheSuite) TestSame(){recordCacheCall(s.T(),"a")}
__SECOND_ANNOTATION__
func(s *SecondCacheSuite) TestSame(){recordCacheCall(s.T(),"b");s.T().Error("different declaration must execute")}
func TestSwitch(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err==nil{f.Close();suite.Run(t,new(FirstCacheSuite));t.Error("retry parent");return};if !os.IsExist(err){t.Fatal(err)}
 suite.Run(t,new(SecondCacheSuite))
}
`, "__SECOND_ANNOTATION__", annotation)
			writeBuildFixture(t, dir, map[string]string{"cache_suite_test.go": source})
			bin := filepath.Join(t.TempDir(), executableName("cache-identity.test"))
			out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
			if code != 0 {
				t.Fatalf("compile %d: %s %s", code, out, stderr)
			}
			for _, mode := range []string{"in_process", "process"} {
				t.Run(mode, func(t *testing.T) {
					counter := filepath.Join(t.TempDir(), "calls")
					_, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestSwitch$"}, Policy: policySettings{Retry: true}, Env: []string{"CACHE_CALLS=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
					if got.code != 1 {
						t.Fatalf("different declaration's failure was hidden: %d %s %s", got.code, got.out, got.stderr)
					}
					data, err := os.ReadFile(counter)
					want := "abb"
					if excludedSecond {
						want = "ab"
					}
					if err != nil || string(data) != want {
						t.Fatalf("declaration calls=%q want=%q err=%v", data, want, err)
					}
				})
			}
		})
	}
}

func checkMiniNestedWorkerDeclarations(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"nested_worker_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
func recordNestedCall(t *testing.T,letter string){f,err:=os.OpenFile(os.Getenv("NESTED_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString(letter);f.Close();if err!=nil{t.Fatal(err)}}
type OuterExcludedSuite struct{suite.Suite}
type InnerOrdinarySuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *OuterExcludedSuite) TestOuter(){recordNestedCall(s.T(),"o");suite.Run(s.T(),new(InnerOrdinarySuite))}
func(s *InnerOrdinarySuite) TestInner(){recordNestedCall(s.T(),"i")}
func TestNestedWorker(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
 if err==nil{f.Close();t.Error("fail before discovery");return};if !os.IsExist(err){t.Fatal(err)}
 suite.Run(t,new(OuterExcludedSuite))
}
`})
	bin := filepath.Join(t.TempDir(), executableName("nested-worker.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, efd := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-efd=%t", mode, efd), func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "calls")
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestNestedWorker$"}, Policy: policySettings{Retry: !efd, EFD: efd}, Env: []string{"NESTED_CALLS=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", !efd), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				if got.code != 0 {
					t.Fatalf("valid nested worker result failed: %d %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if err != nil || string(data) != "oi" {
					t.Fatalf("nested callback calls=%q: %v", data, err)
				}
				outer := 0
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					if meta["test.name"] == "TestNestedWorker/TestOuter" {
						outer++
						if meta["test.test_management.no_auto_retry"] != "true" || meta["test.is_retry"] != nil {
							t.Fatalf("outer declaration metadata: %v", meta)
						}
					}
					if meta["test.name"] == "TestNestedWorker/TestOuter/TestInner" && meta["test.test_management.no_auto_retry"] != nil {
						t.Fatalf("ordinary nested method inherited exclusion: %v", meta)
					}
				}
				if outer != 1 {
					t.Fatalf("outer execution events=%d", outer)
				}
			})
		}
	}
}

func checkMiniLargeExclusionPayload(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"large_exclusion_test.go": `package fixture_test
import("testing";"os";"fmt";"strings";"github.com/stretchr/testify/suite")
type LargeExcludedSuite struct{suite.Suite}
//ddtest:no_auto_retry
func(s *LargeExcludedSuite) TestExcluded(){for i:=0;i<4096;i++{s.T().Run(fmt.Sprintf("row-%03d-%s",i,strings.Repeat("x",110)),func(t *testing.T){f,err:=os.OpenFile(os.Getenv("LARGE_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString("x");f.Close();if err!=nil{t.Fatal(err)}})}}
func(s *LargeExcludedSuite) TestRecovery(){f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.Close();s.T().Error("recover on retry")}else if !os.IsExist(err){s.T().Fatal(err)}}
func TestLargeRoot(t *testing.T){if path:=os.Getenv("LARGE_DISCOVERY");path!=""{f,err:=os.OpenFile(path,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.Close();t.Error("discover excluded rows during retry");return};if !os.IsExist(err){t.Fatal(err)}};suite.Run(t,new(LargeExcludedSuite))}
`})
	bin := filepath.Join(t.TempDir(), executableName("large-exclusion.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, late := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/late=%t", mode, late), func(t *testing.T) {
				counter := filepath.Join(t.TempDir(), "calls")
				discovery := ""
				if late {
					discovery = filepath.Join(t.TempDir(), "discovery")
				}
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestLargeRoot$"}, Policy: policySettings{Retry: true}, Env: []string{"LARGE_CALLS=" + counter, "LARGE_DISCOVERY=" + discovery, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				if got.code != 0 {
					t.Fatalf("large excluded cache/events blocked ordinary retry: %d %s %s", got.code, got.out, got.stderr)
				}
				data, err := os.ReadFile(counter)
				if err != nil || len(data) != 4096 {
					t.Fatalf("excluded row callbacks=%d want4096 err=%v", len(data), err)
				}
				seen := map[string]bool{}
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					name, _ := meta["test.name"].(string)
					if name != "TestLargeRoot/TestExcluded" && !strings.HasPrefix(name, "TestLargeRoot/TestExcluded/") {
						continue
					}
					if seen[name] || meta["test.test_management.no_auto_retry"] != "true" || meta["test.is_retry"] != nil {
						t.Fatalf("excluded row duplicated or declaration lost: %v", meta)
					}
					seen[name] = true
				}
				if len(seen) != 4097 {
					t.Fatalf("excluded execution events=%d want4097", len(seen))
				}
			})
		}
	}
}

func checkMiniExcludedCleanupTerminal(t *testing.T) {
	for _, directive := range []string{"no_auto_retry", "prevent_failure_suppression"} {
		t.Run(directive, func(t *testing.T) {
			dir, driver := prepareMiniFixture(t)
			writeBuildFixture(t, dir, map[string]string{"cleanup_terminal_test.go": `package fixture_test
import("testing";"os")
//ddtest:` + directive + `
func TestCleanupTerminal(t *testing.T){t.Cleanup(func(){panic("excluded cleanup terminal")})}
func TestLaterThanCleanup(t *testing.T){if err:=os.WriteFile(os.Getenv("AFTER_CLEANUP"),[]byte("ran"),0600);err!=nil{t.Fatal(err)}}
`})
			native := filepath.Join(t.TempDir(), executableName("native.test"))
			out, stderr, code := command(t, dir, testEnv(), "go", "test", "-c", "-o", native, ".")
			if code != 0 {
				t.Fatalf("native compile: %d %s %s", code, out, stderr)
			}
			bin := filepath.Join(t.TempDir(), executableName("cleanup.test"))
			out, stderr, code = command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, false)...)
			if code != 0 {
				t.Fatalf("Mini compile: %d %s %s", code, out, stderr)
			}
			for _, target := range []struct{ name, bin string }{{"native", native}, {"Mini", bin}} {
				t.Run(target.name, func(t *testing.T) {
					after := filepath.Join(t.TempDir(), "after")
					receiver, got := runParityCase(t, dir, target.bin, parityCase{Args: []string{"-test.run=^Test(CleanupTerminal|LaterThanCleanup)$"}, Env: []string{"AFTER_CLEANUP=" + after}})
					if got.code != 2 || !strings.Contains(got.stderr, "excluded cleanup terminal") {
						t.Fatalf("cleanup panic: exit=%d out=%s stderr=%s", got.code, got.out, got.stderr)
					}
					if _, err := os.Stat(after); !os.IsNotExist(err) {
						t.Fatalf("later test ran after terminal cleanup: %v", err)
					}
					if target.name == "Mini" {
						var tests []map[string]any
						for _, event := range receiver.events {
							if event["type"] == "test" {
								tests = append(tests, event["content"].(map[string]any))
							}
						}
						if len(tests) != 1 {
							t.Fatalf("cleanup events=%v", tests)
						}
						meta := tests[0]["meta"].(map[string]any)
						if meta["test.status"] != "fail" || meta["test.test_management."+directive] != "true" {
							t.Fatalf("cleanup final event=%v", meta)
						}
					}
				})
			}
		})
	}
}

func checkMiniNestedManagementCache(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"nested_managed_test.go": `package fixture_test
import("testing";"os";"strings";"github.com/stretchr/testify/suite")
type NestedManagedSuite struct{suite.Suite}
type OuterManagedSuite struct{suite.Suite}
func(s *OuterManagedSuite) TestManaged(){if os.Getenv("NESTED_NO_RETRY_ANCESTOR")=="true"{suite.Run(s.T(),new(NoRetryAncestor))}else{suite.Run(s.T(),new(NestedManagedSuite))}}
type NoRetryAncestor struct{suite.Suite}
//ddtest:no_auto_retry
func(s *NoRetryAncestor) TestAncestor(){suite.Run(s.T(),new(NestedManagedSuite))}
func managedPath(t *testing.T,key string)string{p:=os.Getenv(key);mark:="/"+os.Getenv("MANAGED_METHOD");if !strings.HasSuffix(t.Name(),mark)&&!strings.Contains(t.Name(),mark+"/"){p+=".other"};return p}
func appendManagedCall(t *testing.T)int {p:=managedPath(t,"MANAGED_CALLS");f,err:=os.OpenFile(p,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString("x");f.Close();if err!=nil{t.Fatal(err)};data,err:=os.ReadFile(p);if err!=nil{t.Fatal(err)};return len(data)}
func runManagedRows(t *testing.T){callback:=func(t *testing.T){f,err:=os.OpenFile(managedPath(t,"MANAGED_ROWS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString(t.Name()+"\n");f.Close();if err!=nil{t.Fatal(err)}};t.Run("same",callback);t.Run("same",callback)}
//ddtest:no_auto_retry
func(s *NestedManagedSuite) TestOnce(){appendManagedCall(s.T());runManagedRows(s.T())}
//ddtest:no_auto_retry
//ddtest:prevent_failure_suppression
func(s *NestedManagedSuite) TestProtected(){n:=appendManagedCall(s.T());runManagedRows(s.T());if os.Getenv("FILTERED_PROTECTED_FAIL")=="true"||os.Getenv("MANAGED_METHOD")=="TestProtected"&&n==1{s.T().Error("first protected callback must remain failed")}}
func TestNestedManaged(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);first:=err==nil;if first{f.Close()}else if !os.IsExist(err){t.Fatal(err)}
 suite.Run(t,new(OuterManagedSuite));if first&&os.Getenv("NESTED_NO_PARENT_FAILURE")!="true"{t.Error("retry the parent")}
}
`})
	bin := filepath.Join(t.TempDir(), executableName("nested-managed.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	t.Run("native-filter", func(t *testing.T) {
		path := filepath.Join(dir, "nested_managed_test.go")
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Error(err)
			}
		})
		on := strings.Replace(string(source), "//ddtest:no_auto_retry\nfunc(s *NestedManagedSuite) TestOnce", "func(s *NestedManagedSuite) TestOnce", 1)
		off := strings.Replace(on, "//ddtest:no_auto_retry\n//ddtest:prevent_failure_suppression\nfunc(s *NestedManagedSuite) TestProtected", "func(s *NestedManagedSuite) TestProtected", 1)
		if off == on {
			t.Fatal("filter control annotation missing")
		}
		for _, variant := range []struct {
			name, source string
			native       bool
		}{{"native", on, true}, {"annotation-off", off, false}, {"annotation-on", on, false}} {
			t.Run(variant.name, func(t *testing.T) {
				if err := os.WriteFile(path, []byte(variant.source), 0600); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), executableName("filter.test"))
				builder := driver
				if variant.native {
					builder = "go"
				}
				out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), builder, exclusionBuildArgs(target, true)...)
				if code != 0 {
					t.Fatalf("compile %d: %s %s", code, out, stderr)
				}
				calls := filepath.Join(t.TempDir(), "calls")
				rows := filepath.Join(t.TempDir(), "rows")
				receiver, got := runParityCase(t, dir, target, parityCase{Args: []string{"-test.run=^TestNestedManaged$/^TestManaged$/^TestOnce$"}, Policy: policySettings{Quarantined: true, ManagementTarget: "TestNestedManaged/TestManaged", ManagementSuite: "nested_managed_test.go/OuterManagedSuite"}, Env: []string{"MANAGED_CALLS=" + calls, "MANAGED_ROWS=" + rows, "MANAGED_METHOD=TestOnce", "FILTERED_PROTECTED_FAIL=true", "NESTED_NO_PARENT_FAILURE=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process"}})
				_, otherErr := os.Stat(calls + ".other")
				t.Logf("filter variant=%s exit=%d filteredCallbackAdmitted=%t", variant.name, got.code, otherErr == nil)
				if got.code != 0 {
					t.Fatalf("filtered protected method changed selected ordinary command: exit=%d %s %s events=%v", got.code, got.out, got.stderr, receiver.events)
				}
				if variant.native || variant.name == "annotation-on" {
					if !os.IsNotExist(otherErr) {
						t.Fatalf("filtered-out method ran: %v", otherErr)
					}
				}
			})
		}
	})
	t.Run("protected-excluded-ancestor", func(t *testing.T) {
		calls := filepath.Join(t.TempDir(), "calls")
		rows := filepath.Join(t.TempDir(), "rows")
		_, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestNestedManaged$/^TestManaged$/^TestAncestor$/^TestProtected$"}, Policy: policySettings{EFD: true, Quarantined: true, ManagementTarget: "TestNestedManaged/TestManaged", ManagementSuite: "nested_managed_test.go/OuterManagedSuite"}, Env: []string{"MANAGED_CALLS=" + calls, "MANAGED_ROWS=" + rows, "MANAGED_METHOD=TestProtected", "NESTED_NO_RETRY_ANCESTOR=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=true"}})
		data, err := os.ReadFile(calls)
		if got.code != 1 || err != nil || string(data) != "x" {
			t.Fatalf("excluded ancestor lost protected descendant failure: exit=%d calls=%q err=%v %s %s", got.code, data, err, got.out, got.stderr)
		}
	})
	qNativeInvocations := 2
	t.Run("ordinary-count-control", func(t *testing.T) {
		path := filepath.Join(dir, "nested_managed_test.go")
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		ordinary := strings.Replace(string(source), "//ddtest:no_auto_retry\nfunc(s *NestedManagedSuite) TestOnce", "func(s *NestedManagedSuite) TestOnce", 1)
		if ordinary == string(source) {
			t.Fatal("ordinary control annotation missing")
		}
		if err = os.WriteFile(path, []byte(ordinary), 0600); err != nil {
			t.Fatal(err)
		}
		ordinaryBin := filepath.Join(t.TempDir(), executableName("ordinary-managed.test"))
		out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(ordinaryBin, true)...)
		if err = os.WriteFile(path, source, 0600); err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			t.Fatalf("ordinary compile %d: %s %s", code, out, stderr)
		}
		calls := filepath.Join(t.TempDir(), "calls")
		rows := filepath.Join(t.TempDir(), "rows")
		_, got := runParityCase(t, dir, ordinaryBin, parityCase{Args: []string{"-test.run=^TestNestedManaged$/^TestManaged$/^TestOnce$", "-test.count=2"}, Policy: policySettings{Retry: true, Quarantined: true, ManagementTarget: "TestNestedManaged/TestManaged", ManagementSuite: "nested_managed_test.go/OuterManagedSuite"}, Env: []string{"MANAGED_CALLS=" + calls, "MANAGED_ROWS=" + rows, "MANAGED_METHOD=TestOnce", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=process", "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
		data, err := os.ReadFile(calls)
		t.Logf("ordinary nested quarantine calls=%q exit=%d", data, got.code)
		if got.code != 0 || err != nil || len(data) < 1 {
			t.Fatalf("ordinary nested isolation baseline changed: calls=%q err=%v exit=%d %s %s", data, err, got.code, got.out, got.stderr)
		}
		qNativeInvocations = min(len(data), 2)
	})
	for _, mode := range []string{"in_process", "process"} {
		for _, atf := range []bool{false, true} {
			for _, efd := range []bool{false, true} {
				for _, protected := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/atf=%t/efd=%t/protected=%t", mode, atf, efd, protected), func(t *testing.T) {
						calls := filepath.Join(t.TempDir(), "calls")
						rows := filepath.Join(t.TempDir(), "rows")
						wantCalls := 2
						method := "TestOnce"
						want := 0
						if protected {
							method = "TestProtected"
							want = 1
						}
						// The existing native masking fallback does not admit this nested
						// quarantine declaration in in-process mode; retain its boundary.
						if !atf && mode == "in_process" {
							want, wantCalls = 0, 0
						} else if !atf && !efd {
							// The ordinary control above has one admitted isolated
							// invocation for this native-count/parent-retry combination.
							wantCalls = qNativeInvocations
						}
						receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestNestedManaged$/^TestManaged$/^" + method + "$", "-test.count=2"}, Policy: policySettings{Retry: !efd, EFD: efd, Quarantined: !atf, AttemptToFix: atf, ManagementTarget: "TestNestedManaged/TestManaged", ManagementSuite: "nested_managed_test.go/OuterManagedSuite"}, Env: []string{"MANAGED_CALLS=" + calls, "MANAGED_ROWS=" + rows, "MANAGED_METHOD=" + method, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", !efd), fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", efd), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
						if got.code != want {
							t.Fatalf("nested policy outcome=%d want%d: %s %s", got.code, want, got.out, got.stderr)
						}
						data, err := os.ReadFile(calls)
						if len(data) != wantCalls || got.code != want {
							for _, event := range receiver.events {
								if event["type"] == "test" {
									meta := event["content"].(map[string]any)["meta"].(map[string]any)
									t.Logf("event name=%v status=%v noRetry=%v protected=%v retry=%v reason=%v", meta["test.name"], meta["test.status"], meta["test.test_management.no_auto_retry"], meta["test.test_management.prevent_failure_suppression"], meta["test.is_retry"], meta["test.retry_reason"])
								}
							}
						}
						if wantCalls == 0 {
							if !os.IsNotExist(err) {
								t.Fatalf("unadmitted declaration ran: %q %v", data, err)
							}
						} else if err != nil || len(data) != wantCalls {
							t.Fatalf("excluded callback calls=%q want%d err=%v", data, wantCalls, err)
						}
						rowData, rowErr := os.ReadFile(rows)
						if wantCalls > 0 {
							lines := strings.Fields(string(rowData))
							counts := map[string]int{}
							for _, line := range lines {
								counts[line]++
							}
							if rowErr != nil || len(lines) != wantCalls*2 || len(counts) != 2 {
								t.Fatalf("shared duplicate row callbacks=%q err=%v", rowData, rowErr)
							}
							for _, count := range counts {
								if count != wantCalls {
									t.Fatalf("native count/duplicate row callbacks=%v", counts)
								}
							}
						} else if !os.IsNotExist(rowErr) {
							t.Fatalf("unadmitted rows ran: %q %v", rowData, rowErr)
						}
						count := 0
						for _, event := range receiver.events {
							if event["type"] != "test" {
								continue
							}
							meta := event["content"].(map[string]any)["meta"].(map[string]any)
							if meta["test.name"] == "TestNestedManaged/TestManaged/"+method {
								count++
								if meta["test.test_management.no_auto_retry"] != "true" || meta["test.is_retry"] != nil || meta["test.retry_reason"] != nil {
									t.Fatalf("excluded event=%v", meta)
								}
							}
						}
						if count != wantCalls {
							t.Fatalf("excluded events=%d want%d", count, wantCalls)
						}
					})
				}
			}
		}
	}
}

func checkMiniAncestorDeclarationCache(t *testing.T) {
	dir, driver := prepareTestifyFixture(t, false)
	writeBuildFixture(t, dir, map[string]string{"ancestor_cache_test.go": `package fixture_test
import("testing";"os";"github.com/stretchr/testify/suite")
func ancestorCall(t *testing.T,s string){f,err:=os.OpenFile(os.Getenv("ANCESTOR_CALLS"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)};_,err=f.WriteString(s);f.Close();if err!=nil{t.Fatal(err)}}
type IndependentLeaf struct{suite.Suite}
//ddtest:no_auto_retry
func(s *IndependentLeaf) TestLeaf(){ancestorCall(s.T(),"d");s.T().Run("generated",func(t *testing.T){ancestorCall(t,"r")})}
type FirstAncestor struct{suite.Suite}
type SecondAncestor struct{suite.Suite}
//ddtest:no_auto_retry
func(s *FirstAncestor) TestSame(){ancestorCall(s.T(),"a");suite.Run(s.T(),new(IndependentLeaf))}
//ddtest:no_auto_retry
func(s *SecondAncestor) TestSame(){ancestorCall(s.T(),"b");suite.Run(s.T(),new(IndependentLeaf))}
type OrdinaryAncestorOuter struct{suite.Suite}
func(s *OrdinaryAncestorOuter) TestOuter(){f,err:=os.OpenFile(os.Getenv("ANCESTOR_SWITCH"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.Close();suite.Run(s.T(),new(FirstAncestor));s.T().Error("retry ordinary ancestor");return};if !os.IsExist(err){s.T().Fatal(err)};suite.Run(s.T(),new(SecondAncestor))}
func TestSwitchedAncestor(t *testing.T){f,err:=os.OpenFile(os.Getenv("POC_RETRY_COUNTER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if os.Getenv("ANCESTOR_ORDINARY")=="true"{if err==nil{f.Close();t.Error("discover ordinary wrapper on retry");return};if !os.IsExist(err){t.Fatal(err)};suite.Run(t,new(OrdinaryAncestorOuter));return};if err==nil{f.Close();suite.Run(t,new(FirstAncestor));t.Error("retry parent");return};if !os.IsExist(err){t.Fatal(err)};suite.Run(t,new(SecondAncestor))}
`})
	bin := filepath.Join(t.TempDir(), executableName("ancestor-cache.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, ordinary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/ordinary=%t", mode, ordinary), func(t *testing.T) {
				calls := filepath.Join(t.TempDir(), "calls")
				_, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestSwitchedAncestor$"}, Policy: policySettings{Retry: true}, Env: []string{"ANCESTOR_CALLS=" + calls, "ANCESTOR_SWITCH=" + filepath.Join(t.TempDir(), "switch"), fmt.Sprintf("ANCESTOR_ORDINARY=%t", ordinary), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
				data, err := os.ReadFile(calls)
				if got.code != 0 || err != nil || string(data) != "adrb" {
					t.Fatalf("changed ancestor lost independent descendant history: calls=%q err=%v exit=%d %s %s", data, err, got.code, got.out, got.stderr)
				}
			})
		}
	}
}
