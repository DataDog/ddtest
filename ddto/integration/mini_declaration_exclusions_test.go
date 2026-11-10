package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture's counter files record how many times each test body ran, so
// retries and suppression are observed in the process, not only in events.
const declarationExclusionsFixture = `package fixture_test
import("fmt";"os";"path/filepath";"testing";"github.com/stretchr/testify/suite")
func count(t *testing.T,name string)int{
 path:=filepath.Join(os.Getenv("EXCLUSION_COUNTER"),name)
 f,err:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{t.Fatal(err)}
 data,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 return len(data)
}
//dd:test.no_auto_retry
func TestNoRetry(t *testing.T){count(t,"noretry");t.Error("no_auto_retry failure")}
func TestOrdinaryRetry(t *testing.T){if count(t,"ordinary")==1{t.Error("first ordinary failure")}}
//dd:test.prevent_failure_suppression
func TestProtected(t *testing.T){count(t,"protected");t.Error("protected failure")}
func TestSuppressed(t *testing.T){count(t,"suppressed");t.Error("suppressed failure")}
type ExclusionSuite struct{suite.Suite}
//dd:test.no_auto_retry
func (s *ExclusionSuite) TestOnce(){count(s.T(),"method-once")}
func (s *ExclusionSuite) TestFlaky(){if count(s.T(),"method-flaky")==1{s.T().Error("first sibling failure")}}
func TestExclusionSuite(t *testing.T){suite.Run(t,new(ExclusionSuite))}
//dd:test.no_auto_retry
func onceRow(t *testing.T){count(t,"row-once")}
func TestRows(t *testing.T){
 t.Run("once",onceRow)
 t.Run("flaky",func(t *testing.T){if count(t,"row-flaky")==1{t.Error("first row failure")}})
}
type ProtectedSuite struct{suite.Suite}
//dd:test.prevent_failure_suppression
func (s *ProtectedSuite) TestProtectedMethod(){count(s.T(),"protected-method");s.T().Error("protected method failure")}
func (s *ProtectedSuite) TestSuppressedMethod(){count(s.T(),"suppressed-method");s.T().Error("suppressed method failure")}
func TestProtectedSuite(t *testing.T){suite.Run(t,new(ProtectedSuite))}
//dd:test.prevent_failure_suppression
func FuzzProtected(f *testing.F){f.Add(1);f.Fuzz(func(t *testing.T,v int){count(t,"fuzz");t.Error("protected fuzz failure")})}
//dd:test.prevent_failure_suppression
func Example_protected(){
 fmt.Println("actual")
 // Output: expected
}
`

