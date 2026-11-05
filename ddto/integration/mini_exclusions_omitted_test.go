package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniOmittedExcludedFailure(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"omitted_test.go": `package fixture_test
import("os";"testing";"flag";"strings";"github.com/stretchr/testify/suite";"github.com/DataDog/ddtest/ddto/testopt")
type OmittedSuite struct{suite.Suite}
func(s *OmittedSuite)TestCritical(){if err:=os.WriteFile(os.Getenv("OMITTED_CALLS"),[]byte("x"),0600);err!=nil{s.T().Fatal(err)};s.T().Error("sole excluded execution fails")}
func init(){testopt.RegisterTestExclusions((*OmittedSuite).TestCritical,testopt.TestExclusions{NoAutoRetry:os.Getenv("OMITTED_NO_RETRY")=="true",PreventFailureSuppression:os.Getenv("OMITTED_PROTECTED")=="true"})}
func TestOmittedRoot(t *testing.T){if strings.Contains(flag.Lookup("test.run").Value.String(),"TestCritical"){suite.Run(t,new(OmittedSuite));return};f,err:=os.OpenFile(os.Getenv("OMITTED_MARKER"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600);if err==nil{f.WriteString("initial");f.Close();if os.Getenv("OMITTED_LATE")=="true"{if os.Getenv("OMITTED_EARLY_PASS")=="true"{return};t.Error("retry before first discovery");return};suite.Run(t,new(OmittedSuite));t.Error("retry parent independently");return};if !os.IsExist(err){t.Fatal(err)};if os.Getenv("OMITTED_LATE")=="true"{data,err:=os.ReadFile(os.Getenv("OMITTED_MARKER"));if err!=nil{t.Fatal(err)};if string(data)=="initial"{if err:=os.WriteFile(os.Getenv("OMITTED_MARKER"),[]byte("discovered"),0600);err!=nil{t.Fatal(err)};suite.Run(t,new(OmittedSuite));t.Error("retry after first discovery");return}};if os.Getenv("OMITTED_REDISCOVER")=="true"{suite.Run(t,new(OmittedSuite))}}
`})
	bin := filepath.Join(t.TempDir(), executableName("omitted.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile %d %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, family := range []string{"atr", "efd"} {
			for _, flags := range []struct{ noRetry, protected, quarantined, childQuarantined, rediscover, late, earlyPass bool }{{true, false, false, false, false, false, false}, {true, true, false, false, false, false, false}, {true, true, true, false, false, false, false}, {false, true, false, false, false, false, false}, {true, false, false, true, false, false, false}, {true, false, false, true, true, false, false}, {true, false, false, true, false, true, false}, {true, false, false, false, false, true, true}, {true, true, false, false, false, true, true}, {false, true, false, false, false, true, true}, {true, false, false, true, false, true, true}} {
				if flags.earlyPass && family != "efd" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/noRetry=%t/protected=%t/quarantined=%t/childQuarantined=%t/rediscover=%t/late=%t/earlyPass=%t", mode, family, flags.noRetry, flags.protected, flags.quarantined, flags.childQuarantined, flags.rediscover, flags.late, flags.earlyPass), func(t *testing.T) {
					calls := filepath.Join(t.TempDir(), "calls")
					target, suiteName := "TestOmittedRoot", "omitted_test.go"
					if flags.childQuarantined {
						target = "TestOmittedRoot/TestCritical"
						suiteName = "omitted_test.go/OmittedSuite"
					}
					receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestOmittedRoot$"}, Policy: policySettings{Retry: family == "atr", EFD: family == "efd", Quarantined: flags.quarantined || flags.childQuarantined, ManagementTarget: target, ManagementSuite: suiteName}, Env: []string{"OMITTED_CALLS=" + calls, "OMITTED_MARKER=" + filepath.Join(t.TempDir(), "marker"), fmt.Sprintf("OMITTED_EARLY_PASS=%t", flags.earlyPass), fmt.Sprintf("OMITTED_LATE=%t", flags.late), fmt.Sprintf("OMITTED_REDISCOVER=%t", flags.rediscover), fmt.Sprintf("OMITTED_NO_RETRY=%t", flags.noRetry), fmt.Sprintf("OMITTED_PROTECTED=%t", flags.protected), "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, fmt.Sprintf("DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=%t", family == "atr"), "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2", fmt.Sprintf("DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED=%t", family == "efd"), "DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_MAX_RETRIES=2"}})
					want := 0
					if flags.noRetry && !flags.childQuarantined {
						want = 1
					}
					if got.code != want {
						t.Errorf("retained sole failure exit=%d want%d: %s %s", got.code, want, got.out, got.stderr)
					}
					data, err := os.ReadFile(calls)
					if err != nil && !(flags.childQuarantined && os.IsNotExist(err)) || !flags.childQuarantined && string(data) != "x" {
						t.Fatalf("sole declaration execution=%q err=%v", data, err)
					}
					parentFinal := ""
					childEvents := 0
					parentPasses := 0
					for _, event := range receiver.events {
						if event["type"] != "test" {
							continue
						}
						meta := event["content"].(map[string]any)["meta"].(map[string]any)
						if meta["test.name"] == "TestOmittedRoot/TestCritical" {
							childEvents++
							if flags.childQuarantined {
								if string(data) == "x" && !strings.Contains(fmt.Sprint(meta["error.message"]), "sole excluded execution fails") {
									t.Fatalf("expected actual quarantined declaration failure, got %v", meta)
								}
								if meta["test.test_management.is_quarantined"] != "true" || meta["test.test_management.prevent_failure_suppression"] != nil {
									t.Fatalf("quarantine assignment changed: %v", meta)
								}
							} else if meta["test.status"] != "fail" {
								t.Fatalf("excluded initial failure=%v", meta)
							}
						}
						if meta["test.name"] == "TestOmittedRoot" {
							if meta["test.status"] == "pass" {
								parentPasses++
							}
							if flags.earlyPass && meta["test.has_failed_all_retries"] == "true" {
								t.Fatalf("earlier passing attempt was erased: %v", meta)
							}
							if status, ok := meta["test.final_status"].(string); ok {
								parentFinal = status
							}
						}
					}
					final := "pass"
					if flags.noRetry && !flags.childQuarantined {
						final = "fail"
					}
					wantChildren := 1
					// Ordinary subtests discovered only in a process retry retain the existing
					// excluded-only event transport boundary.
					if mode == "process" && flags.earlyPass && !flags.noRetry {
						wantChildren = 0
					}
					if flags.earlyPass && parentPasses == 0 {
						t.Fatal("initial passing parent execution was erased")
					}
					if parentFinal != final || childEvents != wantChildren {
						t.Fatalf("retained verdict final=%q want%s childEvents=%d", parentFinal, final, childEvents)
					}
				})
			}
		}
	}
}