// //dd:test.no_auto_retry and //dd:test.prevent_failure_suppression are read
// from the test's own declaration, as //dd:test.unskippable is. A no_auto_retry
// test runs once, and a Testify suite that ran one is not retried either.
// prevent_failure_suppression overrides only the test's own quarantine or
// disable assignment.
func TestMiniDeclarationExclusions(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"exclusions_test.go": declarationExclusionsFixture})
	bin := filepath.Join(t.TempDir(), executableName("exclusions.test"))
	args := []string{"test", "-c", "-o", bin, "-mod=mod"}
	if os.Getenv("PARITY_TEST_MODE") == "race" {
		args = append(args, "-race")
	}
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, append(args, ".")...)
	if code != 0 {
		t.Fatalf("compile %d: %s %s", code, out, stderr)
	}
	retry := []string{"DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}
	for _, tc := range []struct {
		name     string
		run      string
		policy   policySettings
		env      []string
		wantExit int
		// counts maps a counter to the number of body executions; "" means none.
		counts map[string]string
		check  func(t *testing.T, events map[string][]map[string]any)
		// verify replaces the wantExit check when the outcome depends on the mode.
		verify func(t *testing.T, counters string, exit int)
	}{
		{
			name: "auto-retry", run: "^Test(NoRetry|OrdinaryRetry|ExclusionSuite|Rows)$", policy: policySettings{Retry: true}, env: retry, wantExit: 1,
			// The suite and TestRows ran a no_auto_retry method or callback, so
			// their flaky siblings are not retried.
			counts: map[string]string{"noretry": "x", "ordinary": "xx", "method-once": "x", "method-flaky": "x", "row-once": "x", "row-flaky": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				for _, name := range []string{"TestNoRetry", "TestExclusionSuite/TestOnce", "TestRows/once"} {
					requireSingleEvent(t, events, name, map[string]any{"test.test_management.no_auto_retry": "true", "test.is_retry": nil})
				}
				for _, name := range []string{"TestExclusionSuite", "TestRows"} {
					requireSingleEvent(t, events, name, map[string]any{"test.is_retry": nil, "test.test_management.no_auto_retry": nil})
				}
				if len(events["TestOrdinaryRetry"]) != 2 {
					t.Fatalf("ordinary test events=%d, want its retry", len(events["TestOrdinaryRetry"]))
				}
			},
		},
		{
			name: "attempt-to-fix", run: "^TestNoRetry$", policy: policySettings{AttemptToFix: true, ManagementTarget: "TestNoRetry", ManagementSuite: "exclusions_test.go"}, wantExit: 1,
			counts: map[string]string{"noretry": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestNoRetry", map[string]any{"test.test_management.is_attempt_to_fix": nil})
			},
		},
		{
			name: "quarantined-protected", run: "^TestProtected$", policy: policySettings{Quarantined: true, ManagementTarget: "TestProtected", ManagementSuite: "exclusions_test.go"}, wantExit: 1,
			counts: map[string]string{"protected": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestProtected", map[string]any{"test.test_management.prevent_failure_suppression": "true", "test.test_management.is_quarantined": nil, "test.status": "fail"})
			},
		},
		{
			name: "quarantined-control", run: "^TestSuppressed$", policy: policySettings{Quarantined: true, ManagementTarget: "TestSuppressed", ManagementSuite: "exclusions_test.go"}, wantExit: 0,
			counts: map[string]string{"suppressed": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestSuppressed", map[string]any{"test.test_management.is_quarantined": "true", "test.test_management.prevent_failure_suppression": nil})
			},
		},
		{
			name: "disabled-protected", run: "^TestProtected$", policy: policySettings{Disabled: true, ManagementTarget: "TestProtected", ManagementSuite: "exclusions_test.go"}, wantExit: 1,
			counts: map[string]string{"protected": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestProtected", map[string]any{"test.test_management.is_test_disabled": nil, "test.status": "fail"})
			},
		},
		{
			name: "quarantined-protected-method", run: "^TestProtectedSuite$/^TestProtectedMethod$", policy: policySettings{Quarantined: true, ManagementTarget: "TestProtectedSuite/TestProtectedMethod", ManagementSuite: "exclusions_test.go/ProtectedSuite"}, wantExit: 1,
			counts: map[string]string{"protected-method": "x"},
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestProtectedSuite/TestProtectedMethod", map[string]any{"test.test_management.prevent_failure_suppression": "true", "test.test_management.is_quarantined": nil, "test.status": "fail"})
			},
		},
		{
			// Whether a quarantined Testify method's body runs depends on the
			// mode (race isolation runs it in a child); its failure stays suppressed.
			name: "quarantined-method-control", run: "^TestProtectedSuite$/^TestSuppressedMethod$", policy: policySettings{Quarantined: true, ManagementTarget: "TestProtectedSuite/TestSuppressedMethod", ManagementSuite: "exclusions_test.go/ProtectedSuite"}, wantExit: 0,
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestProtectedSuite/TestSuppressedMethod", map[string]any{"test.test_management.is_quarantined": "true"})
			},
		},
		{
			// A disabled no_auto_retry method does not run in the first execution,
			// so it does not stop its suite's retries. (A process-mode retry child
			// runs Testify methods without Test Management, so it may run there.)
			name: "disabled-noretry-method", run: "^TestExclusionSuite$", policy: policySettings{Disabled: true, Retry: true, ManagementTarget: "TestExclusionSuite/TestOnce", ManagementSuite: "exclusions_test.go/ExclusionSuite"}, env: retry, wantExit: 0,
			counts: map[string]string{"method-flaky": "xx"},
			verify: requireAtMostOneExecution("method-once", 0),
		},
		{
			// Race isolation runs a quarantined Testify method in a child; the
			// method must still run at most once, whichever attempt runs it.
			name: "quarantined-noretry-method", run: "^TestExclusionSuite$", policy: policySettings{Quarantined: true, Retry: true, ManagementTarget: "TestExclusionSuite/TestOnce", ManagementSuite: "exclusions_test.go/ExclusionSuite"}, env: retry, wantExit: -1,
			verify: requireAtMostOneExecution("method-once", -1),
		},
		{
			// Race isolation rebuilds the suite's events in the parent.
			name: "quarantined-suite-tags", run: "^TestExclusionSuite$", policy: policySettings{Quarantined: true, ManagementTarget: "TestExclusionSuite", ManagementSuite: "exclusions_test.go"}, wantExit: 0,
			check: func(t *testing.T, events map[string][]map[string]any) {
				requireSingleEvent(t, events, "TestExclusionSuite/TestOnce", map[string]any{"test.test_management.no_auto_retry": "true"})
			},
		},
		{
			name: "disabled-protected-fuzz", run: "^FuzzProtected$", policy: policySettings{Disabled: true, ManagementTarget: "FuzzProtected", ManagementSuite: "exclusions_test.go"}, wantExit: 1,
			counts: map[string]string{"fuzz": "x"},
		},
		{
			// A disabled example replays its declared output; a protected one runs.
			name: "disabled-protected-example", run: "^Example_protected$", policy: policySettings{Disabled: true, ManagementTarget: "Example_protected", ManagementSuite: "exclusions_test.go"}, wantExit: 1,
		},
	} {
		for _, mode := range []string{"in_process", "process"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				counters := t.TempDir()
				env := append([]string{"EXCLUSION_COUNTER=" + counters, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode}, tc.env...)
				receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.v", "-test.run=" + tc.run}, Policy: tc.policy, Env: env})
				if tc.verify != nil {
					tc.verify(t, counters, got.code)
				}
				if tc.wantExit >= 0 && got.code != tc.wantExit {
					t.Fatalf("exit %d, want %d: %s %s", got.code, tc.wantExit, got.out, got.stderr)
				}
				for name, want := range tc.counts {
					if ran := executions(t, counters, name); ran != want {
						t.Fatalf("%s executions=%q want=%q\nout: %s", name, ran, want, got.out)
					}
				}
				events := map[string][]map[string]any{}
				for _, event := range receiver.events {
					if event["type"] != "test" {
						continue
					}
					meta := event["content"].(map[string]any)["meta"].(map[string]any)
					name, _ := meta["test.name"].(string)
					events[name] = append(events[name], meta)
				}
				if tc.check != nil {
					tc.check(t, events)
				}
			})
		}
	}
}

// requireAtMostOneExecution requires that a no_auto_retry body ran at most
// once and that the exit matches the flaky sibling's outcome: it fails unless
// a retry recovered it. wantExit -1 derives the exit from the sibling alone.
func requireAtMostOneExecution(counter string, wantExit int) func(*testing.T, string, int) {
	return func(t *testing.T, counters string, exit int) {
		t.Helper()
		once, flaky := executions(t, counters, counter), executions(t, counters, "method-flaky")
		recovered := flaky == "xx"
		if len(once) > 1 || flaky != "x" && !recovered || recovered != (exit == 0) || wantExit >= 0 && exit != wantExit {
			t.Fatalf("%s=%q method-flaky=%q exit=%d", counter, once, flaky, exit)
		}
	}
}

// executions returns the counter file's content, one "x" per body execution.
func executions(t *testing.T, counters, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(counters, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// requireSingleEvent requires one event for name whose tags match want; a nil
// value requires the tag to be absent.
func requireSingleEvent(t *testing.T, events map[string][]map[string]any, name string, want map[string]any) {
	t.Helper()
	if len(events[name]) != 1 {
		names := make([]string, 0, len(events))
		for key, list := range events {
			names = append(names, key+"="+strings.Repeat("x", len(list)))
		}
		t.Fatalf("%s events=%d, want 1; events: %v", name, len(events[name]), names)
	}
	meta := events[name][0]
	for key, value := range want {
		if meta[key] != value {
			t.Fatalf("%s %s=%v, want %v: %v", name, key, meta[key], value, meta)
		}
	}
}
